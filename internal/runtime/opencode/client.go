// Package opencode is the sole runtime adapter. OpenCode owns model configuration.
package opencode

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"ai-worker-mcp/internal/agent"
)

type Client struct {
	BaseURL       string
	Directory     string
	HTTP          *http.Client
	InstanceID    string
	Generation    int64
	ControlDB     string
	ControlBinary string
}

func New(base, dir string) *Client {
	dataDir := dir
	if filepath.Base(dir) == "runtime-workspace" {
		dataDir = filepath.Dir(dir)
	}
	return &Client{BaseURL: strings.TrimRight(base, "/"), Directory: dir, ControlDB: filepath.Join(dataDir, "llmtools.db"), HTTP: &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func (c *Client) request(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var b io.Reader
	if body != nil {
		data, e := json.Marshal(body)
		if e != nil {
			return nil, agent.E("INTERNAL_ERROR")
		}
		b = bytes.NewReader(data)
	}
	u := c.BaseURL + path
	if c.Directory != "" {
		separator := "?"
		if strings.Contains(u, "?") {
			separator = "&"
		}
		u += separator + "directory=" + url.QueryEscape(c.Directory)
	}
	req, e := http.NewRequestWithContext(ctx, method, u, b)
	if e != nil {
		return nil, agent.E("CONFIG_ERROR")
	}
	req.Header.Set("Content-Type", "application/json")
	r, e := c.HTTP.Do(req)
	if e != nil {
		return nil, agent.E("RUNTIME_UNAVAILABLE")
	}
	return r, nil
}
func (c *Client) json(ctx context.Context, method, path string, body, out any) error {
	r, e := c.request(ctx, method, path, body)
	if e != nil {
		return e
	}
	defer r.Body.Close()
	if r.StatusCode == 404 {
		return agent.E("SESSION_LOST")
	}
	if r.StatusCode < 200 || r.StatusCode >= 300 {
		return agent.E("RUNTIME_UNAVAILABLE")
	}
	b, e := io.ReadAll(io.LimitReader(r.Body, 4*1024*1024+1))
	if e != nil || len(b) > 4*1024*1024 {
		return agent.E("RUNTIME_PROTOCOL_ERROR")
	}
	if out != nil && json.Unmarshal(b, out) != nil {
		return agent.E("RUNTIME_PROTOCOL_ERROR")
	}
	return nil
}
func (c *Client) RuntimeKind() string { return Kind }

// AgentCatalogueSupported is true: the OpenCode server owns a real agent
// catalogue, so a routing agent name is meaningful here and Core validates it.
func (c *Client) AgentCatalogueSupported() bool { return true }

func (c *Client) Health(ctx context.Context) (agent.Health, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var h agent.Health
	e := c.json(ctx, "GET", "/global/health", nil, &h)
	if e == nil && (!h.Healthy || h.Version == "") {
		e = agent.E("RUNTIME_PROTOCOL_ERROR")
	}
	return h, e
}
func (c *Client) ListAgents(ctx context.Context) ([]agent.AgentInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var a []agent.AgentInfo
	e := c.json(ctx, "GET", "/agent", nil, &a)
	if e == nil {
		for _, v := range a {
			if v.Name == "" || len(v.Name) > 64 {
				e = agent.E("RUNTIME_PROTOCOL_ERROR")
				break
			}
		}
	}
	return a, e
}

func (c *Client) ListModels(ctx context.Context) ([]agent.RuntimeModel, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for attempt := 0; attempt < 3; attempt++ {
		models, e := c.listModelsOnce(ctx)
		if e == nil {
			return models, nil
		}
		if agent.Safe(e).Code != "RUNTIME_PROTOCOL_ERROR" || attempt == 2 {
			return nil, e
		}
		select {
		case <-ctx.Done():
			return nil, agent.E("RUNTIME_UNAVAILABLE")
		case <-time.After(150 * time.Millisecond):
		}
	}
	return nil, agent.E("RUNTIME_PROTOCOL_ERROR")
}

func (c *Client) listModelsOnce(ctx context.Context) ([]agent.RuntimeModel, error) {
	var response struct {
		Data []struct {
			ID         string `json:"id"`
			ProviderID string `json:"providerID"`
			Name       string `json:"name"`
			Enabled    bool   `json:"enabled"`
		} `json:"data"`
	}
	path := "/api/model?location%5Bdirectory%5D=" + url.QueryEscape(c.Directory)
	r, e := c.request(ctx, "GET", path, nil)
	if e != nil {
		return nil, e
	}
	defer r.Body.Close()
	if r.StatusCode < 200 || r.StatusCode >= 300 {
		return nil, agent.E("RUNTIME_UNAVAILABLE")
	}
	// The real catalog can contain thousands of models. Decode only the four
	// public identity fields above, with a catalog-specific finite bound.
	b, e := io.ReadAll(io.LimitReader(r.Body, 16*1024*1024+1))
	if e != nil || len(b) > 16*1024*1024 || json.Unmarshal(b, &response) != nil {
		return nil, agent.E("RUNTIME_PROTOCOL_ERROR")
	}
	models := make([]agent.RuntimeModel, 0, len(response.Data))
	seen := map[string]bool{}
	for _, model := range response.Data {
		id := model.ProviderID + "/" + model.ID
		if !model.Enabled || !validModelID(id) || seen[id] {
			continue
		}
		seen[id] = true
		models = append(models, agent.RuntimeModel{ID: id, ProviderID: model.ProviderID, ModelID: model.ID, DisplayName: model.Name})
	}
	if len(models) == 0 {
		return nil, agent.E("RUNTIME_PROTOCOL_ERROR")
	}
	return models, nil
}

func validModelID(id string) bool {
	parts := strings.Split(id, "/")
	if len(parts) < 2 || len(id) > 256 {
		return false
	}
	for _, part := range parts {
		if !agentName(part) {
			return false
		}
	}
	return true
}

func agentName(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("_.-", r)) {
			return false
		}
	}
	return true
}

type permission struct {
	Permission string `json:"permission"`
	Pattern    string `json:"pattern"`
	Action     string `json:"action"`
}

// The effective upper bound. OpenCode hides every tool whose last matching
// rule is a deny, so a session below simply has no write, edit, bash, web,
// subagent, native read/glob/grep, or external-directory capability. Only the
// control-plane tools llmTools registers are re-allowed after the deny, and
// repository reads are served by llmTools code that re-confines every access.
func permissions(repoRead bool, token ...string) []permission {
	name := controlMCPName
	if len(token) > 0 {
		name = controlServerName(token[0])
	}
	out := []permission{{"*", "*", "deny"}, {"question", "*", "allow"}, {name + "_request_context", "*", "allow"}}
	if repoRead {
		out = append(out,
			permission{name + "_repo_read", "*", "allow"},
			permission{name + "_repo_glob", "*", "allow"},
			permission{name + "_repo_grep", "*", "allow"})
	}
	return out
}

func handlePermissions(h agent.RuntimeHandle) []permission {
	out := permissions(h.RepoRead, h.ControlToken)
	name := controlServerName(h.ControlToken)
	if h.RepoWrite {
		out = append(out, permission{name + "_repo_write", "*", "allow"}, permission{name + "_repo_edit", "*", "allow"})
	}
	if h.Shell {
		out = append(out, permission{name + "_repo_exec", "*", "allow"})
	}
	return out
}
func validHandleSession(s session, h agent.RuntimeHandle) bool {
	if !strings.HasPrefix(s.ID, "ses_") || strings.ContainsAny(s.ID, "/\\?#") {
		return false
	}
	if reflect.DeepEqual(s.Permission, handlePermissions(h)) {
		return true
	}
	return !h.RepoWrite && !h.Shell && validSession(s, h.RepoRead)
}

type session struct {
	ID         string       `json:"id"`
	Permission []permission `json:"permission"`
}

// validSession requires the exact ruleset to have persisted: an appended or
// reordered rule would silently widen the session's execution surface.
func validSession(s session, repoRead bool, token ...string) bool {
	want := permissions(repoRead, token...)
	return strings.HasPrefix(s.ID, "ses_") && !strings.ContainsAny(s.ID, "/\\?#") && reflect.DeepEqual(s.Permission, want)
}
func (c *Client) StartTask(ctx context.Context, t agent.AgentTask, runtimeAgent, runtimeModel string) (agent.RuntimeHandle, error) {
	if e := t.Capabilities.ValidateForRole(t.Agent); e != nil {
		return agent.RuntimeHandle{}, e
	}
	repoRead := t.Capabilities.RepoRead
	if repoRead && (t.Workspace == nil || t.Workspace.CanonicalRoot == "" || t.Workspace.CanonicalRoot != c.Directory) {
		// A read run must execute inside its own bound workspace, never the
		// managed runtime's scratch directory.
		return agent.RuntimeHandle{}, agent.E("WORKSPACE_ACCESS_DENIED")
	}
	if e := c.QuestionAvailable(ctx); e != nil {
		return agent.RuntimeHandle{}, e
	}
	token := randomID("ctl_")
	if e := c.ensureContextTool(ctx, token); e != nil {
		return agent.RuntimeHandle{}, e
	}
	registered := false
	defer func() {
		if !registered {
			c.disconnectControl(agent.RuntimeHandle{ControlToken: token})
		}
	}()
	var s session
	e := c.json(ctx, "POST", "/session", map[string]any{"title": "llmTools bounded task", "permission": handlePermissions(agent.RuntimeHandle{RepoRead: repoRead, RepoWrite: t.Capabilities.RepoWrite, Shell: t.Capabilities.Shell, ControlToken: token})}, &s)
	if e != nil {
		return agent.RuntimeHandle{}, agent.E("SESSION_CREATE_FAILED")
	}
	if !validHandleSession(s, agent.RuntimeHandle{RepoRead: repoRead, RepoWrite: t.Capabilities.RepoWrite, Shell: t.Capabilities.Shell, ControlToken: token}) {
		return agent.RuntimeHandle{}, agent.E("QUESTION_TOOL_UNAVAILABLE")
	}
	st, e := c.controlStore()
	if e != nil {
		return agent.RuntimeHandle{}, e
	}
	e = st.RegisterContextControl(token, s.ID)
	_ = st.Close()
	if e != nil {
		var ok bool
		_ = c.json(ctx, "POST", "/session/"+url.PathEscape(s.ID)+"/abort", nil, &ok)
		return agent.RuntimeHandle{}, e
	}
	source := "agent_default"
	if runtimeModel != "" {
		source = "explicit"
	}
	registered = true
	return agent.RuntimeHandle{SessionID: s.ID, Agent: runtimeAgent, Model: runtimeModel, ModelSource: source, ControlToken: token, RepoRead: repoRead, RepoWrite: t.Capabilities.RepoWrite, Shell: t.Capabilities.Shell, WorkspaceRoot: c.Directory}, nil
}
func (c *Client) verify(ctx context.Context, h agent.RuntimeHandle) error {
	var s session
	if e := c.json(ctx, "GET", "/session/"+url.PathEscape(h.SessionID), nil, &s); e != nil {
		return e
	}
	// Legacy suspended sessions keep their original exact ruleset.
	if !validHandleSession(s, h) || s.ID != h.SessionID {
		return agent.E("QUESTION_TOOL_UNAVAILABLE")
	}
	return nil
}
func (c *Client) idle(ctx context.Context, id string) (bool, error) {
	var s map[string]struct {
		Type string `json:"type"`
	}
	if e := c.json(ctx, "GET", "/session/status", nil, &s); e != nil {
		return false, e
	}
	v, ok := s[id]
	return !ok || v.Type == "idle", nil
}
func (c *Client) CancelTask(ctx context.Context, h agent.RuntimeHandle) error {
	if h.SessionID == "" {
		return nil
	}
	if h.ControlToken != "" {
		if st, e := c.controlStore(); e == nil {
			st.CancelContextControl(h.ControlToken)
			_ = st.Close()
		}
	}
	var ok bool
	if c.json(ctx, "POST", "/session/"+url.PathEscape(h.SessionID)+"/abort", nil, &ok) != nil || !ok {
		return agent.E("ABORT_UNCONFIRMED")
	}
	for i := 0; i < 20; i++ {
		idle, e := c.idle(ctx, h.SessionID)
		if e != nil {
			return agent.E("ABORT_UNCONFIRMED")
		}
		if idle {
			pending, e := c.ListPendingQuestions(ctx)
			if e != nil {
				return agent.E("ABORT_UNCONFIRMED")
			}
			rejected := false
			for _, q := range pending {
				if q.SessionID == h.SessionID {
					rejected = true
					if c.RejectQuestion(ctx, q) != nil {
						return agent.E("ABORT_UNCONFIRMED")
					}
				}
			}
			if rejected {
				// Reject wakes a Deferred; fence any resumed agent work with abort.
				if c.json(ctx, "POST", "/session/"+url.PathEscape(h.SessionID)+"/abort", nil, &ok) != nil || !ok {
					return agent.E("ABORT_UNCONFIRMED")
				}
				continue
			}
			remaining, e := c.ListPendingQuestions(ctx)
			if e != nil {
				return agent.E("ABORT_UNCONFIRMED")
			}
			for _, q := range remaining {
				if q.SessionID == h.SessionID {
					return agent.E("ABORT_UNCONFIRMED")
				}
			}
			c.disconnectControl(h)
			return nil
		}
		select {
		case <-ctx.Done():
			return agent.E("ABORT_UNCONFIRMED")
		case <-time.After(50 * time.Millisecond):
		}
	}
	return agent.E("ABORT_UNCONFIRMED")
}

// SubscribeProgress opens before prompt dispatch, so even short turns have an observer.
// SSE is advisory progress only. A disconnected stream reconnects twice; final
// completion is independently verified from the anchored assistant message and idle state.
func (c *Client) SubscribeProgress(ctx context.Context, h agent.RuntimeHandle, emit agent.Emit) (<-chan error, error) {
	return c.subscribe(ctx, h, emit, nil)
}
func (c *Client) subscribe(ctx context.Context, h agent.RuntimeHandle, emit agent.Emit, question func(Question) error) (<-chan error, error) {
	open := func() (*http.Response, error) {
		r, e := c.request(ctx, "GET", "/event", nil)
		if e != nil {
			return nil, e
		}
		if r.StatusCode != 200 || !strings.HasPrefix(r.Header.Get("Content-Type"), "text/event-stream") {
			r.Body.Close()
			return nil, agent.E("RUNTIME_PROTOCOL_ERROR")
		}
		return r, nil
	}
	first, e := open()
	if e != nil {
		return nil, e
	}
	done := make(chan error, 1)
	go func() {
		defer close(done)
		r := first
		for attempt := 0; attempt < 3; attempt++ {
			err := c.events(ctx, r, h, emit, question)
			r.Body.Close()
			if ctx.Err() != nil {
				return
			}
			if err != nil && agent.Safe(err).Code == "RUNTIME_PROTOCOL_ERROR" {
				done <- err
				return
			}
			if attempt == 2 {
				done <- agent.E("RUNTIME_UNAVAILABLE")
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(100 * time.Millisecond):
			}
			r, err = open()
			if err != nil {
				done <- err
				return
			}
		}
	}()
	return done, nil
}
func (c *Client) events(ctx context.Context, r *http.Response, h agent.RuntimeHandle, emit agent.Emit, question func(Question) error) error {
	sc := bufio.NewScanner(r.Body)
	sc.Buffer(make([]byte, 4096), 1024*1024)
	var data strings.Builder
	dispatch := func() error {
		if data.Len() == 0 {
			return nil
		}
		raw := data.String()
		data.Reset()
		var e struct {
			Type       string `json:"type"`
			Properties struct {
				SessionID string `json:"sessionID"`
				Part      struct {
					SessionID string `json:"sessionID"`
					Type      string `json:"type"`
				} `json:"part"`
				Info struct {
					SessionID string `json:"sessionID"`
				} `json:"info"`
			} `json:"properties"`
		}
		if json.Unmarshal([]byte(raw), &e) != nil || e.Type == "" {
			return agent.E("RUNTIME_PROTOCOL_ERROR")
		}
		id := e.Properties.SessionID
		if id == "" {
			id = e.Properties.Part.SessionID
		}
		if id == "" {
			id = e.Properties.Info.SessionID
		}
		if id != h.SessionID {
			return nil
		}
		stage := ""
		switch e.Type {
		case "message.part.delta":
			stage = "MODEL_GENERATING"
		case "message.part.updated":
			if e.Properties.Part.Type == "tool" {
				stage = "TOOL_RUNNING"
			} else if e.Properties.Part.Type == "text" {
				stage = "MODEL_GENERATING"
			}
		case "question.asked", "question.v2.asked":
			if question != nil {
				var envelope struct {
					Properties Question `json:"properties"`
				}
				if json.Unmarshal([]byte(raw), &envelope) != nil {
					return agent.E("RUNTIME_PROTOCOL_ERROR")
				}
				if err := question(envelope.Properties); err != nil {
					return err
				}
			}
			stage = "WAITING_FOR_DECISION"
		case "session.error":
			return agent.E("RUNTIME_UNAVAILABLE")
		case "permission.asked":
			return agent.E("RUNTIME_PROTOCOL_ERROR")
		}
		if stage != "" && emit != nil {
			emit(agent.Progress{Stage: stage})
		}
		return nil
	}
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			if e := dispatch(); e != nil {
				return e
			}
		} else if strings.HasPrefix(line, "data:") {
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	if errors.Is(sc.Err(), bufio.ErrTooLong) && ctx.Err() == nil {
		return agent.E("RUNTIME_PROTOCOL_ERROR")
	}
	return io.EOF
}
