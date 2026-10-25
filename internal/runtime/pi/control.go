package pi

import (
	"ai-worker-mcp/internal/agent"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// stateVersion is the Pi adapter's private durable state format. It lives in
// the existing run record's opaque runtime state columns; no Pi table, file or
// Core field exists.
const stateVersion = 1

type runState struct {
	Version      int                     `json:"version"`
	SessionID    string                  `json:"session_id"`
	ControlToken string                  `json:"control_token"`
	Model        string                  `json:"model"`
	Requests     []agent.ContextRequest  `json:"requests,omitempty"`
	Decisions    []agent.DecisionRequest `json:"decisions,omitempty"`
}

// pendingControl is what the bridge reported on a control tool result.
type pendingControl struct {
	kind      string // context | decision
	requests  []agent.ContextRequest
	questions []piQuestion
}

// piQuestion mirrors the bridge schema. Pi has no native question tool, so the
// model supplies this shape and the adapter normalizes it into the same
// agent.DecisionRequest Core already displays.
type piQuestion struct {
	ID       string `json:"id"`
	Question string `json:"question"`
	Header   string `json:"header,omitempty"`
	Options  []struct {
		Label       string `json:"label"`
		Description string `json:"description,omitempty"`
	} `json:"options,omitempty"`
	Multiple    *bool `json:"multiple,omitempty"`
	AllowCustom *bool `json:"allow_custom,omitempty"`
}

func (a *Adapter) suspension(h agent.RuntimeHandle, c *pendingControl) error {
	state := runState{Version: stateVersion, SessionID: h.SessionID, ControlToken: h.ControlToken, Model: h.Model}
	if c.kind == "context" {
		state.Requests = c.requests
		if !agent.ContextRequestsValid(state.Requests) {
			// The same malformed handoff must carry the same code whichever
			// runtime received it; OpenCode's control tool reports this one.
			return agent.E("CONTEXT_INPUT_INVALID")
		}
	} else {
		state.Decisions = decisionsOf(c, h)
		if len(state.Decisions) == 0 {
			return agent.E("RUNTIME_PROTOCOL_ERROR")
		}
	}
	raw, e := json.Marshal(state)
	if e != nil {
		return agent.E("INTERNAL_ERROR")
	}
	if c.kind == "context" {
		return &agent.ContextPending{Requests: state.Requests, State: raw}
	}
	return &agent.DecisionPending{Requests: state.Decisions, State: raw}
}

func decisionsOf(c *pendingControl, h agent.RuntimeHandle) []agent.DecisionRequest {
	if c.kind != "decision" {
		return nil
	}
	seen := map[string]bool{}
	out := make([]agent.DecisionRequest, 0, len(c.questions))
	for i, q := range c.questions {
		text := strings.TrimSpace(q.Question)
		if text == "" || len(text) > 16384 || len(q.Options) > 100 || seen[q.ID] {
			continue
		}
		allowCustom := q.AllowCustom == nil || *q.AllowCustom
		if len(q.Options) == 0 && !allowCustom {
			continue
		}
		seen[q.ID] = true
		request := agent.DecisionRequest{
			ID:          decisionID(h.SessionID, q.ID, i),
			Question:    text,
			Header:      truncate(q.Header, 200),
			Reason:      "The execution agent requests a parent-owned decision.",
			Multiple:    q.Multiple != nil && *q.Multiple,
			AllowCustom: allowCustom,
			CreatedAt:   time.Now().UTC().Format(time.RFC3339Nano),
		}
		labels := map[string]bool{}
		for j, o := range q.Options {
			label := strings.TrimSpace(o.Label)
			if label == "" || labels[label] {
				continue
			}
			labels[label] = true
			description := label
			if strings.TrimSpace(o.Description) != "" {
				description += " — " + strings.TrimSpace(o.Description)
			}
			request.Options = append(request.Options, agent.Option{ID: fmt.Sprintf("option_%d", j+1), Description: truncate(description, 4096)})
		}
		out = append(out, request)
	}
	return out
}

// decisionID keeps adapter-private ids stable within one session so a resumed
// process can match answers to requests, without exposing Pi structures to Core.
func decisionID(sessionID, given string, index int) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s:%d:%s", sessionID, index, given)))
	return "pi_dec_" + hex.EncodeToString(sum[:12])
}

func truncate(v string, max int) string {
	if len(v) <= max {
		return v
	}
	return v[:max]
}

// parseControl reads one tool result payload. Only the two control tools can
// produce a suspension; anything else is ordinary execution.
func parseControl(toolName string, details json.RawMessage) *pendingControl {
	var kind string
	switch toolName {
	case "llmtools_control_request_context":
		kind = "context"
	case "llmtools_control_request_decision":
		kind = "decision"
	default:
		return nil
	}
	if len(details) == 0 {
		return &pendingControl{kind: kind}
	}
	var payload struct {
		LLMToolsControl string          `json:"llmtools_control"`
		Requests        json.RawMessage `json:"requests"`
		Questions       json.RawMessage `json:"questions"`
	}
	if json.Unmarshal(details, &payload) != nil {
		return &pendingControl{kind: kind}
	}
	out := &pendingControl{kind: kind}
	if payload.LLMToolsControl != kind {
		return out
	}
	if kind == "context" {
		var requests []struct {
			ID     string `json:"id"`
			Kind   string `json:"kind"`
			Target string `json:"target"`
			Reason string `json:"reason"`
		}
		if json.Unmarshal(payload.Requests, &requests) == nil {
			for _, r := range requests {
				kind := r.Kind
				if kind == "" {
					kind = "other"
				}
				out.requests = append(out.requests, agent.ContextRequest{ID: truncate(r.ID, 80), Kind: kind, Target: truncate(r.Target, 4096), Reason: truncate(r.Reason, 16384)})
			}
		}
		return out
	}
	if json.Unmarshal(payload.Questions, &out.questions) == nil {
		return out
	}
	return out
}

// pendingState validates that a persisted suspension still belongs to this
// handle and is the kind of suspension Core is continuing. A state blob from
// another session, token, or format version is never replayed.
func (a *Adapter) pendingState(raw json.RawMessage, h agent.RuntimeHandle, kind string) (runState, error) {
	lost := "PENDING_CONTEXT_LOST"
	if kind == "decision" {
		lost = "PENDING_DECISION_LOST"
	}
	var state runState
	if len(raw) == 0 || agent.Strict(raw, &state) != nil || state.Version != stateVersion {
		return state, agent.E(lost)
	}
	if state.SessionID != h.SessionID || state.ControlToken == "" || state.ControlToken != h.ControlToken {
		return state, agent.E(lost)
	}
	return state, nil
}
