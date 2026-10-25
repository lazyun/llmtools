package opencode

import (
	"ai-worker-mcp/internal/agent"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"reflect"
	"strings"
	"time"
)

// Verified against OpenCode 1.18.31's QuestionV1 schema and OpenAPI. Runtime IDs
// stay in this private state and never enter AgentResult or MCP progress.
type QuestionOption struct {
	Label       string `json:"label"`
	Description string `json:"description"`
}
type QuestionInfo struct {
	Question string           `json:"question"`
	Header   string           `json:"header"`
	Options  []QuestionOption `json:"options"`
	Multiple bool             `json:"multiple,omitempty"`
	Custom   *bool            `json:"custom,omitempty"`
}
type Question struct {
	ID        string         `json:"id"`
	SessionID string         `json:"sessionID"`
	Questions []QuestionInfo `json:"questions"`
	Tool      *struct {
		MessageID string `json:"messageID"`
		CallID    string `json:"callID"`
	} `json:"tool,omitempty"`
}
type decisionState struct {
	Version       int                     `json:"version"`
	BaseURL       string                  `json:"base_url"`
	InstanceID    string                  `json:"instance_id"`
	Generation    int64                   `json:"generation"`
	UserMessageID string                  `json:"user_message_id"`
	Request       Question                `json:"request"`
	Normalized    []agent.DecisionRequest `json:"normalized"`
}

func (c *Client) QuestionAvailable(ctx context.Context) error {
	var ids []string
	if c.json(ctx, "GET", "/experimental/tool/ids", nil, &ids) != nil {
		return agent.E("QUESTION_TOOL_UNAVAILABLE")
	}
	for _, id := range ids {
		if id == "question" {
			return nil
		}
	}
	return agent.E("QUESTION_TOOL_UNAVAILABLE")
}
func (c *Client) ListPendingQuestions(ctx context.Context) ([]Question, error) {
	var out []Question
	e := c.json(ctx, "GET", "/question", nil, &out)
	return out, e
}
func safeRuntimeID(id, prefix string) bool {
	return strings.HasPrefix(id, prefix) && len(id) < 200 && !strings.ContainsAny(id, "/\\?#%\r\n")
}
func (c *Client) RejectQuestion(ctx context.Context, q Question) error {
	if !safeRuntimeID(q.ID, "que") {
		return agent.E("RUNTIME_PROTOCOL_ERROR")
	}
	var ok bool
	e := c.json(ctx, "POST", "/question/"+url.PathEscape(q.ID)+"/reject", nil, &ok)
	if e != nil {
		return e
	}
	if !ok {
		return agent.E("RUNTIME_PROTOCOL_ERROR")
	}
	return nil
}
func (c *Client) questionState(h agent.RuntimeHandle, userID string, q Question) (*agent.DecisionPending, error) {
	if q.SessionID != h.SessionID || !safeRuntimeID(q.ID, "que") || q.Tool == nil || !safeRuntimeID(q.Tool.MessageID, "msg") || len(q.Questions) == 0 || len(q.Questions) > 20 {
		return nil, agent.E("RUNTIME_PROTOCOL_ERROR")
	}
	state := decisionState{Version: 1, BaseURL: c.BaseURL, InstanceID: c.InstanceID, Generation: c.Generation, UserMessageID: userID, Request: q}
	for i, question := range q.Questions {
		custom := question.Custom == nil || *question.Custom
		if strings.TrimSpace(question.Question) == "" || len(question.Question) > 16384 || len(question.Options) > 100 || (len(question.Options) == 0 && !custom) {
			return nil, agent.E("RUNTIME_PROTOCOL_ERROR")
		}
		sum := sha256.Sum256([]byte(fmt.Sprintf("%s:%d", q.ID, i)))
		request := agent.DecisionRequest{ID: "dec_" + hex.EncodeToString(sum[:12]), Question: question.Question, Header: question.Header, Reason: "The execution agent requests a parent-owned decision.", Multiple: question.Multiple, AllowCustom: custom, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
		seen := map[string]bool{}
		for j, o := range question.Options {
			if strings.TrimSpace(o.Label) == "" || seen[o.Label] {
				return nil, agent.E("RUNTIME_PROTOCOL_ERROR")
			}
			seen[o.Label] = true
			description := o.Label
			if o.Description != "" {
				description += " — " + o.Description
			}
			request.Options = append(request.Options, agent.Option{ID: fmt.Sprintf("option_%d", j+1), Description: description})
		}
		state.Normalized = append(state.Normalized, request)
	}
	public, _ := json.Marshal(state.Normalized)
	if strings.Contains(string(public), q.ID) || strings.Contains(string(public), h.SessionID) {
		return nil, agent.E("RUNTIME_PROTOCOL_ERROR")
	}
	raw, _ := json.Marshal(state)
	return &agent.DecisionPending{Requests: state.Normalized, State: raw}, nil
}
func decodeDecision(raw json.RawMessage) (decisionState, error) {
	var state decisionState
	if agent.Strict(raw, &state) != nil || state.Version != 1 || state.UserMessageID == "" || state.Request.ID == "" || len(state.Normalized) != len(state.Request.Questions) {
		return state, agent.E("PENDING_DECISION_LOST")
	}
	return state, nil
}
func (c *Client) decisionIdentity(raw json.RawMessage, h agent.RuntimeHandle) (decisionState, error) {
	state, e := decodeDecision(raw)
	if e != nil || state.Request.SessionID != h.SessionID || state.BaseURL != c.BaseURL || state.InstanceID != c.InstanceID || state.Generation != c.Generation {
		return state, agent.E("PENDING_DECISION_LOST")
	}
	return state, nil
}
func (c *Client) ResumeDecision(ctx context.Context, h agent.RuntimeHandle, raw json.RawMessage, answers []agent.DecisionResponse, emit agent.Emit) (agent.AgentResult, error) {
	state, e := c.decisionIdentity(raw, h)
	if e != nil {
		return agent.AgentResult{}, e
	}
	if e = c.verify(ctx, h); e != nil {
		return agent.AgentResult{}, agent.E("PENDING_DECISION_LOST")
	}
	pending, e := c.ListPendingQuestions(ctx)
	if e != nil {
		return agent.AgentResult{}, agent.E("PENDING_DECISION_LOST")
	}
	found := false
	for _, q := range pending {
		if q.ID == state.Request.ID && reflect.DeepEqual(q, state.Request) {
			found = true
		}
	}
	if !found {
		return agent.AgentResult{}, agent.E("PENDING_DECISION_LOST")
	}
	// Request membership and answer shapes are validated again at the adapter
	// boundary; ordinary text prompts are never used to answer a native question.
	byID := map[string]agent.DecisionResponse{}
	for _, a := range answers {
		if _, ok := byID[a.RequestID]; ok {
			return agent.AgentResult{}, agent.E("INVALID_DECISION_RESPONSE")
		}
		byID[a.RequestID] = a
	}
	if len(byID) != len(state.Normalized) {
		return agent.AgentResult{}, agent.E("INVALID_DECISION_RESPONSE")
	}
	native := make([][]string, len(state.Normalized))
	for i, q := range state.Normalized {
		a, ok := byID[q.ID]
		if !ok || agent.ValidateDecisionResponse(q, a) != nil || a.Reject {
			return agent.AgentResult{}, agent.E("INVALID_DECISION_RESPONSE")
		}
		selected := a.Selections
		if a.Selection != "" {
			selected = []string{a.Selection}
		}
		for _, id := range selected {
			for j, o := range q.Options {
				if id == o.ID {
					native[i] = append(native[i], state.Request.Questions[i].Options[j].Label)
				}
			}
		}
		if a.CustomText != "" {
			native[i] = append(native[i], a.CustomText)
		}
	}
	return c.observeExecution(ctx, h, state.UserMessageID, emit, func() error {
		var ok bool
		if e := c.json(ctx, "POST", "/question/"+url.PathEscape(state.Request.ID)+"/reply", map[string]any{"answers": native}, &ok); e != nil || !ok {
			return agent.E("PENDING_DECISION_LOST")
		}
		return nil
	})
}
