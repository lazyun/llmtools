package integration_test

import (
	"ai-worker-mcp/internal/agent"
	"ai-worker-mcp/internal/config"
	"ai-worker-mcp/internal/store"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// These cases drive the experimental Pi runtime through the real deployment
// route: the same MCP binary, the same durable store, the same role policy and
// the same workspace registry, with only the runtime adapter swapped by a
// process-local environment variable. A real Pi model is invoked, so the whole
// file is opt-in:
//
//	LLMTOOLS_PI_SPIKE=1 LLMTOOLS_TEST_PI=$(command -v pi) \
//	LLMTOOLS_PI_MODEL=huggingface/deepseek-ai/DeepSeek-V4-Flash \
//	  go test ./internal/integration -run PiSpike -timeout 30m -v
//
// Nothing here installs Pi, configures a provider, or writes a credential: the
// model identity is passed to Pi and Pi resolves its own configuration.

const piSpikeTimeout = 8 * time.Minute

type piRoute struct {
	dir        string
	sessionDir string
	binary     string
	dbPath     string
	workspace  store.Workspace
	audit      *syncBuf
	session    *sdk.ClientSession
	model      string
	progress   *piProgressLog
}

// piProgressLog records the progress notifications one route emits. A real
// cancellation has to interrupt a call that is still in flight, and the run
// identity only reaches the client through that stream.
type piProgressLog struct {
	mu    sync.Mutex
	items []agent.Progress
}

func (l *piProgressLog) add(p agent.Progress) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.items = append(l.items, p)
}

func (l *piProgressLog) snapshot() []agent.Progress {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]agent.Progress(nil), l.items...)
}

// waitForRun blocks until the worker has created the run for the in-flight
// call, which is the earliest moment a client can cancel it.
func (l *piProgressLog) waitForRun(t *testing.T, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, p := range l.snapshot() {
			if p.WorkerRunID != "" {
				return p.WorkerRunID
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("no progress notification carried a worker_run_id within %s", timeout)
	return ""
}

// requirePiSpike skips unless the operator pointed the harness at a real Pi
// binary and one already-configured Pi model. An absent environment is reported
// as a skip rather than a pass so a spike can never be claimed as closed
// without a real run.
func requirePiSpike(t *testing.T) (piBinary, model string) {
	t.Helper()
	if os.Getenv("LLMTOOLS_PI_SPIKE") != "1" {
		t.Skip("set LLMTOOLS_PI_SPIKE=1 to run the real Pi runtime spike")
	}
	piBinary, model = os.Getenv("LLMTOOLS_TEST_PI"), os.Getenv("LLMTOOLS_PI_MODEL")
	if piBinary == "" || model == "" {
		t.Skip("set LLMTOOLS_TEST_PI and LLMTOOLS_PI_MODEL to a real Pi binary and an already-configured Pi model")
	}
	if _, e := os.Stat(piBinary); e != nil {
		t.Skipf("Pi binary unavailable: %v", e)
	}
	return piBinary, model
}

// setupPiRoute builds the worker binary, writes an isolated spike configuration
// into a temporary data directory, grants one fixture workspace through the
// durable registry API the Desktop picker uses, and connects MCP over stdio
// with the process-local Pi selection.
func setupPiRoute(t *testing.T) *piRoute {
	t.Helper()
	piBinary, model := requirePiSpike(t)
	return startPiRoute(t, piBinary, model, t.TempDir())
}

// preparePiStore creates the durable half of a spike route once: an isolated
// config directory, a routing table whose model identity comes from the
// environment, and one granted fixture workspace. A restart reuses all three.
func preparePiStore(t *testing.T) (dir, dbPath string, workspace store.Workspace) {
	t.Helper()
	_, model := requirePiSpike(t)
	dir = t.TempDir()
	c := config.Default()
	c.Directory = dir
	// Routing uses one Pi model identity; the OpenCode fields stay at their
	// defaults because persisted configuration cannot select the spike at all.
	c.Agents = map[string]config.AgentRouting{
		"implementation": {Exposed: true, Agent: "implementation", Model: model},
		"review":         {Exposed: true, Agent: "review", Model: model},
	}
	if e := config.Save(filepath.Join(dir, "config.toml"), c); e != nil {
		t.Fatal(e)
	}
	fixtureRoot := filepath.Join(t.TempDir(), "workspace")
	if e := os.MkdirAll(filepath.Join(fixtureRoot, "src"), 0o755); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(fixtureRoot, "src", "alpha.txt"), []byte("ALPHA-7f3c9d2e\n"), 0o644); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(fixtureRoot, "src", "beta.txt"), []byte("BETA-4a1b8c6d\n"), 0o644); e != nil {
		t.Fatal(e)
	}
	if e := os.MkdirAll(filepath.Join(filepath.Dir(fixtureRoot), "outside"), 0o755); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(filepath.Dir(fixtureRoot), "outside", "secret.txt"), []byte("OUTSIDE-9e8d7c6b\n"), 0o644); e != nil {
		t.Fatal(e)
	}
	dbPath = filepath.Join(dir, c.Storage.Database)
	st, e := store.Open(dbPath)
	if e != nil {
		t.Fatal(e)
	}
	w, _, e := st.AddWorkspace(fixtureRoot, "pi-spike", "desktop_user_grant")
	if e != nil {
		t.Fatal(e)
	}
	st.Close()
	return dir, dbPath, w
}

// dialPi starts a worker process for an already prepared store. Closing the
// returned session ends that process, which is how the restart case proves
// continuation across a real process boundary rather than in memory.
func dialPi(t *testing.T, piBinary, model, sessionDir, dir, dbPath string, workspace store.Workspace) *piRoute {
	t.Helper()
	b := binary(t)
	audit := &syncBuf{}
	progress := &piProgressLog{}
	session := connectPi(t, b, dir, audit, progress, piBinary, sessionDir)
	return &piRoute{dir: dir, sessionDir: sessionDir, binary: b, dbPath: dbPath, workspace: workspace, audit: audit, session: session, model: model, progress: progress}
}

// startPiRoute is setupPiRoute with a caller-chosen session directory, so the
// restart case can reconnect to the same durable Pi session tree from a new
// worker process.
func startPiRoute(t *testing.T, piBinary, model, sessionDir string) *piRoute {
	t.Helper()
	dir, dbPath, workspace := preparePiStore(t)
	return dialPi(t, piBinary, model, sessionDir, dir, dbPath, workspace)
}

func connectPi(t *testing.T, b, dir string, audit *syncBuf, progress *piProgressLog, piBinary, sessionDir string) *sdk.ClientSession {
	t.Helper()
	client := sdk.NewClient(&sdk.Implementation{Name: "llmtools-pi-spike", Version: "1"}, &sdk.ClientOptions{
		ProgressNotificationHandler: func(_ context.Context, r *sdk.ProgressNotificationClientRequest) {
			var p agent.Progress
			if json.Unmarshal([]byte(r.Params.Message), &p) == nil {
				progress.add(p)
			}
		},
	})
	cmd := exec.Command(b, "--config-dir", dir)
	cmd.Stderr = audit
	cmd.Env = append(os.Environ(), "LLMTOOLS_RUNTIME_SPIKE=pi", "LLMTOOLS_PI_BINARY="+piBinary, "LLMTOOLS_PI_SESSION_DIR="+sessionDir)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	session, e := client.Connect(ctx, &sdk.CommandTransport{Command: cmd}, nil)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

// outcome is the terminal answer of one MCP call, reported through a channel so
// a cancellation can race a real in-flight turn.
type outcome struct {
	result agent.AgentResult
	err    error
}

// callAsync issues one MCP tool call without failing the test from a non-test
// goroutine; the caller asserts on the returned outcome.
func (r *piRoute) callAsync(tool string, args any, timeout time.Duration) chan outcome {
	ch := make(chan outcome, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		p := &sdk.CallToolParams{Name: tool, Arguments: args}
		p.SetProgressToken("pi-spike-async")
		res, e := r.session.CallTool(ctx, p)
		if e != nil {
			ch <- outcome{err: e}
			return
		}
		body, _ := json.Marshal(res.StructuredContent)
		var result agent.AgentResult
		if e = json.Unmarshal(body, &result); e != nil {
			ch <- outcome{err: fmt.Errorf("decode %s result: %s", tool, body)}
			return
		}
		ch <- outcome{result: result}
	}()
	return ch
}

// call runs one MCP tool and returns its public result together with the durable
// run it produced. A non-completed run reports the worker process stream, which
// is the only evidence available for a failed real run.
func (r *piRoute) call(t *testing.T, tool string, args any) (agent.AgentResult, agent.Run) {
	t.Helper()
	return r.callWithTimeout(t, tool, args, piSpikeTimeout)
}

func (r *piRoute) callWithTimeout(t *testing.T, tool string, args any, timeout time.Duration) (agent.AgentResult, agent.Run) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	p := &sdk.CallToolParams{Name: tool, Arguments: args}
	p.SetProgressToken("pi-spike")
	res, e := r.session.CallTool(ctx, p)
	if e != nil {
		t.Fatalf("%s: %v\n%s", tool, e, r.tail())
	}
	body, _ := json.Marshal(res.StructuredContent)
	var result agent.AgentResult
	if e = json.Unmarshal(body, &result); e != nil {
		t.Fatalf("%s result: %s", tool, body)
	}
	st, e := store.Open(r.dbPath)
	if e != nil {
		t.Fatal(e)
	}
	defer st.Close()
	if result.WorkerRunID == "" {
		t.Fatalf("%s created no run: %+v\n%s", tool, result, r.tail())
	}
	run, e := st.Get(result.WorkerRunID)
	if e != nil {
		t.Fatalf("run %s: %v (%+v)", result.WorkerRunID, e, result)
	}
	if result.Status == "completed" {
		return result, run
	}
	t.Logf("pi run %s status=%s\n%s", run.ID, result.Status, r.tail())
	return result, run
}

func (r *piRoute) tail() string {
	log := r.audit.String()
	if len(log) > 6000 {
		log = log[len(log)-6000:]
	}
	return "worker stderr tail: " + log
}

// runEvidence is the spike record section 42 asks for: which run, which runtime,
// which session, which model, which workspace and which final status.
func (r *piRoute) evidence(t *testing.T, run agent.Run, result agent.AgentResult) string {
	t.Helper()
	lines := []string{
		fmt.Sprintf("worker_run_id=%s", run.ID),
		fmt.Sprintf("runtime_kind=%s", run.Runtime),
		fmt.Sprintf("runtime_session_id=%s", run.RuntimeSessionID),
		fmt.Sprintf("agent=%s model=%s source=%s", run.RuntimeAgent, run.RuntimeModel, run.ModelSource),
		fmt.Sprintf("workspace_id=%s grant_revision=%d", run.Workspace.WorkspaceID, run.Workspace.GrantRevision),
		fmt.Sprintf("capabilities=%+v", run.Capabilities),
		fmt.Sprintf("status=%s result_runtime=%s", result.Status, result.Runtime),
	}
	return strings.Join(lines, "\n")
}

func writePiEvidence(t *testing.T, name string, payload any) {
	t.Helper()
	b, e := json.MarshalIndent(payload, "", "  ")
	if e != nil {
		t.Fatal(e)
	}
	dir := filepath.Join("..", "..", ".smoke", "pi-spike")
	if e = os.MkdirAll(dir, 0o755); e != nil {
		t.Log(e)
		return
	}
	if e = os.WriteFile(filepath.Join(dir, name), b, 0o644); e != nil {
		t.Log(e)
	}
}

// piErrorCode reports a public result's failure code without assuming one
// exists: a completed, cancelled or suspended result may carry no error at all.
func piErrorCode(result agent.AgentResult) string {
	if result.Error == nil {
		return ""
	}
	return result.Error.Code
}

// requirePiModelUsable records a provider-side block and skips the rest of the
// real run. A depleted or unconfigured Pi model is an environment limit: the
// harness never switches models, edits Pi configuration, or reports it as a pass.
// The block is written to its own file so it cannot overwrite a run's evidence.
func requirePiModelUsable(t *testing.T, evidence string, run agent.Run, result agent.AgentResult) {
	t.Helper()
	if result.Error == nil || result.Error.Code != "MODEL_NOT_AVAILABLE" {
		return
	}
	writePiEvidence(t, strings.TrimSuffix(evidence, ".json")+"-blocked.json", map[string]any{
		"run_id": run.ID, "runtime_kind": run.Runtime, "status": result.Status,
		"error_code": result.Error.Code, "error_message": result.Error.Message,
		"blocked": "the configured Pi model could not serve a real run",
	})
	t.Skipf("real Pi run blocked by the provider: %s", result.Error.Message)
}

// TestPiSpikeRealImplementationReadOnly is the spike's central claim: a real Pi
// model completes an Implementation task whose only filesystem authority is the
// llmTools-owned repository tool surface.
func TestPiSpikeRealImplementationReadOnly(t *testing.T) {
	r := setupPiRoute(t)
	result, run := r.call(t, "delegate_implementation", agent.DelegateTask{Prompt: "Report the exact unique token in each of src/alpha.txt and src/beta.txt. Find those files yourself with the repository glob tool, then read them; do not guess. " +
		"Then attempt to read ../outside/secret.txt and report what happens." + "\n\nConstraints:\n" +
		"Use only the repository read tools for file access." + "\n" + "Report the outcome of the denied attempt verbatim." + "\n\nAcceptanceCriteria:\n" +
		"Both workspace tokens are reported exactly." + "\n" + "The denied attempt is reported as a denial rather than worked around.", WorkspaceID: r.workspace.ID,
	})
	t.Log(r.evidence(t, run, result))
	requirePiModelUsable(t, "implementation.json", run, result)
	if result.Status != "completed" {
		t.Fatalf("Pi implementation run: %+v", result)
	}
	if run.Runtime != "pi" || result.Runtime != "pi" {
		t.Fatalf("runtime kind: run=%q result=%q", run.Runtime, result.Runtime)
	}
	if run.RuntimeModel != r.model || result.Model != r.model {
		t.Fatalf("model identity: run=%q result=%q want %q", run.RuntimeModel, result.Model, r.model)
	}
	if run.Capabilities != (agent.CapabilityPolicy{RepoRead: true}) {
		t.Fatalf("capability snapshot: %+v", run.Capabilities)
	}
	if run.Workspace.WorkspaceID != r.workspace.ID {
		t.Fatalf("workspace snapshot: %+v", run.Workspace)
	}
	if run.RuntimeSessionID == "" || strings.HasPrefix(run.RuntimeSessionID, "ses_") {
		t.Fatalf("Pi session identity: %q", run.RuntimeSessionID)
	}
	for _, token := range []string{"ALPHA-7f3c9d2e", "BETA-4a1b8c6d"} {
		if !strings.Contains(result.Content, token) {
			t.Fatalf("worker did not report %s from real reads: %s", token, result.Content)
		}
	}
	if strings.Contains(result.Content, "OUTSIDE-9e8d7c6b") {
		t.Fatalf("outside content reached the worker: %s", result.Content)
	}
	if strings.Contains(result.Content, "ctl_") || strings.Contains(result.Content, r.workspace.CanonicalRoot) {
		t.Fatalf("final response leaked the token or canonical root: %s", result.Content)
	}
	// The repository authority is the durable grant, observed through the audit
	// the shared implementation writes for every access.
	log := r.audit.String()
	for _, want := range []string{"repo_access_allowed", "tool=repo_read", "workspace_id=" + r.workspace.ID, "code=WORKSPACE_ACCESS_DENIED"} {
		if !strings.Contains(log, want) {
			t.Fatalf("audit missing %q:\n%s", want, log)
		}
	}
	for _, leak := range []string{"ALPHA-7f3c9d2e", "OUTSIDE-9e8d7c6b", "ctl_", r.workspace.CanonicalRoot} {
		if strings.Contains(log, leak) {
			t.Fatalf("audit leaked %q", leak)
		}
	}
	writePiEvidence(t, "implementation.json", map[string]any{
		"run_id": run.ID, "runtime_kind": run.Runtime, "runtime_session_id": run.RuntimeSessionID,
		"model": run.RuntimeModel, "workspace_id": run.Workspace.WorkspaceID,
		"capabilities": run.Capabilities, "status": result.Status,
		"audit_allowed": strings.Count(log, "repo_access_allowed"), "audit_denied": strings.Count(log, "repo_access_denied"),
		"final_response": result.Content,
	})
}

// piReleaseCode is the parent-held fact the context spike withholds from the
// workspace, so a model that follows the execution contract must suspend.
const piReleaseCode = "RELEASE-4d2f9a7c"

// piBridgeTools is the exact execution surface a real Pi run may use. A tool
// name outside this set reaching the model would mean Pi's native surface leaked
// into a run llmTools is supposed to control.
var piBridgeTools = map[string]bool{
	"llmtools_control_repo_read":        true,
	"llmtools_control_repo_glob":        true,
	"llmtools_control_repo_grep":        true,
	"llmtools_control_request_context":  true,
	"llmtools_control_request_decision": true,
}

var piToolExecuted = regexp.MustCompile(`LLMTOOLS_PI tool_executed name="([^"]*)"`)

// assertPiRunIdentity checks the evidence section 42 asks of every real run:
// which runtime, which session, which model, which workspace, which snapshot.
func assertPiRunIdentity(t *testing.T, run agent.Run, r *piRoute) {
	t.Helper()
	if run.Runtime != "pi" {
		t.Fatalf("runtime kind: %q", run.Runtime)
	}
	if run.RuntimeModel != r.model {
		t.Fatalf("model identity: %q want %q", run.RuntimeModel, r.model)
	}
	if run.Capabilities != (agent.CapabilityPolicy{RepoRead: true}) {
		t.Fatalf("capability snapshot: %+v", run.Capabilities)
	}
	if run.Workspace.WorkspaceID != r.workspace.ID {
		t.Fatalf("workspace snapshot: %+v", run.Workspace)
	}
	if run.RuntimeSessionID == "" || strings.HasPrefix(run.RuntimeSessionID, "ses_") {
		t.Fatalf("Pi session identity: %q", run.RuntimeSessionID)
	}
}

// TestPiSpikeRealContextSuspensionAndContinuation proves a real Pi model reaches
// Core's NEEDS_CONTEXT outcome through the llmTools control tool, and that the
// parent's answer resumes the same durable run on the same Pi session.
func TestPiSpikeRealContextSuspensionAndContinuation(t *testing.T) {
	r := setupPiRoute(t)
	result, run := r.call(t, "delegate_implementation", agent.DelegateTask{Prompt: "Report two things: the release code for this run, and the exact token in src/alpha.txt. " +
		"The release code exists only with your parent - it is not in the workspace and not in this prompt - so request it with llmtools_control_request_context instead of guessing it. " +
		"After the answer arrives, read src/alpha.txt with the repository read tool and report both values." + "\n\nConstraints:\n" +
		"Never invent the release code." + "\n" + "Use only the repository read tools for file access." + "\n\nAcceptanceCriteria:\n" +
		"The run suspends for the missing release code instead of guessing it." + "\n" + "Both the parent-supplied code and the workspace token are reported exactly.", WorkspaceID: r.workspace.ID,
	})
	t.Log(r.evidence(t, run, result))
	requirePiModelUsable(t, "context-suspension.json", run, result)
	if result.Status != "needs_context" {
		t.Fatalf("expected a real context suspension, got %+v", result)
	}
	if run.Status != "NEEDS_CONTEXT" {
		t.Fatalf("durable status: %q", run.Status)
	}
	assertPiRunIdentity(t, run, r)
	if len(result.ContextRequests) == 0 {
		t.Fatal("Pi suspension carried no context requests")
	}
	sessionBefore := run.RuntimeSessionID
	writePiEvidence(t, "context-suspension.json", map[string]any{
		"run_id": run.ID, "runtime_session_id": sessionBefore, "status": result.Status,
		"context_requests": result.ContextRequests,
	})

	for _, q := range result.ContextRequests {
		if q.ID == "" || strings.TrimSpace(q.Target) == "" || strings.TrimSpace(q.Reason) == "" {
			t.Fatalf("Pi produced an unusable context request: %+v", q)
		}
	}
	// The parent answers every pending request; a second question is still a
	// valid suspension, so the loop follows the protocol instead of failing.
	current := result
	currentRun := run
	for attempt := 1; current.Status == "needs_context" && attempt <= 4; attempt++ {
		current, currentRun = r.answerContext(t, current)
		t.Logf("context continuation %d -> %s", attempt, current.Status)
	}
	t.Log(r.evidence(t, currentRun, current))
	requirePiModelUsable(t, "context-continuation.json", currentRun, current)
	if current.Status != "completed" {
		t.Fatalf("Pi run did not complete after the parent answered: %+v", current)
	}
	if currentRun.ID != run.ID {
		t.Fatalf("continuation changed the run: %q -> %q", run.ID, currentRun.ID)
	}
	if currentRun.RuntimeSessionID != sessionBefore {
		t.Fatalf("continuation changed the Pi session: %q -> %q", sessionBefore, currentRun.RuntimeSessionID)
	}
	if currentRun.Runtime != "pi" || current.Runtime != "pi" {
		t.Fatalf("continuation runtime: run=%q result=%q", currentRun.Runtime, current.Runtime)
	}
	for _, want := range []string{piReleaseCode, "ALPHA-7f3c9d2e"} {
		if !strings.Contains(current.Content, want) {
			t.Fatalf("final response missing %q: %s", want, current.Content)
		}
	}
	assertPiResponseClean(t, current, r, run)
	writePiEvidence(t, "context-continuation.json", map[string]any{
		"run_id": currentRun.ID, "runtime_session_id": currentRun.RuntimeSessionID,
		"status": current.Status, "final_response": current.Content,
	})
}

// answerContext is the parent's side of the continuation protocol: every pending
// request is answered exactly once, and the same durable run is resumed.
func (r *piRoute) answerContext(t *testing.T, pending agent.AgentResult) (agent.AgentResult, agent.Run) {
	t.Helper()
	if len(pending.ContextRequests) == 0 {
		t.Fatalf("suspension carried no requests to answer: %+v", pending)
	}
	c := agent.Continuation{WorkerRunID: pending.WorkerRunID}
	for _, q := range pending.ContextRequests {
		answer := agent.AdditionalContext{RequestID: q.ID, Type: "contract", Content: piReleaseCode}
		if q.Kind == "file" {
			// Core requires a file answer to name the file the request asked for.
			answer.Type, answer.Path, answer.Content = "file", q.Target, piReleaseCode+"\n"
		}
		c.AdditionalContext = append(c.AdditionalContext, answer)
	}
	return r.call(t, "ai_worker_continue", c)
}

// TestPiSpikeRealDecisionSuspensionAndContinuation proves Pi reaches Core's
// NEEDS_DECISION outcome with the same durable semantics as the OpenCode route:
// the worker stops, the parent answers, the same run resumes.
func TestPiSpikeRealDecisionSuspensionAndContinuation(t *testing.T) {
	r := setupPiRoute(t)
	result, run := r.call(t, "delegate_implementation", agent.DelegateTask{Prompt: "This task needs the public API shape for a new reporting function, and that choice belongs to your parent, not to you. " +
		"Ask for the decision with llmtools_control_request_decision, offering exactly two concrete options with a one-line description each. " +
		"After the parent answers, state the chosen shape and finish." + "\n\nConstraints:\n" +
		"Do not choose the API shape yourself." + "\n" + "Do not request context for this; it is a decision." + "\n\nAcceptanceCriteria:\n" +
		"The run suspends on a decision request with two options." + "\n" + "The parent's choice is reported back verbatim.", WorkspaceID: r.workspace.ID,
	})
	t.Log(r.evidence(t, run, result))
	requirePiModelUsable(t, "decision-suspension.json", run, result)
	if result.Status != "needs_decision" {
		t.Fatalf("expected a real decision suspension, got %+v\n%s", result, r.tail())
	}
	if run.Status != "NEEDS_DECISION" {
		t.Fatalf("durable status: %q", run.Status)
	}
	assertPiRunIdentity(t, run, r)
	if len(result.DecisionRequests) == 0 {
		t.Fatal("Pi suspension carried no decision requests")
	}
	first := result.DecisionRequests[0]
	if strings.TrimSpace(first.Question) == "" {
		t.Fatalf("decision request without a question: %+v", first)
	}
	writePiEvidence(t, "decision-suspension.json", map[string]any{
		"run_id": run.ID, "runtime_session_id": run.RuntimeSessionID,
		"status": result.Status, "decision_requests": result.DecisionRequests,
	})

	c := agent.Continuation{WorkerRunID: result.WorkerRunID}
	chosen := ""
	for _, d := range result.DecisionRequests {
		if len(d.Options) > 0 {
			// The adapter composes an option as "label — description"; the label
			// is what the worker can reasonably repeat.
			chosen = optionLabel(d.Options[0].Description)
			c.DecisionResponses = append(c.DecisionResponses, agent.DecisionResponse{
				RequestID: d.ID, Selection: d.Options[0].ID, Explanation: "Parent-selected option.",
			})
			continue
		}
		chosen = "" // A free-text answer cannot be echoed back by the model.
		c.DecisionResponses = append(c.DecisionResponses, agent.DecisionResponse{
			RequestID: d.ID, CustomText: "parent-approved shape", Explanation: "Parent supplied the choice directly.",
		})
	}
	continued, continuedRun := r.call(t, "ai_worker_continue", c)
	t.Log(r.evidence(t, continuedRun, continued))
	requirePiModelUsable(t, "decision-continuation.json", continuedRun, continued)
	if continued.Status != "completed" {
		t.Fatalf("Pi run did not complete after the parent decided: %+v", continued)
	}
	if continuedRun.ID != run.ID || continuedRun.RuntimeSessionID != run.RuntimeSessionID {
		t.Fatalf("decision continuation changed run or session: %q/%q -> %q/%q",
			run.ID, run.RuntimeSessionID, continuedRun.ID, continuedRun.RuntimeSessionID)
	}
	if chosen != "" && !piChoiceEchoed(chosen, continued.Content) {
		t.Fatalf("final response did not carry the parent's choice %q: %s", chosen, continued.Content)
	}
	assertPiResponseClean(t, continued, r, run)
	writePiEvidence(t, "decision-continuation.json", map[string]any{
		"run_id": continuedRun.ID, "runtime_session_id": continuedRun.RuntimeSessionID,
		"status": continued.Status, "chosen": chosen, "final_response": continued.Content,
	})
}

// piChoiceEchoed reports whether the response repeats the parent's decision.
// A model may rewrap an option in its own punctuation, so the comparison
// ignores everything that is not a letter or a digit.
func piChoiceEchoed(chosen, content string) bool {
	needle := piCompact(chosen)
	if len(needle) > 40 {
		needle = needle[:40]
	}
	return len(needle) > 0 && strings.Contains(piCompact(content), needle)
}

func piCompact(v string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(v) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// optionLabel recovers the option's own label from the composed description.
func optionLabel(description string) string {
	if i := strings.Index(description, " — "); i >= 0 {
		return description[:i]
	}
	return description
}

// assertPiResponseClean checks the two leak rules that apply to every real Pi
// result: workspace confinement and control-secret opacity.
func assertPiResponseClean(t *testing.T, result agent.AgentResult, r *piRoute, run agent.Run) {
	t.Helper()
	if strings.Contains(result.Content, "OUTSIDE-9e8d7c6b") {
		t.Fatalf("outside content reached the worker: %s", result.Content)
	}
	if strings.Contains(result.Content, "ctl_") || strings.Contains(result.Content, r.workspace.CanonicalRoot) {
		t.Fatalf("final response leaked the token or canonical root: %s", result.Content)
	}
	if run.RuntimeSessionID != "" && strings.Contains(result.Content, run.RuntimeSessionID) {
		t.Fatalf("final response leaked the Pi session id: %s", result.Content)
	}
}

// TestPiSpikeRealCancellationAbortsPi cancels a turn that is genuinely in
// flight and requires the durable cancellation contract plus a real process
// teardown: an aborted Pi child would otherwise outlive the run.
func TestPiSpikeRealCancellationAbortsPi(t *testing.T) {
	r := setupPiRoute(t)
	running := r.callAsync("delegate_implementation", agent.DelegateTask{Prompt: "Read src/alpha.txt and src/beta.txt with the repository read tools, repeating the same two reads twenty times in a row. " +
		"Report nothing until all twenty rounds are done, then report both tokens." + "\n\nConstraints:\n" +
		"Use only the repository read tools for file access." + "\n" + "Do not stop early." + "\n\nAcceptanceCriteria:\n" +
		"All twenty rounds are performed.", WorkspaceID: r.workspace.ID,
	}, piSpikeTimeout)
	runID := r.progress.waitForRun(t, 3*time.Minute)
	toolStarted := false
	deadline := time.Now().Add(3 * time.Minute)
	for !toolStarted && time.Now().Before(deadline) {
		select {
		case out := <-running:
			// The turn ended before any tool ran, so there is nothing in flight to
			// abort. A provider-side block is the one legitimate cause here and is
			// recorded as a block rather than reported as a cancellation pass.
			if out.err == nil {
				requirePiModelUsable(t, "cancellation.json", agent.Run{ID: runID, Runtime: "pi"}, out.result)
			}
			t.Fatalf("the Pi turn ended before a tool executed: %+v %v", out.result, out.err)
		default:
		}
		for _, p := range r.progress.snapshot() {
			if p.WorkerRunID == runID && (p.Stage == "TOOL_RUNNING" || p.Stage == "TOOL_COMPLETED") {
				toolStarted = true
				break
			}
		}
		if !toolStarted {
			time.Sleep(100 * time.Millisecond)
		}
	}
	if !toolStarted {
		t.Fatal("the Pi turn never reached a tool execution, so cancelling it would prove nothing")
	}
	cancelled, cancelRun := r.call(t, "ai_worker_cancel", map[string]string{"worker_run_id": runID})
	t.Logf("cancel result: status=%s code=%s run=%s", cancelled.Status, piErrorCode(cancelled), cancelRun.Status)
	if cancelled.Status != "cancelled" {
		t.Fatalf("cancellation was not confirmed: %+v", cancelled)
	}
	if cancelRun.Status != "CANCELLED" {
		t.Fatalf("durable cancellation status: %q", cancelRun.Status)
	}
	if cancelRun.Runtime != "pi" || cancelled.Runtime != "pi" {
		t.Fatalf("cancelled run identity: run=%q result=%q", cancelRun.Runtime, cancelled.Runtime)
	}
	select {
	case out := <-running:
		// The delegate call must not report success for a cancelled turn; an
		// transport error is equally acceptable because the client call was
		// interrupted by the abort.
		if out.err == nil && out.result.Status == "completed" {
			t.Fatalf("cancelled run still returned a completed result: %+v", out.result)
		}
	case <-time.After(2 * time.Minute):
		t.Fatal("the cancelled delegate call never returned")
	}
	// No orphan: Pi and the control gateway it spawns share this process group,
	// which is keyed by the run's private session directory.
	left := piProcesses(t, r.sessionDir)
	if left != "" {
		t.Fatalf("cancelled run left Pi processes alive:\n%s", left)
	}
	writePiEvidence(t, "cancellation.json", map[string]string{
		"run_id": cancelRun.ID, "runtime_session_id": cancelRun.RuntimeSessionID,
		"status": cancelRun.Status, "cancel_result": cancelled.Status,
	})
}

// piProcesses lists live processes still running against one spike session
// directory, which is what an orphaned Pi runtime would keep open.
func piProcesses(t *testing.T, sessionDir string) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		out, e := exec.Command("pgrep", "-f", sessionDir).Output()
		body := strings.TrimSpace(string(out))
		if e != nil || body == "" {
			return ""
		}
		if !time.Now().Before(deadline) {
			list, _ := exec.Command("ps", "-o", "pid=,command=", "-p", strings.ReplaceAll(body, "\n", ",")).CombinedOutput()
			return strings.TrimSpace(string(list))
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// TestPiSpikeRealToolSurfaceIsTheBridgeOnly records, from a real run, which
// tools Pi actually executed and which tools the bridge registered, so the
// native-tool question is answered with observation instead of assertion.
func TestPiSpikeRealToolSurfaceIsTheBridgeOnly(t *testing.T) {
	r := setupPiRoute(t)
	result, run := r.call(t, "delegate_implementation", agent.DelegateTask{Prompt: "Report the exact token in src/alpha.txt. Before reading it, list the workspace files with the repository glob tool. " +
		"Then list the tools you can actually invoke and say explicitly whether a shell, a native file reader, a writer or a web tool is available to you." + "\n\nConstraints:\n" +
		"Attempt any native capability you believe you have and report the real outcome." + "\n\nAcceptanceCriteria:\n" +
		"The workspace token is reported exactly." + "\n" + "The report names only the tools that really executed.", WorkspaceID: r.workspace.ID,
	})
	if result.Status != "completed" {
		requirePiModelUsable(t, "tool-surface.json", run, result)
		t.Fatalf("tool surface probe did not complete: %+v", result)
	}
	assertPiRunIdentity(t, run, r)
	log := r.audit.String()
	loaded := regexp.MustCompile(`LLMTOOLS_PI loaded repo_read=(\S+) tools=(\[[^\n]*)`).FindString(log)
	if loaded == "" {
		t.Fatalf("no bridge load line in the worker stream:\n%s", log)
	}
	if !strings.Contains(loaded, "repo_read=true") {
		t.Fatalf("bridge registered without repository reads: %s", loaded)
	}
	for _, name := range []string{"llmtools_control_repo_read", "llmtools_control_repo_glob", "llmtools_control_repo_grep", "llmtools_control_request_context", "llmtools_control_request_decision"} {
		if !strings.Contains(loaded, name) {
			t.Fatalf("bridge load list missing %s: %s", name, loaded)
		}
	}
	executed := piToolExecuted.FindAllStringSubmatch(log, -1)
	if len(executed) == 0 {
		t.Fatal("the real run executed no tool, so the surface was never exercised")
	}
	seen := map[string]int{}
	for _, m := range executed {
		seen[m[1]]++
		if !piBridgeTools[m[1]] {
			t.Fatalf("Pi executed a tool outside the bridge surface: %q\n%s", m[1], loaded)
		}
	}
	if strings.Contains(result.Content, "OUTSIDE-9e8d7c6b") {
		t.Fatalf("outside content reached the worker: %s", result.Content)
	}
	assertPiResponseClean(t, result, r, run)
	t.Logf("real Pi tool executions: %v", seen)
	writePiEvidence(t, "tool-surface.json", map[string]any{
		"run_id": run.ID, "runtime_session_id": run.RuntimeSessionID,
		"bridge_load_line": loaded, "executed_tools": seen, "status": result.Status,
		"final_response": result.Content,
	})
}

// TestPiSpikeRealProcessRestartContinuation is the honest cross-process check:
// the first worker process ends entirely, a second one starts against the same
// durable store and the same Pi session tree, and the suspended run resumes.
func TestPiSpikeRealProcessRestartContinuation(t *testing.T) {
	piBinary, model := requirePiSpike(t)
	sessionDir := t.TempDir()
	dir, dbPath, workspace := preparePiStore(t)
	first := dialPi(t, piBinary, model, sessionDir, dir, dbPath, workspace)
	result, run := first.call(t, "delegate_implementation", agent.DelegateTask{Prompt: "Report the release code for this run together with the token in src/beta.txt. " +
		"The release code is held only by your parent, so request it with llmtools_control_request_context rather than guessing. " +
		"After the answer arrives, read src/beta.txt and report both values." + "\n\nConstraints:\n" +
		"Never invent the release code." + "\n\nAcceptanceCriteria:\n" +
		"The run suspends for the missing code." + "\n" + "Both values are reported exactly.", WorkspaceID: workspace.ID,
	})
	if result.Status != "needs_context" {
		requirePiModelUsable(t, "process-restart.json", run, result)
		t.Fatalf("expected a context suspension before the restart, got %+v", result)
	}
	assertPiRunIdentity(t, run, first)
	suspended := run
	// End the first worker for real: closing the MCP session terminates that
	// process, and nothing about the run survives in it.
	if e := first.session.Close(); e != nil {
		t.Log(e)
	}
	second := dialPi(t, piBinary, model, sessionDir, dir, dbPath, workspace)
	continued, continuedRun := second.answerContext(t, result)
	if continued.Status == "needs_context" {
		// A second question is still a valid resume; answer it once more.
		continued, continuedRun = second.answerContext(t, continued)
	}
	if continued.Status != "completed" {
		requirePiModelUsable(t, "process-restart.json", continuedRun, continued)
		t.Fatalf("run did not complete after a real worker restart: %+v", continued)
	}
	if continuedRun.ID != suspended.ID {
		t.Fatalf("restart changed the run identity: %q -> %q", suspended.ID, continuedRun.ID)
	}
	if continuedRun.RuntimeSessionID != suspended.RuntimeSessionID {
		t.Fatalf("restart did not restore the Pi session: %q -> %q", suspended.RuntimeSessionID, continuedRun.RuntimeSessionID)
	}
	if continuedRun.Runtime != "pi" {
		t.Fatalf("restarted runtime kind: %q", continuedRun.Runtime)
	}
	for _, want := range []string{piReleaseCode, "BETA-4a1b8c6d"} {
		if !strings.Contains(continued.Content, want) {
			t.Fatalf("resumed response missing %q: %s", want, continued.Content)
		}
	}
	assertPiResponseClean(t, continued, second, continuedRun)
	writePiEvidence(t, "process-restart.json", map[string]any{
		"run_id": continuedRun.ID, "runtime_session_id": continuedRun.RuntimeSessionID,
		"suspended_at": suspended.Status, "status": continued.Status,
		"session_dir": sessionDir, "final_response": continued.Content,
	})
}

// TestPiSpikeRealReviewReadOnly extends the spike to the second role that the
// frozen baseline grants repository reads, once Implementation passes.
func TestPiSpikeRealReviewReadOnly(t *testing.T) {
	r := setupPiRoute(t)
	result, run := r.call(t, "delegate_review", agent.DelegateTask{Prompt: "Review the two source files in this workspace. Locate them with the repository glob tool, read them with the repository read tool, " +
		"and report each file path with its exact token. Then say whether the workspace looks internally consistent." + "\n\nConstraints:\n" +
		"Read-only review; propose changes instead of making them." + "\n\nAcceptanceCriteria:\n" +
		"Both file paths and tokens are reported." + "\n" + "The review states a consistency judgement.", WorkspaceID: r.workspace.ID,
	})
	t.Log(r.evidence(t, run, result))
	requirePiModelUsable(t, "review.json", run, result)
	if result.Status == "needs_decision" {
		t.Skipf("the review model asked the parent for a decision; recorded as a real suspension: %+v", result.DecisionRequests)
	}
	if result.Status != "completed" {
		t.Fatalf("review run: %+v", result)
	}
	if run.Agent != "review" {
		t.Fatalf("review role identity: %q", run.Agent)
	}
	assertPiRunIdentity(t, run, r)
	for _, want := range []string{"ALPHA-7f3c9d2e", "BETA-4a1b8c6d"} {
		if !strings.Contains(result.Content, want) {
			t.Fatalf("review did not report %s from real reads: %s", want, result.Content)
		}
	}
	assertPiResponseClean(t, result, r, run)
	log := r.audit.String()
	writePiEvidence(t, "review.json", map[string]any{
		"run_id": run.ID, "runtime_session_id": run.RuntimeSessionID, "agent": run.Agent,
		"model": run.RuntimeModel, "status": result.Status,
		"audit_allowed": strings.Count(log, "repo_access_allowed"), "audit_denied": strings.Count(log, "repo_access_denied"),
		"final_response": result.Content,
	})
}
