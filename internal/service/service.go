// Package service owns task lifecycle and policy, independently of runtime APIs.
package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"ai-worker-mcp/internal/agent"
	"ai-worker-mcp/internal/config"
	"ai-worker-mcp/internal/localfs"
	"ai-worker-mcp/internal/projectexec"
	"ai-worker-mcp/internal/store"
)

type Service struct {
	Store       *store.Store
	Runtime     agent.RuntimeAdapter
	Config      config.Config
	logger      *slog.Logger
	timeoutMu   sync.Mutex
	taskTimeout time.Duration
}

func New(c config.Config, s *store.Store, r agent.RuntimeAdapter) *Service {
	level := slog.LevelInfo
	_ = level.UnmarshalText([]byte(c.General.LogLevel))
	return &Service{Store: s, Runtime: r, Config: c, logger: slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level})), taskTimeout: time.Duration(c.General.TaskTimeoutSeconds) * time.Second}
}

// RuntimeKind is the generic runtime identity Core records for a run. Core
// never branches on it; a runtime adapter reports its own name.
func (s *Service) RuntimeKind() string {
	if s.Runtime == nil {
		return ""
	}
	return s.Runtime.RuntimeKind()
}

// Failure is agent.Failure stamped with this service's runtime identity, so no
// layer hardcodes which runtime served a run.
func (s *Service) Failure(id string, err error) agent.AgentResult {
	result := agent.Failure(id, err)
	result.Runtime = s.RuntimeKind()
	return result
}
func (s *Service) identity() string {
	runtime := s.Config.Runtime
	if runtime.Mode == "managed" {
		runtime.BaseURL = ""
		runtime.Host = "127.0.0.1"
	}
	b, _ := json.Marshal(struct {
		Runtime   config.Runtime
		Directory string
	}{runtime, s.Config.Directory})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
func newID() string {
	b := make([]byte, 16)
	if _, e := rand.Read(b); e != nil {
		panic("secure randomness unavailable")
	}
	return "wrk_" + hex.EncodeToString(b)
}
func validContext(typ, content string) bool {
	switch typ {
	case "file", "symbol", "contract", "text", "other":
		return strings.TrimSpace(content) != ""
	}
	return false
}
func (s *Service) Run(ctx context.Context, raw json.RawMessage, emit agent.Emit) agent.AgentResult {
	var t agent.AgentTask
	if len(raw) > s.Config.General.MaxContextBytes || agent.Strict(raw, &t) != nil || strings.TrimSpace(t.Objective) == "" || len(t.Objective) > 16384 || !config.Name.MatchString(t.Agent) {
		return s.Failure("", agent.E("CONFIG_ERROR"))
	}
	return s.runTask(ctx, t, emit, false)
}

// RunRole is the thin role-native adapter used by the public delegate tools.
// Lifecycle, routing, persistence and runtime execution remain in runTask.
func (s *Service) RunRole(ctx context.Context, role string, raw json.RawMessage, emit agent.Emit) agent.AgentResult {
	var public agent.DelegateTask
	if len(raw) > s.Config.General.MaxContextBytes || agent.Strict(raw, &public) != nil || strings.TrimSpace(public.Prompt) == "" || !config.Name.MatchString(role) {
		return s.Failure("", agent.E("CONFIG_ERROR"))
	}
	t := agent.AgentTask{Agent: role, Objective: public.Prompt, WorkspaceID: public.WorkspaceID}
	return s.runTask(ctx, t, emit, true)
}

func (s *Service) runTask(ctx context.Context, t agent.AgentTask, emit agent.Emit, serverOwnedCapabilities bool) agent.AgentResult {
	for _, item := range t.Context {
		if !validContext(item.Type, item.Content) {
			return s.Failure("", agent.E("CONFIG_ERROR"))
		}
	}
	routing, ok := s.routingFor(t.Agent)
	if !ok {
		return s.Failure("", agent.E("AGENT_MAPPING_NOT_FOUND"))
	}
	if !routing.Exposed {
		return s.Failure("", agent.E("ROLE_NOT_EXPOSED"))
	}
	if serverOwnedCapabilities {
		var policyErr error
		t.Capabilities, policyErr = RoleCapabilities(t.Agent)
		if policyErr != nil || t.Capabilities.ValidateForRole(t.Agent) != nil {
			return s.Failure("", agent.E("ROLE_CAPABILITY_POLICY_INVALID"))
		}
	} else if e := t.Capabilities.Validate(); e != nil {
		// Compatibility path for older internal clients: false values request
		// nothing, while any true execution capability exceeds the V1 policy.
		return s.Failure("", e)
	} else if t.Capabilities.RepoRead {
		// Repository read is enabled only by the server-owned role policy below.
		return s.Failure("", agent.E("CONFIG_ERROR"))
	}
	workspace, workspaceErr := s.resolveWorkspace(t.WorkspaceID, t.Capabilities.RepoRead)
	if workspaceErr != nil {
		return s.Failure("", workspaceErr)
	}
	if t.Capabilities.Shell {
		if err := projectexec.Available(); err != nil {
			return s.Failure("", err)
		}
		lock, err := s.Store.LockWorkspaceWriter(workspace.WorkspaceID)
		if err != nil {
			return s.Failure("", err)
		}
		defer localfs.Unlock(lock)
		active, err := s.Store.ActiveWorkspaceWriter(workspace.WorkspaceID)
		if err != nil {
			return s.Failure("", err)
		}
		if active {
			return s.Failure("", agent.E("WORKSPACE_BUSY"))
		}
	}
	var conversationKey, sessionID string
	sessionRuntime, sharesSessions := s.Runtime.(agent.SessionTaskRuntime)
	if conversation := agent.Conversation(ctx); sharesSessions && conversation != "" {
		sum := sha256.Sum256([]byte(s.identity() + "\x00" + conversation))
		conversationKey = hex.EncodeToString(sum[:])
		lock, err := s.Store.LockConversation(conversationKey)
		if err != nil {
			return s.Failure("", err)
		}
		defer localfs.Unlock(lock)
		previous, err := s.Store.ConversationRun(conversationKey)
		if err != nil {
			return s.Failure("", err)
		}
		switch previous.Status {
		case "RUNNING", "NEEDS_CONTEXT", "NEEDS_DECISION":
			return s.Failure(previous.ID, agent.E("SESSION_BUSY"))
		}
		sessionID = previous.RuntimeSessionID
	}
	// The adapter receives the resolved root through this internal field only.
	// It is excluded from the task JSON, so no public contract can carry a path.
	t.Workspace = &workspace
	modelSource := "agent_default"
	if routing.Model != "" {
		modelSource = "explicit"
	}
	if _, e := s.Runtime.Health(ctx); e != nil {
		s.logger.Error("routing preflight failed", "stage", "health", "code", agent.Safe(e).Code)
		return s.Failure("", e)
	}
	// Only a runtime that owns an agent catalogue can validate the route's agent
	// selector. A catalogue-less runtime has no selector to fill in, so an empty
	// agent is not an unavailable agent and an empty catalogue is not an error.
	// Core branches on this capability, never on a runtime name.
	if s.Runtime.AgentCatalogueSupported() {
		agents, e := s.Runtime.ListAgents(ctx)
		if e != nil {
			s.logger.Error("routing preflight failed", "stage", "agents", "code", agent.Safe(e).Code)
			return s.Failure("", e)
		}
		found := false
		for _, a := range agents {
			if a.Name == routing.Agent {
				found = true
			}
		}
		if !found {
			return s.Failure("", agent.E("AGENT_NOT_FOUND"))
		}
	}
	if routing.Model != "" {
		models, modelErr := s.Runtime.ListModels(ctx)
		if modelErr != nil {
			s.logger.Error("routing preflight failed", "stage", "models", "code", agent.Safe(modelErr).Code)
			return s.Failure("", modelErr)
		}
		available := false
		for _, model := range models {
			available = available || model.ID == routing.Model
		}
		if !available {
			return s.Failure("", agent.E("MODEL_NOT_AVAILABLE"))
		}
	}
	run := agent.Run{ID: newID(), Agent: t.Agent, LogicalRole: t.Agent, RuntimeAgent: routing.Agent, RuntimeModel: routing.Model, ModelSource: modelSource, RoutingRevision: routingRevision(t.Agent, routing), Runtime: s.RuntimeKind(), Status: "RUNNING", Objective: t.Objective, Created: time.Now().UTC(), Updated: time.Now().UTC(), Capabilities: t.Capabilities, Workspace: workspace}
	run.ConversationKey = conversationKey
	run.RuntimeIdentity = s.identity()
	if run.Runtime == "pi" {
		run.RuntimeBinary = s.Config.Runtime.Binary
		run.RuntimeSessionDir = s.Config.Runtime.SessionDir
	}
	lock, e := s.Store.Lock(run.ID)
	if e != nil {
		return s.Failure("", e)
	}
	defer localfs.Unlock(lock)
	if e = s.Store.Create(run); e != nil {
		return s.Failure("", e)
	}
	ctx, cancel := s.taskContext(ctx, run.ID)
	defer cancel()
	progress := s.progress(run, emit)
	progress(agent.Progress{Stage: "STARTING"})
	progress(agent.Progress{Stage: "CONNECTING_RUNTIME"})
	var h agent.RuntimeHandle
	if conversationKey != "" {
		h, e = sessionRuntime.StartTaskInSession(ctx, t, routing.Agent, routing.Model, sessionID)
	} else {
		h, e = s.Runtime.StartTask(ctx, t, routing.Agent, routing.Model)
	}
	if e != nil {
		return s.finish(ctx, run, agent.AgentResult{}, e, progress)
	}
	run.RuntimeSessionID = h.SessionID
	run.RuntimeSessionFile = h.SessionFile
	run.RuntimeControl = h.ControlToken
	if e = s.Store.Save(run, "RUNNING"); e != nil {
		aCtx, c := context.WithTimeout(context.Background(), 5*time.Second)
		defer c()
		_ = s.Runtime.CancelTask(aCtx, h)
		return s.Failure(run.ID, e)
	}
	if run.Capabilities.RepoRead {
		// The read capability exists only while this run executes, and only for
		// the grant frozen above. Registering before the binding re-check means a
		// revocation that lands in this window is recorded against the run and
		// fails it closed instead of racing past it.
		e = s.Store.RegisterRepoAccess(store.RepoAccess{Token: h.ControlToken, RunID: run.ID, SessionID: h.SessionID, WorkspaceID: workspace.WorkspaceID, CanonicalRoot: workspace.CanonicalRoot, GrantRevision: workspace.GrantRevision})
		if e == nil {
			e = s.validateWorkspaceBinding(workspace)
		}
		if e != nil {
			aCtx, c := context.WithTimeout(context.Background(), 5*time.Second)
			defer c()
			_ = s.Runtime.CancelTask(aCtx, h)
			s.releaseRepoAccess(run)
			return s.Failure(run.ID, e)
		}
	}
	progress(agent.Progress{Stage: "SESSION_CREATED"})
	r, e := s.Runtime.ContinueTask(ctx, h, t, progress)
	return s.finish(ctx, run, r, e, progress)
}

// RoleCapabilities is the authoritative server-owned execution policy for
// public role-native delegation. Implementation and Review may inspect exactly
// one user-authorized workspace read-only. Developer alone may modify and test
// the project through the controlled repository and execution tools.
// Control plane question/request_context permissions are enforced separately
// by the runtime adapter and are not capability grants. Every display surface
// (MCP metadata, Desktop diagnostics) reads this same function, so a report
// can never advertise access the runtime withholds.
func RoleCapabilities(role string) (agent.CapabilityPolicy, error) {
	switch role {
	case "developer":
		return agent.DeveloperCapabilities(), nil
	case "implementation", "review":
		return agent.CapabilityPolicy{RepoRead: true}, nil
	case "research":
		return agent.CapabilityPolicy{}, nil
	default:
		return agent.CapabilityPolicy{}, agent.E("ROLE_CAPABILITY_POLICY_INVALID")
	}
}

// RoleCapabilityPolicy reports the same policy for display, where an unknown
// role means no access rather than an error.
func RoleCapabilityPolicy(role string) agent.CapabilityPolicy {
	p, e := RoleCapabilities(role)
	if e != nil {
		return agent.CapabilityPolicy{}
	}
	return p
}

// resolveWorkspace turns a public workspace_id into a frozen binding. An
// explicit id is validated strictly and fails closed; it never falls back to
// another grant. For a role with repository read, the binding is what makes
// the run executable at all: zero grants, or an ambiguous choice among several,
// is a stable error rather than a silent downgrade to context-only execution.
// A context-only role still auto-binds the single ACTIVE grant for continuity.
func (s *Service) resolveWorkspace(id string, repoRead bool) (agent.WorkspaceSnapshot, error) {
	if id != "" {
		return s.workspaceSnapshot(id)
	}
	active, e := s.Store.ActiveWorkspaces()
	if e != nil {
		return agent.WorkspaceSnapshot{}, e
	}
	if !repoRead {
		if len(active) == 1 && s.workspaceDirAvailable(active[0].CanonicalRoot) {
			return snapshotOf(active[0]), nil
		}
		return agent.WorkspaceSnapshot{}, nil
	}
	switch len(active) {
	case 0:
		return agent.WorkspaceSnapshot{}, agent.E("WORKSPACE_REQUIRED")
	case 1:
		if !s.workspaceDirAvailable(active[0].CanonicalRoot) {
			return agent.WorkspaceSnapshot{}, agent.E("WORKSPACE_UNAVAILABLE")
		}
		return snapshotOf(active[0]), nil
	default:
		return agent.WorkspaceSnapshot{}, agent.E("WORKSPACE_SELECTION_REQUIRED")
	}
}

func (s *Service) workspaceSnapshot(id string) (agent.WorkspaceSnapshot, error) {
	w, e := s.Store.Workspace(id)
	if e != nil {
		return agent.WorkspaceSnapshot{}, e
	}
	if w.State != "ACTIVE" {
		return agent.WorkspaceSnapshot{}, agent.E("WORKSPACE_AUTHORIZATION_REVOKED")
	}
	if !s.workspaceDirAvailable(w.CanonicalRoot) {
		return agent.WorkspaceSnapshot{}, agent.E("WORKSPACE_UNAVAILABLE")
	}
	return snapshotOf(w), nil
}

func snapshotOf(w store.Workspace) agent.WorkspaceSnapshot {
	return agent.WorkspaceSnapshot{WorkspaceID: w.ID, CanonicalRoot: w.CanonicalRoot, GrantRevision: w.Revision}
}

func (s *Service) workspaceDirAvailable(root string) bool {
	st, e := os.Stat(root)
	return e == nil && st.IsDir()
}

// validateWorkspaceBinding rechecks a frozen snapshot against the live
// registry. A revoked grant, a changed revision or an unavailable directory
// fails closed; the run is never rebound to another workspace.
func (s *Service) validateWorkspaceBinding(snapshot agent.WorkspaceSnapshot) error {
	if snapshot.WorkspaceID == "" {
		return nil
	}
	w, e := s.Store.Workspace(snapshot.WorkspaceID)
	if e != nil {
		return e
	}
	if w.State != "ACTIVE" || w.Revision != snapshot.GrantRevision || w.CanonicalRoot != snapshot.CanonicalRoot {
		return agent.E("WORKSPACE_AUTHORIZATION_REVOKED")
	}
	if !s.workspaceDirAvailable(w.CanonicalRoot) {
		return agent.E("WORKSPACE_UNAVAILABLE")
	}
	return nil
}

// AddWorkspaceByPath records a workspace authorization for a canonical directory
// path. This is the MCP tool entry point: the path is validated, canonicalized,
// and recorded with source "mcp_client_grant". The user can see and revoke it
// through the Desktop UI at any time.
func (s *Service) AddWorkspaceByPath(path string) (store.Workspace, bool, error) {
	canonical, e := localfs.CanonicalDir(path)
	if e != nil {
		return store.Workspace{}, false, agent.E("WORKSPACE_INVALID")
	}
	return s.Store.AddWorkspace(canonical, filepath.Base(canonical), "mcp_client_grant")
}

// ListWorkspaces returns all workspace grants (active and revoked) for display.
func (s *Service) ListWorkspaces() ([]store.Workspace, error) {
	return s.Store.Workspaces()
}

// ActiveWorkspaces returns only the active workspace grants.
func (s *Service) ActiveWorkspaces() ([]store.Workspace, error) {
	return s.Store.ActiveWorkspaces()
}

// RevokeWorkspace durably revokes a workspace grant. The record is retained for
// audit; the revision increments so stale snapshots fail closed.
func (s *Service) RevokeWorkspace(id string) error {
	return s.Store.RevokeWorkspace(id)
}

// RoleExposed reports whether a logical role can be offered to a parent agent
// right now. It is the single place that decides exposure, so the public tool
// surface, the Desktop projection and an actual run can never disagree about
// which runtime needs which routing fields.
func (s *Service) RoleExposed(role string) bool {
	routing, ok := s.routingFor(role)
	return ok && routing.Exposed
}

func (s *Service) routingFor(role string) (config.AgentRouting, bool) {
	agents := s.Config.Agents
	path := filepath.Join(s.Config.Directory, "config.toml")
	if loaded, e := config.Load(path); e == nil && loaded.Runtime == s.Config.Runtime && loaded.Storage == s.Config.Storage {
		agents = loaded.Agents
	}
	routing, ok := agents[role]
	if !ok {
		return routing, false
	}
	// The agent selector is a runtime-native name, so whether a route needs one
	// depends on the runtime's catalogue, not on the role. A catalogue-less
	// runtime routes on the model alone.
	hasCatalogue := s.Runtime != nil && s.Runtime.AgentCatalogueSupported()
	return routing, routing.Agent != "" || !hasCatalogue
}

func routingRevision(role string, routing config.AgentRouting) string {
	b, _ := json.Marshal(struct {
		Role    string
		Routing config.AgentRouting
	}{role, routing})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:16])
}
func (s *Service) Continue(ctx context.Context, raw json.RawMessage, emit agent.Emit) agent.AgentResult {
	var input agent.Continuation
	if len(raw) > s.Config.General.MaxContextBytes || agent.Strict(raw, &input) != nil {
		return s.Failure("", agent.E("CONTINUATION_INVALID"))
	}
	lock, e := s.Store.Lock(input.WorkerRunID)
	if e != nil {
		return s.Failure(input.WorkerRunID, e)
	}
	defer localfs.Unlock(lock)
	r, e := s.Store.Get(input.WorkerRunID)
	if e != nil {
		return s.Failure(input.WorkerRunID, e)
	}
	bindSinglePending(&input, r)
	if e = validateContinuation(r, input); e != nil {
		return s.Failure(r.ID, e)
	}
	if r.Capabilities.ValidateForRole(r.LogicalRole) != nil {
		return s.Failure(r.ID, agent.E("ROLE_CAPABILITY_POLICY_INVALID"))
	}
	if e := s.validateWorkspaceBinding(r.Workspace); e != nil {
		return s.Failure(r.ID, e)
	}
	if r.RuntimeIdentity != s.identity() {
		return s.Failure(r.ID, agent.E("CONTINUATION_INVALID"))
	}
	if r.Capabilities.RepoWrite {
		lock, err := s.Store.LockWorkspaceWriter(r.Workspace.WorkspaceID)
		if err != nil {
			return s.Failure(r.ID, err)
		}
		defer localfs.Unlock(lock)
	}
	previous := r.Status
	r.Status = "RUNNING"
	r.Result = agent.AgentResult{}
	if e = s.Store.Save(r, previous); e != nil {
		return s.Failure(r.ID, e)
	}
	ctx, cancel := s.taskContext(ctx, r.ID)
	defer cancel()
	progress := s.progress(r, emit)
	progress(agent.Progress{Stage: "CONNECTING_RUNTIME"})
	// Only answers are sent. The original transcript belongs to the existing runtime session.
	// Translate public aliases only after exact pending validation. Older persisted
	// runs retain their former run-prefixed compatibility mapping.
	for i := range input.AdditionalContext {
		input.AdditionalContext[i].RequestID = nativeRequestID(input.AdditionalContext[i].RequestID, "context_", r.PendingContextIDs, r.PendingContextBase, r.ID)
	}
	for i := range input.DecisionResponses {
		input.DecisionResponses[i].RequestID = nativeRequestID(input.DecisionResponses[i].RequestID, "decision_", r.PendingDecisionIDs, r.PendingDecisionBase, r.ID)
	}
	var result agent.AgentResult
	handle := runtimeHandle(r)
	if previous == "NEEDS_DECISION" {
		rejected := false
		for _, answer := range input.DecisionResponses {
			rejected = rejected || answer.Reject
		}
		if rejected {
			e = s.Runtime.CancelTask(ctx, handle)
			if e == nil {
				e = context.Canceled
			}
		} else if runtime, ok := s.Runtime.(agent.DecisionRuntime); ok {
			result, e = runtime.ResumeDecision(ctx, handle, r.RuntimeDecision, input.DecisionResponses, progress)
		} else {
			e = agent.E("PENDING_DECISION_LOST")
		}
	} else if runtime, ok := s.Runtime.(agent.ContextRuntime); ok {
		result, e = runtime.ResumeContext(ctx, handle, r.RuntimeContext, input.AdditionalContext, progress)
	} else {
		e = agent.E("PENDING_CONTEXT_LOST")
	}
	return s.finish(ctx, r, result, e, progress)
}

func bindSinglePending(c *agent.Continuation, r agent.Run) {
	if r.Status == "NEEDS_CONTEXT" && len(r.Result.ContextRequests) == 1 && len(c.AdditionalContext) == 1 && c.AdditionalContext[0].RequestID == "" {
		c.AdditionalContext[0].RequestID = r.Result.ContextRequests[0].ID
	}
	if r.Status == "NEEDS_DECISION" && len(r.Result.DecisionRequests) == 1 && len(c.DecisionResponses) == 1 && c.DecisionResponses[0].RequestID == "" {
		c.DecisionResponses[0].RequestID = r.Result.DecisionRequests[0].ID
	}
}

func nativeRequestID(alias, prefix string, native []string, base int, runID string) string {
	if strings.HasPrefix(alias, prefix) {
		var index int
		if _, e := fmt.Sscanf(strings.TrimPrefix(alias, prefix), "%d", &index); e == nil && index > base && index <= base+len(native) {
			return native[index-base-1]
		}
	}
	return strings.TrimPrefix(alias, runID+"_")
}

func runtimeHandle(r agent.Run) agent.RuntimeHandle {
	h := agent.RuntimeHandle{SessionID: r.RuntimeSessionID, Agent: r.RuntimeAgent, Model: r.RuntimeModel, ModelSource: r.ModelSource, DecisionState: r.RuntimeDecision, ContextState: r.RuntimeContext, ControlToken: r.RuntimeControl, RepoWrite: r.Capabilities.RepoWrite, Shell: r.Capabilities.Shell}
	if r.Capabilities.RepoRead {
		h.RepoRead = true
		h.WorkspaceRoot = r.Workspace.CanonicalRoot
	}
	return h
}

// releaseRepoAccess ends a run's durable read grant once it can no longer
// execute. Terminal states never regain repository access, not even through an
// explicit continuation, so the record is removed instead of left to expire.
func (s *Service) releaseRepoAccess(r agent.Run) {
	if r.Capabilities.RepoRead {
		s.Store.DeleteRepoAccess(r.ID)
		if r.Capabilities.RepoWrite {
			projectexec.Cleanup(s.Store.Directory, r.ID)
		}
	}
}
func validateContinuation(r agent.Run, c agent.Continuation) error {
	if r.Status != "NEEDS_CONTEXT" && r.Status != "NEEDS_DECISION" {
		return agent.E("CONTINUATION_INVALID")
	}
	seen := map[string]bool{}
	if r.Status == "NEEDS_CONTEXT" {
		if len(c.DecisionResponses) != 0 || len(c.AdditionalContext) != len(r.Result.ContextRequests) {
			return agent.E("INVALID_CONTEXT_RESPONSE")
		}
		pending := map[string]agent.ContextRequest{}
		for _, q := range r.Result.ContextRequests {
			pending[q.ID] = q
		}
		for _, a := range c.AdditionalContext {
			q, ok := pending[a.RequestID]
			if !ok || seen[a.RequestID] || !validContext(a.Type, a.Content) || (q.Kind == "file" && (a.Type != "file" || a.Path != q.Target)) {
				return agent.E("INVALID_CONTEXT_RESPONSE")
			}
			seen[a.RequestID] = true
		}
	} else {
		if len(c.AdditionalContext) != 0 || len(c.DecisionResponses) != len(r.Result.DecisionRequests) {
			return agent.E("INVALID_DECISION_RESPONSE")
		}
		pending := map[string]agent.DecisionRequest{}
		for _, q := range r.Result.DecisionRequests {
			pending[q.ID] = q
		}
		for _, a := range c.DecisionResponses {
			q, ok := pending[a.RequestID]
			if !ok || seen[a.RequestID] || agent.ValidateDecisionResponse(q, a) != nil {
				return agent.E("INVALID_DECISION_RESPONSE")
			}

			seen[a.RequestID] = true
		}
	}
	return nil
}
func (s *Service) progress(r agent.Run, emit agent.Emit) agent.Emit {
	var mu sync.Mutex
	var last string
	var lastAt time.Time
	return func(p agent.Progress) {
		if emit == nil {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if p.Stage == last && time.Since(lastAt) < 100*time.Millisecond {
			return
		}
		last = p.Stage
		lastAt = time.Now()
		p.WorkerRunID = r.ID
		p.ElapsedMS = time.Since(r.Created).Milliseconds()
		p.Agent = r.Agent
		p.Runtime = s.RuntimeKind()
		emit(p)
	}
}
func (s *Service) finish(ctx context.Context, r agent.Run, result agent.AgentResult, err error, emit agent.Emit) agent.AgentResult {
	// Suspensions are runtime control state, never final assistant content.
	var contextPending *agent.ContextPending
	if errors.As(err, &contextPending) && ctx.Err() == nil {
		ids := requestContextIDs(contextPending.Requests)
		if overlaps(ids, r.UsedContextIDs) {
			err = agent.E("INVALID_AGENT_RESULT")
		} else {
			result = agent.AgentResult{Status: "needs_context", Summary: "Required context requested; runtime execution is suspended.", ContextRequests: contextPending.Requests}
			r.RuntimeContext = contextPending.State
			r.PendingContextIDs = ids
			r.UsedContextIDs = append(r.UsedContextIDs, ids...)
			r.PendingContextBase = r.NextContextAlias
			r.NextContextAlias += len(contextPending.Requests)
			err = nil
		}
	}
	var pending *agent.DecisionPending
	if errors.As(err, &pending) && ctx.Err() == nil {
		result = agent.AgentResult{Status: "needs_decision", Summary: "Parent decision required; runtime execution is suspended.", DecisionRequests: pending.Requests}
		r.RuntimeDecision = pending.State
		r.PendingDecisionIDs = requestDecisionIDs(pending.Requests)
		r.PendingDecisionBase = r.NextDecisionAlias
		r.NextDecisionAlias += len(pending.Requests)
		err = nil
	}
	// Cancellation/timeout always overrides a racing final result.
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	if s.Store.CancelRequested(r.ID) {
		// A workspace revocation raises the same durable cancellation bit but
		// carries a reason. Reporting it keeps the failure specific instead of
		// collapsing an authorization loss into a plain user CANCELLED.
		if reason := s.Store.AbortReason(r.ID); reason != "" {
			err = agent.E(reason)
		} else {
			err = context.Canceled
		}
	}
	if err == nil {
		if result.Status == "completed" {
			if result.Agent != r.RuntimeAgent || result.Model == "" || (r.ModelSource == "explicit" && result.Model != r.RuntimeModel) {
				err = agent.E("INVALID_AGENT_RESULT")
			} else if r.ModelSource == "agent_default" && r.RuntimeModel == "" {
				r.RuntimeModel = result.Model
			}
		}
	}
	if err == nil {
		if e := agent.ValidateResult(result); e != nil {
			err = e
		}
	}
	if err != nil {
		code := agent.Safe(err).Code
		if errors.Is(err, context.Canceled) {
			code = "CANCELLED"
		}
		if errors.Is(err, context.DeadlineExceeded) {
			code = "TIMEOUT"
		}
		originalCode := code
		if r.RuntimeSessionID != "" && code != "SESSION_LOST" && code != "PENDING_DECISION_LOST" && code != "PENDING_CONTEXT_LOST" {
			aCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			abort := s.Runtime.CancelTask(aCtx, runtimeHandle(r))
			cancel()
			if abort != nil {
				code = "ABORT_UNCONFIRMED"
			}
		}
		if code != originalCode {
			s.logger.Error("runtime failure cleanup changed result", "worker_run_id", r.ID, "original_code", originalCode, "result_code", code)
		}
		result = s.Failure(r.ID, agent.E(code))
		if code == "CANCELLED" {
			result.Status = "cancelled"
			result.Summary = "Task cancelled; runtime abort confirmed."
		}
	}
	result.WorkerRunID = r.ID
	for i := range result.ContextRequests {
		result.ContextRequests[i].ID = fmt.Sprintf("context_%d", r.PendingContextBase+i+1)
	}
	for i := range result.DecisionRequests {
		result.DecisionRequests[i].ID = fmt.Sprintf("decision_%d", r.PendingDecisionBase+i+1)
	}
	result.Agent = r.RuntimeAgent
	result.Model = r.RuntimeModel
	result.Runtime = s.RuntimeKind()
	if result.Status != "needs_decision" {
		r.RuntimeDecision = nil
		r.PendingDecisionIDs = nil
		r.PendingDecisionBase = 0
	}
	if result.Status != "needs_context" {
		r.RuntimeContext = nil
		r.PendingContextIDs = nil
		r.PendingContextBase = 0
	}
	r.Result = result
	r.Status = strings.ToUpper(result.Status)
	if e := s.Store.Save(r, "RUNNING"); e != nil {
		current, getErr := s.Store.Get(r.ID)
		if getErr == nil && (current.Status == "CANCELLED" || current.Status == "FAILED") {
			return current.Result
		}
		if getErr == nil && current.Status == "RUNNING" && result.Status != "cancelled" && result.Status != "failed" && s.Store.CancelRequested(r.ID) {
			return s.finish(ctx, current, agent.AgentResult{}, context.Canceled, emit)
		}
		if result.Status == "needs_decision" || result.Status == "needs_context" {
			abortCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			abortErr := s.Runtime.CancelTask(abortCtx, runtimeHandle(r))
			cancel()
			if abortErr != nil {
				e = agent.E("ABORT_UNCONFIRMED")
			}
		}
		failure := s.Failure(r.ID, e)
		r.RuntimeDecision = nil
		r.RuntimeContext = nil
		r.Status = "FAILED"
		r.Result = failure
		_ = s.Store.Save(r, "RUNNING")
		s.releaseRepoAccess(r)
		return failure
	}
	if r.Status != "NEEDS_CONTEXT" && r.Status != "NEEDS_DECISION" {
		s.releaseRepoAccess(r)
	}
	stage := r.Status
	if stage == "NEEDS_CONTEXT" {
		stage = "WAITING_FOR_CONTEXT"
	}
	if stage == "NEEDS_DECISION" {
		stage = "WAITING_FOR_DECISION"
	}
	emit(agent.Progress{Stage: stage})
	if result.Error != nil {
		s.logger.Error("worker state", "worker_run_id", r.ID, "status", r.Status, "code", result.Error.Code)
	} else {
		s.logger.Info("worker state", "worker_run_id", r.ID, "status", r.Status)
	}
	return result
}

func requestContextIDs(requests []agent.ContextRequest) []string {
	ids := make([]string, len(requests))
	for i := range requests {
		ids[i] = requests[i].ID
	}
	return ids
}
func requestDecisionIDs(requests []agent.DecisionRequest) []string {
	ids := make([]string, len(requests))
	for i := range requests {
		ids[i] = requests[i].ID
	}
	return ids
}
func overlaps(ids, used []string) bool {
	seen := make(map[string]bool, len(used))
	for _, id := range used {
		seen[id] = true
	}
	for _, id := range ids {
		if seen[id] {
			return true
		}
	}
	return false
}
func (s *Service) Cancel(ctx context.Context, id string) agent.AgentResult {
	r, e := s.Store.Get(id)
	if e != nil {
		return s.Failure(id, e)
	}
	if r.RuntimeIdentity != s.identity() {
		return s.Failure(id, agent.E("CONTINUATION_INVALID"))
	}
	if r.Status == "COMPLETED" || r.Status == "FAILED" || r.Status == "CANCELLED" {
		return s.Failure(id, agent.E("CONTINUATION_INVALID"))
	}
	// Abort does not acquire the execution lock: it must interrupt an in-flight call.
	if e = s.Store.RequestCancel(id); e != nil {
		return s.Failure(id, e)
	}
	c, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	e = s.Runtime.CancelTask(c, runtimeHandle(r))
	// The durable cancellation intent also stops a dispatch racing with this abort.
	// Do not report cancellation before the executing process releases its lock.
	deadline := time.NewTimer(7 * time.Second)
	defer deadline.Stop()
	for {
		lock, lockErr := s.Store.Lock(id)
		if lockErr == nil {
			defer localfs.Unlock(lock)
			break
		}
		select {
		case <-deadline.C:
			return s.unconfirmedCancel(id)
		case <-ctx.Done():
			return s.unconfirmedCancel(id)
		case <-time.After(20 * time.Millisecond):
		}
	}
	current, getErr := s.Store.Get(id)
	if getErr != nil {
		return s.Failure(id, getErr)
	}
	if current.Status == "CANCELLED" || current.Status == "FAILED" {
		return current.Result
	}
	r = current
	result := agent.AgentResult{WorkerRunID: id, Status: "cancelled", Summary: "Task cancelled; runtime abort confirmed.", Runtime: s.RuntimeKind(), Agent: r.Agent}
	if e != nil {
		result = s.Failure(id, agent.E("ABORT_UNCONFIRMED"))
	}
	previous := r.Status
	r.Status = strings.ToUpper(result.Status)
	r.RuntimeDecision = nil
	r.RuntimeContext = nil
	r.Result = result
	if e = s.Store.Save(r, previous); e != nil {
		return s.Failure(id, e)
	}
	s.releaseRepoAccess(r)
	return result
}

func (s *Service) unconfirmedCancel(id string) agent.AgentResult {
	failure := s.Failure(id, agent.E("ABORT_UNCONFIRMED"))
	r, e := s.Store.Get(id)
	if e != nil {
		return failure
	}
	if r.Status == "COMPLETED" || r.Status == "FAILED" || r.Status == "CANCELLED" {
		return r.Result
	}
	previous := r.Status
	r.Status = "FAILED"
	r.Result = failure
	_ = s.Store.Save(r, previous)
	s.releaseRepoAccess(r)
	return failure
}

// Only the task deadline is refreshed. Runtime identity and suspended routing
// stay frozen, and an already executing call retains its original deadline.
func (s *Service) currentTaskTimeout() time.Duration {
	s.timeoutMu.Lock()
	defer s.timeoutMu.Unlock()
	if s.Config.Directory != "" {
		loaded, err := config.Load(filepath.Join(s.Config.Directory, "config.toml"))
		if err == nil && loaded.Runtime == s.Config.Runtime && loaded.Storage == s.Config.Storage {
			s.taskTimeout = time.Duration(loaded.General.TaskTimeoutSeconds) * time.Second
		}
	}
	return s.taskTimeout
}

// Polls only this communication-state cancellation bit, not runtime history.
func (s *Service) taskContext(parent context.Context, id string) (context.Context, func()) {
	ctx, cancel := context.WithTimeout(parent, s.currentTaskTimeout())
	done := make(chan struct{})
	go func() {
		defer close(done)
		tick := time.NewTicker(20 * time.Millisecond)
		defer tick.Stop()
		for {
			if s.Store.CancelRequested(id) {
				cancel()
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
		}
	}()
	return ctx, func() { cancel(); <-done }
}

// Recover interrupted owners without disturbing work in other live processes.
// No execution is resumed automatically; only an explicit continuation may run.
func (s *Service) Recover() {
	runs, e := s.Store.Running()
	if e != nil {
		return
	}
	for _, r := range runs {
		if r.Status != "RUNNING" {
			continue
		}
		lock, e := s.Store.Lock(r.ID)
		if e != nil {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		code := "RUNTIME_UNAVAILABLE"
		if r.RuntimeIdentity != s.identity() || s.Runtime.CancelTask(ctx, runtimeHandle(r)) != nil {
			code = "ABORT_UNCONFIRMED"
		}
		cancel()
		r.Status = "FAILED"
		r.Result = s.Failure(r.ID, agent.E(code))
		_ = s.Store.Save(r, "RUNNING")
		s.releaseRepoAccess(r)
		localfs.Unlock(lock)
	}
}
