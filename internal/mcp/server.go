// Package mcp maps neutral AgentService calls onto the official MCP SDK.
package mcp

import (
	"ai-worker-mcp/internal/agent"
	"ai-worker-mcp/internal/service"
	"context"
	"encoding/json"
	"github.com/google/jsonschema-go/jsonschema"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"io"
	"log/slog"
	"sync"
)

func schema[T any]() any {
	s, e := jsonschema.For[T](nil)
	if e != nil {
		panic(e)
	}
	return s
}
func New(s *service.Service) *sdk.Server {
	exposed := make([]roleMetadata, 0, len(roles))
	for _, role := range roles {
		if s.RoleExposed(role.Name) {
			// Read the policy from the service so metadata cannot drift from what
			// the runtime will actually enforce for this role.
			policy := service.RoleCapabilityPolicy(role.Name)
			role.RepoRead = policy.RepoRead
			role.RepoWrite = policy.RepoWrite
			role.Shell = policy.Shell
			exposed = append(exposed, role)
		}
	}
	workspaces := []string{}
	if active, e := s.Store.ActiveWorkspaces(); e == nil {
		for _, w := range active {
			source := w.Source
			if source == "" {
				source = "unknown"
			}
			workspaces = append(workspaces, w.ID+" · "+w.Label+" · "+source)
		}
	}
	instructions := serverInstructions(exposed, workspaces)
	if _, ok := s.Runtime.(agent.SessionTaskRuntime); ok {
		instructions += "\n\nDelegations in the same Codex conversation share one durable worker session, including history from earlier roles and workspaces. Every delegate call still creates a new worker_run_id with its own model routing and permissions. Delegate sequentially. If SESSION_BUSY is returned, wait for the active call or continue/cancel the suspended worker_run_id before starting another task. Reconnecting MCP preserves this mapping through Codex's threadId metadata; requests without threadId use isolated sessions. Workers do not inherit the full Codex chat."
	}
	server := sdk.NewServer(&sdk.Implementation{Name: "ai-worker-mcp", Version: "0.3.0"}, &sdk.ServerOptions{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Instructions: instructions})
	add := func(name, title, description string, input any, call func(context.Context, json.RawMessage, agent.Emit) agent.AgentResult) {
		server.AddTool(&sdk.Tool{Name: name, Title: title, Description: description, InputSchema: input, OutputSchema: schema[agent.AgentResult]()}, func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			var mu sync.Mutex
			var count float64
			emit := func(p agent.Progress) {
				token := req.Params.GetProgressToken()
				if token == nil {
					return
				}
				mu.Lock()
				defer mu.Unlock()
				count++
				b, _ := json.Marshal(p)
				_ = req.Session.NotifyProgress(ctx, &sdk.ProgressNotificationParams{ProgressToken: token, Progress: count, Message: string(b)})
			}
			// Codex injects threadId into tools/call metadata. It is distinct
			// from the MCP transport session, which may reconnect or be shared.
			if threadID, ok := req.Params.GetMeta()["threadId"].(string); ok && threadID != "" && len(threadID) <= 256 {
				ctx = agent.WithConversation(ctx, threadID)
			}
			r := call(ctx, req.Params.Arguments, emit)
			body := r.Content
			if r.Status != "completed" {
				b, _ := json.Marshal(r)
				body = string(b)
			}
			return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: body}}, StructuredContent: r, IsError: r.Status == "failed"}, nil
		})
	}
	for _, role := range exposed {
		role := role
		add("delegate_"+role.Name, role.Title, role.Description, schema[agent.DelegateTask](), func(ctx context.Context, raw json.RawMessage, emit agent.Emit) agent.AgentResult {
			return s.RunRole(ctx, role.Name, raw, emit)
		})
	}
	add("ai_worker_continue", "Continue External Agent Run", continueDescription, schema[agent.Continuation](), s.Continue)
	type cancelInput struct {
		WorkerRunID string `json:"worker_run_id" jsonschema:"Durable llmTools run identity returned by a delegate tool. Use the same value for continuation/cancellation."`
	}
	add("ai_worker_cancel", "Cancel External Agent Run", cancelDescription, schema[cancelInput](), func(ctx context.Context, raw json.RawMessage, _ agent.Emit) agent.AgentResult {
		var in cancelInput
		if agent.Strict(raw, &in) != nil {
			return s.Failure("", agent.E("CONTINUATION_INVALID"))
		}
		return s.Cancel(ctx, in.WorkerRunID)
	})
	// Workspace authorization tools: allow MCP clients to manage workspace
	// authorizations. These return their own result types, not AgentResult.
	addWorkspace := func(name, title, description string, input any, call func(context.Context, json.RawMessage) (any, error)) {
		server.AddTool(&sdk.Tool{Name: name, Title: title, Description: description, InputSchema: input}, func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			r, e := call(ctx, req.Params.Arguments)
			if e != nil {
				errResult := map[string]any{"error": agent.Safe(e).Code, "message": agent.Safe(e).Message}
				b, _ := json.Marshal(errResult)
				return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: string(b)}}, IsError: true}, nil
			}
			b, _ := json.Marshal(r)
			return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: string(b)}}}, nil
		})
	}
	type addWorkspaceInput struct {
		Path  string `json:"path" jsonschema:"Absolute path to the directory to authorize. Must exist and be a directory."`
		Label string `json:"label,omitempty" jsonschema:"Optional human-readable label for this workspace. Defaults to the directory basename."`
	}
	addWorkspace("ai_worker_add_workspace", "Authorize Workspace", addWorkspaceDescription, schema[addWorkspaceInput](), func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in addWorkspaceInput
		if agent.Strict(raw, &in) != nil {
			return nil, agent.E("WORKSPACE_INVALID")
		}
		w, created, e := s.AddWorkspaceByPath(in.Path)
		if e != nil {
			return nil, e
		}
		return map[string]any{
			"workspace": w,
			"created":   created,
			"message":   workspaceGrantMessage(created),
		}, nil
	})
	addWorkspace("ai_worker_list_workspaces", "List Authorized Workspaces", listWorkspacesDescription, schema[struct{}](), func(ctx context.Context, raw json.RawMessage) (any, error) {
		workspaces, e := s.ListWorkspaces()
		if e != nil {
			return nil, e
		}
		return map[string]any{"workspaces": workspaces, "count": len(workspaces)}, nil
	})
	type revokeWorkspaceInput struct {
		WorkspaceID string `json:"workspace_id" jsonschema:"The workspace ID to revoke. Use ai_worker_list_workspaces to find workspace IDs."`
	}
	addWorkspace("ai_worker_revoke_workspace", "Revoke Workspace Authorization", revokeWorkspaceDescription, schema[revokeWorkspaceInput](), func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in revokeWorkspaceInput
		if agent.Strict(raw, &in) != nil {
			return nil, agent.E("WORKSPACE_INVALID")
		}
		if e := s.RevokeWorkspace(in.WorkspaceID); e != nil {
			return nil, e
		}
		return map[string]any{"revoked": true, "workspace_id": in.WorkspaceID, "message": "Workspace authorization revoked. Historical run records and audit information are retained."}, nil
	})
	return server
}
