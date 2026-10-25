package repocontrol

import (
	"ai-worker-mcp/internal/agent"
	"ai-worker-mcp/internal/localfs"
	"ai-worker-mcp/internal/projectexec"
	"ai-worker-mcp/internal/repoaccess"
	"ai-worker-mcp/internal/store"
	"context"
	"encoding/json"
	"path/filepath"
	"time"
)

type WriteInput struct {
	ControlToken string `json:"control_token"`
	Path         string `json:"path"`
	Content      string `json:"content"`
}
type EditInput struct {
	ControlToken string `json:"control_token"`
	Path         string `json:"path"`
	OldText      string `json:"old_text"`
	NewText      string `json:"new_text"`
}
type ExecInput struct {
	ControlToken   string `json:"control_token"`
	Command        string `json:"command"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
}

const DescriptionWrite = "Create or replace a UTF-8 project file inside this run's authorized workspace. Parent directories are created. Outside paths, escaping symlinks and the workspace's .git metadata are denied. Available only to the developer role."
const DescriptionEdit = "Replace exactly one occurrence of old_text with new_text in an authorized project file. Zero or multiple matches fail without editing. Available only to the developer role."
const DescriptionExec = "Run a foreground implementation or test command in the authorized project through the macOS execution sandbox. Writes are limited to the workspace and this run's scratch; .git writes and outbound network access (including localhost) are denied. Standard toolchains and dependency caches are readable. Provider keys are not inherited. Report the actual exit_code, output and any timed_out or truncated flags. timeout_seconds defaults to 120 and accepts 1..3600. Available only to the developer role."

func authorizeExecution(st *store.Store, token string) (store.RepoAccess, string, error) {
	access, root, err := authorize(st, token)
	if err != nil {
		return access, "", err
	}
	run, err := st.Get(access.RunID)
	if err != nil || run.LogicalRole != "developer" || run.Capabilities != agent.DeveloperCapabilities() {
		return access, "", agent.E("EXECUTION_NOT_ALLOWED")
	}
	return access, root, nil
}

func executeTool(ctx context.Context, st *store.Store, tool string, raw json.RawMessage) (json.RawMessage, error) {
	var input struct {
		ControlToken string `json:"control_token"`
	}
	if json.Unmarshal(raw, &input) != nil {
		return nil, agent.E("REPO_INPUT_INVALID")
	}
	access, root, err := authorizeExecution(st, input.ControlToken)
	if err != nil {
		return nil, err
	}
	// Pi gateways are separate processes and runtimes may batch tools. Serialize
	// the whole operation so a formatter/test command cannot race repo_edit.
	for {
		lock, lockErr := localfs.Lock(filepath.Join(st.Directory, filepath.Base(projectexec.Scratch(st.Directory, access.RunID))+".operation.lock"))
		if lockErr == nil {
			defer localfs.Unlock(lock)
			break
		}
		if !localfs.IsLockBusy(lockErr) {
			return nil, agent.E("INTERNAL_ERROR")
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
		if _, _, err = authorizeExecution(st, input.ControlToken); err != nil {
			return nil, err
		}
	}
	if _, _, err = authorizeExecution(st, input.ControlToken); err != nil {
		return nil, err
	}
	switch tool {
	case "repo_write":
		var arg WriteInput
		if agent.Strict(raw, &arg) != nil {
			return nil, agent.E("REPO_INPUT_INVALID")
		}
		result, err := repoaccess.Write(root, arg.Path, arg.Content)
		if err != nil {
			return nil, err
		}
		auditAccess(access, tool, arg.Path, len(arg.Content))
		return json.Marshal(result)
	case "repo_edit":
		var arg EditInput
		if agent.Strict(raw, &arg) != nil {
			return nil, agent.E("REPO_INPUT_INVALID")
		}
		result, err := repoaccess.Edit(root, arg.Path, arg.OldText, arg.NewText)
		if err != nil {
			return nil, err
		}
		auditAccess(access, tool, arg.Path, 1)
		return json.Marshal(result)
	case "repo_exec":
		var arg ExecInput
		if agent.Strict(raw, &arg) != nil {
			return nil, agent.E("REPO_INPUT_INVALID")
		}
		if arg.TimeoutSeconds == 0 {
			arg.TimeoutSeconds = 120
		}
		liveCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		stopped := make(chan struct{})
		defer close(stopped)
		go func() {
			timer := time.NewTicker(100 * time.Millisecond)
			defer timer.Stop()
			for {
				select {
				case <-stopped:
					return
				case <-liveCtx.Done():
					return
				case <-timer.C:
					if _, _, err := authorizeExecution(st, input.ControlToken); err != nil {
						cancel()
						return
					}
				}
			}
		}()
		result, err := projectexec.Run(liveCtx, root, projectexec.Scratch(st.Directory, access.RunID), arg.Command, arg.TimeoutSeconds)
		if _, _, liveErr := authorizeExecution(st, input.ControlToken); liveErr != nil {
			return nil, liveErr
		}
		if err != nil {
			return nil, err
		}
		auditAccess(access, tool, "project command", result.ExitCode)
		return json.Marshal(result)
	}
	return nil, agent.E("REPO_INPUT_INVALID")
}
