package service_test

import (
	"ai-worker-mcp/internal/agent"
	"ai-worker-mcp/internal/config"
	"ai-worker-mcp/internal/runtime/opencode"
	"ai-worker-mcp/internal/service"
	"ai-worker-mcp/internal/store"
	"ai-worker-mcp/internal/testutil"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func raw(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
func setup(t *testing.T, mode string, results ...agent.AgentResult) (*service.Service, *testutil.Runtime) {
	t.Helper()
	mock := testutil.New(mode, results...)
	t.Cleanup(mock.Close)
	c := config.Default()
	c.Runtime = config.Runtime{Type: "opencode", Mode: "managed", Binary: "opencode", Host: "127.0.0.1"}
	c.Directory = t.TempDir()
	c.Agents = map[string]config.AgentRouting{"implementation": {Exposed: true, Agent: "fixture"}, "review": {Exposed: true, Agent: "fixture"}, "research": {Exposed: true, Agent: "fixture"}}
	c.General.TaskTimeoutSeconds = 2
	if e := config.Save(filepath.Join(c.Directory, "config.toml"), c); e != nil {
		t.Fatal(e)
	}
	st, e := store.Open(filepath.Join(c.Directory, c.Storage.Database))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { st.Close() })
	// The fixture client is scoped exactly like a managed runtime: its own
	// scratch workspace directory. A repository-read run is re-scoped to its
	// bound workspace, so tests that execute need a grant at this root.
	return service.New(c, st, opencode.New(mock.Server.URL, filepath.Join(c.Directory, "runtime-workspace"))), mock
}

func TestExplicitModelRoutingSnapshotAndConfigChange(t *testing.T) {
	s, m := setup(t, "", request("ctx_1"), agent.AgentResult{Status: "completed", Content: "Terra completed"})
	c, e := config.Load(filepath.Join(s.Config.Directory, "config.toml"))
	if e != nil {
		t.Fatal(e)
	}
	c.Agents["implementation"] = config.AgentRouting{Exposed: true, Agent: "fixture", Model: "mock/terra"}
	if e = config.Save(filepath.Join(c.Directory, "config.toml"), c); e != nil {
		t.Fatal(e)
	}
	first := s.Run(context.Background(), raw(task()), nil)
	if first.Status != "needs_context" || first.Agent != "fixture" || first.Model != "mock/terra" {
		t.Fatal(first)
	}
	before, _ := s.Store.Get(first.WorkerRunID)
	if before.RuntimeModel != "mock/terra" || before.ModelSource != "explicit" || before.RoutingRevision == "" {
		t.Fatal(before)
	}
	c.Agents["implementation"] = config.AgentRouting{Exposed: true, Agent: "fixture", Model: "mock/sol"}
	if e = config.Save(filepath.Join(c.Directory, "config.toml"), c); e != nil {
		t.Fatal(e)
	}
	final := s.Continue(context.Background(), raw(answer(first.WorkerRunID, "ctx_1")), nil)
	if final.Status != "completed" || final.Model != "mock/terra" {
		t.Fatal(final)
	}
	after, _ := s.Store.Get(first.WorkerRunID)
	if before.RuntimeSessionID != after.RuntimeSessionID || after.RuntimeModel != "mock/terra" || before.Capabilities != (agent.CapabilityPolicy{}) || after.Capabilities != before.Capabilities {
		t.Fatal(before, after)
	}
	second := s.Run(context.Background(), raw(task()), nil)
	if second.Status != "needs_context" || second.Model != "mock/sol" {
		t.Fatal(second)
	}
	sessions, _, _ := m.Snapshot()
	models := map[string]int{}
	for _, session := range sessions {
		models[session.Model]++
	}
	if len(sessions) != 2 || models["mock/terra"] != 1 || models["mock/sol"] != 1 {
		t.Fatal(sessions)
	}
}

func TestAgentDefaultAndMissingExplicitModel(t *testing.T) {
	s, m := setup(t, "", agent.AgentResult{Status: "completed", Content: "Default completed"})
	r := s.Run(context.Background(), raw(task()), nil)
	if r.Status != "completed" || r.Model != "mock/fixture" {
		t.Fatal(r)
	}
	stored, _ := s.Store.Get(r.WorkerRunID)
	if stored.ModelSource != "agent_default" || stored.RuntimeModel != "mock/fixture" {
		t.Fatal(stored)
	}
	sessions, _, _ := m.Snapshot()
	if strings.Contains(string(sessions[0].Inputs[0]), `"model"`) {
		t.Fatal("agent-default routing sent a model override")
	}

	c, _ := config.Load(filepath.Join(s.Config.Directory, "config.toml"))
	c.Agents["implementation"] = config.AgentRouting{Exposed: true, Agent: "fixture", Model: "mock/missing"}
	if e := config.Save(filepath.Join(c.Directory, "config.toml"), c); e != nil {
		t.Fatal(e)
	}
	failed := s.Run(context.Background(), raw(task()), nil)
	if failed.Error == nil || failed.Error.Code != "MODEL_NOT_AVAILABLE" {
		t.Fatal(failed)
	}
	sessions, _, _ = m.Snapshot()
	if len(sessions) != 1 {
		t.Fatal("missing model reached session creation", sessions)
	}
}

func TestMissingRoutedAgentFailsBeforeRunCreation(t *testing.T) {
	s, m := setup(t, "")
	c, e := config.Load(filepath.Join(s.Config.Directory, "config.toml"))
	if e != nil {
		t.Fatal(e)
	}
	c.Agents["implementation"] = config.AgentRouting{Exposed: true, Agent: "missing"}
	if e = config.Save(filepath.Join(c.Directory, "config.toml"), c); e != nil {
		t.Fatal(e)
	}
	result := s.Run(context.Background(), raw(task()), nil)
	if result.Error == nil || result.Error.Code != "AGENT_NOT_FOUND" || result.WorkerRunID != "" {
		t.Fatal(result)
	}
	sessions, _, _ := m.Snapshot()
	if len(sessions) != 0 {
		t.Fatal("missing agent reached session creation", sessions)
	}
}

func task() agent.AgentTask {
	return agent.AgentTask{Agent: "implementation", Objective: "Bounded fixture task"}
}
func request(id string) agent.AgentResult {
	return agent.AgentResult{Status: "needs_context", Summary: "Need a supplied fact", ContextRequests: []agent.ContextRequest{{ID: id, Kind: "file", Target: "contract.txt", Reason: "Missing contract"}}}
}
func answer(id, q string) agent.Continuation {
	alias := "context_1"
	if q == "ctx_2" {
		alias = "context_2"
	}
	if q == "wrong" {
		alias = "context_wrong"
	}
	return agent.Continuation{WorkerRunID: id, AdditionalContext: []agent.AdditionalContext{{RequestID: alias, Type: "file", Path: "contract.txt", Content: "Supplied contract"}}}
}
func TestRepeatedContextAndDurableTerminal(t *testing.T) {
	s, m := setup(t, "", request("ctx_1"), request("ctx_2"), agent.AgentResult{Status: "completed", Summary: "Done"})
	r := s.Run(context.Background(), raw(task()), nil)
	if r.Status != "needs_context" {
		t.Fatalf("%+v", r)
	}
	id := r.WorkerRunID
	bad := s.Continue(context.Background(), raw(answer(id, "wrong")), nil)
	if bad.Error.Code != "INVALID_CONTEXT_RESPONSE" {
		t.Fatal(bad)
	}
	r = s.Continue(context.Background(), raw(answer(id, "ctx_1")), nil)
	if r.Status != "needs_context" {
		t.Fatal(r)
	}
	bad = s.Continue(context.Background(), raw(answer(id, "ctx_1")), nil)
	if bad.Error.Code != "INVALID_CONTEXT_RESPONSE" {
		t.Fatal(bad)
	}
	r = s.Continue(context.Background(), raw(answer(id, "ctx_2")), nil)
	if r.Status != "completed" || r.WorkerRunID != id {
		t.Fatal(r)
	}
	persisted, e := s.Store.Get(id)
	if e != nil || persisted.Status != "COMPLETED" {
		t.Fatal(persisted, e)
	}
	if s.Continue(context.Background(), raw(answer(id, "ctx_2")), nil).Error.Code != "CONTINUATION_INVALID" {
		t.Fatal("terminal continuation accepted")
	}
	sessions, _, _ := m.Snapshot()
	if len(sessions) != 1 || len(sessions[0].Inputs) != 1 {
		t.Fatal(sessions)
	}
	var policy []struct{ Action, Pattern, Permission string }
	encoded, _ := json.Marshal(sessions[0].Permission)
	if json.Unmarshal(encoded, &policy) != nil || len(policy) != 3 || policy[0].Action != "deny" || policy[0].Permission != "*" || policy[1].Permission != "question" || !strings.HasPrefix(policy[2].Permission, "llmtools_control_") || !strings.HasSuffix(policy[2].Permission, "_request_context") || policy[2].Action != "allow" {
		t.Fatalf("missing actual runtime permission: %s", encoded)
	}
	if strings.Contains(string(sessions[0].Inputs[0]), `"format"`) || strings.Contains(string(sessions[0].Inputs[0]), "json_schema") {
		t.Fatal("structured output was requested")
	}
}

func TestOrdinaryAssistantCompletionAndMarkdownPreservation(t *testing.T) {
	s, m := setup(t, "", agent.AgentResult{Status: "completed", Content: "PONG"})
	r := s.Run(context.Background(), raw(task()), nil)
	if r.Status != "completed" || r.Content != "PONG" || r.Summary != "PONG" {
		t.Fatal(r)
	}
	sessions, _, _ := m.Snapshot()
	if len(sessions) != 1 || len(sessions[0].Inputs) != 1 {
		t.Fatal(sessions)
	}
	input := string(sessions[0].Inputs[0])
	for _, forbidden := range []string{`"format"`, "json_schema"} {
		if strings.Contains(input, forbidden) {
			t.Fatalf("ordinary completion requested structured output: %s", input)
		}
	}

	s, _ = setup(t, "markdown_parts", agent.AgentResult{Status: "completed", Content: "unused"})
	r = s.Run(context.Background(), raw(task()), nil)
	want := "## Result\n\nPreserved markdown\n\n```diff\n-old\n+new\n```"
	if r.Status != "completed" || r.Content != want {
		t.Fatalf("content was not preserved:\n%q\nwant:\n%q", r.Content, want)
	}
}

func TestTerminalWithoutContentAndProviderErrorAfterPartialFail(t *testing.T) {
	for _, tc := range []struct {
		name string
		mode string
		code string
	}{
		{name: "empty", mode: "", code: "EMPTY_AGENT_RESULT"},
		// A provider that answers for a model the catalogue lists is an execution
		// boundary, reported the same way whichever runtime served the turn.
		{name: "provider_error", mode: "provider_error_after_partial", code: "UPSTREAM_UNAVAILABLE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := setup(t, tc.mode, agent.AgentResult{Status: "completed"})
			r := s.Run(context.Background(), raw(task()), nil)
			if r.Status != "failed" || r.Error == nil || r.Error.Code != tc.code || r.Content != "" {
				t.Fatal(r)
			}
		})
	}
}

func TestIdlePublicationRaceWaitsForTerminalAssistant(t *testing.T) {
	s, _ := setup(t, "idle_before_terminal", agent.AgentResult{Status: "completed", Content: "PONG"})
	r := s.Run(context.Background(), raw(task()), nil)
	if r.Status != "completed" || r.Content != "PONG" {
		t.Fatal(r)
	}
}
func TestDecisionsAndValidation(t *testing.T) {
	d := agent.AgentResult{Status: "needs_decision", Summary: "Parent choice required", DecisionRequests: []agent.DecisionRequest{{ID: "dec_1", Question: "Choose format", Reason: "API compatibility", Options: []agent.Option{{ID: "a", Description: "A"}, {ID: "b", Description: "B"}}}}}
	s, _ := setup(t, "", d)
	r := s.Run(context.Background(), raw(task()), nil)
	if r.Status != "needs_decision" {
		t.Fatal(r)
	}
	for _, in := range []agent.Continuation{{WorkerRunID: r.WorkerRunID}, {WorkerRunID: r.WorkerRunID, DecisionResponses: []agent.DecisionResponse{{RequestID: r.DecisionRequests[0].ID, Selection: "invalid"}}}, {WorkerRunID: r.WorkerRunID, DecisionResponses: []agent.DecisionResponse{{RequestID: r.DecisionRequests[0].ID, Selection: "option_1"}, {RequestID: r.DecisionRequests[0].ID, Selection: "option_1"}}}} {
		if v := s.Continue(context.Background(), raw(in), nil); v.Error == nil || v.Error.Code != "INVALID_DECISION_RESPONSE" {
			t.Fatal(v)
		}
	}
	in := agent.Continuation{WorkerRunID: r.WorkerRunID, DecisionResponses: []agent.DecisionResponse{{RequestID: r.DecisionRequests[0].ID, Selection: "option_1"}}}
	if v := s.Continue(context.Background(), raw(in), nil); v.Status != "completed" {
		t.Fatal(v)
	}
	if v := s.Continue(context.Background(), raw(in), nil); v.Error.Code != "CONTINUATION_INVALID" {
		t.Fatal(v)
	}
	if v := s.Continue(context.Background(), raw(agent.Continuation{WorkerRunID: "wrk_unknown"}), nil); v.Error.Code != "CONTINUATION_INVALID" {
		t.Fatal(v)
	}
}
func TestPolicyAndProtocolFailures(t *testing.T) {
	for _, mode := range []string{"unavailable", "malformed_agents", "bad_permission", "lost", "malformed", "partial", "http_failure", "malformed_event", "disconnect", "timeout", "abort_failure"} {
		t.Run(mode, func(t *testing.T) {
			s, m := setup(t, mode)
			r := s.Run(context.Background(), raw(task()), nil)
			if r.Status != "failed" || r.Error == nil {
				t.Fatal(r)
			}
			if mode == "unavailable" || mode == "malformed_agents" {
				if r.WorkerRunID != "" {
					t.Fatal("preflight failure created a run", r)
				}
				return
			}
			stored, e := s.Store.Get(r.WorkerRunID)
			if e != nil || stored.Status != "FAILED" {
				t.Fatal(stored, e)
			}
			_, aborts, _ := m.Snapshot()
			if (mode == "timeout" || mode == "disconnect") && aborts == 0 {
				t.Fatal("no runtime abort")
			}
			if mode == "abort_failure" && r.Error.Code != "ABORT_UNCONFIRMED" {
				t.Fatal(r)
			}
		})
	}
	s, m := setup(t, "")
	for _, p := range []agent.CapabilityPolicy{{RepoRead: true}, {RepoWrite: true}, {Shell: true}, {Web: true}, {Subagent: true}} {
		task := task()
		task.Capabilities = p
		if r := s.Run(context.Background(), raw(task), nil); r.Error == nil || r.Error.Code != "CONFIG_ERROR" {
			t.Fatal(r)
		}
	}
	ss, _, _ := m.Snapshot()
	if len(ss) != 0 {
		t.Fatal("unauthorized runtime call")
	}
}
func TestCancellationAndProgress(t *testing.T) {
	s, m := setup(t, "timeout")
	ctx, cancel := context.WithCancel(context.Background())
	var mu sync.Mutex
	stages := []string{}
	r := s.Run(ctx, raw(task()), func(p agent.Progress) {
		mu.Lock()
		stages = append(stages, p.Stage)
		mu.Unlock()
		if p.Stage == "MODEL_GENERATING" {
			cancel()
		}
	})
	if r.Status != "cancelled" {
		t.Fatal(r)
	}
	_, aborts, _ := m.Snapshot()
	if aborts == 0 {
		t.Fatal("abort not propagated")
	}
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(strings.Join(stages, ","), "MODEL_GENERATING") {
		t.Fatal(stages)
	}
}
func TestConcurrentContinuationOnlyOneWins(t *testing.T) {
	s, m := setup(t, "slow", request("ctx_1"))
	r := s.Run(context.Background(), raw(task()), nil)
	if r.Status != "needs_context" {
		t.Fatal(r)
	}
	ch := make(chan agent.AgentResult, 2)
	for i := 0; i < 2; i++ {
		go func() { ch <- s.Continue(context.Background(), raw(answer(r.WorkerRunID, "ctx_1")), nil) }()
	}
	a, b := <-ch, <-ch
	if (a.Status == "completed") == (b.Status == "completed") {
		t.Fatal(a, b)
	}
	ss, _, _ := m.Snapshot()
	if len(ss[0].Inputs) != 1 {
		t.Fatal("multiple continuation dispatches")
	}
}
func TestExplicitCancelAndRecovery(t *testing.T) {
	s, _ := setup(t, "", request("ctx_1"))
	r := s.Run(context.Background(), raw(task()), nil)
	v := s.Cancel(context.Background(), r.WorkerRunID)
	if v.Status != "cancelled" {
		t.Fatal(v)
	}
	if s.Continue(context.Background(), raw(answer(r.WorkerRunID, "ctx_1")), nil).Error.Code != "CONTINUATION_INVALID" {
		t.Fatal("cancelled run continued")
	}
	orphan := agent.Run{ID: "wrk_orphan", Status: "RUNNING", Created: time.Now(), Updated: time.Now()}
	if e := s.Store.Create(orphan); e != nil {
		t.Fatal(e)
	}
	s.Recover()
	stored, _ := s.Store.Get(orphan.ID)
	if stored.Status != "FAILED" {
		t.Fatal(stored)
	}
}
func TestReusedRuntimeRequestIDFailsClosed(t *testing.T) {
	s, _ := setup(t, "", request("ctx_1"), request("ctx_1"))
	r := s.Run(context.Background(), raw(task()), nil)
	v := s.Continue(context.Background(), raw(answer(r.WorkerRunID, "ctx_1")), nil)
	if v.Status != "failed" || v.Error.Code != "INVALID_AGENT_RESULT" {
		t.Fatal(v)
	}
	stored, _ := s.Store.Get(r.WorkerRunID)
	if stored.Status != "FAILED" {
		t.Fatal(stored)
	}
}

func TestExplicitCancellationAcrossServicesWhileExecuting(t *testing.T) {
	s, m := setup(t, "timeout")
	otherStore, e := store.Open(filepath.Join(s.Config.Directory, s.Config.Storage.Database))
	if e != nil {
		t.Fatal(e)
	}
	defer otherStore.Close()
	other := service.New(s.Config, otherStore, s.Runtime)
	started := make(chan string, 1)
	done := make(chan agent.AgentResult, 1)
	go func() {
		done <- s.Run(context.Background(), raw(task()), func(p agent.Progress) {
			if p.Stage == "MODEL_GENERATING" {
				select {
				case started <- p.WorkerRunID:
				default:
				}
			}
		})
	}()
	var id string
	select {
	case id = <-started:
	case <-time.After(time.Second):
		t.Fatal("task did not start")
	}
	cancelled := other.Cancel(context.Background(), id)
	if cancelled.Status != "cancelled" {
		t.Fatal(cancelled)
	}
	select {
	case result := <-done:
		if result.Status != "cancelled" {
			t.Fatal(result)
		}
	case <-time.After(time.Second):
		t.Fatal("cancel reported before execution stopped")
	}
	_, aborts, _ := m.Snapshot()
	if aborts < 1 {
		t.Fatal("no abort")
	}
	stored, _ := s.Store.Get(id)
	if stored.Status != "CANCELLED" {
		t.Fatal(stored)
	}
}

func TestRequestIDsCannotCrossRunsOrRuntimeConfigurations(t *testing.T) {
	s, _ := setup(t, "", request("ctx_1"))
	a := s.Run(context.Background(), raw(task()), nil)
	b := s.Run(context.Background(), raw(task()), nil)
	input := answer(b.WorkerRunID, "ctx_1")
	input.AdditionalContext[0].RequestID = "context_2"
	if r := s.Continue(context.Background(), raw(input), nil); r.Error == nil || r.Error.Code != "INVALID_CONTEXT_RESPONSE" {
		t.Fatal(r)
	}
	s.Config.Runtime.Binary = "another-runtime"
	if r := s.Continue(context.Background(), raw(answer(a.WorkerRunID, "ctx_1")), nil); r.Error.Code != "CONTINUATION_INVALID" {
		t.Fatal(r)
	}
}

func TestPreflightFailuresDoNotCreateRuns(t *testing.T) {
	for _, kind := range []string{"unmapped", "runtime_unavailable"} {
		t.Run(kind, func(t *testing.T) {
			s, _ := setup(t, "unavailable")
			expected := "RUNTIME_UNAVAILABLE"
			if kind == "unmapped" {
				s.Config.Agents = map[string]config.AgentRouting{}
				if e := config.Save(filepath.Join(s.Config.Directory, "config.toml"), s.Config); e != nil {
					t.Fatal(e)
				}
				expected = "AGENT_MAPPING_NOT_FOUND"
			}
			result := s.Run(context.Background(), raw(task()), nil)
			if result.Error == nil || result.Error.Code != expected {
				t.Fatal(result)
			}
			if result.WorkerRunID != "" {
				t.Fatal("preflight failure created a run", result)
			}
		})
	}
}

func TestHiddenRoleRejectsNewRunButExistingRunContinues(t *testing.T) {
	s, m := setup(t, "", request("native_context"), agent.AgentResult{Status: "completed", Content: "continued"})
	pending := s.Run(context.Background(), raw(task()), nil)
	if pending.Status != "needs_context" || pending.ContextRequests[0].ID != "context_1" {
		t.Fatal(pending)
	}
	c, _ := config.Load(filepath.Join(s.Config.Directory, "config.toml"))
	routing := c.Agents["implementation"]
	routing.Exposed = false
	c.Agents["implementation"] = routing
	if e := config.Save(filepath.Join(c.Directory, "config.toml"), c); e != nil {
		t.Fatal(e)
	}
	rejected := s.Run(context.Background(), raw(task()), nil)
	if rejected.Error == nil || rejected.Error.Code != "ROLE_NOT_EXPOSED" || rejected.WorkerRunID != "" {
		t.Fatal(rejected)
	}
	final := s.Continue(context.Background(), raw(agent.Continuation{WorkerRunID: pending.WorkerRunID, AdditionalContext: []agent.AdditionalContext{{Type: "file", Path: "contract.txt", Content: "answer"}}}), nil)
	if final.Status != "completed" || final.WorkerRunID != pending.WorkerRunID {
		t.Fatal(final)
	}
	sessions, _, _ := m.Snapshot()
	if len(sessions) != 1 {
		t.Fatal(sessions)
	}
}

func TestRoleAdapterUsesSimpleContextContract(t *testing.T) {
	s, _ := setup(t, "", agent.AgentResult{Status: "completed", Content: "done"})
	executableWorkspace(t, s, "repo")
	input := agent.DelegateTask{Prompt: "review" + "\n\nContext:\n" + "Requirements" + "\n" + "No taxonomy needed"}
	r := s.RunRole(context.Background(), "implementation", raw(input), nil)
	if r.Status != "completed" {
		t.Fatal(r)
	}
}

// The capability snapshot is a server-owned decision, never a caller field:
// Implementation and Review carry read-only access to their bound workspace,
// while Research stays context-only.
func TestRoleNativeServerInjectsServerOwnedCapabilitySnapshot(t *testing.T) {
	s, m := setup(t, "",
		agent.AgentResult{Status: "completed", Content: "implementation"},
		agent.AgentResult{Status: "completed", Content: "review"},
		agent.AgentResult{Status: "completed", Content: "research"},
	)
	c, _ := config.Load(filepath.Join(s.Config.Directory, "config.toml"))
	for _, role := range []string{"implementation", "review", "research"} {
		c.Agents[role] = config.AgentRouting{Exposed: true, Agent: "fixture"}
	}
	if e := config.Save(filepath.Join(c.Directory, "config.toml"), c); e != nil {
		t.Fatal(e)
	}
	executableWorkspace(t, s, "repo")
	for _, role := range []string{"implementation", "review", "research"} {
		result := s.RunRole(context.Background(), role, raw(agent.DelegateTask{Prompt: role}), nil)
		if result.Status != "completed" {
			t.Fatal(role, result)
		}
		expected := agent.CapabilityPolicy{}
		if role != "research" {
			expected = agent.CapabilityPolicy{RepoRead: true}
		}
		run, e := s.Store.Get(result.WorkerRunID)
		if e != nil || run.Capabilities != expected {
			t.Fatal(role, run, e)
		}
	}
	sessions, _, _ := m.Snapshot()
	if len(sessions) != 3 {
		t.Fatal(sessions)
	}
}

func TestRoleNativeRejectsCallerCapabilityFieldBeforeRun(t *testing.T) {
	s, m := setup(t, "")
	r := s.RunRole(context.Background(), "implementation", json.RawMessage(`{"prompt":"escalate","capabilities":{"shell":true}}`), nil)
	if r.Error == nil || r.Error.Code != "CONFIG_ERROR" || r.WorkerRunID != "" {
		t.Fatal(r)
	}
	sessions, _, _ := m.Snapshot()
	if len(sessions) != 0 {
		t.Fatal("unknown public capability field reached runtime", sessions)
	}
}

func TestLegacyFalseCapabilitiesRequestNothing(t *testing.T) {
	s, _ := setup(t, "", agent.AgentResult{Status: "completed", Content: "legacy"})
	legacy := task()
	legacy.Capabilities = agent.CapabilityPolicy{}
	r := s.Run(context.Background(), raw(legacy), nil)
	if r.Status != "completed" {
		t.Fatal(r)
	}
	stored, e := s.Store.Get(r.WorkerRunID)
	if e != nil || stored.Capabilities != (agent.CapabilityPolicy{}) {
		t.Fatal(stored, e)
	}
}

func TestSinglePendingDecisionMayOmitRequestID(t *testing.T) {
	s, _ := setup(t, "", decision(false, false), agent.AgentResult{Status: "completed", Content: "chosen"})
	pending := s.Run(context.Background(), raw(task()), nil)
	if pending.DecisionRequests[0].ID != "decision_1" {
		t.Fatal(pending)
	}
	final := s.Continue(context.Background(), raw(agent.Continuation{WorkerRunID: pending.WorkerRunID, DecisionResponses: []agent.DecisionResponse{{Selection: "option_2"}}}), nil)
	if final.Status != "completed" {
		t.Fatal(final)
	}
}

func TestMultiplePendingContextRequiresShortAliases(t *testing.T) {
	pendingResult := agent.AgentResult{Status: "needs_context", Summary: "two files", ContextRequests: []agent.ContextRequest{
		{ID: "native_a", Kind: "file", Target: "a.txt", Reason: "A"},
		{ID: "native_b", Kind: "file", Target: "b.txt", Reason: "B"},
	}}
	s, _ := setup(t, "", pendingResult, agent.AgentResult{Status: "completed", Content: "done"})
	pending := s.Run(context.Background(), raw(task()), nil)
	if len(pending.ContextRequests) != 2 || pending.ContextRequests[0].ID != "context_1" || pending.ContextRequests[1].ID != "context_2" {
		t.Fatal(pending)
	}
	ambiguous := agent.Continuation{WorkerRunID: pending.WorkerRunID, AdditionalContext: []agent.AdditionalContext{{Type: "file", Path: "a.txt", Content: "A"}, {Type: "file", Path: "b.txt", Content: "B"}}}
	if got := s.Continue(context.Background(), raw(ambiguous), nil); got.Error == nil || got.Error.Code != "INVALID_CONTEXT_RESPONSE" {
		t.Fatal(got)
	}
	answers := ambiguous
	answers.AdditionalContext[0].RequestID = "context_1"
	answers.AdditionalContext[1].RequestID = "context_2"
	if got := s.Continue(context.Background(), raw(answers), nil); got.Status != "completed" {
		t.Fatal(got)
	}
}

func TestDecisionContinuationKeepsFrozenModelAfterConfigChange(t *testing.T) {
	s, m := setup(t, "", decision(false, false), agent.AgentResult{Status: "completed", Content: "Terra decision completed"})
	c, e := config.Load(filepath.Join(s.Config.Directory, "config.toml"))
	if e != nil {
		t.Fatal(e)
	}
	c.Agents["implementation"] = config.AgentRouting{Exposed: true, Agent: "fixture", Model: "mock/terra"}
	if e = config.Save(filepath.Join(c.Directory, "config.toml"), c); e != nil {
		t.Fatal(e)
	}
	pending := s.Run(context.Background(), raw(task()), nil)
	if pending.Status != "needs_decision" || pending.Model != "mock/terra" {
		t.Fatal(pending)
	}
	before, _ := s.Store.Get(pending.WorkerRunID)
	c.Agents["implementation"] = config.AgentRouting{Exposed: true, Agent: "fixture", Model: "mock/sol"}
	if e = config.Save(filepath.Join(c.Directory, "config.toml"), c); e != nil {
		t.Fatal(e)
	}
	final := s.Continue(context.Background(), raw(decisionAnswer(pending, "option_1")), nil)
	after, _ := s.Store.Get(pending.WorkerRunID)
	if final.Status != "completed" || final.Model != "mock/terra" || before.RuntimeSessionID != after.RuntimeSessionID || after.RuntimeModel != "mock/terra" {
		t.Fatal(final, before, after)
	}
	sessions, _, _ := m.Snapshot()
	if len(sessions) != 1 || sessions[0].Model != "mock/terra" {
		t.Fatal(sessions)
	}
}

func decision(multiple, custom bool) agent.AgentResult {
	return agent.AgentResult{Status: "needs_decision", Summary: "Parent must decide", DecisionRequests: []agent.DecisionRequest{{ID: "fixture", Question: "Choose compatibility", Reason: "Public API choice", Options: []agent.Option{{ID: "A", Description: "Change public API"}, {ID: "B", Description: "Preserve public API"}}, Multiple: multiple, AllowCustom: custom}}}
}
func decisionAnswer(r agent.AgentResult, selection string) agent.Continuation {
	return agent.Continuation{WorkerRunID: r.WorkerRunID, DecisionResponses: []agent.DecisionResponse{{RequestID: r.DecisionRequests[0].ID, Selection: selection}}}
}
func TestNativeQuestionMultipleCyclesAndInvalidAnswers(t *testing.T) {
	s, m := setup(t, "", decision(false, false), decision(false, false), agent.AgentResult{Status: "completed", Summary: "Done"})
	first := s.Run(context.Background(), raw(task()), nil)
	if first.Status != "needs_decision" {
		t.Fatal(first)
	}
	before, _ := s.Store.Get(first.WorkerRunID)
	if len(before.RuntimeDecision) == 0 {
		t.Fatal("pending mapping not durable")
	}
	if strings.Contains(string(raw(first)), "que_mock") {
		t.Fatal("runtime question leaked")
	}
	wrong := decisionAnswer(first, "option_2")
	wrong.DecisionResponses[0].RequestID = "another_run_decision"
	if got := s.Continue(context.Background(), raw(wrong), nil); got.Error == nil || got.Error.Code != "INVALID_DECISION_RESPONSE" {
		t.Fatal(got)
	}
	second := s.Continue(context.Background(), raw(decisionAnswer(first, "option_2")), nil)
	if second.Status != "needs_decision" || second.DecisionRequests[0].ID == first.DecisionRequests[0].ID {
		t.Fatal(second)
	}
	if got := s.Continue(context.Background(), raw(decisionAnswer(first, "option_2")), nil); got.Error == nil {
		t.Fatal("duplicate first reply accepted")
	}
	final := s.Continue(context.Background(), raw(decisionAnswer(second, "option_1")), nil)
	after, _ := s.Store.Get(first.WorkerRunID)
	sessions, _, _ := m.Snapshot()
	pending, replies := m.QuestionSnapshot()
	if final.Status != "completed" || after.RuntimeSessionID != before.RuntimeSessionID || len(after.RuntimeDecision) != 0 || len(sessions) != 1 || len(sessions[0].Inputs) != 1 || pending != 0 || replies != 2 {
		t.Fatal(final, after, pending, replies)
	}
}
func TestNativeQuestionWrongAliasAndLostPending(t *testing.T) {
	s, m := setup(t, "", decision(false, false))
	a := s.Run(context.Background(), raw(task()), nil)
	b := s.Run(context.Background(), raw(task()), nil)
	response := decisionAnswer(a, "option_1")
	response.WorkerRunID = b.WorkerRunID
	response.DecisionResponses[0].RequestID = "decision_2"
	if got := s.Continue(context.Background(), raw(response), nil); got.Error == nil || got.Error.Code != "INVALID_DECISION_RESPONSE" {
		t.Fatal(got)
	}
	m.LoseQuestions()
	got := s.Continue(context.Background(), raw(decisionAnswer(a, "option_1")), nil)
	if got.Error == nil || got.Error.Code != "PENDING_DECISION_LOST" {
		t.Fatal(got)
	}
	stored, _ := s.Store.Get(a.WorkerRunID)
	sessions, _, _ := m.Snapshot()
	if stored.Status != "FAILED" || len(sessions) != 2 || len(sessions[0].Inputs) != 1 || len(sessions[1].Inputs) != 1 {
		t.Fatal("lost question replayed", stored, sessions)
	}
}
func TestNativeQuestionWaitingCancelAndReject(t *testing.T) {
	for _, reject := range []bool{false, true} {
		t.Run(fmt.Sprint(reject), func(t *testing.T) {
			s, m := setup(t, "", decision(false, false))
			pending := s.Run(context.Background(), raw(task()), nil)
			if pending.Status != "needs_decision" {
				t.Fatal(pending)
			}
			// The per-execution context has ended; the native wait remains live.
			time.Sleep(100 * time.Millisecond)
			count, _ := m.QuestionSnapshot()
			if count != 1 {
				t.Fatal("question died with observer")
			}
			var result agent.AgentResult
			if reject {
				result = s.Continue(context.Background(), raw(agent.Continuation{WorkerRunID: pending.WorkerRunID, DecisionResponses: []agent.DecisionResponse{{RequestID: pending.DecisionRequests[0].ID, Reject: true}}}), nil)
			} else {
				result = s.Cancel(context.Background(), pending.WorkerRunID)
			}
			count, _ = m.QuestionSnapshot()
			_, aborts, _ := m.Snapshot()
			if result.Status != "cancelled" || count != 0 || aborts == 0 {
				t.Fatal(result, count, aborts)
			}
		})
	}
}
func TestNativeQuestionMultiSelectAndCustom(t *testing.T) {
	s, m := setup(t, "", decision(true, true))
	pending := s.Run(context.Background(), raw(task()), nil)
	if pending.Status != "needs_decision" || !pending.DecisionRequests[0].Multiple || !pending.DecisionRequests[0].AllowCustom {
		t.Fatal(pending)
	}
	answer := agent.Continuation{WorkerRunID: pending.WorkerRunID, DecisionResponses: []agent.DecisionResponse{{RequestID: pending.DecisionRequests[0].ID, Selections: []string{"option_1", "option_2"}, CustomText: "Parent-approved third alternative"}}}
	if got := s.Continue(context.Background(), raw(answer), nil); got.Status != "completed" {
		t.Fatal(got)
	}
	_, replies := m.QuestionSnapshot()
	if replies != 1 {
		t.Fatal(replies)
	}
}

func TestQuestionUnavailableFailsBeforeExecution(t *testing.T) {
	for _, mode := range []string{"no_question", "bad_permission"} {
		t.Run(mode, func(t *testing.T) {
			s, m := setup(t, mode)
			r := s.Run(context.Background(), raw(task()), nil)
			if r.Error == nil || r.Error.Code != "QUESTION_TOOL_UNAVAILABLE" {
				t.Fatalf("%+v", r)
			}
			sessions, _, _ := m.Snapshot()
			for _, session := range sessions {
				if len(session.Inputs) != 0 {
					t.Fatal("execution dispatched without question permission")
				}
			}
		})
	}
}

func TestNativeQuestionGenerationFence(t *testing.T) {
	d := agent.AgentResult{Status: "needs_decision", Summary: "Choice", DecisionRequests: []agent.DecisionRequest{{ID: "q", Question: "Choose", Reason: "Compatibility", Options: []agent.Option{{ID: "a", Description: "A"}, {ID: "b", Description: "B"}}}}}
	s, m := setup(t, "", d)
	c := s.Runtime.(*opencode.Client)
	c.InstanceID = "rt_fixture"
	c.Generation = 1
	r := s.Run(context.Background(), raw(task()), nil)
	if r.Status != "needs_decision" {
		t.Fatal(r)
	}
	c.Generation = 2
	out := s.Continue(context.Background(), raw(agent.Continuation{WorkerRunID: r.WorkerRunID, DecisionResponses: []agent.DecisionResponse{{RequestID: r.DecisionRequests[0].ID, Selection: "option_1"}}}), nil)
	if out.Error == nil || out.Error.Code != "PENDING_DECISION_LOST" {
		t.Fatal(out)
	}
	sessions, _, _ := m.Snapshot()
	_, replies := m.QuestionSnapshot()
	if len(sessions) != 1 || len(sessions[0].Inputs) != 1 || replies != 0 {
		t.Fatal("generation mismatch replayed or replied")
	}
}

func addWorkspace(t *testing.T, s *service.Service, label string) (string, string) {
	t.Helper()
	return addWorkspaceRoot(t, s, label, t.TempDir())
}

func addWorkspaceRoot(t *testing.T, s *service.Service, label, root string) (string, string) {
	t.Helper()
	w, _, e := s.Store.AddWorkspace(root, label, "desktop_user_grant")
	if e != nil {
		t.Fatal(e)
	}
	return w.ID, root
}

// executableWorkspace grants the directory the fixture runtime actually runs
// in. Implementation and Review bind their read-only repository access to that
// one directory, so this is the only grant under which they can start.
func executableWorkspace(t *testing.T, s *service.Service, label string) (string, string) {
	t.Helper()
	root := filepath.Join(s.Config.Directory, "runtime-workspace")
	if e := os.MkdirAll(root, 0700); e != nil {
		t.Fatal(e)
	}
	return addWorkspaceRoot(t, s, label, root)
}

func runWithWorkspace(s *service.Service, objective, id string) agent.AgentResult {
	return runRoleWithWorkspace(s, "implementation", objective, id)
}

func runRoleWithWorkspace(s *service.Service, role, objective, id string) agent.AgentResult {
	payload := map[string]any{"prompt": objective}
	if id != "" {
		payload["workspace_id"] = id
	}
	return s.RunRole(context.Background(), role, raw(payload), nil)
}

func TestWorkspaceSelectionIsValidatedAndFrozen(t *testing.T) {
	s, m := setup(t, "", request("ctx_1"), agent.AgentResult{Status: "completed", Content: "done"})
	id, root := executableWorkspace(t, s, "repo")
	first := runWithWorkspace(s, "bounded", id)
	if first.Status != "needs_context" {
		t.Fatal(first)
	}
	run, _ := s.Store.Get(first.WorkerRunID)
	if run.Workspace.WorkspaceID != id || run.Workspace.CanonicalRoot != root || run.Workspace.GrantRevision != 1 {
		t.Fatalf("workspace snapshot: %+v", run.Workspace)
	}
	if run.Capabilities != (agent.CapabilityPolicy{RepoRead: true}) {
		t.Fatalf("capability policy: %+v", run.Capabilities)
	}
	final := s.Continue(context.Background(), raw(answer(first.WorkerRunID, "ctx_1")), nil)
	if final.Status != "completed" {
		t.Fatal(final)
	}
	after, _ := s.Store.Get(first.WorkerRunID)
	if after.Workspace != run.Workspace {
		t.Fatalf("continuation drifted workspace: %+v -> %+v", run.Workspace, after.Workspace)
	}
	if sessions, _, _ := m.Snapshot(); len(sessions) != 1 {
		t.Fatal("workspace run did not reuse its session")
	}
}

func TestWorkspaceRenameDoesNotInvalidateRun(t *testing.T) {
	s, _ := setup(t, "", request("ctx_1"), agent.AgentResult{Status: "completed", Content: "done"})
	id, _ := executableWorkspace(t, s, "old")
	first := runWithWorkspace(s, "bounded", id)
	if first.Status != "needs_context" {
		t.Fatal(first)
	}
	if e := s.Store.RenameWorkspace(id, "new"); e != nil {
		t.Fatal(e)
	}
	if final := s.Continue(context.Background(), raw(answer(first.WorkerRunID, "ctx_1")), nil); final.Status != "completed" {
		t.Fatalf("rename invalidated authority: %+v", final)
	}
}

func TestWorkspaceUnknownAndRevokedFailClosed(t *testing.T) {
	s, m := setup(t, "")
	unknown := "ws_" + strings.Repeat("0", 32)
	if r := runWithWorkspace(s, "x", unknown); r.Error == nil || r.Error.Code != "WORKSPACE_NOT_FOUND" || r.WorkerRunID != "" {
		t.Fatalf("unknown workspace not rejected: %+v", r)
	}
	id, _ := addWorkspace(t, s, "repo")
	if e := s.Store.RevokeWorkspace(id); e != nil {
		t.Fatal(e)
	}
	if r := runWithWorkspace(s, "x", id); r.Error == nil || r.Error.Code != "WORKSPACE_AUTHORIZATION_REVOKED" || r.WorkerRunID != "" {
		t.Fatalf("revoked workspace not rejected: %+v", r)
	}
	if sessions, _, _ := m.Snapshot(); len(sessions) != 0 {
		t.Fatal("rejected workspace reached runtime session creation")
	}
}

func TestWorkspacePublicSchemaRejectsPathFields(t *testing.T) {
	s, m := setup(t, "")
	for _, field := range []string{"workspace_root", "path", "directory"} {
		payload := []byte(`{"prompt":"x","` + field + `":"/tmp"}`)
		r := s.RunRole(context.Background(), "implementation", json.RawMessage(payload), nil)
		if r.Error == nil || r.Error.Code != "CONFIG_ERROR" {
			t.Fatalf("path field %s accepted: %+v", field, r)
		}
	}
	if sessions, _, _ := m.Snapshot(); len(sessions) != 0 {
		t.Fatal("caller path field reached runtime")
	}
}

func TestWorkspaceRevocationBlocksSuspendedContinuation(t *testing.T) {
	s, _ := setup(t, "", request("ctx_1"), agent.AgentResult{Status: "completed", Content: "done"})
	id, _ := executableWorkspace(t, s, "repo")
	first := runWithWorkspace(s, "bounded", id)
	if first.Status != "needs_context" {
		t.Fatal(first)
	}
	if e := s.Store.RevokeWorkspace(id); e != nil {
		t.Fatal(e)
	}
	final := s.Continue(context.Background(), raw(answer(first.WorkerRunID, "ctx_1")), nil)
	if final.Error == nil || final.Error.Code != "WORKSPACE_AUTHORIZATION_REVOKED" {
		t.Fatalf("revoked workspace resumed: %+v", final)
	}
}

func TestWorkspaceUnavailableDirectoryFailsClosed(t *testing.T) {
	s, _ := setup(t, "")
	id, root := addWorkspace(t, s, "repo")
	if e := os.RemoveAll(root); e != nil {
		t.Fatal(e)
	}
	if r := runWithWorkspace(s, "x", id); r.Error == nil || r.Error.Code != "WORKSPACE_UNAVAILABLE" {
		t.Fatalf("missing directory not rejected: %+v", r)
	}
}

// A context-only role keeps its historical auto-binding. A repository-read
// role has no unbound execution at all: it auto-binds one ACTIVE grant and
// refuses to guess between several.
func TestWorkspaceAutoBindUniqueActiveAndNoneWhenAmbiguous(t *testing.T) {
	s, _ := setup(t, "", agent.AgentResult{Status: "completed", Content: "done"})
	first := runRoleWithWorkspace(s, "research", "one", "")
	if first.Status != "completed" {
		t.Fatal(first)
	}
	run, _ := s.Store.Get(first.WorkerRunID)
	if run.Workspace.WorkspaceID != "" {
		t.Fatalf("unexpected binding with no grant: %+v", run.Workspace)
	}
	if r := runWithWorkspace(s, "one", ""); r.Error == nil || r.Error.Code != "WORKSPACE_REQUIRED" || r.WorkerRunID != "" {
		t.Fatalf("read role ran without a workspace: %+v", r)
	}
	id, _ := executableWorkspace(t, s, "a")
	second := runRoleWithWorkspace(s, "research", "two", "")
	run2, _ := s.Store.Get(second.WorkerRunID)
	if run2.Workspace.WorkspaceID != id || run2.Workspace.GrantRevision != 1 {
		t.Fatalf("unique grant not auto-bound: %+v", run2.Workspace)
	}
	if r := runWithWorkspace(s, "three", ""); r.Status != "completed" {
		t.Fatalf("auto-bound read role failed: %+v", r)
	}
	addWorkspace(t, s, "b")
	if r := runWithWorkspace(s, "four", ""); r.Error == nil || r.Error.Code != "WORKSPACE_SELECTION_REQUIRED" || r.WorkerRunID != "" {
		t.Fatalf("ambiguous grants auto-bound or guessed: %+v", r)
	}
	fifth := runRoleWithWorkspace(s, "research", "five", "")
	run5, _ := s.Store.Get(fifth.WorkerRunID)
	if run5.Workspace.WorkspaceID != "" {
		t.Fatalf("ambiguous grants auto-bound: %+v", run5.Workspace)
	}
}

// A durable read grant exists only for as long as the run can execute. A
// suspended run holds no live access, cancellation releases the grant, and a
// terminal run cannot get it back through a continuation.
func TestRepoAccessGrantLifecycleIsBoundToExecution(t *testing.T) {
	s, _ := setup(t, "", request("ctx_1"), agent.AgentResult{Status: "completed", Content: "done"})
	id, _ := executableWorkspace(t, s, "repo")
	first := runWithWorkspace(s, "bounded", id)
	if first.Status != "needs_context" {
		t.Fatalf("%+v", first)
	}
	run, _ := s.Store.Get(first.WorkerRunID)
	if _, e := s.Store.RepoAccessByRun(run.ID); e != nil {
		t.Fatalf("suspended run lost its durable grant: %v", e)
	}
	if _, _, e := s.Store.LiveRepoRoot(run.RuntimeControl); agent.Safe(e).Code != "WORKSPACE_ACCESS_DENIED" {
		t.Fatalf("suspended run kept live read access: %v", e)
	}
	if final := s.Continue(context.Background(), raw(answer(run.ID, "ctx_1")), nil); final.Status != "completed" {
		t.Fatalf("%+v", final)
	}
	if _, e := s.Store.RepoAccessByRun(run.ID); agent.Safe(e).Code != "WORKSPACE_ACCESS_DENIED" {
		t.Fatalf("grant survived completion: %v", e)
	}
	if _, _, e := s.Store.LiveRepoRoot(run.RuntimeControl); e == nil {
		t.Fatal("completed run can still read the workspace")
	}

	s2, _ := setup(t, "timeout")
	id2, _ := executableWorkspace(t, s2, "repo")
	cancelling := make(chan agent.AgentResult, 1)
	go func() { cancelling <- runWithWorkspace(s2, "read the repository", id2) }()
	live := runningReadRun(t, s2, id2)
	if v := s2.Cancel(context.Background(), live.ID); v.Status != "cancelled" {
		t.Fatalf("%+v", v)
	}
	if res := <-cancelling; res.Status != "cancelled" {
		t.Fatalf("cancelled read run reported: %+v", res)
	}
	if _, e := s2.Store.RepoAccessByRun(live.ID); agent.Safe(e).Code != "WORKSPACE_ACCESS_DENIED" {
		t.Fatalf("grant survived cancellation: %v", e)
	}
	if _, _, e := s2.Store.LiveRepoRoot(live.RuntimeControl); e == nil {
		t.Fatal("cancelled run can still read the workspace")
	}
}

// Revoking the workspace while a read run is executing ends that run with its
// own reason, and access stops before the failure is even reported.
func TestLiveWorkspaceRevocationEndsRunningReadRun(t *testing.T) {
	s, _ := setup(t, "timeout")
	id, root := executableWorkspace(t, s, "repo")
	done := make(chan agent.AgentResult, 1)
	go func() { done <- runWithWorkspace(s, "read the repository", id) }()
	live := runningReadRun(t, s, id)
	if _, got, e := s.Store.LiveRepoRoot(live.RuntimeControl); e != nil || got != root {
		t.Fatalf("no live grant during the run: %q %v", got, e)
	}
	if e := s.Store.RevokeWorkspace(id); e != nil {
		t.Fatal(e)
	}
	if _, _, e := s.Store.LiveRepoRoot(live.RuntimeControl); agent.Safe(e).Code != "WORKSPACE_AUTHORIZATION_REVOKED" {
		t.Fatalf("revoked grant still authorizes: %v", e)
	}
	res := <-done
	if res.Error == nil || res.Error.Code != "WORKSPACE_AUTHORIZATION_REVOKED" {
		t.Fatalf("revocation reported as: %+v", res)
	}
	if _, e := s.Store.RepoAccessByRun(live.ID); agent.Safe(e).Code != "WORKSPACE_ACCESS_DENIED" {
		t.Fatalf("grant survived revocation: %v", e)
	}
}

// runningReadRun waits until the workspace-bound read run is executing with a
// grant the store will actually honor.
func runningReadRun(t *testing.T, s *service.Service, workspaceID string) agent.Run {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		runs, e := s.Store.Running()
		if e != nil {
			t.Fatal(e)
		}
		for _, r := range runs {
			if r.Workspace.WorkspaceID != workspaceID || !r.Capabilities.RepoRead || r.RuntimeControl == "" {
				continue
			}
			if _, _, e := s.Store.LiveRepoRoot(r.RuntimeControl); e == nil {
				return r
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("read run never reached live execution")
	return agent.Run{}
}

func TestRolePromptIsForwardedWithoutMetadataOrSystemInstructions(t *testing.T) {
	s, mock := setup(t, "", agent.AgentResult{Status: "completed", Content: " \t原文\r\n```text\nexact\n```\n "})
	executableWorkspace(t, s, "repo")
	prompt := "  完整需求\r\n\n{\"context\":\"literal, not a task field\"}\n\n "
	result := s.RunRole(context.Background(), "implementation", raw(agent.DelegateTask{Prompt: prompt}), nil)
	if result.Status != "completed" || result.Content != " \t原文\r\n```text\nexact\n```\n " {
		t.Fatal(result)
	}
	sessions, _, _ := mock.Snapshot()
	if len(sessions) != 1 || len(sessions[0].Inputs) != 1 {
		t.Fatal(sessions)
	}
	var input struct {
		System *string                       `json:"system"`
		Parts  []struct{ Type, Text string } `json:"parts"`
	}
	if json.Unmarshal(sessions[0].Inputs[0], &input) != nil || input.System != nil || len(input.Parts) != 1 || input.Parts[0].Type != "text" || input.Parts[0].Text != prompt {
		t.Fatalf("changed request: %s", sessions[0].Inputs[0])
	}
}

func TestRoleRejectsLegacyAssemblyFields(t *testing.T) {
	s, mock := setup(t, "", agent.AgentResult{Status: "completed", Content: "done"})
	for _, payload := range []string{
		`{"objective":"legacy"}`,
		`{"prompt":"raw","context":[{"content":"would be silently lost"}]}`,
		`{"prompt":"raw","constraints":["must not lose"]}`,
		`{"prompt":" \t\r\n"}`,
	} {
		r := s.RunRole(context.Background(), "research", json.RawMessage(payload), nil)
		if r.Error == nil || r.Error.Code != "CONFIG_ERROR" {
			t.Fatalf("accepted %s: %+v", payload, r)
		}
	}
	sessions, _, _ := mock.Snapshot()
	if len(sessions) != 0 {
		t.Fatal("invalid input reached runtime")
	}
}
