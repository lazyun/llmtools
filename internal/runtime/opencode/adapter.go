package opencode

import (
	"ai-worker-mcp/internal/agent"
	"context"
	"encoding/json"
)

// Kind is the runtime identity Core records for runs served here.
const Kind = "opencode"

// Adapter lazily starts managed runtimes; MCP initialization never starts one.
type Adapter struct{ Manager *Manager }

func (a *Adapter) RuntimeKind() string { return Kind }

// AgentCatalogueSupported is true because the managed OpenCode server owns a
// real agent catalogue, so a configured routing agent name is meaningful and
// Core validates it against ListAgents.
func (a *Adapter) AgentCatalogueSupported() bool { return true }

// client returns the connection for one execution. A repository-read run is
// re-scoped to its own frozen workspace directory, so a shared managed server
// never gives two runs the same filesystem authority. Ownership, lease and
// fencing are unchanged: only the per-request directory scope differs.
func (a *Adapter) client(ctx context.Context, start, repoRead bool, root string) (*Client, error) {
	c, e := a.Manager.Connect(ctx, start)
	if e != nil {
		return nil, e
	}
	a.identify(ctx, c)
	if repoRead {
		if root == "" {
			return nil, agent.E("WORKSPACE_ACCESS_DENIED")
		}
		c = a.Manager.Scoped(c, root)
	}
	return c, nil
}

func (a *Adapter) handle(ctx context.Context, start bool, h agent.RuntimeHandle) (*Client, error) {
	return a.client(ctx, start, h.RepoRead, h.WorkspaceRoot)
}

func (a *Adapter) Health(ctx context.Context) (agent.Health, error) {
	c, e := a.client(ctx, true, false, "")
	if e != nil {
		return agent.Health{}, e
	}
	return c.Health(ctx)
}
func (a *Adapter) ListAgents(ctx context.Context) ([]agent.AgentInfo, error) {
	c, e := a.client(ctx, true, false, "")
	if e != nil {
		return nil, e
	}
	return c.ListAgents(ctx)
}
func (a *Adapter) ListModels(ctx context.Context) ([]agent.RuntimeModel, error) {
	c, e := a.client(ctx, true, false, "")
	if e != nil {
		return nil, e
	}
	return c.ListModels(ctx)
}
func (a *Adapter) StartTask(ctx context.Context, t agent.AgentTask, name, model string) (agent.RuntimeHandle, error) {
	root := ""
	if t.Workspace != nil {
		root = t.Workspace.CanonicalRoot
	}
	c, e := a.client(ctx, true, t.Capabilities.RepoRead, root)
	if e != nil {
		return agent.RuntimeHandle{}, e
	}
	return c.StartTask(ctx, t, name, model)
}
func (a *Adapter) ContinueTask(ctx context.Context, h agent.RuntimeHandle, input any, emit agent.Emit) (agent.AgentResult, error) {
	c, e := a.handle(ctx, true, h)
	if e != nil {
		return agent.AgentResult{}, e
	}
	return c.ContinueTask(ctx, h, input, emit)
}
func (a *Adapter) CancelTask(ctx context.Context, h agent.RuntimeHandle) error {
	if h.SessionID == "" {
		return nil
	}
	c, e := a.handle(ctx, false, h)
	if e != nil {
		return agent.E("ABORT_UNCONFIRMED")
	}
	if len(h.DecisionState) > 0 {
		if _, e := c.decisionIdentity(h.DecisionState, h); e != nil {
			return e
		}
	}
	if len(h.ContextState) > 0 {
		if _, e := c.contextIdentity(h.ContextState, h); e != nil {
			return e
		}
	}
	err := c.CancelTask(ctx, h)
	if err != nil && a.Manager.Owned() {
		_ = a.Manager.Close()
	}
	return err
}
func (a *Adapter) SubscribeProgress(ctx context.Context, h agent.RuntimeHandle, emit agent.Emit) (<-chan error, error) {
	c, e := a.handle(ctx, true, h)
	if e != nil {
		return nil, e
	}
	return c.SubscribeProgress(ctx, h, emit)
}

func (a *Adapter) identify(ctx context.Context, c *Client) {
	if a.Manager.Config.Runtime.Mode == "managed" {
		r := a.Manager.Snapshot(ctx).Instance
		c.InstanceID = r.ID
		c.Generation = r.Generation
	}
}
func (a *Adapter) ResumeDecision(ctx context.Context, h agent.RuntimeHandle, state json.RawMessage, answers []agent.DecisionResponse, emit agent.Emit) (agent.AgentResult, error) {
	c, e := a.handle(ctx, false, h)
	if e != nil {
		return agent.AgentResult{}, agent.E("PENDING_DECISION_LOST")
	}
	return c.ResumeDecision(ctx, h, state, answers, emit)
}

func (a *Adapter) ResumeContext(ctx context.Context, h agent.RuntimeHandle, state json.RawMessage, answers []agent.AdditionalContext, emit agent.Emit) (agent.AgentResult, error) {
	c, e := a.handle(ctx, false, h)
	if e != nil {
		return agent.AgentResult{}, agent.E("PENDING_CONTEXT_LOST")
	}
	return c.ResumeContext(ctx, h, state, answers, emit)
}
