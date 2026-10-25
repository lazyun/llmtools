// Package repocontrol is the single implementation of llmTools' controlled
// repository and project execution tools. Runtime adapters reach it through their own
// transport only: the OpenCode control MCP server and the one-shot control
// invocation used by the experimental Pi runtime both call Call, so a second
// path validator, registry, or revoke rule can never appear per runtime.
package repocontrol

import (
	"ai-worker-mcp/internal/agent"
	"ai-worker-mcp/internal/repoaccess"
	"ai-worker-mcp/internal/store"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"
)

type ReadInput struct {
	ControlToken string `json:"control_token"`
	Path         string `json:"path"`
}
type GlobInput struct {
	ControlToken string `json:"control_token"`
	Pattern      string `json:"pattern"`
	Path         string `json:"path,omitempty"`
}
type GrepInput struct {
	ControlToken string `json:"control_token"`
	Pattern      string `json:"pattern"`
	Glob         string `json:"glob,omitempty"`
	Path         string `json:"path,omitempty"`
}

// Tools is the authoritative repository tool set. Names are stable
// across transports so per-run authorization, audit lines and role policy never
// have to know which runtime asked.
var Tools = []string{"repo_read", "repo_glob", "repo_grep", "repo_write", "repo_edit", "repo_exec"}

const (
	DescriptionRead = "Read one file from the workspace authorized for this run. Paths may be workspace-relative or absolute inside the workspace. Read-only: outside paths, traversal, and symlink escapes are denied by the server."
	DescriptionGlob = "List files in the workspace authorized for this run by glob pattern, for example \"src/**\" or \"**/*.go\". Read-only and confined: results never include paths outside the workspace or reached through an escaping symlink."
	DescriptionGrep = "Search file contents in the workspace authorized for this run with a regular expression, optionally narrowed by a glob or a path. Read-only: matches outside the workspace are never returned."
)

// The audit records which workspace was read and how, never the bytes that were
// read. It is the operator-visible evidence for an authorization failure, a
// revocation, or an outside-root denial a worker cannot otherwise report.
func auditAccess(access store.RepoAccess, tool, target string, count int) {
	log.New(os.Stderr, "", 0).Println(fmt.Sprintf("repo_access_allowed run_id=%s workspace_id=%s tool=%s target=%q count=%d at=%s",
		access.RunID, access.WorkspaceID, tool, target, count, time.Now().UTC().Format(time.RFC3339)))
}

func auditDenied(access store.RepoAccess, code string) {
	log.New(os.Stderr, "", 0).Println(fmt.Sprintf("repo_access_denied run_id=%s workspace_id=%s code=%s at=%s",
		access.RunID, access.WorkspaceID, code, time.Now().UTC().Format(time.RFC3339)))
}

// authorize resolves the live grant for one control token. The frozen snapshot
// never authorizes anything by itself: revocation, a changed revision, a
// changed root, or a missing directory all deny this access.
func authorize(st *store.Store, token string) (store.RepoAccess, string, error) {
	access, root, e := st.LiveRepoRoot(token)
	if e != nil {
		code := agent.Safe(e).Code
		auditDenied(access, code)
		return access, "", agent.E(code)
	}
	return access, root, nil
}

// Call executes one repository tool against the live grant carried by the
// opaque per-run control token. The returned JSON is the exact payload the
// runtime transport should hand back to the execution agent.
func Call(ctx context.Context, st *store.Store, tool string, args json.RawMessage) (json.RawMessage, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	switch tool {
	case "repo_write", "repo_edit", "repo_exec":
		return executeTool(ctx, st, tool, args)
	case "repo_read":
		var input ReadInput
		if json.Unmarshal(args, &input) != nil {
			return nil, agent.E("REPO_INPUT_INVALID")
		}
		access, root, e := authorize(st, input.ControlToken)
		if e != nil {
			return nil, e
		}
		out, e := repoaccess.Read(root, input.Path, repoaccess.MaxFileBytes)
		if e != nil {
			auditDenied(access, agent.Safe(e).Code)
			return nil, e
		}
		auditAccess(access, "repo_read", out.Path, 1)
		return json.Marshal(out)
	case "repo_glob":
		var input GlobInput
		if json.Unmarshal(args, &input) != nil {
			return nil, agent.E("REPO_INPUT_INVALID")
		}
		access, root, e := authorize(st, input.ControlToken)
		if e != nil {
			return nil, e
		}
		out, e := repoaccess.Glob(root, input.Pattern, input.Path)
		if e != nil {
			auditDenied(access, agent.Safe(e).Code)
			return nil, e
		}
		auditAccess(access, "repo_glob", input.Pattern, len(out))
		return json.Marshal(map[string]any{"paths": out, "count": len(out)})
	case "repo_grep":
		var input GrepInput
		if json.Unmarshal(args, &input) != nil {
			return nil, agent.E("REPO_INPUT_INVALID")
		}
		access, root, e := authorize(st, input.ControlToken)
		if e != nil {
			return nil, e
		}
		out, e := repoaccess.Grep(root, input.Pattern, input.Glob, input.Path)
		if e != nil {
			auditDenied(access, agent.Safe(e).Code)
			return nil, e
		}
		auditAccess(access, "repo_grep", input.Pattern, len(out))
		return json.Marshal(map[string]any{"matches": out, "count": len(out)})
	default:
		return nil, agent.E("REPO_INPUT_INVALID")
	}
}
