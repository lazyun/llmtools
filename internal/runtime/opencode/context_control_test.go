package opencode

// Model-free coverage of the blocking context handoff. The runtime starts this
// server as a child process, so the only way to exercise its input contract
// without a model is to register the same tool on an in-memory MCP session.

import (
	"ai-worker-mcp/internal/agent"
	"ai-worker-mcp/internal/store"
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

var stderrMu sync.Mutex

func contextControlSession(t *testing.T, st *store.Store) *sdk.ClientSession {
	t.Helper()
	server := sdk.NewServer(&sdk.Implementation{Name: controlMCPName, Version: "0.3.0"}, nil)
	if e := addContextControlTool(server, st); e != nil {
		t.Fatal(e)
	}
	client := sdk.NewClient(&sdk.Implementation{Name: "context-control-test", Version: "0.3.0"}, nil)
	clientTransport, serverTransport := sdk.NewInMemoryTransports()
	if _, e := server.Connect(context.Background(), serverTransport, nil); e != nil {
		t.Fatal(e)
	}
	cs, e := client.Connect(context.Background(), clientTransport, nil)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func contextRequestArgs(token string, requests ...map[string]any) map[string]any {
	return map[string]any{"control_token": token, "requests": requests}
}

// captureStderr redirects the process error stream for the duration of fn and
// returns what was written to it. The control tool reports a refusal there,
// because the agent-facing error must stay free of the request payload.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	stderrMu.Lock()
	defer stderrMu.Unlock()
	read, write, e := os.Pipe()
	if e != nil {
		t.Fatal(e)
	}
	original := os.Stderr
	os.Stderr = write
	collected := make(chan string, 1)
	go func() {
		body, _ := io.ReadAll(read)
		collected <- string(body)
	}()
	fn()
	e = write.Close()
	os.Stderr = original
	if e != nil {
		t.Fatal(e)
	}
	select {
	case out := <-collected:
		return out
	case <-time.After(5 * time.Second):
		t.Fatal("stderr was never flushed")
		return ""
	}
}

// TestContextControlToolAdvertisesAcceptedKinds is the regression a real model
// produced: with an open string in the schema it sent kind="fact" for a value
// that is not a file, and the suspension was refused. The tool now names the
// kinds Core accepts, and a refusal says so in terms the model can act on.
func TestContextControlToolAdvertisesAcceptedKinds(t *testing.T) {
	st, token, _ := openRepoGrant(t)
	if e := st.RegisterContextControl(token, "ses_1"); e != nil {
		t.Fatal(e)
	}
	cs := contextControlSession(t, st)
	list, e := cs.ListTools(context.Background(), nil)
	if e != nil {
		t.Fatal(e)
	}
	var tool *sdk.Tool
	for _, x := range list.Tools {
		if x.Name == "request_context" {
			tool = x
		}
	}
	if tool == nil {
		t.Fatalf("request_context is not registered: %v", list.Tools)
	}
	raw, e := json.Marshal(tool.InputSchema)
	if e != nil {
		t.Fatal(e)
	}
	schema := string(raw)
	for _, kind := range agent.ContextRequestKinds {
		if !strings.Contains(schema, `"`+kind+`"`) {
			t.Fatalf("schema does not advertise kind %q: %s", kind, schema)
		}
	}
	if !strings.Contains(tool.Description, "file") || !strings.Contains(tool.Description, "contract") {
		t.Fatalf("tool description names no accepted kind: %q", tool.Description)
	}
	for _, forbidden := range []string{"\"root\"", "\"directory\"", "\"cwd\"", "workspace_root"} {
		if strings.Contains(strings.ToLower(schema), forbidden) {
			t.Fatalf("context tool schema exposes %q: %s", forbidden, schema)
		}
	}
	// A kind outside the enumeration is refused with the code that explains it,
	// and the operator line carries the rule name only: the request itself may
	// name a private file or carry parent-only text.
	var callError error
	logLine := captureStderr(t, func() {
		_, callError = cs.CallTool(context.Background(), &sdk.CallToolParams{Name: "request_context", Arguments: contextRequestArgs(token,
			map[string]any{"id": "ctx_release", "kind": "fact", "target": "the private release code", "reason": "only the parent holds it"})})
	})
	if callError == nil || !strings.Contains(callError.Error(), "CONTEXT_INPUT_INVALID") {
		t.Fatalf("unaccepted kind was not refused with the context code: %v", callError)
	}
	if !strings.Contains(logLine, "context_control_rejected reason=UNKNOWN_KIND") {
		t.Fatalf("operator stream does not name the refused rule: %q", logLine)
	}
	for _, leak := range []string{token, "the private release code", "only the parent holds it"} {
		if strings.Contains(logLine, leak) || strings.Contains(callError.Error(), leak) {
			t.Fatalf("refusal carried %q", leak)
		}
	}
	// The run never entered a pending state, so no parent answer is outstanding.
	control, e := st.ContextControl(token)
	if e != nil || control.State != "ready" {
		t.Fatalf("a refused context request left state %q: %v", control.State, e)
	}
}

// TestContextControlToolSuspendsUntilTheParentAnswers covers the handoff itself:
// the tool call blocks, the request is durable, the parent's answer is what the
// execution receives, and the state records single delivery.
func TestContextControlToolSuspendsUntilTheParentAnswers(t *testing.T) {
	st, token, _ := openRepoGrant(t)
	if e := st.RegisterContextControl(token, "ses_1"); e != nil {
		t.Fatal(e)
	}
	cs := contextControlSession(t, st)
	type outcome struct {
		text string
		e    error
	}
	done := make(chan outcome, 1)
	go func() {
		res, e := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: "request_context", Arguments: contextRequestArgs(token,
			map[string]any{"id": "ctx_release", "kind": "contract", "target": "release code", "reason": "held only by the parent"})})
		if e != nil {
			done <- outcome{e: e}
			return
		}
		done <- outcome{text: textOf(res)}
	}()
	deadline := time.Now().Add(10 * time.Second)
	for {
		control, e := st.ContextControl(token)
		if e != nil {
			t.Fatal(e)
		}
		if control.State == "pending" {
			if len(control.Requests) != 1 || control.Requests[0].Kind != "contract" || control.Requests[0].ID != "ctx_release" {
				t.Fatalf("durable request: %+v", control.Requests)
			}
			break
		}
		if !time.Now().Before(deadline) {
			t.Fatal("the context request never became pending")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if e := st.AnswerContextControl(token, []agent.AdditionalContext{
		{RequestID: "ctx_release", Type: "contract", Content: "RELEASE-8b3e6d1a"},
	}); e != nil {
		t.Fatal(e)
	}
	select {
	case out := <-done:
		if out.e != nil {
			t.Fatalf("the answered handoff failed: %v", out.e)
		}
		if !strings.Contains(out.text, "RELEASE-8b3e6d1a") {
			t.Fatalf("the parent's answer did not reach the execution: %s", out.text)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the blocked tool call never returned after the parent answered")
	}
	if control, e := st.ContextControl(token); e != nil || control.State != "delivered" {
		t.Fatalf("state after delivery: %q %v", control.State, e)
	}
}

// TestContextRequestsFollowOneShapeRule keeps the shared validator and the
// per-field diagnostic in step: any payload the validator refuses has to have a
// name, or an operator sees a refusal with no way to tell which rule fired.
func TestContextRequestsFollowOneShapeRule(t *testing.T) {
	cases := []struct {
		name     string
		requests []agent.ContextRequest
		want     string
	}{
		{"accepted", []agent.ContextRequest{{ID: "r1", Kind: "file", Target: "a.go", Reason: "why"}}, ""},
		{"empty", nil, "EMPTY"},
		{"unknown kind", []agent.ContextRequest{{ID: "r1", Kind: "fact", Target: "x", Reason: "why"}}, "UNKNOWN_KIND"},
		{"missing id", []agent.ContextRequest{{Kind: "file", Target: "x", Reason: "why"}}, "MISSING_ID"},
		{"duplicate id", []agent.ContextRequest{{ID: "r1", Kind: "file", Target: "x", Reason: "w"}, {ID: "r1", Kind: "file", Target: "y", Reason: "w"}}, "DUPLICATE_ID"},
		{"missing target", []agent.ContextRequest{{ID: "r1", Kind: "file", Target: "  ", Reason: "why"}}, "MISSING_TARGET"},
		{"missing reason", []agent.ContextRequest{{ID: "r1", Kind: "file", Target: "x"}}, "MISSING_REASON"},
	}
	for _, c := range cases {
		if got := agent.ContextRequestRejection(c.requests); got != c.want {
			t.Fatalf("%s: rejection = %q want %q", c.name, got, c.want)
		}
		if agent.ContextRequestsValid(c.requests) != (c.want == "") {
			t.Fatalf("%s: validator and diagnostic disagree", c.name)
		}
	}
	// A payload is never part of the diagnostic, so nothing here can leak.
	if strings.Contains(agent.ContextRequestRejection([]agent.ContextRequest{{ID: "r", Kind: "secret-kind", Target: "secret-target", Reason: "secret-reason"}}), "secret") {
		t.Fatal("rejection carried request content")
	}
}
