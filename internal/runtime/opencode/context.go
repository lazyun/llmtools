package opencode

import (
	"ai-worker-mcp/internal/agent"
	"ai-worker-mcp/internal/store"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const controlMCPName = "llmtools_control"
const contextToolName = controlMCPName + "_request_context"

type contextToolInput struct {
	ControlToken string                 `json:"control_token"`
	Requests     []agent.ContextRequest `json:"requests"`
}

func contextRequestsValid(requests []agent.ContextRequest) bool {
	return agent.ContextRequestsValid(requests)
}

func runContextControlMCP(dbPath string) int {
	if !filepath.IsAbs(dbPath) || filepath.Base(dbPath) == "." {
		return 2
	}
	st, e := store.Open(dbPath)
	if e != nil {
		return 2
	}
	defer st.Close()
	server := sdk.NewServer(&sdk.Implementation{Name: controlMCPName, Version: "0.3.0"}, &sdk.ServerOptions{Instructions: "request_context is a blocking control-plane handoff to the parent Codex agent. Repository tools are confined to the workspace authorized for the calling run. Only the developer role may use repo_write, repo_edit and sandboxed repo_exec; the other roles retain their read-only or context-only policies."})
	bound := os.Getenv("LLMTOOLS_CONTROL_TOKEN")
	if e = addRepoTools(server, st, bound); e != nil {
		return 2
	}
	if e = addContextControlTool(server, st, bound); e != nil {
		return 2
	}
	if e = server.Run(context.Background(), &sdk.StdioTransport{MaxLineLength: 4*1024*1024 + 131072}); e != nil && !errors.Is(e, context.Canceled) {
		return 1
	}
	return 0
}

// contextToolSchema is the input schema the executing model sees. Core accepts
// exactly agent.ContextRequestKinds, and a schema inferred from the struct alone
// leaves "kind" an open string: a real model then sends kind="fact" for a value
// that is not a file, and its suspension is refused. Naming the accepted values
// and the purpose of each field is what keeps the handoff usable.
func contextToolSchema(boundToken ...string) (*jsonschema.Schema, error) {
	s, e := jsonschema.For[contextToolInput](nil)
	if e != nil {
		return nil, e
	}
	if len(boundToken) > 0 && boundToken[0] != "" {
		hideControlToken(s)
	}
	requests := s.Properties["requests"]
	if requests == nil || requests.Items == nil || len(requests.Items.Properties) == 0 {
		return nil, agent.E("INTERNAL_ERROR")
	}
	for name, description := range map[string]string{
		"id":     "Unique request id for this session.",
		"kind":   "One of: file, symbol, contract, other.",
		"target": "What is needed, named as precisely as possible.",
		"reason": "Why this execution cannot continue without it.",
	} {
		property := requests.Items.Properties[name]
		if property == nil {
			return nil, agent.E("INTERNAL_ERROR")
		}
		property.Description = description
	}
	kind := requests.Items.Properties["kind"]
	kind.Enum = make([]any, 0, len(agent.ContextRequestKinds))
	for _, k := range agent.ContextRequestKinds {
		kind.Enum = append(kind.Enum, k)
	}
	return s, nil
}

// addContextControlTool registers the blocking context handoff on the control
// MCP server, so a test can exercise the same boundary the runtime starts as a
// child process.
func addContextControlTool(server *sdk.Server, st *store.Store, boundToken ...string) error {
	schema, e := contextToolSchema(boundToken...)
	if e != nil {
		return e
	}
	server.AddTool(&sdk.Tool{Name: "request_context", Description: "Suspend this execution and request missing context from the parent Codex agent: a file, a symbol, a contract, or another kind of fact. Wait for the parent response before continuing.", InputSchema: schema}, func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		var input contextToolInput
		reason := "PAYLOAD_INVALID"
		switch {
		case json.Unmarshal(req.Params.Arguments, &input) != nil:
		case !contextRequestsValid(input.Requests):
			// The fixed rule name is what an operator needs here. The request
			// itself may name a private file or carry parent-only text, so it is
			// never written to the stream.
			reason = agent.ContextRequestRejection(input.Requests)
		default:
			reason = ""
		}
		if reason != "" {
			log.New(os.Stderr, "", 0).Println("context_control_rejected reason=" + reason)
			return nil, agent.E("CONTEXT_INPUT_INVALID")
		}
		if len(boundToken) > 0 && boundToken[0] != "" {
			input.ControlToken = boundToken[0]
		}
		if e := st.BeginContextControl(input.ControlToken, input.Requests); e != nil {
			return nil, e
		}
		poll := time.NewTicker(100 * time.Millisecond)
		defer poll.Stop()
		heartbeat := time.NewTicker(5 * time.Second)
		defer heartbeat.Stop()
		for {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-heartbeat.C:
				if token := req.Params.GetProgressToken(); token != nil {
					_ = req.Session.NotifyProgress(ctx, &sdk.ProgressNotificationParams{ProgressToken: token, Progress: 0, Message: "Waiting for parent context"})
				}
			case <-poll.C:
				control, e := st.ContextControl(input.ControlToken)
				if e != nil {
					return nil, e
				}
				switch control.State {
				case "answered":
					if e = st.DeliverContextControl(input.ControlToken); e != nil {
						return nil, e
					}
					b, _ := json.Marshal(control.Responses)
					return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: string(b)}}}, nil
				case "cancelled":
					return nil, context.Canceled
				case "pending":
				default:
					return nil, agent.E("PENDING_CONTEXT_LOST")
				}
			}
		}
	})
	return nil
}

func (c *Client) controlStore() (*store.Store, error) {
	if c.ControlDB == "" {
		return nil, agent.E("CONTEXT_TOOL_UNAVAILABLE")
	}
	return store.Open(c.ControlDB)
}

// Each new session gets a transport-bound control server. Tokens never enter
// model-visible prompts, schemas or arguments, and concurrent sessions cannot
// choose another session's authority.
func controlServerName(token string) string {
	if token == "" {
		return controlMCPName
	}
	sum := sha256.Sum256([]byte(token))
	return controlMCPName + "_" + hex.EncodeToString(sum[:8])
}
func hideControlToken(schema *jsonschema.Schema) {
	delete(schema.Properties, "control_token")
	var required []string
	for _, name := range schema.Required {
		if name != "control_token" {
			required = append(required, name)
		}
	}
	schema.Required = required
}
func (c *Client) disconnectControl(h agent.RuntimeHandle) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = c.json(ctx, "POST", "/mcp/"+controlServerName(h.ControlToken)+"/disconnect", nil, nil)
}
func (c *Client) ensureContextTool(ctx context.Context, token string) error {
	name := controlServerName(token)
	var status map[string]struct {
		Status string `json:"status"`
	}
	if e := c.json(ctx, "GET", "/mcp", nil, &status); e != nil {
		return agent.E("CONTEXT_TOOL_UNAVAILABLE")
	}
	if status[name].Status == "connected" {
		return nil
	}
	binary := c.ControlBinary
	if binary == "" {
		binary, _ = os.Executable()
	}
	if binary == "" || c.ControlDB == "" {
		return agent.E("CONTEXT_TOOL_UNAVAILABLE")
	}
	body := map[string]any{"name": name, "config": map[string]any{"type": "local", "command": []string{binary, "--llmtools-context-control", c.ControlDB}, "environment": map[string]string{"LLMTOOLS_CONTROL_TOKEN": token}, "cwd": filepath.Dir(c.ControlDB), "enabled": true, "timeout": 30000}}
	if e := c.json(ctx, "POST", "/mcp", body, &status); e != nil || status[name].Status != "connected" {
		return agent.E("CONTEXT_TOOL_UNAVAILABLE")
	}
	return nil
}

type contextState struct {
	Version       int                    `json:"version"`
	BaseURL       string                 `json:"base_url"`
	InstanceID    string                 `json:"instance_id"`
	Generation    int64                  `json:"generation"`
	UserMessageID string                 `json:"user_message_id"`
	ControlToken  string                 `json:"control_token"`
	Requests      []agent.ContextRequest `json:"requests"`
}

func (c *Client) pendingContext(h agent.RuntimeHandle, userID string) (*agent.ContextPending, error) {
	st, e := c.controlStore()
	if e != nil {
		return nil, e
	}
	defer st.Close()
	control, e := st.ContextControl(h.ControlToken)
	if e != nil {
		return nil, e
	}
	if control.SessionID != h.SessionID || control.State != "pending" || !contextRequestsValid(control.Requests) {
		return nil, nil
	}
	state := contextState{Version: 1, BaseURL: c.BaseURL, InstanceID: c.InstanceID, Generation: c.Generation, UserMessageID: userID, ControlToken: h.ControlToken, Requests: control.Requests}
	raw, _ := json.Marshal(state)
	return &agent.ContextPending{Requests: control.Requests, State: raw}, nil
}

func decodeContext(raw json.RawMessage) (contextState, error) {
	var state contextState
	if agent.Strict(raw, &state) != nil || state.Version != 1 || state.UserMessageID == "" || state.ControlToken == "" || !contextRequestsValid(state.Requests) {
		return state, agent.E("PENDING_CONTEXT_LOST")
	}
	return state, nil
}

func (c *Client) contextIdentity(raw json.RawMessage, h agent.RuntimeHandle) (contextState, error) {
	state, e := decodeContext(raw)
	if e != nil || state.BaseURL != c.BaseURL || state.InstanceID != c.InstanceID || state.Generation != c.Generation || state.ControlToken != h.ControlToken {
		return state, agent.E("PENDING_CONTEXT_LOST")
	}
	return state, nil
}

func (c *Client) ResumeContext(ctx context.Context, h agent.RuntimeHandle, raw json.RawMessage, answers []agent.AdditionalContext, emit agent.Emit) (agent.AgentResult, error) {
	state, e := c.contextIdentity(raw, h)
	if e != nil {
		return agent.AgentResult{}, e
	}
	if e = c.verify(ctx, h); e != nil {
		return agent.AgentResult{}, agent.E("PENDING_CONTEXT_LOST")
	}
	st, e := c.controlStore()
	if e != nil {
		return agent.AgentResult{}, agent.E("PENDING_CONTEXT_LOST")
	}
	defer st.Close()
	control, e := st.ContextControl(state.ControlToken)
	if e != nil || control.SessionID != h.SessionID || control.State != "pending" || !reflect.DeepEqual(control.Requests, state.Requests) {
		return agent.AgentResult{}, agent.E("PENDING_CONTEXT_LOST")
	}
	return c.observeExecution(ctx, h, state.UserMessageID, emit, func() error {
		return st.AnswerContextControl(state.ControlToken, answers)
	})
}
