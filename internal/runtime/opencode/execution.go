package opencode

import (
	"ai-worker-mcp/internal/agent"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"
)

type runtimeMessage struct {
	Info struct {
		ID         string          `json:"id"`
		ParentID   string          `json:"parentID"`
		Role       string          `json:"role"`
		SessionID  string          `json:"sessionID"`
		ProviderID string          `json:"providerID"`
		ModelID    string          `json:"modelID"`
		Agent      string          `json:"agent"`
		Finish     string          `json:"finish"`
		Error      json.RawMessage `json:"error"`
		Time       struct {
			Completed int64 `json:"completed"`
		} `json:"time"`
	} `json:"info"`
	Parts []struct {
		Type      string `json:"type"`
		Text      string `json:"text"`
		Synthetic bool   `json:"synthetic"`
		Ignored   bool   `json:"ignored"`
	} `json:"parts"`
}

// messageError turns the error OpenCode attaches to an assistant message into
// llmTools' runtime-neutral vocabulary. A provider that refuses to serve a model
// the catalogue does list is an execution boundary, not an unavailable server:
// reporting RUNTIME_UNAVAILABLE for it would send the operator to the runtime
// while the provider was the one that answered. Only the error's own name and
// message decide the code, and neither is ever reported.
func messageError(raw json.RawMessage) error {
	var e struct {
		Name string `json:"name"`
		Data struct {
			Message string `json:"message"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &e) != nil {
		return agent.E("RUNTIME_UNAVAILABLE")
	}
	if agent.MentionsUpstreamBlock(strings.ToLower(e.Name + " " + e.Data.Message)) {
		return agent.E("UPSTREAM_UNAVAILABLE")
	}
	return agent.E("RUNTIME_UNAVAILABLE")
}

func (c *Client) ContinueTask(ctx context.Context, h agent.RuntimeHandle, input any, emit agent.Emit) (agent.AgentResult, error) {
	if e := c.verify(ctx, h); e != nil {
		return agent.AgentResult{}, e
	}
	idle, e := c.idle(ctx, h.SessionID)
	if e != nil {
		return agent.AgentResult{}, e
	}
	if !idle {
		return agent.AgentResult{}, agent.E("CONTINUATION_INVALID")
	}
	// OpenCode sorts message IDs. Match its ascending 48-bit millisecond/counter
	// prefix, placing the user message before the server-generated assistant.
	userID := fmt.Sprintf("msg_%012x%s", (uint64(time.Now().UnixMilli())*0x1000)&0xffffffffffff, randomID("")[:14])
	task, ok := input.(agent.AgentTask)
	if !ok {
		return agent.AgentResult{}, agent.E("INVALID_AGENT_RESULT")
	}
	body := map[string]any{"messageID": userID, "agent": h.Agent, "parts": []any{map[string]any{"type": "text", "text": task.Objective}}}
	if h.Model != "" {
		parts := strings.SplitN(h.Model, "/", 2)
		if len(parts) != 2 {
			return agent.AgentResult{}, agent.E("MODEL_NOT_AVAILABLE")
		}
		body["model"] = map[string]string{"providerID": parts[0], "modelID": parts[1]}
	}
	return c.observeExecution(ctx, h, userID, emit, func() error {
		// Detached server execution survives this HTTP request and MCP observer exit.
		// Only explicit timeout/cancel in AgentService invokes session abort.
		return c.json(ctx, "POST", "/session/"+url.PathEscape(h.SessionID)+"/prompt_async", body, nil)
	})
}
func (c *Client) observeExecution(ctx context.Context, h agent.RuntimeHandle, userID string, emit agent.Emit, dispatch func() error) (agent.AgentResult, error) {
	streamCtx, stop := context.WithCancel(ctx)
	defer stop()
	questions := make(chan Question, 8)
	events, e := c.subscribe(streamCtx, h, emit, func(q Question) error {
		select {
		case questions <- q:
		default:
		}
		return nil
	})
	if e != nil {
		return agent.AgentResult{}, e
	}
	if emit != nil {
		emit(agent.Progress{Stage: "RUNNING"})
	}
	if e = dispatch(); e != nil {
		return agent.AgentResult{}, e
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var idleSince time.Time
	for {
		select {
		case <-ctx.Done():
			return agent.AgentResult{}, ctx.Err()
		case e := <-events:
			if e == nil {
				e = agent.E("RUNTIME_UNAVAILABLE")
			}
			return agent.AgentResult{}, e
		case <-questions: // The event is a wake-up; authoritative pending state is verified below.
		case <-ticker.C:
		}
		pending, e := c.ListPendingQuestions(ctx)
		if e != nil {
			return agent.AgentResult{}, e
		}
		for _, q := range pending {
			if q.SessionID == h.SessionID {
				if q.Tool == nil {
					return agent.AgentResult{}, agent.E("RUNTIME_PROTOCOL_ERROR")
				}
				var message runtimeMessage
				if e = c.json(ctx, "GET", "/session/"+url.PathEscape(h.SessionID)+"/message/"+url.PathEscape(q.Tool.MessageID), nil, &message); e != nil {
					return agent.AgentResult{}, e
				}
				if message.Info.SessionID != h.SessionID || message.Info.ParentID != userID {
					return agent.AgentResult{}, agent.E("RUNTIME_PROTOCOL_ERROR")
				}
				suspension, e := c.questionState(h, userID, q)
				if e != nil {
					return agent.AgentResult{}, e
				}
				return agent.AgentResult{}, suspension
			}
		}
		contextPending, e := c.pendingContext(h, userID)
		if e != nil {
			return agent.AgentResult{}, e
		}
		if contextPending != nil {
			if emit != nil {
				emit(agent.Progress{Stage: "WAITING_FOR_CONTEXT"})
			}
			return agent.AgentResult{}, contextPending
		}
		idle, e := c.idle(ctx, h.SessionID)
		if e != nil {
			return agent.AgentResult{}, e
		}
		if !idle {
			idleSince = time.Time{}
			continue
		}
		if idleSince.IsZero() {
			idleSince = time.Now()
		}
		// Only the newest assistant can finish this execution. Limit the page to
		// avoid decoding older user formats (OpenCode 1.18.31 serializes those
		// persisted Schema.Class values incorrectly on the unpaginated path).
		var messages []runtimeMessage
		if e = c.json(ctx, "GET", "/session/"+url.PathEscape(h.SessionID)+"/message?limit=1", nil, &messages); e != nil {
			return agent.AgentResult{}, e
		}
		for i := len(messages) - 1; i >= 0; i-- {
			info := messages[i].Info
			if info.Role != "assistant" || info.ParentID != userID || info.SessionID != h.SessionID {
				continue
			}
			if len(info.Error) > 0 && string(info.Error) != "null" {
				return agent.AgentResult{}, messageError(info.Error)
			}
			if info.Time.Completed == 0 || info.Finish != "stop" {
				// OpenCode can publish idle immediately before the final message
				// transaction becomes visible. Require a stable idle window before
				// classifying a non-terminal message; never promote it to a result.
				if time.Since(idleSince) < 2*time.Second {
					continue
				}
				return agent.AgentResult{}, agent.E("INVALID_AGENT_RESULT")
			}
			actualModel := info.ProviderID + "/" + info.ModelID
			if info.Agent != h.Agent || !validModelID(actualModel) || (h.Model != "" && actualModel != h.Model) {
				return agent.AgentResult{}, agent.E("INVALID_AGENT_RESULT")
			}
			var text []string
			for _, part := range messages[i].Parts {
				if part.Type == "text" && !part.Synthetic && !part.Ignored {
					text = append(text, part.Text)
				}
			}
			content := strings.Join(text, "")
			if strings.TrimSpace(content) == "" {
				return agent.AgentResult{}, agent.E("EMPTY_AGENT_RESULT")
			}
			if strings.Contains(content, h.SessionID) || (h.ControlToken != "" && strings.Contains(content, h.ControlToken)) {
				return agent.AgentResult{}, agent.E("INVALID_AGENT_RESULT")
			}
			if emit != nil {
				emit(agent.Progress{Stage: "VALIDATING"})
			}
			if st, storeErr := c.controlStore(); storeErr == nil {
				st.DeleteContextControl(h.ControlToken)
				_ = st.Close()
			}
			c.disconnectControl(h)
			// Summary is a direct compatibility alias. Content is authoritative;
			// llmTools never infers business fields from the assistant response.
			return agent.AgentResult{Status: "completed", Content: content, Summary: content, Agent: info.Agent, Model: actualModel}, nil
		}
		if time.Since(idleSince) >= 2*time.Second {
			return agent.AgentResult{}, agent.E("INVALID_AGENT_RESULT")
		}
	}
}
