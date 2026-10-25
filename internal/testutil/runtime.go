// Package testutil provides an explicitly fake OpenCode HTTP/SSE server.
package testutil

import (
	"ai-worker-mcp/internal/agent"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"

	"ai-worker-mcp/internal/store"
)

type Session struct {
	ControlToken string
	ID           string
	Directory    string
	Permission   any
	Inputs       []json.RawMessage
	Agent        string
	Model        string
	Busy         bool
	Aborted      bool
	Next         int
	Cancel       context.CancelFunc
	Messages     []map[string]any
}
type Runtime struct {
	Server        *httptest.Server
	mu            sync.Mutex
	Sessions      map[string]*Session
	Results       []agent.AgentResult
	Mode          string
	Aborts        int
	Events        int
	Replies       int
	Directories   []string
	Questions     map[string]*PendingQuestion
	QuestionCount int
	ControlDB     string
	ControlTokens map[string]string
	subscribers   map[chan string]bool
}

func New(mode string, results ...agent.AgentResult) *Runtime {
	r := &Runtime{Sessions: map[string]*Session{}, Results: results, Mode: mode, subscribers: map[chan string]bool{}, Questions: map[string]*PendingQuestion{}}
	r.Server = httptest.NewServer(http.HandlerFunc(r.serve))
	return r
}
func (r *Runtime) Close() {
	r.mu.Lock()
	for _, s := range r.Sessions {
		if s.Cancel != nil {
			s.Cancel()
		}
	}
	r.mu.Unlock()
	r.Server.Close()
}
func (r *Runtime) Snapshot() ([]Session, int, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := []Session{}
	for _, s := range r.Sessions {
		c := *s
		c.Inputs = append([]json.RawMessage{}, s.Inputs...)
		out = append(out, c)
	}
	return out, r.Aborts, r.Events
}
func (r *Runtime) event(s string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for c := range r.subscribers {
		select {
		case c <- s:
		default:
		}
	}
}
func (r *Runtime) serve(w http.ResponseWriter, q *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	write := func(v any) { _ = json.NewEncoder(w).Encode(v) }
	// Every llmTools request carries an explicit `directory` scope. Recording it
	// lets tests prove which filesystem authority a run was addressed with.
	if d := q.URL.Query().Get("directory"); d != "" {
		r.mu.Lock()
		r.Directories = append(r.Directories, d)
		r.mu.Unlock()
	}
	if strings.HasPrefix(q.URL.Path, "/question/") {
		parts := strings.Split(q.URL.Path, "/")
		r.mu.Lock()
		pending := r.Questions[parts[2]]
		if pending == nil {
			r.mu.Unlock()
			w.WriteHeader(404)
			return
		}
		delete(r.Questions, parts[2])
		r.mu.Unlock()
		if len(parts) > 3 && parts[3] == "reply" {
			var input struct {
				Answers [][]string `json:"answers"`
			}
			_ = json.NewDecoder(q.Body).Decode(&input)
			r.mu.Lock()
			r.Replies++
			r.mu.Unlock()
			pending.Answers <- input.Answers
		} else {
			pending.Answers <- nil
		}
		write(true)
		return
	}
	if strings.HasPrefix(q.URL.Path, "/mcp/") && strings.HasSuffix(q.URL.Path, "/disconnect") {
		r.mu.Lock()
		delete(r.ControlTokens, strings.TrimSuffix(strings.TrimPrefix(q.URL.Path, "/mcp/"), "/disconnect"))
		r.mu.Unlock()
		write(true)
		return
	}
	switch q.URL.Path {
	case "/mcp":
		if q.Method == "POST" {
			var body struct {
				Name   string `json:"name"`
				Config struct {
					Command     []string          `json:"command"`
					Environment map[string]string `json:"environment"`
				} `json:"config"`
			}
			_ = json.NewDecoder(q.Body).Decode(&body)
			r.mu.Lock()
			if len(body.Config.Command) > 0 {
				r.ControlDB = body.Config.Command[len(body.Config.Command)-1]
			}
			if r.ControlTokens == nil {
				r.ControlTokens = map[string]string{}
			}
			r.ControlTokens[body.Name] = body.Config.Environment["LLMTOOLS_CONTROL_TOKEN"]
			r.mu.Unlock()
		}
		r.mu.Lock()
		status := map[string]any{}
		for name := range r.ControlTokens {
			status[name] = map[string]string{"status": "connected"}
		}
		r.mu.Unlock()
		write(status)
		return
	case "/experimental/tool/ids":
		if r.Mode == "no_question" {
			write([]string{"read"})
		} else {
			write([]string{"question", "read", "bash"})
		}
		return
	case "/question":
		r.mu.Lock()
		list := []any{}
		for _, p := range r.Questions {
			list = append(list, p.Request)
		}
		r.mu.Unlock()
		write(list)
		return
	case "/global/health":
		if r.Mode == "unavailable" {
			w.WriteHeader(503)
			return
		}
		write(agent.Health{Healthy: true, Version: "mock-1.18.31"})
		return
	case "/agent":
		if r.Mode == "malformed_agents" {
			fmt.Fprint(w, `{"not":"array"}`)
			return
		}
		write([]agent.AgentInfo{{Name: "fixture", Mode: "primary", Description: "Mock agent"}})
		return
	case "/api/model":
		write(map[string]any{"data": []any{
			map[string]any{"id": "fixture", "providerID": "mock", "name": "Fixture", "enabled": true},
			map[string]any{"id": "terra", "providerID": "mock", "name": "Terra", "enabled": true},
			map[string]any{"id": "sol", "providerID": "mock", "name": "Sol", "enabled": true},
		}})
		return
	case "/session":
		var body struct {
			Permission any `json:"permission"`
		}
		_ = json.NewDecoder(q.Body).Decode(&body)
		r.mu.Lock()
		id := fmt.Sprintf("ses_mock%d", len(r.Sessions)+1)
		s := &Session{ID: id, Directory: q.URL.Query().Get("directory"), Permission: body.Permission}
		for name, token := range r.ControlTokens {
			encoded, _ := json.Marshal(body.Permission)
			if strings.Contains(string(encoded), name+"_request_context") {
				s.ControlToken = token
			}
		}
		r.Sessions[id] = s
		r.mu.Unlock()
		if r.Mode == "bad_permission" {
			write(map[string]any{"id": id})
			return
		}
		write(map[string]any{"id": id, "permission": body.Permission})
		return
	case "/session/status":
		r.mu.Lock()
		m := map[string]any{}
		for id, s := range r.Sessions {
			if s.Busy {
				m[id] = map[string]string{"type": "busy"}
			}
		}
		r.mu.Unlock()
		write(m)
		return
	case "/event":
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"type\":\"server.connected\",\"properties\":{}}\n\n")
		w.(http.Flusher).Flush()
		r.mu.Lock()
		r.Events++
		r.mu.Unlock()
		if r.Mode == "disconnect" {
			return
		}
		if r.Mode == "malformed_event" {
			fmt.Fprint(w, "data: NOT_JSON\n\n")
			w.(http.Flusher).Flush()
			return
		}
		ch := make(chan string, 16)
		r.mu.Lock()
		r.subscribers[ch] = true
		r.mu.Unlock()
		defer func() { r.mu.Lock(); delete(r.subscribers, ch); r.mu.Unlock() }()
		for {
			select {
			case <-q.Context().Done():
				return
			case s := <-ch:
				fmt.Fprintf(w, "data: %s\n\n", s)
				w.(http.Flusher).Flush()
			}
		}
	}
	parts := strings.Split(strings.Trim(q.URL.Path, "/"), "/")
	if len(parts) < 2 || parts[0] != "session" {
		w.WriteHeader(404)
		return
	}
	id := parts[1]
	r.mu.Lock()
	s := r.Sessions[id]
	r.mu.Unlock()
	if s == nil || r.Mode == "lost" {
		w.WriteHeader(404)
		return
	}
	if len(parts) == 2 {
		write(map[string]any{"id": id, "permission": s.Permission})
		return
	}
	if parts[2] == "abort" {
		r.mu.Lock()
		r.Aborts++
		s.Busy = false
		s.Aborted = true
		if s.Cancel != nil {
			s.Cancel()
		}
		for key, pending := range r.Questions {
			if pending.Request["sessionID"] == id {
				delete(r.Questions, key)
			}
		}
		r.mu.Unlock()
		if r.Mode == "abort_failure" {
			w.WriteHeader(503)
			return
		}
		write(true)
		return
	}

	if parts[2] == "message" && q.Method == "GET" {
		if r.Mode == "malformed" {
			fmt.Fprint(w, "invalid")
			return
		}
		if r.Mode == "http_failure" {
			w.WriteHeader(500)
			return
		}
		r.mu.Lock()
		data, _ := json.Marshal(s.Messages)
		r.mu.Unlock()
		var messages []map[string]any
		_ = json.Unmarshal(data, &messages)
		if len(parts) == 4 {
			for _, m := range messages {
				if m["info"].(map[string]any)["id"] == parts[3] {
					write(m)
					return
				}
			}
			w.WriteHeader(404)
			return
		}
		if q.URL.Query().Get("limit") == "1" && len(messages) > 1 {
			messages = messages[len(messages)-1:]
		}
		write(messages)
		return
	}
	if parts[2] != "prompt_async" || q.Method != "POST" {
		w.WriteHeader(404)
		return
	}
	var input json.RawMessage
	_ = json.NewDecoder(q.Body).Decode(&input)
	var body struct {
		MessageID string `json:"messageID"`
		Agent     string `json:"agent"`
		Model     *struct {
			ProviderID string `json:"providerID"`
			ModelID    string `json:"modelID"`
		} `json:"model"`
	}
	_ = json.Unmarshal(input, &body)
	if body.MessageID == "" {
		w.WriteHeader(400)
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.mu.Lock()
	s.Cancel = cancel
	s.Inputs = append(s.Inputs, input)
	s.Agent = body.Agent
	s.Model = "mock/fixture"
	if body.Model != nil {
		s.Model = body.Model.ProviderID + "/" + body.Model.ModelID
	}
	s.Busy = true
	s.Aborted = false
	r.mu.Unlock()
	go func() { defer cancel(); r.execute(ctx, s, body.MessageID) }()
	w.WriteHeader(204)
}

type PendingQuestion struct {
	Request map[string]any
	Answers chan [][]string
}

func (r *Runtime) QuestionSnapshot() (int, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.Questions), r.Replies
}
func (r *Runtime) LoseQuestions() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range r.Sessions {
		if s.Cancel != nil {
			s.Cancel()
		}
	}
	r.Questions = map[string]*PendingQuestion{}
}
func (r *Runtime) execute(ctx context.Context, s *Session, userID string) {
	defer func() { r.mu.Lock(); s.Busy = false; r.mu.Unlock() }()
	for {
		r.mu.Lock()
		index := s.Next
		s.Next++
		r.mu.Unlock()
		id := s.ID
		r.mu.Lock()
		model := s.Model
		// A real runtime answers as the agent it was addressed with, including
		// when the route named no agent at all. Hard-coding a name here would hide
		// a catalogue-less route's own identity from the client's checks.
		agentName := s.Agent
		r.mu.Unlock()
		modelParts := strings.SplitN(model, "/", 2)
		info := map[string]any{"id": fmt.Sprintf("msg_mock_%s_%d", id, index), "parentID": userID, "role": "assistant", "sessionID": id, "providerID": modelParts[0], "modelID": modelParts[1], "agent": agentName}
		message := map[string]any{"info": info, "parts": []any{}}
		r.mu.Lock()
		s.Messages = append(s.Messages, message)
		r.mu.Unlock()
		r.event(fmt.Sprintf(`{"type":"message.part.delta","properties":{"sessionID":%q,"delta":"partial SECRET should never leave progress"}}`, id))
		r.event(fmt.Sprintf(`{"type":"message.part.updated","properties":{"part":{"sessionID":%q,"type":"tool","tool":"internal-name"}}}`, id))
		if r.Mode == "timeout" || r.Mode == "disconnect" || r.Mode == "abort_failure" || r.Mode == "malformed_event" {
			<-ctx.Done()
			return
		}
		if r.Mode == "slow" {
			select {
			case <-ctx.Done():
				return
			case <-time.After(200 * time.Millisecond):
			}
		}
		result := agent.AgentResult{Status: "completed", Summary: "Fixture complete"}
		if index < len(r.Results) {
			result = r.Results[index]
		}
		if result.Status == "needs_decision" {
			questions := []any{}
			for _, q := range result.DecisionRequests {
				options := []any{}
				for _, o := range q.Options {
					options = append(options, map[string]string{"label": o.ID, "description": o.Description})
				}
				questions = append(questions, map[string]any{"question": q.Question, "header": "Parent choice", "options": options, "multiple": q.Multiple, "custom": q.AllowCustom})
			}
			r.mu.Lock()
			r.QuestionCount++
			qid := fmt.Sprintf("que_mock_%d", r.QuestionCount)
			request := map[string]any{"id": qid, "sessionID": id, "questions": questions, "tool": map[string]string{"messageID": info["id"].(string), "callID": "call_fixture"}}
			pending := &PendingQuestion{Request: request, Answers: make(chan [][]string, 1)}
			r.Questions[qid] = pending
			r.mu.Unlock()
			event, _ := json.Marshal(map[string]any{"type": "question.asked", "properties": request})
			r.event(string(event))
			select {
			case <-ctx.Done():
				r.mu.Lock()
				delete(r.Questions, qid)
				r.mu.Unlock()
				return
			case answer := <-pending.Answers:
				if answer == nil {
					return
				}
			}
			r.mu.Lock()
			info["finish"] = "tool-calls"
			info["time"] = map[string]int64{"completed": time.Now().UnixMilli()}
			r.mu.Unlock()
			continue
		}
		if result.Status == "needs_context" {
			token := s.ControlToken
			r.mu.Lock()
			dbPath := r.ControlDB
			r.mu.Unlock()
			if token == "" || dbPath == "" {
				return
			}
			st, e := store.Open(dbPath)
			if e != nil || st.BeginContextControl(token, result.ContextRequests) != nil {
				if st != nil {
					_ = st.Close()
				}
				return
			}
			for {
				control, e := st.ContextControl(token)
				if e != nil || control.State == "cancelled" {
					_ = st.Close()
					return
				}
				if control.State == "answered" {
					_ = st.DeliverContextControl(token)
					_ = st.Close()
					break
				}
				select {
				case <-ctx.Done():
					_ = st.Close()
					return
				case <-time.After(20 * time.Millisecond):
				}
			}
			r.mu.Lock()
			info["finish"] = "tool-calls"
			info["time"] = map[string]int64{"completed": time.Now().UnixMilli()}
			r.mu.Unlock()
			continue
		}
		content := result.Content
		if content == "" {
			content = result.Summary
		}
		if r.Mode == "idle_before_terminal" {
			r.mu.Lock()
			s.Busy = false
			r.mu.Unlock()
			time.Sleep(250 * time.Millisecond)
		}
		r.mu.Lock()
		info["finish"] = "stop"
		if r.Mode == "markdown_parts" {
			message["parts"] = []any{
				map[string]any{"type": "reasoning", "text": "internal analysis"},
				map[string]any{"type": "text", "text": "## Result\n\nPreserved markdown"},
				map[string]any{"type": "text", "text": "\n\n"},
				map[string]any{"type": "text", "text": "```diff\n-old\n+new\n```"},
				map[string]any{"type": "text", "text": "ignored", "ignored": true},
				map[string]any{"type": "text", "text": "synthetic", "synthetic": true},
			}
		} else {
			message["parts"] = []any{map[string]any{"type": "text", "text": content}}
		}
		if r.Mode == "provider_error_after_partial" {
			info["error"] = map[string]string{"name": "ProviderError"}
		}
		if r.Mode != "partial" {
			info["time"] = map[string]int64{"completed": time.Now().UnixMilli()}
		}
		if s.Aborted {
			info["error"] = map[string]string{"name": "MessageAbortedError"}
		}
		r.mu.Unlock()
		return
	}
}
