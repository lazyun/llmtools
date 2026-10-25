package pi

import (
	"ai-worker-mcp/internal/agent"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Kind is the runtime discriminator this adapter reports to Core.
const Kind = "pi"

// launchIdleTTL reaps a Pi process that Core created a session for but never
// dispatched, so a failed handoff cannot leave an orphan runtime behind.
const launchIdleTTL = 2 * time.Minute

// healthTTL bounds how often the passive RPC handshake re-runs. Core calls
// Health both as a pre-flight check and again from StartTask, and each probe is
// a process launch, so the answer is held briefly. It is a freshness window for
// a passive observation, not a claim that readiness can change in that window.
const healthTTL = 30 * time.Second

// Spec carries everything the adapter needs to launch Pi. The session and
// working directories are llmTools-owned scratch, never a user workspace.
type Spec struct {
	Binary        string // pi executable
	ControlBinary string // this llmTools executable, used by the extension bridge
	ControlDB     string // absolute run store path
	SessionDir    string // absolute directory for Pi session trees
	WorkingDir    string // absolute cwd for the Pi process (never a workspace)
}

// Adapter runs llmTools tasks on Pi. Pi is an alternate runtime, not a second
// production path: every Pi-specific detail stays below Core.
type Adapter struct {
	spec Spec

	mu        sync.Mutex
	live      map[string]*session // pi session id -> live process
	extension string              // materialised bridge path
	models    []agent.RuntimeModel
	modelsAt  time.Time
	version   string
	healthAt  time.Time
}

type session struct {
	proc *process
	last time.Time
}

// New prepares the adapter without starting anything: MCP initialization must
// never launch a runtime.
func New(spec Spec) (*Adapter, error) {
	if spec.Binary == "" {
		return nil, agent.E("RUNTIME_NOT_FOUND")
	}
	if spec.ControlDB == "" || !filepath.IsAbs(spec.ControlDB) {
		return nil, agent.E("CONFIG_ERROR")
	}
	if spec.SessionDir == "" || !filepath.IsAbs(spec.SessionDir) {
		return nil, agent.E("CONFIG_ERROR")
	}
	if e := os.MkdirAll(spec.SessionDir, 0o700); e != nil {
		return nil, agent.E("RUNTIME_UNAVAILABLE")
	}
	if spec.WorkingDir == "" {
		spec.WorkingDir = spec.SessionDir
	}
	if e := os.MkdirAll(spec.WorkingDir, 0o700); e != nil {
		return nil, agent.E("RUNTIME_UNAVAILABLE")
	}
	path, e := materializeExtension(spec.SessionDir)
	if e != nil {
		return nil, e
	}
	return &Adapter{spec: spec, live: map[string]*session{}, extension: path}, nil
}

func (a *Adapter) RuntimeKind() string { return Kind }

// AgentCatalogueSupported is false: Pi has no runtime-native agent selector to
// enumerate. llmTools' logical roles are routing identities in Core's config,
// not agents Pi can list, so this adapter never reports them as its own.
func (a *Adapter) AgentCatalogueSupported() bool { return false }

// Health proves the Pi transport can start and speak its protocol. It is
// passive by construction: the probe runs --offline with every tool disabled and
// without persisting a session, so a healthy result says nothing about a model,
// a provider or a credential, and no provider traffic is generated.
func (a *Adapter) Health(ctx context.Context) (agent.Health, error) {
	a.mu.Lock()
	cached, probed := a.version, a.healthAt
	a.mu.Unlock()
	if cached != "" && probed.Add(healthTTL).After(time.Now()) {
		return agent.Health{Healthy: true, Version: cached}, nil
	}
	bin, e := exec.LookPath(a.spec.Binary)
	if e != nil {
		return agent.Health{}, agent.E("RUNTIME_NOT_FOUND")
	}
	if cached == "" {
		cmd, commandErr := piCommand(ctx, bin, "--version")
		if commandErr != nil {
			return agent.Health{}, agent.E("RUNTIME_NOT_FOUND")
		}
		cmd.Env = commandEnvironment(bin, nil)
		out, e := cmd.Output()
		if e != nil {
			return agent.Health{}, agent.E("RUNTIME_NOT_FOUND")
		}
		cached = strings.TrimSpace(string(out))
		if cached == "" {
			return agent.Health{}, agent.E("RUNTIME_PROTOCOL_ERROR")
		}
	}
	if e := a.probeHandshake(ctx, bin); e != nil {
		return agent.Health{}, e
	}
	a.mu.Lock()
	a.version, a.healthAt = cached, time.Now()
	a.mu.Unlock()
	return agent.Health{Healthy: true, Version: cached}, nil
}

// probeHandshake starts one RPC-only Pi process and completes a single
// get_state round trip. Anything that cannot answer the protocol is not
// accepted as a usable runtime, so a misinstalled or incompatible Pi fails
// closed here rather than at the first dispatched turn.
func (a *Adapter) probeHandshake(ctx context.Context, bin string) error {
	l := launch{binary: bin, extension: a.extension, workdir: a.spec.WorkingDir}
	args := []string{
		"--offline", "--no-session", "--no-tools", "--no-builtin-tools",
		"--no-extensions", "--no-context-files", "--no-skills", "--no-prompt-templates",
		"--no-themes", "--no-approve", "--session-dir", a.spec.SessionDir,
	}
	callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	p, e := startProcess(callCtx, l, args)
	if e != nil {
		return e
	}
	defer p.kill()
	data, e := p.request(callCtx, "health-"+shortID(), map[string]any{"type": "get_state"})
	if e != nil {
		return mapProcessError(p, e)
	}
	var state sessionInfo
	if json.Unmarshal(data, &state) != nil || state.SessionID == "" {
		return agent.E("RUNTIME_PROTOCOL_ERROR")
	}
	return nil
}

// ListAgents returns no entries, because there is no Pi catalogue to report. An
// empty catalogue is not an error and does not mean agents are unavailable;
// Core must consult AgentCatalogueSupported before requiring a name.
func (a *Adapter) ListAgents(context.Context) ([]agent.AgentInfo, error) {
	return nil, nil
}

// ListModels reads Pi's own model catalogue. Provider and credential management
// stay inside Pi; llmTools never stores or edits them.
func (a *Adapter) ListModels(ctx context.Context) ([]agent.RuntimeModel, error) {
	a.mu.Lock()
	fresh := a.modelsAt.Add(60 * time.Second).After(time.Now())
	models := a.models
	a.mu.Unlock()
	if fresh && len(models) > 0 {
		return models, nil
	}
	cmd, commandErr := piCommand(ctx, a.spec.Binary, "--offline", "--list-models")
	if commandErr != nil {
		return nil, agent.E("RUNTIME_NOT_FOUND")
	}
	cmd.Env = commandEnvironment(a.spec.Binary, nil)
	out, e := cmd.Output()
	if e != nil {
		return nil, agent.E("RUNTIME_UNAVAILABLE")
	}
	models = parseModelTable(string(out))
	if len(models) == 0 {
		// The command succeeded but enumerated nothing, so discovery failed. One
		// specific model being absent is reported by StartTask; an unreadable
		// catalogue must not pretend to be that answer.
		return nil, agent.E("RUNTIME_UNAVAILABLE")
	}
	a.mu.Lock()
	a.models, a.modelsAt = models, time.Now()
	a.mu.Unlock()
	return models, nil
}

func (a *Adapter) findModel(id string) (agent.RuntimeModel, bool) {
	models, e := a.ListModels(context.Background())
	if e != nil {
		return agent.RuntimeModel{}, false
	}
	for _, m := range models {
		if m.ID == id {
			return m, true
		}
	}
	return agent.RuntimeModel{}, false
}

// StartTask creates the Pi session and mints the run's opaque control token.
// The process stays registered for the immediately following dispatch; if Core
// never dispatches, the idle reaper and CancelTask both remove it.
func (a *Adapter) StartTask(ctx context.Context, t agent.AgentTask, name, model string) (agent.RuntimeHandle, error) {
	return a.StartTaskInSession(ctx, t, name, model, "")
}

// StartTaskInSession binds a new task to the conversation's durable Pi history.
// A new token and launch policy apply even when the session already exists.
func (a *Adapter) StartTaskInSession(ctx context.Context, t agent.AgentTask, name, model, sessionID string) (agent.RuntimeHandle, error) {
	if err := t.Capabilities.ValidateForRole(t.Agent); err != nil {
		return agent.RuntimeHandle{}, err
	}
	if _, e := a.Health(ctx); e != nil {
		return agent.RuntimeHandle{}, e
	}
	provider, modelID, e := splitModel(model)
	if e != nil {
		return agent.RuntimeHandle{}, e
	}
	if _, found := a.findModel(model); !found {
		return agent.RuntimeHandle{}, agent.E("MODEL_NOT_AVAILABLE")
	}
	token := newControlToken()
	l, e := a.base()
	if e != nil {
		return agent.RuntimeHandle{}, e
	}
	args := a.launchArgs(&l, token, t.Capabilities.RepoRead, provider, modelID, sessionID, t.Capabilities.RepoWrite, t.Capabilities.Shell)
	p, e := startProcess(ctx, l, args)
	if e != nil {
		return agent.RuntimeHandle{}, e
	}
	id, sessionFile, e := a.attach(ctx, p)
	if e != nil {
		p.kill()
		return agent.RuntimeHandle{}, e
	}
	if sessionID != "" && id != sessionID {
		p.kill()
		return agent.RuntimeHandle{}, agent.E("SESSION_LOST")
	}
	a.mu.Lock()
	a.live[id] = &session{proc: p, last: time.Now()}
	a.mu.Unlock()
	return agent.RuntimeHandle{SessionID: id, SessionFile: sessionFile, Agent: name, Model: model, ControlToken: token, RepoRead: t.Capabilities.RepoRead, RepoWrite: t.Capabilities.RepoWrite, Shell: t.Capabilities.Shell, WorkspaceRoot: workspaceRoot(t)}, nil
}

// ContinueTask forwards the caller-authored message without serializing task metadata.
func (a *Adapter) ContinueTask(ctx context.Context, h agent.RuntimeHandle, input any, emit agent.Emit) (agent.AgentResult, error) {
	task, ok := input.(agent.AgentTask)
	if !ok {
		return agent.AgentResult{}, agent.E("INVALID_AGENT_RESULT")
	}
	return a.runTurn(ctx, h, task.Objective, emit)
}

func (a *Adapter) ResumeContext(ctx context.Context, h agent.RuntimeHandle, raw json.RawMessage, answers []agent.AdditionalContext, emit agent.Emit) (agent.AgentResult, error) {
	state, e := a.pendingState(raw, h, "context")
	if e != nil {
		return agent.AgentResult{}, e
	}
	if !agent.ContextRequestsValid(state.Requests) {
		return agent.AgentResult{}, agent.E("PENDING_CONTEXT_LOST")
	}
	if len(answers) != len(state.Requests) {
		return agent.AgentResult{}, agent.E("INVALID_CONTEXT_RESPONSE")
	}
	pending := map[string]bool{}
	for _, q := range state.Requests {
		pending[q.ID] = true
	}
	for _, answer := range answers {
		if !pending[answer.RequestID] {
			return agent.AgentResult{}, agent.E("INVALID_CONTEXT_RESPONSE")
		}
		delete(pending, answer.RequestID)
	}
	// Only the answers travel. The original task stays in the durable Pi session.
	payload, _ := json.Marshal(map[string]any{"llmtools_control_response": "context", "answers": answers})
	return a.runTurn(ctx, h, string(payload), emit)
}

func (a *Adapter) ResumeDecision(ctx context.Context, h agent.RuntimeHandle, raw json.RawMessage, answers []agent.DecisionResponse, emit agent.Emit) (agent.AgentResult, error) {
	state, e := a.pendingState(raw, h, "decision")
	if e != nil {
		return agent.AgentResult{}, e
	}
	if len(state.Decisions) == 0 || len(answers) != len(state.Decisions) {
		return agent.AgentResult{}, agent.E("PENDING_DECISION_LOST")
	}
	byID := map[string]agent.DecisionResponse{}
	for _, answer := range answers {
		if _, dup := byID[answer.RequestID]; dup {
			return agent.AgentResult{}, agent.E("INVALID_DECISION_RESPONSE")
		}
		byID[answer.RequestID] = answer
	}
	if len(byID) != len(state.Decisions) {
		return agent.AgentResult{}, agent.E("INVALID_DECISION_RESPONSE")
	}
	for _, q := range state.Decisions {
		answer, ok := byID[q.ID]
		if !ok || agent.ValidateDecisionResponse(q, answer) != nil {
			return agent.AgentResult{}, agent.E("INVALID_DECISION_RESPONSE")
		}
		if answer.Reject {
			return a.reject(ctx, h)
		}
	}
	payload, _ := json.Marshal(map[string]any{"llmtools_control_response": "decision", "answers": answers})
	return a.runTurn(ctx, h, string(payload), emit)
}

// reject ends a refused decision the same way cancellation does: the run stops
// and nothing is replayed into the durable session.
func (a *Adapter) reject(ctx context.Context, h agent.RuntimeHandle) (agent.AgentResult, error) {
	if e := a.CancelTask(ctx, h); e != nil {
		return agent.AgentResult{}, e
	}
	return agent.AgentResult{}, context.Canceled
}

// CancelTask aborts a live turn. A session with no live process is already
// idle, which is a confirmed cancellation rather than a failure to abort.
func (a *Adapter) CancelTask(ctx context.Context, h agent.RuntimeHandle) error {
	if h.SessionID == "" {
		return nil
	}
	a.mu.Lock()
	s := a.live[h.SessionID]
	delete(a.live, h.SessionID)
	a.mu.Unlock()
	if s == nil {
		return nil
	}
	defer s.proc.kill()
	callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if _, e := s.proc.request(callCtx, "cancel-"+shortID(), map[string]any{"type": "abort"}); e != nil && callCtx.Err() == nil {
		return agent.E("ABORT_UNCONFIRMED")
	}
	return nil
}

// SubscribeProgress reports a live Pi process dying. Pi turns are observed
// inline by ContinueTask, so an idle session has nothing to fail.
func (a *Adapter) SubscribeProgress(ctx context.Context, h agent.RuntimeHandle, emit agent.Emit) (<-chan error, error) {
	a.mu.Lock()
	s := a.live[h.SessionID]
	a.mu.Unlock()
	out := make(chan error, 1)
	if s == nil {
		return out, nil
	}
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case <-s.proc.closed:
				out <- agent.E("RUNTIME_UNAVAILABLE")
				return
			case <-time.After(time.Second):
				a.mu.Lock()
				known := a.live[h.SessionID]
				a.mu.Unlock()
				if known == nil {
					return
				}
			}
		}
	}()
	return out, nil
}

// Close reaps every live Pi process. Orphaned Pi processes would keep the
// per-run control bridge alive, so shutdown is unconditional.
func (a *Adapter) Close() {
	a.mu.Lock()
	sessions := a.live
	a.live = map[string]*session{}
	a.mu.Unlock()
	for _, s := range sessions {
		s.proc.kill()
	}
}
