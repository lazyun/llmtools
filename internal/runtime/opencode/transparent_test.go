package opencode

import (
	"ai-worker-mcp/internal/agent"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestBoundControlConnectionKeepsTokenOutOfModelInputs(t *testing.T) {
	st, token, _ := openRepoGrant(t)
	if err := st.RegisterContextControl(token, "ses_1"); err != nil {
		t.Fatal(err)
	}
	server := sdk.NewServer(&sdk.Implementation{Name: "control-test", Version: "1"}, nil)
	if err := addRepoTools(server, st, token); err != nil {
		t.Fatal(err)
	}
	if err := addContextControlTool(server, st, token); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ct, ss := sdk.NewInMemoryTransports()
	session, err := server.Connect(ctx, ss, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	client, err := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	list, err := client.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range list.Tools {
		b, _ := json.Marshal(tool.InputSchema)
		if strings.Contains(string(b), "control_token") || strings.Contains(string(b), token) {
			t.Fatalf("model sees authority: %s", b)
		}
	}
	res, err := client.CallTool(ctx, &sdk.CallToolParams{Name: "repo_read", Arguments: map[string]any{"path": "notes.md"}})
	if err != nil || res.IsError {
		t.Fatalf("bound read: %+v %v", res, err)
	}
	b, _ := json.Marshal(res.Content)
	if !strings.Contains(string(b), "ALPHA-7f3c9d2e") {
		t.Fatalf("wrong repository: %s", b)
	}
	// Supplying a different token must never redirect the connection's authority.
	res, err = client.CallTool(ctx, &sdk.CallToolParams{Name: "repo_read", Arguments: map[string]any{"path": "notes.md", "control_token": "ctl_" + strings.Repeat("f", 32)}})
	if err == nil && !res.IsError {
		b, _ = json.Marshal(res.Content)
		if !strings.Contains(string(b), "ALPHA-7f3c9d2e") {
			t.Fatal("caller replaced bound authority")
		}
	}
	answers := []agent.AdditionalContext{{RequestID: "ctx_1", Type: "text", Content: " \t原文\r\n "}}
	done := make(chan error, 1)
	go func() {
		res, err := client.CallTool(ctx, &sdk.CallToolParams{Name: "request_context", Arguments: map[string]any{"requests": []agent.ContextRequest{{ID: "ctx_1", Kind: "other", Target: "parent fact", Reason: "needed"}}}})
		if err == nil {
			if res.IsError {
				err = agent.E("INVALID_AGENT_RESULT")
			} else {
				var got []agent.AdditionalContext
				text := res.Content[0].(*sdk.TextContent).Text
				if json.Unmarshal([]byte(text), &got) != nil || len(got) != 1 || got[0].Content != answers[0].Content {
					err = agent.E("INVALID_CONTEXT_RESPONSE")
				}
			}
		}
		done <- err
	}()
	for {
		control, err := st.ContextControl(token)
		if err != nil {
			t.Fatal(err)
		}
		if control.State == "pending" {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("bound context tool did not suspend")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if err := st.AnswerContextControl(token, answers); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestSessionRulesPermitOnlyTheirBoundControlConnection(t *testing.T) {
	a, b := "ctl_"+strings.Repeat("a", 32), "ctl_"+strings.Repeat("b", 32)
	for _, read := range []bool{false, true} {
		first := session{ID: "ses_one", Permission: permissions(read, a)}
		if !validSession(first, read, a) || validSession(first, read, b) {
			t.Fatal("session authority was interchangeable")
		}
		if hasPermission(first.Permission, controlServerName(b)+"_repo_read") || hasPermission(first.Permission, controlServerName(b)+"_request_context") {
			t.Fatal("cross-session tool permitted")
		}
		for _, rule := range first.Permission {
			if strings.Contains(rule.Permission, a) {
				t.Fatal("token leaked into tool name")
			}
		}
	}
}
