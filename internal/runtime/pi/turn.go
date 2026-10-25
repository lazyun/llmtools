package pi

import (
	"ai-worker-mcp/internal/agent"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// launch is the process-local description of how to start one Pi turn. Pi is a
// session runtime, not an HTTP server, so there is no URL, port, or health
// endpoint anywhere in this path.
type launch struct {
	binary    string
	extension string
	workdir   string
	env       []string
	args      []string
}

func (a *Adapter) base() (launch, error) {
	if a.extension == "" {
		return launch{}, agent.E("RUNTIME_UNAVAILABLE")
	}
	bin, e := exec.LookPath(a.spec.Binary)
	if e != nil {
		return launch{}, agent.E("RUNTIME_NOT_FOUND")
	}
	return launch{binary: bin, extension: a.extension, workdir: a.spec.WorkingDir, env: []string{
		"LLMTOOLS_CONTROL_BIN=" + a.spec.ControlBinary,
		"LLMTOOLS_CONTROL_DB=" + a.spec.ControlDB,
	}}, nil
}

// toolAllowlist is the exact execution surface one Pi turn may see. Anything
// not listed here is absent from the model's tool list, and the bridge denies
// it again at call time.
func toolAllowlist(repoRead bool, execution ...bool) []string {
	tools := []string{"llmtools_control_request_context", "llmtools_control_request_decision"}
	if repoRead {
		tools = append([]string{"llmtools_control_repo_read", "llmtools_control_repo_glob", "llmtools_control_repo_grep"}, tools...)
	}
	if len(execution) > 0 && execution[0] {
		tools = append(tools, "llmtools_control_repo_write", "llmtools_control_repo_edit")
	}
	if len(execution) > 1 && execution[1] {
		tools = append(tools, "llmtools_control_repo_exec")
	}
	return tools
}

func (a *Adapter) launchArgs(l *launch, token string, repoRead bool, provider, model, sessionID string, execution ...bool) []string {
	l.env = append(l.env, "LLMTOOLS_CONTROL_TOKEN="+token, fmt.Sprintf("LLMTOOLS_REPO_READ=%d", boolInt(repoRead)))
	write, shell := false, false
	if len(execution) > 0 {
		write = execution[0]
	}
	if len(execution) > 1 {
		shell = execution[1]
	}
	l.env = append(l.env, fmt.Sprintf("LLMTOOLS_REPO_WRITE=%d", boolInt(write)), fmt.Sprintf("LLMTOOLS_SHELL=%d", boolInt(shell)))
	tools := toolAllowlist(repoRead, write, shell)
	l.args = append(l.args,
		"-e", l.extension,
		"--no-builtin-tools",
		"--tools", strings.Join(tools, ","),
		// Discovery is off everywhere except this run: an extension, skill,
		// prompt template, theme, AGENTS.md or project-local setting found in the
		// working directory or the user's Pi config can neither join the tool
		// surface nor shape the prompt.
		"--no-extensions", "--no-context-files", "--no-skills", "--no-prompt-templates", "--no-themes", "--no-approve",
		"--session-dir", a.spec.SessionDir,
		"--provider", provider, "--model", model,
	)
	if sessionID != "" {
		l.args = append(l.args, "--session", sessionID)
	}
	return l.args
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

// attach waits for Pi to report the session identity it actually created or
// restored, before Core freezes the run binding.
func (a *Adapter) attach(ctx context.Context, p *process) (string, string, error) {
	deadline := time.Now().Add(30 * time.Second)
	for {
		state, e := p.sessionState(ctx)
		if e == nil && state.SessionID != "" {
			return state.SessionID, state.SessionFile, nil
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			if e == nil {
				e = agent.E("SESSION_CREATE_FAILED")
			}
			return "", "", mapProcessError(p, e)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// processFor returns the process StartTask prepared, or restores the durable Pi
// session in a fresh process. Restoring is the normal path for a continuation,
// because Pi keeps its session tree on disk rather than in this process.
func (a *Adapter) processFor(ctx context.Context, h agent.RuntimeHandle, l launch) (*process, error) {
	a.mu.Lock()
	s := a.live[h.SessionID]
	if s != nil {
		delete(a.live, h.SessionID)
	}
	a.mu.Unlock()
	if s != nil {
		return s.proc, nil
	}
	provider, model, e := splitModel(h.Model)
	if e != nil {
		return nil, e
	}
	args := a.launchArgs(&l, h.ControlToken, h.RepoRead, provider, model, h.SessionID, h.RepoWrite, h.Shell)
	p, e := startProcess(ctx, l, args)
	if e != nil {
		return nil, e
	}
	id, _, e := a.attach(ctx, p)
	if e != nil {
		p.kill()
		return nil, e
	}
	// A restored process reporting a different session would replay the wrong
	// transcript, so it is refused rather than trusted.
	if id != h.SessionID {
		p.kill()
		return nil, agent.E("SESSION_LOST")
	}
	return p, nil
}

// retire ends one turn's process. One Pi process per turn is safe precisely
// because the session tree is durable.
func (a *Adapter) retire(h agent.RuntimeHandle, p *process) {
	p.kill()
	a.mu.Lock()
	if s := a.live[h.SessionID]; s != nil && s.proc == p {
		delete(a.live, h.SessionID)
	}
	a.mu.Unlock()
}

// runTurn dispatches one message on a Pi session and maps Pi's event stream
// onto llmTools' runtime neutral result, suspension and progress vocabulary.
func (a *Adapter) runTurn(ctx context.Context, h agent.RuntimeHandle, message string, emit agent.Emit) (agent.AgentResult, error) {
	l, e := a.base()
	if e != nil {
		return agent.AgentResult{}, e
	}
	p, e := a.processFor(ctx, h, l)
	if e != nil {
		return agent.AgentResult{}, e
	}
	defer a.retire(h, p)

	emitStage(emit, "RUNNING")
	if _, e = p.request(ctx, "prompt-"+shortID(), map[string]any{"type": "prompt", "message": message}); e != nil {
		return agent.AgentResult{}, mapProcessError(p, e)
	}
	tail, control, e := observe(ctx, p, emit)
	if e != nil {
		return agent.AgentResult{}, mapProcessError(p, e)
	}
	// One operator-visible diagnostic line per turn: what Pi's own terminal
	// assistant record said, so a rejected result is explainable without a
	// transcript. It carries identity and lengths only, never content.
	piDebugf("RESULT provider=%q model=%q stop=%q sawMessage=%v control=%v", tail.provider, tail.model, tail.stopReason, tail.sawMessage, control != nil)
	if control != nil {
		emitStage(emit, "WAITING_FOR_CONTEXT")
		return agent.AgentResult{}, a.suspension(h, control)
	}
	// The control bridge aborts the loop after a suspension is recorded, so an
	// aborted tail without a recorded request is a real cancellation.
	if tail.sawMessage && tail.stopReason == "aborted" {
		return agent.AgentResult{}, context.Canceled
	}
	if tail.sawMessage && tail.stopReason == "error" {
		// Pi keeps the provider's failure text on the assistant record. It selects
		// a code and appears in operator diagnostics; it never becomes Core state.
		piAuditf("assistant_error %q", tail.errText)
		return agent.AgentResult{}, mapAssistantError(tail.errText)
	}
	emitStage(emit, "VALIDATING")
	text, e := p.lastAssistantText(ctx)
	if e != nil {
		return agent.AgentResult{}, mapProcessError(p, e)
	}
	if strings.TrimSpace(text) == "" {
		piDebugf("REJECT empty_final")
		return agent.AgentResult{}, agent.E("EMPTY_AGENT_RESULT")
	}
	// Pi reports the provider and the catalogue id separately, and a catalogue id
	// may itself contain "/" (for example "deepseek-ai/DeepSeek-V4-Flash"), so the
	// routing identity is the provider prefix joined exactly once.
	model := tail.model
	if model != "" && tail.provider != "" && !strings.HasPrefix(model, tail.provider+"/") {
		model = tail.provider + "/" + model
	}
	if model == "" {
		piDebugf("REJECT no_model text_len=%d", len(text))
		return agent.AgentResult{}, agent.E("INVALID_AGENT_RESULT")
	}
	if h.Model != "" && model != h.Model {
		piDebugf("REJECT model_mismatch actual=%q want=%q", model, h.Model)
		return agent.AgentResult{}, agent.E("INVALID_AGENT_RESULT")
	}
	// The final response is operator-visible, so a leaked session id or control
	// token is a contract violation rather than a warning.
	if strings.Contains(text, h.SessionID) || (h.ControlToken != "" && strings.Contains(text, h.ControlToken)) {
		piDebugf("REJECT identity_leak text_len=%d", len(text))
		return agent.AgentResult{}, agent.E("INVALID_AGENT_RESULT")
	}
	return agent.AgentResult{Status: "completed", Content: text, Summary: text, Agent: h.Agent, Model: model}, nil
}

type assistantTail struct {
	provider   string
	model      string
	stopReason string
	sawMessage bool
	errText    string
}

// observe drains Pi events until the run settles. agent_settled is the normal
// end signal; an aborted turn never emits it, so agent_end plus an idle poll
// closes that path instead of hanging the run.
func observe(ctx context.Context, p *process, emit agent.Emit) (assistantTail, *pendingControl, error) {
	var tail assistantTail
	var control *pendingControl
	ended := false
	for !ended {
		select {
		case <-ctx.Done():
			return tail, control, ctx.Err()
		case ev, ok := <-p.events:
			if !ok {
				return tail, control, errRPCStopped
			}
			switch ev.Type {
			case "message_end":
				var msg struct {
					Message struct {
						Role         string          `json:"role"`
						Provider     string          `json:"provider"`
						Model        string          `json:"model"`
						StopReason   string          `json:"stopReason"`
						ErrorMessage json.RawMessage `json:"errorMessage"`
						Error        json.RawMessage `json:"error"`
					} `json:"message"`
				}
				if json.Unmarshal(ev.Raw, &msg) == nil && msg.Message.Role == "assistant" {
					tail = assistantTail{
						provider:   msg.Message.Provider,
						model:      msg.Message.Model,
						stopReason: msg.Message.StopReason,
						sawMessage: true,
						errText:    errorText(msg.Message.ErrorMessage, msg.Message.Error),
					}
				}
			case "tool_execution_start":
				// Which tool names actually executed is the security evidence for
				// the run, so it is recorded unconditionally and without arguments.
				piAuditf("tool_executed name=%q", jsonField(ev.Raw, "toolName"))
				emitStage(emit, "TOOL_RUNNING")
			case "tool_execution_end":
				var record struct {
					ToolName string `json:"toolName"`
					Result   struct {
						Details json.RawMessage `json:"details"`
					} `json:"result"`
				}
				if json.Unmarshal(ev.Raw, &record) == nil {
					if c := parseControl(record.ToolName, record.Result.Details); c != nil {
						control = c
					}
				}
				emitStage(emit, "TOOL_COMPLETED")
			case "agent_end":
				var end struct {
					WillRetry bool `json:"willRetry"`
				}
				_ = json.Unmarshal(ev.Raw, &end)
				if !end.WillRetry {
					if !p.waitIdle(ctx) {
						return tail, control, errRPCStopped
					}
					ended = true
				}
			case "agent_settled":
				ended = true
			case "auto_retry_start":
				emitStage(emit, "RUNNING")
			}
		}
	}
	return tail, control, nil
}

func emitStage(emit agent.Emit, stage string) {
	if emit != nil {
		emit(agent.Progress{Stage: stage})
	}
}
