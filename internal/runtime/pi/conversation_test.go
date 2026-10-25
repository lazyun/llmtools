package pi

import (
	"ai-worker-mcp/internal/agent"
	"ai-worker-mcp/internal/config"
	workerMCP "ai-worker-mcp/internal/mcp"
	"ai-worker-mcp/internal/service"
	"ai-worker-mcp/internal/store"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func conversationService(t *testing.T) (*service.Service, *Adapter) {
	t.Helper()
	r := fakeRuntimeSetup(t)
	t.Setenv("FAKE_PI_UNIQUE_SESSIONS", "1")
	t.Setenv("FAKE_PI_TEXT", "task completed")
	c := config.Default()
	c.Directory = filepath.Dir(r.adapter.spec.ControlDB)
	c.Runtime = config.Runtime{Type: Kind, Binary: r.script, SessionDir: r.adapter.spec.SessionDir}
	c.Agents = map[string]config.AgentRouting{}
	for _, role := range []string{"implementation", "review", "research"} {
		c.Agents[role] = config.AgentRouting{Exposed: true, Model: "openai/gpt-5-mini"}
	}
	st, err := store.Open(r.adapter.spec.ControlDB)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if _, _, err = st.AddWorkspace(t.TempDir(), "repo", "desktop_user_grant"); err != nil {
		t.Fatal(err)
	}
	return service.New(c, st, r.adapter), r.adapter
}

func delegateConversation(t *testing.T, s *service.Service, thread, role string) (agent.AgentResult, agent.Run) {
	t.Helper()
	ctx := agent.WithConversation(context.Background(), thread)
	raw, _ := json.Marshal(agent.DelegateTask{Prompt: "task for " + role})
	result := s.RunRole(ctx, role, raw, nil)
	run, err := s.Store.Get(result.WorkerRunID)
	if err != nil {
		t.Fatalf("run unavailable: %+v, %v", result, err)
	}
	return result, run
}

func TestConversationReusesPiSessionAcrossTasksRolesAndReconnects(t *testing.T) {
	s, a := conversationService(t)
	first, before := delegateConversation(t, s, "codex-A", "implementation")
	if first.Status != "completed" {
		t.Fatal(first)
	}
	// A new store connection and adapter have no in-memory session mapping.
	st, err := store.Open(a.spec.ControlDB)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	b, err := New(a.spec)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	reconnected := service.New(s.Config, st, b)
	second, after := delegateConversation(t, reconnected, "codex-A", "research")
	if second.Status != "completed" || after.ID == before.ID || after.RuntimeSessionID != before.RuntimeSessionID || after.ConversationKey != before.ConversationKey {
		t.Fatalf("session not reused: %+v -> %+v (%+v)", before, after, second)
	}
	if after.RuntimeControl == before.RuntimeControl || after.Capabilities.RepoRead || !before.Capabilities.RepoRead {
		t.Fatal("new task inherited prior authority")
	}
	third, other := delegateConversation(t, s, "codex-B", "review")
	if third.Status != "completed" || other.RuntimeSessionID == before.RuntimeSessionID {
		t.Fatal("different Codex conversations shared a session", third)
	}
	_, legacy1 := delegateConversation(t, s, "", "research")
	_, legacy2 := delegateConversation(t, s, "", "research")
	if legacy1.RuntimeSessionID == legacy2.RuntimeSessionID || legacy1.ConversationKey != "" {
		t.Fatal("requests without host identity were conflated")
	}
}

func TestConversationKeepsPendingRunUntilContinuedOrCancelled(t *testing.T) {
	for _, kind := range []string{"context", "decision"} {
		t.Run(kind, func(t *testing.T) {
			s, _ := conversationService(t)
			t.Setenv("FAKE_PI_TOOL", "llmtools_control_request_"+kind)
			details := `{"llmtools_control":"context","requests":[{"id":"q1","kind":"other","target":"requirement","reason":"missing fact"}]}`
			if kind == "decision" {
				details = `{"llmtools_control":"decision","questions":[{"id":"q1","question":"Which option?"}]}`
			}
			t.Setenv("FAKE_PI_DETAILS", details)
			pending, before := delegateConversation(t, s, "codex-A", "implementation")
			if pending.Status != "needs_"+kind {
				t.Fatal(pending)
			}
			blocked, _ := delegateConversation(t, s, "codex-A", "research")
			if blocked.Error == nil || blocked.Error.Code != "SESSION_BUSY" || blocked.WorkerRunID != before.ID {
				t.Fatal("pending run was replaced", blocked)
			}
			t.Setenv("FAKE_PI_TOOL", "")
			input := agent.Continuation{WorkerRunID: before.ID}
			if kind == "context" {
				input.AdditionalContext = []agent.AdditionalContext{{Type: "text", Content: "the missing fact"}}
			} else {
				input.DecisionResponses = []agent.DecisionResponse{{CustomText: "use option A"}}
			}
			raw, _ := json.Marshal(input)
			continued := s.Continue(context.Background(), raw, nil)
			if continued.Status != "completed" {
				t.Fatal(continued)
			}
			next, after := delegateConversation(t, s, "codex-A", "research")
			if next.Status != "completed" || before.RuntimeSessionID != after.RuntimeSessionID {
				t.Fatal("completed continuation lost conversation history", next)
			}
			// Cancel a later suspension, then use that same conversation again.
			t.Setenv("FAKE_PI_TOOL", "llmtools_control_request_"+kind)
			waiting, _ := delegateConversation(t, s, "codex-A", "implementation")
			if cancelled := s.Cancel(context.Background(), waiting.WorkerRunID); cancelled.Status != "cancelled" {
				t.Fatal(cancelled)
			}
			t.Setenv("FAKE_PI_TOOL", "")
			last, final := delegateConversation(t, s, "codex-A", "review")
			if last.Status != "completed" || final.RuntimeSessionID != before.RuntimeSessionID {
				t.Fatal("cancelled task discarded the shared session", last)
			}
		})
	}
}

func TestConversationNewTaskUsesCurrentRoutingAndWorkspace(t *testing.T) {
	s, _ := conversationService(t)
	first, before := delegateConversation(t, s, "codex-A", "implementation")
	if first.Status != "completed" {
		t.Fatal(first)
	}
	workspace, _, err := s.Store.AddWorkspace(t.TempDir(), "second repo", "desktop_user_grant")
	if err != nil {
		t.Fatal(err)
	}
	s.Config.Agents["review"] = config.AgentRouting{Exposed: true, Model: "huggingface/deepseek-ai/DeepSeek-V4-Flash"}
	t.Setenv("FAKE_PI_PROVIDER", "huggingface")
	t.Setenv("FAKE_PI_MODEL", "deepseek-ai/DeepSeek-V4-Flash")
	ctx := agent.WithConversation(context.Background(), "codex-A")
	raw, _ := json.Marshal(agent.DelegateTask{Prompt: "review second repo", WorkspaceID: workspace.ID})
	second := s.RunRole(ctx, "review", raw, nil)
	after, err := s.Store.Get(second.WorkerRunID)
	if err != nil || second.Status != "completed" || after.RuntimeSessionID != before.RuntimeSessionID {
		t.Fatal("routing change replaced the conversation session", second, err)
	}
	if after.RuntimeModel != "huggingface/deepseek-ai/DeepSeek-V4-Flash" || after.Workspace.WorkspaceID != workspace.ID || after.RuntimeControl == before.RuntimeControl {
		t.Fatal("new task did not freeze its own route and workspace", after)
	}
	if _, err := s.Store.RepoAccessByRun(before.ID); err == nil {
		t.Fatal("completed task retained repository authority")
	}
}

func TestConversationRejectsConcurrentDispatchAcrossStores(t *testing.T) {
	s, a := conversationService(t)
	st, err := store.Open(a.spec.ControlDB)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	other := service.New(s.Config, st, a)
	ctx := agent.WithConversation(context.Background(), "codex-A")
	raw, _ := json.Marshal(agent.DelegateTask{Prompt: "first"})
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan agent.AgentResult, 1)
	go func() {
		done <- s.RunRole(ctx, "research", raw, func(p agent.Progress) {
			if p.Stage == "RUNNING" {
				close(entered)
				<-release
			}
		})
	}()
	select {
	case <-entered:
	case result := <-done:
		t.Fatalf("first call never started: %+v", result)
	case <-time.After(10 * time.Second):
		t.Fatal("first call timed out")
	}
	blocked := other.RunRole(ctx, "research", raw, nil)
	close(release)
	if blocked.Error == nil || blocked.Error.Code != "SESSION_BUSY" {
		t.Fatal("concurrent dispatch was accepted", blocked)
	}
	if result := <-done; result.Status != "completed" {
		t.Fatal(result)
	}
}

func TestMCPThreadMetadataControlsPiSessionReuse(t *testing.T) {
	s, _ := conversationService(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	connect := func() *sdk.ClientSession {
		ct, st := sdk.NewInMemoryTransports()
		ss, err := workerMCP.New(s).Connect(ctx, st, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { ss.Close() })
		cs, err := sdk.NewClient(&sdk.Implementation{Name: "Codex", Version: "test"}, nil).Connect(ctx, ct, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { cs.Close() })
		return cs
	}
	call := func(client *sdk.ClientSession, thread, role string) agent.Run {
		params := &sdk.CallToolParams{Name: "delegate_" + role, Arguments: agent.DelegateTask{Prompt: "MCP task"}}
		params.SetMeta(map[string]any{"threadId": thread})
		res, err := client.CallTool(ctx, params)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := json.Marshal(res.StructuredContent)
		var result agent.AgentResult
		if json.Unmarshal(body, &result) != nil || result.Status != "completed" {
			t.Fatalf("call failed: %s", body)
		}
		run, err := s.Store.Get(result.WorkerRunID)
		if err != nil {
			t.Fatal(err)
		}
		return run
	}
	client := connect()
	first := call(client, "codex-A", "implementation")
	second := call(connect(), "codex-A", "research")
	third := call(client, "codex-B", "review")
	if first.RuntimeSessionID != second.RuntimeSessionID || first.RuntimeSessionID == third.RuntimeSessionID {
		t.Fatal("MCP transport identity substituted for the Codex thread")
	}
}

func TestStartTaskInSessionRefusesReplacementSession(t *testing.T) {
	r := fakeRuntimeSetup(t)
	t.Setenv("FAKE_PI_SESSION", "unexpected-session")
	_, err := r.adapter.StartTaskInSession(context.Background(), agent.AgentTask{}, "research", "openai/gpt-5-mini", "original-session")
	if codeOf(t, err) != "SESSION_LOST" {
		t.Fatal("missing session was silently replaced", err)
	}
}

func TestNewTaskRestoresSessionWithoutReplayingOriginalTask(t *testing.T) {
	s, _ := conversationService(t)
	logPath := filepath.Join(t.TempDir(), "rpc.jsonl")
	t.Setenv("FAKE_PI_LOG", logPath)
	first, before := delegateConversation(t, s, "codex-A", "research")
	second, after := delegateConversation(t, s, "codex-A", "review")
	if first.Status != "completed" || second.Status != "completed" || before.RuntimeSessionID != after.RuntimeSessionID {
		t.Fatal(first, second)
	}
	log, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(log), `"type":"prompt"`) != 2 {
		t.Fatalf("tasks replayed: %s", log)
	}
}

// Exercise the complete public MCP -> Core -> Pi -> MCP path. In particular,
// JSON-looking content must remain text, not become a result envelope.
func TestMCPForwardsPromptAndCompletionVerbatim(t *testing.T) {
	for _, tc := range []struct{ name, prompt, response string }{
		{"markdown", " \t请检查这个需求。\r\n```go\nfmt.Println(\"你好\")\n```\n\n", " \t## 结果\r\n\n```diff\n-old\n+新\n```\n\n "},
		{"json looking", `{"objective":"keep exactly","constraints":["literal"]}`, ` {"status":"needs_context","content":"ordinary assistant text"} `},
		{"large prompt", strings.Repeat("需求与上下文。\n", 2000), "done"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := conversationService(t)
			dir := t.TempDir()
			logPath, argvPath := filepath.Join(dir, "rpc.jsonl"), filepath.Join(dir, "argv.txt")
			t.Setenv("FAKE_PI_LOG", logPath)
			t.Setenv("FAKE_PI_ARGV", argvPath)
			encoded, _ := json.Marshal(tc.response)
			t.Setenv("FAKE_PI_TEXT", string(encoded[1:len(encoded)-1]))
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			ct, st := sdk.NewInMemoryTransports()
			ss, err := workerMCP.New(s).Connect(ctx, st, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer ss.Close()
			cs, err := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "1"}, nil).Connect(ctx, ct, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer cs.Close()
			res, err := cs.CallTool(ctx, &sdk.CallToolParams{Name: "delegate_implementation", Arguments: agent.DelegateTask{Prompt: tc.prompt}})
			if err != nil {
				t.Fatal(err)
			}
			if res.IsError || len(res.Content) != 1 {
				t.Fatalf("unexpected result: %+v", res)
			}
			text, ok := res.Content[0].(*sdk.TextContent)
			if !ok || text.Text != tc.response {
				t.Fatalf("completion changed: %#v", res.Content)
			}
			raw, _ := json.Marshal(res.StructuredContent)
			var result agent.AgentResult
			if json.Unmarshal(raw, &result) != nil || result.Status != "completed" || result.Content != tc.response {
				t.Fatalf("metadata lost raw content: %s", raw)
			}
			log, err := os.ReadFile(logPath)
			if err != nil {
				t.Fatal(err)
			}
			prompts := 0
			for _, line := range strings.Split(string(log), "\n") {
				var msg struct{ Type, Message string }
				if json.Unmarshal([]byte(line), &msg) == nil && msg.Type == "prompt" {
					prompts++
					if msg.Message != tc.prompt {
						t.Fatalf("prompt changed: %q", msg.Message)
					}
				}
			}
			if prompts != 1 {
				t.Fatalf("prompt count: %d", prompts)
			}
			argv, err := os.ReadFile(argvPath)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(argv), "system-prompt") {
				t.Fatal("adapter supplied system instructions")
			}
		})
	}
}
