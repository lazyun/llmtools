package pi

import (
	"ai-worker-mcp/internal/agent"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// bridgeToolNames is the exact surface a read-only Pi turn may be offered.
var bridgeToolNames = []string{
	"llmtools_control_repo_read",
	"llmtools_control_repo_glob",
	"llmtools_control_repo_grep",
	"llmtools_control_request_context",
	"llmtools_control_request_decision",
}

// TestToolAllowlistIsTheBridgeSurface pins the security answer for the spike: a
// Pi turn can only ever be offered llmTools' own tools, and repository reads
// appear or disappear with the run's frozen capability snapshot.
func TestToolAllowlistIsTheBridgeSurface(t *testing.T) {
	withoutReads := toolAllowlist(false)
	want := []string{"llmtools_control_request_context", "llmtools_control_request_decision"}
	if strings.Join(withoutReads, ",") != strings.Join(want, ",") {
		t.Fatalf("research surface: %v", withoutReads)
	}
	withReads := toolAllowlist(true)
	if strings.Join(withReads, ",") != strings.Join(bridgeToolNames, ",") {
		t.Fatalf("read-only surface: %v", withReads)
	}
	for _, name := range withReads {
		if !strings.HasPrefix(name, "llmtools_control_") {
			t.Fatalf("native tool reached the allowlist: %q", name)
		}
	}
}

func newTestAdapter(t *testing.T) *Adapter {
	t.Helper()
	dir := t.TempDir()
	a, e := New(Spec{
		Binary:        "/nonexistent/pi-under-test",
		ControlBinary: "/nonexistent/llmtools-under-test",
		ControlDB:     filepath.Join(dir, "runs.db"),
		SessionDir:    filepath.Join(dir, "sessions"),
	})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(a.Close)
	return a
}

// The launch flags are the only thing standing between Pi's defaults and a run
// llmTools controls, so each one is asserted by name.
func TestLaunchArgsPinTheControlledSurface(t *testing.T) {
	a := newTestAdapter(t)
	if _, e := a.base(); codeOf(t, e) != "RUNTIME_NOT_FOUND" {
		t.Fatalf("a missing Pi binary must fail before launch: %v", e)
	}
	// Point the adapter at a binary that exists so the argument list is checkable.
	// The stand-in runtime exists, so the launch list is checkable.
	a.spec.Binary = fakePi(t)
	l, e := a.base()
	if e != nil {
		t.Fatal(e)
	}
	args := a.launchArgs(&l, "ctl_token", true, "huggingface", "deepseek-ai/DeepSeek-V4-Flash", "ses_1")
	for flag, want := range map[string]string{
		"-e":            l.extension,
		"--tools":       strings.Join(bridgeToolNames, ","),
		"--session-dir": a.spec.SessionDir,
		"--provider":    "huggingface",
		"--model":       "deepseek-ai/DeepSeek-V4-Flash",
		"--session":     "ses_1",
	} {
		got, found := valueAfter(args, flag)
		if !found || got != want {
			t.Fatalf("flag %s: %q want %q in %v", flag, got, want, args)
		}
	}
	for _, off := range []string{"--no-builtin-tools", "--no-extensions", "--no-context-files", "--no-skills", "--no-prompt-templates", "--no-themes", "--no-approve"} {
		if indexOfArg(args, off) < 0 {
			t.Fatalf("%s is absent, so Pi would discover a surface llmTools does not control", off)
		}
	}
	// The bridge is loaded explicitly while Pi's own extension discovery stays
	// off: that is the difference between llmTools' tools and any .pi directory.
	if got, _ := valueAfter(args, "-e"); got != a.extension {
		t.Fatalf("bridge path not pinned: %q", got)
	}
	// The run's authority travels as process-local environment values only, so
	// nothing a workspace could observe carries it.
	env := strings.Join(l.env, "\n")
	for _, want := range []string{"LLMTOOLS_REPO_READ=1", "LLMTOOLS_CONTROL_TOKEN=ctl_token", "LLMTOOLS_CONTROL_BIN=", "LLMTOOLS_CONTROL_DB="} {
		if !strings.Contains(env, want) {
			t.Fatalf("launch env missing %q: %v", want, l.env)
		}
	}
	// Both a continuation and a new task may restore a conversation session.
	l2, e := a.base()
	if e != nil {
		t.Fatal(e)
	}
	fresh := a.launchArgs(&l2, "ctl_token", false, "openai", "gpt-5-mini", "")
	if _, found := valueAfter(fresh, "--session"); found {
		t.Fatalf("a fresh session was launched with a restore flag: %v", fresh)
	}
	if got, _ := valueAfter(fresh, "--tools"); got != "llmtools_control_request_context,llmtools_control_request_decision" {
		t.Fatalf("research surface: %q", got)
	}
	if !strings.Contains(strings.Join(l2.env, "\n"), "LLMTOOLS_REPO_READ=0") {
		t.Fatalf("research grant: %v", l2.env)
	}
	if _, found := valueAfter(args, "--append-system-prompt"); found {
		t.Fatalf("adapter added system instructions: %v", args)
	}

}

// TestLaunchArgsNeverCarryTheWorkspaceRoot records the rule that a canonical
// root is not an authorization credential for any runtime.
func TestLaunchArgsNeverCarryTheWorkspaceRoot(t *testing.T) {
	a := newTestAdapter(t)
	// The stand-in runtime exists, so the launch list is checkable.
	a.spec.Binary = fakePi(t)
	a.spec.WorkingDir = t.TempDir()
	l, e := a.base()
	if e != nil {
		t.Fatal(e)
	}
	root := "/Users/example/private-workspace"
	args := a.launchArgs(&l, "ctl_token", true, "openai", "gpt-5-mini", "ses_1")
	for _, arg := range append(append([]string{}, args...), l.env...) {
		if strings.Contains(arg, root) {
			t.Fatalf("workspace root reached the launch: %q", arg)
		}
	}
	if l.workdir == root || l.workdir == "" {
		t.Fatalf("Pi runs with a workspace as its cwd: %q", l.workdir)
	}
	// The handle carries the root for the shared repository tools, not for Pi.
	if got := workspaceRoot(agent.AgentTask{Workspace: &agent.WorkspaceSnapshot{CanonicalRoot: root}}); got != root {
		t.Fatalf("workspaceRoot: %q", got)
	}
}

func valueAfter(args []string, flag string) (string, bool) {
	if i := indexOfArg(args, flag); i >= 0 && i+1 < len(args) {
		return args[i+1], true
	}
	return "", false
}

func indexOfArg(args []string, want string) int {
	for i := range args {
		if args[i] == want {
			return i
		}
	}
	return -1
}

func TestEmitStageIgnoresANoopEmitter(t *testing.T) {
	emitStage(nil, "RUNNING")
	var got []string
	emitStage(func(p agent.Progress) { got = append(got, p.Stage) }, "TOOL_RUNNING")
	if len(got) != 1 || got[0] != "TOOL_RUNNING" {
		t.Fatalf("stages: %v", got)
	}
}

// TestObserveNormalizesPiEvents proves Pi's own event names never reach Core:
// what leaves the observer is llmTools' stage vocabulary plus one tail record.
func TestObserveNormalizesPiEvents(t *testing.T) {
	p, _ := newTestProcess()
	go func() {
		defer close(p.events)
		for _, raw := range []string{
			`{"type":"tool_execution_start","toolName":"llmtools_control_repo_glob"}`,
			`{"type":"tool_execution_end","toolName":"llmtools_control_repo_glob","result":{"details":{}}}`,
			`{"type":"auto_retry_start","attempt":2}`,
			`{"type":"turn_start"}`,
			`{"type":"message_end","message":{"role":"assistant","provider":"huggingface","model":"deepseek-ai/DeepSeek-V4-Flash","stopReason":"stop"}}`,
			`{"type":"message_end","message":{"role":"user"}}`,
			`{"type":"agent_settled"}`,
		} {
			var probe struct {
				Type string `json:"type"`
			}
			if json.Unmarshal([]byte(raw), &probe) != nil {
				t.Errorf("bad fixture: %s", raw)
				return
			}
			p.events <- Event{Type: probe.Type, Raw: json.RawMessage(raw)}
		}
	}()
	var stages []string
	tail, control, e := observe(context.Background(), p, func(pr agent.Progress) { stages = append(stages, pr.Stage) })
	if e != nil {
		t.Fatalf("observe: %v", e)
	}
	if control != nil {
		t.Fatalf("ordinary execution produced a suspension: %+v", control)
	}
	if !tail.sawMessage || tail.provider != "huggingface" || tail.model != "deepseek-ai/DeepSeek-V4-Flash" || tail.stopReason != "stop" {
		t.Fatalf("assistant tail: %+v", tail)
	}
	for _, stage := range stages {
		if stage == "" {
			t.Fatalf("empty stage emitted: %v", stages)
		}
	}
	if strings.Join(stages, ",") != "TOOL_RUNNING,TOOL_COMPLETED,RUNNING" {
		t.Fatalf("Pi event names leaked as stages: %v", stages)
	}
}

// TestObserveCapturesControlResultAndIdleEnd covers the agent_end path, which is
// the only way an aborted turn can end because Pi never emits agent_settled for
// it. The idle poll is answered by a state server.
func TestObserveCapturesControlResultAndIdleEnd(t *testing.T) {
	p, _ := newTestProcess()
	stop := make(chan struct{})
	defer close(stop)
	go serveStates(p, stop)
	go func() {
		p.events <- Event{Type: "tool_execution_end", Raw: json.RawMessage(`{"toolName":"llmtools_control_request_context","result":{"details":{"llmtools_control":"context","requests":[{"id":"r1","kind":"contract","target":"release","reason":"parent-held"}]}}}`)}
		p.events <- Event{Type: "agent_end", Raw: json.RawMessage(`{"willRetry":false}`)}
	}()
	var stages []string
	tail, control, e := observe(context.Background(), p, func(pr agent.Progress) { stages = append(stages, pr.Stage) })
	if e != nil {
		t.Fatalf("observe: %v", e)
	}
	if control == nil || control.kind != "context" || len(control.requests) != 1 {
		t.Fatalf("no suspension captured: %+v", control)
	}
	if strings.Join(stages, ",") != "TOOL_COMPLETED" {
		t.Fatalf("stages: %v", stages)
	}
	if tail.sawMessage {
		t.Fatalf("a turn without message_end reported a tail: %+v", tail)
	}
}

// TestObserveRefusesAnUnsettledStream covers the fail-closed path: a runtime
// whose stream dies mid-turn must not be reported as a completed run.
func TestObserveRefusesAnUnsettledStream(t *testing.T) {
	p, _ := newTestProcess()
	close(p.events)
	if _, _, e := observe(context.Background(), p, nil); e == nil {
		t.Fatal("a dead stream ended the turn successfully")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p2, _ := newTestProcess()
	if _, _, e := observe(ctx, p2, nil); !errors.Is(e, context.Canceled) {
		t.Fatalf("cancelled observer: %v", e)
	}
}

// serveStates answers the get_state polls an idle check performs.
func serveStates(p *process, stop chan struct{}) {
	for {
		select {
		case <-stop:
			return
		case <-time.After(20 * time.Millisecond):
		}
		p.mu.Lock()
		for id, ch := range p.pending {
			if !strings.HasPrefix(id, "state-") {
				continue
			}
			select {
			case ch <- response{ID: id, Success: true, Data: json.RawMessage(`{"sessionId":"ses_1","isStreaming":false}`)}:
			default:
			}
		}
		p.mu.Unlock()
	}
}

func TestBoolInt(t *testing.T) {
	if boolInt(true) != 1 || boolInt(false) != 0 {
		t.Fatal("grant encoding")
	}
}
