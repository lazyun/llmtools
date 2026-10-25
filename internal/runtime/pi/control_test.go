package pi

import (
	"ai-worker-mcp/internal/agent"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func boolPtr(v bool) *bool { return &v }

func TestParseControlRecognisesOnlyTheBridgeTools(t *testing.T) {
	for _, name := range []string{"llmtools_control_repo_read", "llmtools_control_repo_glob", "", "read"} {
		if c := parseControl(name, json.RawMessage(`{"llmtools_control":"context","requests":[{"id":"1","kind":"file","target":"a.go","reason":"need it"}]}`)); c != nil {
			t.Fatalf("%q produced a suspension", name)
		}
	}
	c := parseControl("llmtools_control_request_context", json.RawMessage(`{"llmtools_control":"context","requests":[{"id":"r1","kind":"contract","target":"release","reason":"only the parent holds it"}]}`))
	if c == nil || c.kind != "context" || len(c.requests) != 1 {
		t.Fatalf("context payload: %+v", c)
	}
	if c.requests[0].ID != "r1" || c.requests[0].Target != "release" {
		t.Fatalf("request mapping: %+v", c.requests[0])
	}
	d := parseControl("llmtools_control_request_decision", json.RawMessage(`{"llmtools_control":"decision","questions":[{"id":"q1","question":"Which shape?"}]}`))
	if d == nil || d.kind != "decision" || len(d.questions) != 1 || d.questions[0].Question != "Which shape?" {
		t.Fatalf("decision payload: %+v", d)
	}
}

// A control result that does not name its own kind is ordinary execution: the
// adapter must not suspend on an unrelated payload that happens to carry the
// field names.
func TestParseControlIgnoresForeignAndMalformedPayloads(t *testing.T) {
	c := parseControl("llmtools_control_request_context", json.RawMessage(`{"llmtools_control":"decision","requests":[{"id":"r1","kind":"file","target":"a","reason":"b"}]}`))
	if c == nil || c.kind != "context" || len(c.requests) != 0 {
		t.Fatalf("foreign control marker accepted: %+v", c)
	}
	if c = parseControl("llmtools_control_request_context", json.RawMessage(`{`)); c == nil || len(c.requests) != 0 {
		t.Fatalf("malformed payload: %+v", c)
	}
	if c = parseControl("llmtools_control_request_decision", nil); c == nil || len(c.questions) != 0 {
		t.Fatalf("empty details: %+v", c)
	}
}

// The bridge is model-authored JSON, so every field is bounded and defaulted on
// the way into Core's contract.
func TestParseControlBoundsModelSuppliedFields(t *testing.T) {
	c := parseControl("llmtools_control_request_context", json.RawMessage(`{"llmtools_control":"context","requests":[{"id":"`+strings.Repeat("i", 200)+`","target":"`+strings.Repeat("t", 9000)+`","reason":"`+strings.Repeat("r", 20000)+`"}]}`))
	if len(c.requests) != 1 {
		t.Fatalf("requests: %+v", c.requests)
	}
	if len(c.requests[0].ID) != 80 || len(c.requests[0].Target) != 4096 || len(c.requests[0].Reason) != 16384 {
		t.Fatalf("unbounded request: %+v", c.requests[0])
	}
	// Pi models omit the kind constantly; Core requires one of its own values.
	c = parseControl("llmtools_control_request_context", json.RawMessage(`{"llmtools_control":"context","requests":[{"id":"r1","target":"a","reason":"b"}]}`))
	if c.requests[0].Kind != "other" || !agent.ContextRequestsValid(c.requests) {
		t.Fatalf("defaulted kind: %+v", c.requests[0])
	}
	c = parseControl("llmtools_control_request_context", json.RawMessage(`{"llmtools_control":"context","requests":[{"id":"r1","kind":"screenshot","target":"a","reason":"b"}]}`))
	if agent.ContextRequestsValid(c.requests) {
		t.Fatal("an unknown kind reached Core")
	}
}

func TestDecisionsOfNormalizesBridgeQuestions(t *testing.T) {
	c := &pendingControl{kind: "decision", questions: []piQuestion{
		{ID: "q1", Question: "  Which public API shape?  ", Header: strings.Repeat("h", 500), Options: []struct {
			Label       string `json:"label"`
			Description string `json:"description,omitempty"`
		}{
			{Label: " report(rows) ", Description: "flat list"},
			{Label: "report(rows) ", Description: "duplicate label"},
			{Label: "", Description: "no label"},
			{Label: "report(cursor)"},
		}},
		{ID: "", Question: "First unlabelled question"},
		{ID: "", Question: "Second unlabelled question"},
		{ID: "empty", Question: "   "},
		{ID: "nocustom", Question: "Choose one of none", AllowCustom: boolPtr(false)},
	}}
	h := agent.RuntimeHandle{SessionID: "ses_1"}
	got := decisionsOf(c, h)
	// One question per distinct id; blank questions and an option-less question
	// that forbids a custom answer carry nothing a parent could decide.
	if len(got) != 2 {
		t.Fatalf("decisions: %+v", got)
	}
	first := got[0]
	if first.Question != "Which public API shape?" {
		t.Fatalf("question text: %q", first.Question)
	}
	if len(first.Header) != 200 {
		t.Fatalf("header length: %d", len(first.Header))
	}
	if first.Reason == "" || !first.AllowCustom || first.Multiple {
		t.Fatalf("defaults: %+v", first)
	}
	if len(first.Options) != 2 {
		t.Fatalf("options: %+v", first.Options)
	}
	if first.Options[0].Description != "report(rows) — flat list" {
		t.Fatalf("composed description: %q", first.Options[0].Description)
	}
	if first.Options[1].Description != "report(cursor)" || first.Options[1].ID != "option_4" {
		t.Fatalf("label-only option: %+v", first.Options[1])
	}
	// Option ids stay stable per question position, so a resumed process can
	// match the parent's answer even though labels were deduplicated.
	if !strings.HasPrefix(got[1].ID, "pi_dec_") || got[1].ID == first.ID {
		t.Fatalf("request ids: %q %q", first.ID, got[1].ID)
	}
	if c := decisionsOf(&pendingControl{kind: "context"}, h); c != nil {
		t.Fatalf("a context suspension produced decisions: %+v", c)
	}
}

func TestDecisionIDIsStableWithinASession(t *testing.T) {
	a, b := decisionID("ses_1", "q1", 0), decisionID("ses_1", "q1", 0)
	if a != b {
		t.Fatalf("unstable id: %q %q", a, b)
	}
	if a == decisionID("ses_2", "q1", 0) || a == decisionID("ses_1", "q2", 0) || a == decisionID("ses_1", "q1", 1) {
		t.Fatal("decision id does not bind session, question and position")
	}
	if len(a) > 80 {
		t.Fatalf("id too long for Core's request-id budget: %d", len(a))
	}
}

func TestSuspensionCarriesCoreOutcomesAndDurableState(t *testing.T) {
	h := agent.RuntimeHandle{SessionID: "ses_1", ControlToken: "ctl_x", Model: "openai/gpt-5-mini"}
	a := &Adapter{}
	requests := []agent.ContextRequest{{ID: "r1", Kind: "contract", Target: "release", Reason: "parent-held"}}
	e := a.suspension(h, &pendingControl{kind: "context", requests: requests})
	var pending *agent.ContextPending
	if !errors.As(e, &pending) {
		t.Fatalf("context suspension: %v", e)
	}
	if len(pending.Requests) != 1 || pending.Requests[0].ID != "r1" {
		t.Fatalf("pending requests: %+v", pending.Requests)
	}
	var state runState
	if err := agent.Strict(pending.State, &state); err != nil {
		t.Fatal(err)
	}
	if state.Version != stateVersion || state.SessionID != "ses_1" || state.ControlToken != "ctl_x" || state.Model != h.Model {
		t.Fatalf("durable state: %+v", state)
	}

	e = a.suspension(h, &pendingControl{kind: "decision", questions: []piQuestion{{ID: "q1", Question: "Which shape?", Options: []struct {
		Label       string `json:"label"`
		Description string `json:"description,omitempty"`
	}{{Label: "flat"}, {Label: "cursor"}}}}})
	var dpending *agent.DecisionPending
	if !errors.As(e, &dpending) || len(dpending.Requests) != 1 || len(dpending.Requests[0].Options) != 2 {
		t.Fatalf("decision suspension: %v", e)
	}
	// Nothing usable to ask the parent is a malformed handoff, never a silent
	// pass, and it reports the same code the OpenCode control tool reports.
	if codeOf(t, a.suspension(h, &pendingControl{kind: "context"})) != "CONTEXT_INPUT_INVALID" {
		t.Fatal("empty context suspension accepted")
	}
	if codeOf(t, a.suspension(h, &pendingControl{kind: "context", requests: []agent.ContextRequest{
		{ID: "r1", Kind: "fact", Target: "the release code", Reason: "only the parent holds it"},
	}})) != "CONTEXT_INPUT_INVALID" {
		t.Fatal("context suspension with an unaccepted kind was accepted")
	}
	if codeOf(t, a.suspension(h, &pendingControl{kind: "decision", questions: []piQuestion{{ID: "q"}}})) != "RUNTIME_PROTOCOL_ERROR" {
		t.Fatal("empty decision accepted")
	}
}

func TestPendingStateRefusesForeignState(t *testing.T) {
	h := agent.RuntimeHandle{SessionID: "ses_1", ControlToken: "ctl_x"}
	a := &Adapter{}
	ok := runState{Version: stateVersion, SessionID: "ses_1", ControlToken: "ctl_x", Model: "openai/gpt-5-mini",
		Requests: []agent.ContextRequest{{ID: "r1", Kind: "file", Target: "a.go", Reason: "why"}}}
	raw, e := json.Marshal(ok)
	if e != nil {
		t.Fatal(e)
	}
	state, e := a.pendingState(raw, h, "context")
	if e != nil || state.Requests[0].ID != "r1" {
		t.Fatalf("valid state refused: %+v %v", state, e)
	}
	foreign, _ := json.Marshal(map[string]any{
		"version": stateVersion, "session_id": "ses_2", "control_token": "ctl_x", "model": "openai/gpt-5-mini",
	})
	decisionOnly, _ := json.Marshal(map[string]any{"unknown": true})
	for _, tc := range []struct {
		name, kind string
		raw        json.RawMessage
		handle     agent.RuntimeHandle
		code       string
	}{
		{"empty state", "context", nil, h, "PENDING_CONTEXT_LOST"},
		{"unknown fields", "decision", decisionOnly, h, "PENDING_DECISION_LOST"},
		{"foreign session", "context", foreign, h, "PENDING_CONTEXT_LOST"},
		{"wrong version", "context", json.RawMessage(`{"version":99,"session_id":"ses_1","control_token":"ctl_x"}`), h, "PENDING_CONTEXT_LOST"},
		{"missing token", "context", json.RawMessage(`{"version":1,"session_id":"ses_1","control_token":""}`), h, "PENDING_CONTEXT_LOST"},
		{"foreign token", "context", json.RawMessage(`{"version":1,"session_id":"ses_1","control_token":"ctl_y"}`), h, "PENDING_CONTEXT_LOST"},
		{"handle without a token", "decision", raw, agent.RuntimeHandle{SessionID: "ses_1"}, "PENDING_DECISION_LOST"},
	} {
		if _, e := a.pendingState(tc.raw, tc.handle, tc.kind); codeOf(t, e) != tc.code {
			t.Fatalf("%s: %v", tc.name, e)
		}
	}
}
