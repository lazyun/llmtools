package pi

import (
	"ai-worker-mcp/internal/agent"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeRuntime is the adapter under test plus the stand-in binary's path, so a
// test can prove the child it launched is gone again when the turn ends.
type fakeRuntime struct {
	adapter *Adapter
	script  string
}

func fakeRuntimeSetup(t *testing.T) *fakeRuntime {
	t.Helper()
	script := fakePi(t)
	dir := t.TempDir()
	t.Setenv("FAKE_PI_MODEL_TABLE", "provider     model                          context  max-out  thinking  images\nhuggingface  deepseek-ai/DeepSeek-V4-Flash    1.0M     384K     yes       no\nopenai       gpt-5-mini                     400K     128K     yes       no")
	a, e := New(Spec{
		Binary:        script,
		ControlBinary: "/nonexistent/llmtools-under-test",
		ControlDB:     filepath.Join(dir, "runs.db"),
		SessionDir:    filepath.Join(dir, "sessions"),
	})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		a.Close()
		assertNoPiLeft(t, script)
	})
	return &fakeRuntime{adapter: a, script: script}
}

// assertNoPiLeft proves llmTools owns the whole Pi process tree: one Pi process
// per turn is only acceptable if the turn always ends with it.
func assertNoPiLeft(t *testing.T, script string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		out, e := exec.Command("pgrep", "-f", script).Output()
		body := strings.TrimSpace(string(out))
		if e != nil || body == "" {
			return
		}
		if !time.Now().Before(deadline) {
			t.Fatalf("Pi processes outlived the adapter: %s", body)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (r *fakeRuntime) runTurn(t *testing.T, env map[string]string, h agent.RuntimeHandle) (agent.AgentResult, error, []string) {
	t.Helper()
	// The stand-in reads its behaviour from the environment the adapter forwards,
	// so every value is set explicitly; an empty string means "report nothing".
	defaults := map[string]string{
		"FAKE_PI_SESSION": "pi-test-session", "FAKE_PI_PROVIDER": "openai", "FAKE_PI_MODEL": "gpt-5-mini",
		"FAKE_PI_STOP": "stop", "FAKE_PI_TEXT": "", "FAKE_PI_TOOL": "", "FAKE_PI_DETAILS": "", "FAKE_PI_ERRMSG": "",
	}
	for k, v := range defaults {
		if _, ok := env[k]; !ok {
			env[k] = v
		}
	}
	for k, v := range env {
		t.Setenv(k, v)
	}
	var stages []string
	res, e := r.adapter.runTurn(context.Background(), h, `{"objective":"report the token"}`, func(p agent.Progress) {
		stages = append(stages, p.Stage)
	})
	return res, e, stages
}

// readHandle is the frozen binding a continuation carries: the Pi session the
// run created, the model identity Core routed to, and the opaque control token.
func readHandle() agent.RuntimeHandle {
	return agent.RuntimeHandle{
		SessionID: "pi-test-session", Agent: "implementation",
		Model: "openai/gpt-5-mini", ControlToken: "ctl_test", RepoRead: true,
	}
}

// TestRunTurnCompletesFromPiFinalText is the spike's mapping claim without a
// model call: Pi's own final assistant text becomes AgentResult content, and
// nothing else in the event stream does.
func TestRunTurnCompletesFromPiFinalText(t *testing.T) {
	r := fakeRuntimeSetup(t)
	h := readHandle()
	h.Model = "huggingface/deepseek-ai/DeepSeek-V4-Flash"
	result, e, stages := r.runTurn(t, map[string]string{
		"FAKE_PI_PROVIDER": "huggingface",
		"FAKE_PI_MODEL":    "deepseek-ai/DeepSeek-V4-Flash",
		"FAKE_PI_TEXT":     "alpha.txt holds ALPHA-7f3c9d2e.",
		"FAKE_PI_TOOL":     "llmtools_control_repo_read",
	}, h)
	if e != nil {
		t.Fatalf("completed turn: %v", e)
	}
	if result.Status != "completed" || result.Content != "alpha.txt holds ALPHA-7f3c9d2e." {
		t.Fatalf("result: %+v", result)
	}
	if result.Agent != "implementation" {
		t.Fatalf("role: %+v", result)
	}
	// Pi reports provider and catalogue id separately; Core's identity joins
	// them exactly once even though the id itself contains a slash.
	if result.Model != "huggingface/deepseek-ai/DeepSeek-V4-Flash" {
		t.Fatalf("model identity: %q", result.Model)
	}
	if want := []string{"RUNNING", "TOOL_RUNNING", "TOOL_COMPLETED", "VALIDATING"}; strings.Join(stages, ",") != strings.Join(want, ",") {
		t.Fatalf("stages: %v", stages)
	}
}

// A Pi catalogue id that already carries its provider prefix must not be joined
// twice on the way back to Core.
func TestRunTurnDoesNotDoubleJoinTheProviderPrefix(t *testing.T) {
	r := fakeRuntimeSetup(t)
	h := readHandle()
	h.Model = "huggingface/deepseek-ai/DeepSeek-V4-Flash"
	result, e, _ := r.runTurn(t, map[string]string{
		"FAKE_PI_PROVIDER": "huggingface",
		"FAKE_PI_MODEL":    "huggingface/deepseek-ai/DeepSeek-V4-Flash",
		"FAKE_PI_TEXT":     "done",
	}, h)
	if e != nil {
		t.Fatal(e)
	}
	if result.Model != h.Model {
		t.Fatalf("model: %q want %q", result.Model, h.Model)
	}
}

// TestRunTurnRefusesUnusableResults covers every case where the adapter must
// stop rather than hand Core a run it cannot stand behind.
func TestRunTurnRefusesUnusableResults(t *testing.T) {
	for _, tc := range []struct {
		name      string
		env       map[string]string
		handle    func() agent.RuntimeHandle
		wantCode  string
		wantErrIs error
	}{
		{
			name:     "no final text",
			env:      map[string]string{"FAKE_PI_TEXT": "   "},
			wantCode: "EMPTY_AGENT_RESULT",
		},
		{
			name:     "runtime reports no model",
			env:      map[string]string{"FAKE_PI_MODEL": "", "FAKE_PI_TEXT": "done"},
			wantCode: "INVALID_AGENT_RESULT",
		},
		{
			name:     "different model than the run bound",
			env:      map[string]string{"FAKE_PI_MODEL": "gpt-5", "FAKE_PI_TEXT": "done"},
			wantCode: "INVALID_AGENT_RESULT",
		},
		{
			name:     "final text leaks the session id",
			env:      map[string]string{"FAKE_PI_TEXT": "my session is pi-test-session now"},
			wantCode: "INVALID_AGENT_RESULT",
		},
		{
			name:     "final text leaks the control token",
			env:      map[string]string{"FAKE_PI_TEXT": "using ctl_test for repository access"},
			wantCode: "INVALID_AGENT_RESULT",
		},
		{
			name:      "aborted turn",
			env:       map[string]string{"FAKE_PI_STOP": "aborted"},
			wantErrIs: context.Canceled,
		},
		{
			name:     "session identity changed under the run",
			env:      map[string]string{"FAKE_PI_SESSION": "some-other-session"},
			wantCode: "SESSION_LOST",
		},
		{
			name:     "provider credential failed",
			env:      map[string]string{"FAKE_PI_STOP": "error", "FAKE_PI_ERRMSG": "401 invalid api key for provider huggingface"},
			wantCode: "UPSTREAM_UNAVAILABLE",
		},
		{
			name:     "provider quota exhausted",
			env:      map[string]string{"FAKE_PI_STOP": "error", "FAKE_PI_ERRMSG": "402 you have depleted your monthly included credits"},
			wantCode: "UPSTREAM_UNAVAILABLE",
		},
		{
			name:     "transport failure",
			env:      map[string]string{"FAKE_PI_STOP": "error", "FAKE_PI_ERRMSG": "connection reset by peer"},
			wantCode: "RUNTIME_UNAVAILABLE",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := fakeRuntimeSetup(t)
			h := readHandle()
			if tc.handle != nil {
				h = tc.handle()
			}
			_, e, _ := r.runTurn(t, tc.env, h)
			if tc.wantErrIs != nil {
				if !errors.Is(e, tc.wantErrIs) {
					t.Fatalf("error: %v want %v", e, tc.wantErrIs)
				}
				return
			}
			if codeOf(t, e) != tc.wantCode {
				t.Fatalf("error: %v want %s", e, tc.wantCode)
			}
		})
	}
}

// TestRunTurnSuspendsOnBridgeControl is the unit-level half of the control
// contract: a bridge tool result becomes Core's pending outcome, with the
// adapter's private durable state attached and no final text consulted.
func TestRunTurnSuspendsOnBridgeControl(t *testing.T) {
	r := fakeRuntimeSetup(t)
	_, e, stages := r.runTurn(t, map[string]string{
		"FAKE_PI_TOOL":    "llmtools_control_request_context",
		"FAKE_PI_DETAILS": `{"llmtools_control":"context","requests":[{"id":"r1","kind":"contract","target":"the release code","reason":"only the parent holds it"}]}`,
	}, readHandle())
	var pending *agent.ContextPending
	if !errors.As(e, &pending) {
		t.Fatalf("context turn: %v", e)
	}
	if len(pending.Requests) != 1 || pending.Requests[0].ID != "r1" {
		t.Fatalf("requests: %+v", pending.Requests)
	}
	var state runState
	if err := agent.Strict(pending.State, &state); err != nil || state.SessionID != "pi-test-session" {
		t.Fatalf("durable state: %+v %v", state, err)
	}
	if last := stages[len(stages)-1]; last != "WAITING_FOR_CONTEXT" {
		t.Fatalf("stages: %v", stages)
	}

	_, e, _ = r.runTurn(t, map[string]string{
		"FAKE_PI_TOOL":    "llmtools_control_request_decision",
		"FAKE_PI_DETAILS": `{"llmtools_control":"decision","questions":[{"id":"q1","question":"Which shape?","options":[{"label":"flat","description":"a list"},{"label":"cursor"}]}]}`,
	}, readHandle())
	var dpending *agent.DecisionPending
	if !errors.As(e, &dpending) {
		t.Fatalf("decision turn: %v", e)
	}
	if len(dpending.Requests) != 1 || len(dpending.Requests[0].Options) != 2 {
		t.Fatalf("decisions: %+v", dpending.Requests)
	}
}

// TestRunTurnRecordsToolEvidenceWithoutContent checks the operator evidence the
// spike relies on: which tools really executed is recorded, and neither their
// arguments nor the task text appear in it.
func TestRunTurnRecordsToolEvidenceWithoutContent(t *testing.T) {
	r := fakeRuntimeSetup(t)
	out := captureStderr(t, func() {
		_, e, _ := r.runTurn(t, map[string]string{
			"FAKE_PI_TOOL": "llmtools_control_repo_read",
			"FAKE_PI_TEXT": "done",
		}, readHandle())
		if e != nil {
			t.Errorf("turn: %v", e)
		}
	})
	if !strings.Contains(out, `LLMTOOLS_PI tool_executed name="llmtools_control_repo_read"`) {
		t.Fatalf("no execution evidence:\n%s", out)
	}
	for _, leak := range []string{"ctl_test", "pi-test-session", "ALPHA-", "objective"} {
		if strings.Contains(out, leak) {
			t.Fatalf("evidence leaked %q:\n%s", leak, out)
		}
	}
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	reader, writer, e := os.Pipe()
	if e != nil {
		t.Fatal(e)
	}
	original := os.Stderr
	os.Stderr = writer
	collected := make(chan string, 1)
	go func() {
		var b strings.Builder
		buf := make([]byte, 4096)
		for {
			n, e := reader.Read(buf)
			if n > 0 {
				b.Write(buf[:n])
			}
			if e != nil {
				break
			}
		}
		collected <- b.String()
	}()
	fn()
	os.Stderr = original
	writer.Close()
	select {
	case out := <-collected:
		return out
	case <-time.After(2 * time.Second):
		t.Fatal("stderr capture never drained")
		return ""
	}
}

// TestResumeRoutesRejectForeignOrIncompleteState covers the continuation entry
// points without a live turn: a persisted suspension from another run is never
// replayed, and an answer set that does not match the pending requests is refused
// before Pi is asked for anything.
func TestResumeRoutesRejectForeignOrIncompleteState(t *testing.T) {
	r := fakeRuntimeSetup(t)
	h := readHandle()
	pending := runState{Version: stateVersion, SessionID: h.SessionID, ControlToken: h.ControlToken, Model: h.Model,
		Requests: []agent.ContextRequest{{ID: "r1", Kind: "contract", Target: "release", Reason: "parent-held"}}}
	raw, e := marshalState(pending)
	if e != nil {
		t.Fatal(e)
	}
	foreign, e := marshalState(runState{Version: stateVersion, SessionID: "ses_other", ControlToken: h.ControlToken})
	if e != nil {
		t.Fatal(e)
	}
	answers := []agent.AdditionalContext{{RequestID: "r1", Type: "contract", Content: "RELEASE-4d2f9a7c"}}

	if _, e := r.adapter.ResumeContext(context.Background(), h, foreign, answers, nil); codeOf(t, e) != "PENDING_CONTEXT_LOST" {
		t.Fatalf("foreign context state: %v", e)
	}
	if _, e := r.adapter.ResumeContext(context.Background(), h, raw, nil, nil); codeOf(t, e) != "INVALID_CONTEXT_RESPONSE" {
		t.Fatalf("missing answers: %v", e)
	}
	if _, e := r.adapter.ResumeContext(context.Background(), h, raw, []agent.AdditionalContext{{RequestID: "r2", Type: "contract", Content: "x"}}, nil); codeOf(t, e) != "INVALID_CONTEXT_RESPONSE" {
		t.Fatalf("answer for an unknown request: %v", e)
	}
	if _, e := r.adapter.ResumeDecision(context.Background(), h, raw, nil, nil); codeOf(t, e) != "PENDING_DECISION_LOST" {
		t.Fatalf("context state offered to the decision route: %v", e)
	}
	if _, e := r.adapter.ContinueTask(context.Background(), h, "not a task", nil); codeOf(t, e) != "INVALID_AGENT_RESULT" {
		t.Fatalf("wrong input type: %v", e)
	}
}

func marshalState(v runState) ([]byte, error) { return json.Marshal(v) }

// Metadata must never become part of the caller-authored prompt.
func TestContinueTaskForwardsOnlyTheCallerMessage(t *testing.T) {
	r := fakeRuntimeSetup(t)
	logPath := filepath.Join(t.TempDir(), "rpc.jsonl")
	t.Setenv("FAKE_PI_LOG", logPath)
	t.Setenv("FAKE_PI_TEXT", "done")
	task := agent.AgentTask{
		Agent: "implementation", Objective: "  Report the token.\r\n ",
		Capabilities: agent.CapabilityPolicy{RepoRead: true},
		Workspace:    &agent.WorkspaceSnapshot{WorkspaceID: "ws_1", CanonicalRoot: "/tmp/workspace"},
	}
	if _, e := r.adapter.ContinueTask(context.Background(), readHandle(), task, nil); e != nil {
		t.Fatal(e)
	}
	data, e := os.ReadFile(logPath)
	if e != nil {
		t.Fatal(e)
	}
	for _, line := range strings.Split(string(data), "\n") {
		var command struct{ Type, Message string }
		if json.Unmarshal([]byte(line), &command) == nil && command.Type == "prompt" {
			if command.Message != task.Objective {
				t.Fatalf("metadata or formatting reached the message: %q", command.Message)
			}
			return
		}
	}
	t.Fatal("no prompt sent")
}

// TestContinueTaskRefusesInputThatIsNotAnAgentTask keeps Core's dispatch
// contract explicit: only the shared task type may be sent to a runtime.
func TestContinueTaskRefusesInputThatIsNotAnAgentTask(t *testing.T) {
	r := fakeRuntimeSetup(t)
	for _, in := range []any{nil, "prompt", map[string]any{"objective": "x"}, agent.Continuation{}} {
		if _, e := r.adapter.ContinueTask(context.Background(), readHandle(), in, nil); codeOf(t, e) != "INVALID_AGENT_RESULT" {
			t.Fatalf("%T accepted: %v", in, e)
		}
	}
}

// The adapter surface Core calls has to behave before any turn runs.
func TestAdapterSurfaceWithoutATurn(t *testing.T) {
	if got := (&Adapter{}).RuntimeKind(); got != Kind || Kind != "pi" {
		t.Fatalf("runtime kind: %q", got)
	}
	dir := t.TempDir()
	for _, tc := range []struct {
		name string
		spec func() Spec
		code string
	}{
		{"no binary", func() Spec { return Spec{ControlDB: dir, SessionDir: dir} }, "RUNTIME_NOT_FOUND"},
		{"relative store", func() Spec { return Spec{Binary: "pi", ControlDB: "runs.db", SessionDir: dir} }, "CONFIG_ERROR"},
		{"relative session dir", func() Spec {
			return Spec{Binary: "pi", ControlDB: filepath.Join(dir, "runs.db"), SessionDir: "sessions"}
		}, "CONFIG_ERROR"},
	} {
		if _, e := New(tc.spec()); codeOf(t, e) != tc.code {
			t.Fatalf("%s: %v", tc.name, e)
		}
	}
	r := fakeRuntimeSetup(t)
	// Pi has no runtime-native agent catalogue, so the adapter reports none
	// rather than substituting llmTools' logical roles for Pi agents.
	agents, e := r.adapter.ListAgents(context.Background())
	if e != nil || len(agents) != 0 {
		t.Fatalf("agents: %+v %v", agents, e)
	}
	if r.adapter.AgentCatalogueSupported() {
		t.Fatal("Pi must not claim a runtime agent catalogue")
	}
	health, e := r.adapter.Health(context.Background())
	if e != nil || !health.Healthy || health.Version != "0.85.1-fake" {
		t.Fatalf("health: %+v %v", health, e)
	}
	models, e := r.adapter.ListModels(context.Background())
	if e != nil || len(models) != 2 {
		t.Fatalf("models: %+v %v", models, e)
	}
	if models[0].ID != "huggingface/deepseek-ai/DeepSeek-V4-Flash" {
		t.Fatalf("model identity: %+v", models[0])
	}
	// A handle with no live process is already idle, so cancellation of it is
	// confirmed rather than guessed.
	if e := r.adapter.CancelTask(context.Background(), agent.RuntimeHandle{}); e != nil {
		t.Fatalf("empty handle cancel: %v", e)
	}
	if e := r.adapter.CancelTask(context.Background(), agent.RuntimeHandle{SessionID: "ses_gone"}); e != nil {
		t.Fatalf("unknown session cancel: %v", e)
	}
}

// TestStartTaskRefusesAnUnavailableModel keeps model resolution inside Pi: the
// adapter only ever launches an identity Pi itself listed.
func TestStartTaskRefusesAnUnavailableModel(t *testing.T) {
	r := fakeRuntimeSetup(t)
	task := agent.AgentTask{Capabilities: agent.CapabilityPolicy{RepoRead: true}}
	for _, tc := range []struct {
		name, model, code string
	}{
		{"malformed identity", "gpt-5", "MODEL_NOT_AVAILABLE"},
		{"not in the runtime catalogue", "openai/gpt-5.2", "MODEL_NOT_AVAILABLE"},
	} {
		if _, e := r.adapter.StartTask(context.Background(), task, "implementation", tc.model); codeOf(t, e) != tc.code {
			t.Fatalf("%s: %v", tc.name, e)
		}
	}
}
