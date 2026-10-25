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
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// These cases are the parity round: the scenarios the Pi spike proved through a
// process-local override, driven instead through the supported selection route.
// The isolated configuration persistently declares
//
//	[runtime]
//	type = "pi"
//
// and the worker builds its runtime adapter from that file. LLMTOOLS_RUNTIME_SPIKE
// is deliberately emptied for the child process, so a pass here cannot be an
// accident of the spike environment. A real Pi model is invoked, so the file is
// opt-in:
//
//	LLMTOOLS_PI_PARITY=1 LLMTOOLS_TEST_PI=$(command -v pi) \
//	LLMTOOLS_PI_MODEL=<a model Pi already has credentials for> \
//	  go test ./internal/integration -run PiParity -timeout 60m -v
//
// Nothing here installs Pi, configures a provider, or writes a credential: the
// model identity is handed to Pi, which resolves its own configuration.

const piParityTimeout = 8 * time.Minute

// piParityBlocked is reported when every legitimately available model is stopped
// by quota, auth or an outage. The harness never edits the user's Pi
// configuration to escape that state, and never claims a pass from it.
const piParityBlocked = "PI_RUNTIME_PARITY_REAL_MODEL_BLOCKED"

func requirePiParity(t *testing.T) (piBinary, model string) {
	t.Helper()
	if os.Getenv("LLMTOOLS_PI_PARITY") != "1" {
		t.Skip("set LLMTOOLS_PI_PARITY=1 to run the real Pi parity acceptance")
	}
	piBinary, model = os.Getenv("LLMTOOLS_TEST_PI"), os.Getenv("LLMTOOLS_PI_MODEL")
	if piBinary == "" || model == "" {
		t.Skip("set LLMTOOLS_TEST_PI and LLMTOOLS_PI_MODEL to a real Pi binary and a model Pi already serves")
	}
	if _, e := os.Stat(piBinary); e != nil {
		t.Skipf("Pi binary unavailable: %v", e)
	}
	return piBinary, model
}

// writePiParityEvidence keeps this round's artifacts beside the spike's without
// touching them: the spike record is history.
func writePiParityEvidence(t *testing.T, name string, payload any) {
	t.Helper()
	b, e := json.MarshalIndent(payload, "", "  ")
	if e != nil {
		t.Fatal(e)
	}
	dir := filepath.Join("..", "..", ".smoke", "pi-parity")
	if e = os.MkdirAll(dir, 0o755); e != nil {
		t.Log(e)
		return
	}
	if e = os.WriteFile(filepath.Join(dir, name), b, 0o644); e != nil {
		t.Log(e)
	}
}

// parityEnv is one isolated AI Worker root whose persisted configuration selects
// Pi, plus the fixture workspace runs are bound to.
type parityEnv struct {
	dir        string
	sessionDir string
	dbPath     string
	model      string
	workspace  store.Workspace
}

func newParityEnv(t *testing.T, piBinary, model string) *parityEnv {
	t.Helper()
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	root := t.TempDir()
	env := &parityEnv{dir: filepath.Join(root, "config"), model: model}
	env.sessionDir = filepath.Join(env.dir, "pi-sessions")
	must(os.MkdirAll(env.sessionDir, 0o700))
	// Both roles route with no runtime agent name: Pi has no agent catalogue, and
	// llmTools must not invent one for it.
	doc := "[runtime]\ntype = 'pi'\nbinary = '" + piBinary + "'\nsession_dir = '" + env.sessionDir + "'\n\n" +
		"[agents.implementation]\nexposed = true\nmodel = '" + model + "'\n\n" +
		"[agents.review]\nexposed = true\nmodel = '" + model + "'\n"
	c, err := config.Decode([]byte(doc))
	must(err)
	c.Directory = env.dir
	path := filepath.Join(env.dir, "config.toml")
	must(config.Save(path, c))
	// The bytes on disk, not the in-memory value, are what the worker reads.
	loaded, err := config.Load(path)
	must(err)
	if loaded.Runtime.Type != "pi" || loaded.Runtime.Binary != piBinary || loaded.Runtime.SessionDir != env.sessionDir {
		t.Fatalf("persisted Pi selection did not round-trip: %+v", loaded.Runtime)
	}
	for p, body := range map[string]string{
		"fixture/src/alpha.txt": "ALPHA-7f3c9d2e\n",
		"fixture/src/beta.txt":  "BETA-4a1b8c6d\n",
		"fixture/src/gamma.txt": "GAMMA-2c5d8e1f\n",
		"outside/secret.txt":    "OUTSIDE-9e8d7c6b\n",
	} {
		full := filepath.Join(env.dir, filepath.FromSlash(p))
		must(os.MkdirAll(filepath.Dir(full), 0o755))
		must(os.WriteFile(full, []byte(body), 0o644))
	}
	env.dbPath = filepath.Join(env.dir, loaded.Storage.Database)
	st, err := store.Open(env.dbPath)
	must(err)
	defer st.Close()
	w, _, err := st.AddWorkspace(filepath.Join(env.dir, "fixture"), "pi-parity", "desktop_user_grant")
	must(err)
	env.workspace = w
	return env
}

// dial starts one worker process for this configuration and connects MCP over
// stdio. The spike selectors are cleared rather than merely absent, so the
// persisted file is the only thing that can choose a runtime here.
func (e *parityEnv) dial(t *testing.T) *piRoute {
	t.Helper()
	b := binary(t)
	audit := &syncBuf{}
	progress := &piProgressLog{}
	client := sdk.NewClient(&sdk.Implementation{Name: "llmtools-pi-parity", Version: "1"}, &sdk.ClientOptions{
		ProgressNotificationHandler: func(_ context.Context, r *sdk.ProgressNotificationClientRequest) {
			var p agent.Progress
			if json.Unmarshal([]byte(r.Params.Message), &p) == nil {
				progress.add(p)
			}
		},
	})
	cmd := exec.Command(b, "--config-dir", e.dir)
	cmd.Stderr = audit
	cmd.Env = append(os.Environ(), "LLMTOOLS_RUNTIME_SPIKE=", "LLMTOOLS_PI_BINARY=", "LLMTOOLS_PI_SESSION_DIR=")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	session, err := client.Connect(ctx, &sdk.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return &piRoute{dir: e.dir, sessionDir: e.sessionDir, binary: b, dbPath: e.dbPath, workspace: e.workspace, audit: audit, session: session, model: e.model, progress: progress}
}

// callRaw decodes one tool call's public result without requiring a durable run.
// A refusal that happens before any run exists is still an answer.
func (r *piRoute) callRaw(t *testing.T, tool string, args any, timeout time.Duration) agent.AgentResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	p := &sdk.CallToolParams{Name: tool, Arguments: args}
	p.SetProgressToken("pi-parity")
	res, e := r.session.CallTool(ctx, p)
	if e != nil {
		t.Fatalf("%s: %v\n%s", tool, e, r.tail())
	}
	body, _ := json.Marshal(res.StructuredContent)
	var result agent.AgentResult
	if e = json.Unmarshal(body, &result); e != nil {
		t.Fatalf("%s result: %s", tool, body)
	}
	return result
}

func startPiParity(t *testing.T) *piRoute {
	t.Helper()
	piBinary, model := requirePiParity(t)
	requirePiParityReady(t, piBinary, model)
	return newParityEnv(t, piBinary, model).dial(t)
}

// parityEvidence is section 92's field set. It names the runtime, run, session,
// role, model, workspace, capability snapshot, observed tool calls and final
// status, and never a credential, a control token or workspace file content.
type parityEvidence struct {
	Timestamp       string                 `json:"timestamp"`
	Scenario        string                 `json:"scenario"`
	SelectionSource string                 `json:"selection_source"`
	RuntimeKind     string                 `json:"runtime_kind"`
	WorkerRunID     string                 `json:"worker_run_id"`
	RuntimeSession  string                 `json:"runtime_session_id"`
	Role            string                 `json:"role"`
	RuntimeAgent    string                 `json:"runtime_agent_selector"`
	Model           string                 `json:"model"`
	WorkspaceID     string                 `json:"workspace_id"`
	Capabilities    agent.CapabilityPolicy `json:"capability_snapshot"`
	Status          string                 `json:"status"`
	ErrorCode       string                 `json:"error_code"`
	ToolCalls       []string               `json:"tool_calls"`
	AuditAllowed    int                    `json:"repo_access_allowed"`
	AuditDenied     int                    `json:"repo_access_denied"`
	RunIdentity     string                 `json:"run_identity"`
	FinalResponse   string                 `json:"final_response,omitempty"`
	Timeline        map[string]string      `json:"timeline,omitempty"`
}

func newParityEvidence(scenario string) parityEvidence {
	return parityEvidence{
		Timestamp:       time.Now().UTC().Format(time.RFC3339Nano),
		Scenario:        scenario,
		SelectionSource: `persisted config.toml runtime.type = "pi"; LLMTOOLS_RUNTIME_SPIKE cleared`,
	}
}

// record fills section 92's field set from the durable run, falling back to what
// this harness addressed when the run was refused before its row could be read
// back: a blocked record still has to name the runtime, model, role and
// workspace it is about, or it cannot be read on its own. RunIdentity says which
// of the two the record is reporting.
func (p *parityEvidence) record(r *piRoute, role string, run agent.Run, result agent.AgentResult) {
	p.RuntimeKind = run.Runtime
	p.WorkerRunID = run.ID
	p.RuntimeSession = run.RuntimeSessionID
	p.Role = run.Agent
	p.RuntimeAgent = run.RuntimeAgent
	p.Model = run.RuntimeModel
	p.WorkspaceID = run.Workspace.WorkspaceID
	p.Capabilities = run.Capabilities
	p.Status = string(result.Status)
	p.ErrorCode = piErrorCode(result)
	p.FinalResponse = result.Content
	switch {
	case p.RuntimeKind != "" && p.Role != "":
		p.RunIdentity = "durable run"
	case p.WorkerRunID != "":
		p.RunIdentity = "run row refused before it was readable"
	default:
		p.RunIdentity = "no run row"
	}
	// A refusal that happens before any run exists is still an answer, and the
	// public result is then the only carrier of which runtime gave it.
	if p.RuntimeKind == "" {
		p.RuntimeKind = result.Runtime
	}
	if p.Model == "" {
		p.Model = result.Model
	}
	if p.Role == "" {
		p.Role = role
	}
	if p.WorkspaceID == "" {
		p.WorkspaceID = r.workspace.ID
	}
	if p.Model == "" {
		p.Model = r.model
	}
	log := r.audit.String()
	p.ToolCalls = piExecutedTools(log)
	p.AuditAllowed = strings.Count(log, "repo_access_allowed")
	p.AuditDenied = strings.Count(log, "repo_access_denied")
}

// piExecutedTools reads the bridge's own execution log, which is the observed
// tool surface rather than the advertised one.
func piExecutedTools(log string) []string {
	out := []string{}
	for _, m := range piToolExecuted.FindAllStringSubmatch(log, -1) {
		out = append(out, m[1])
	}
	return out
}

// piParityBlockedCode reports the codes that mean "the environment could not
// serve this model" rather than "llmTools did something wrong". Each one is a
// different diagnosis and none of them is a parity pass.
func piParityBlockedCode(result agent.AgentResult) bool {
	switch piErrorCode(result) {
	case "MODEL_NOT_AVAILABLE", "UPSTREAM_UNAVAILABLE", "RUNTIME_UNAVAILABLE", "RUNTIME_NOT_FOUND":
		return true
	}
	return false
}

// requirePiParityModel records a provider-side stop as a complete section 92
// artifact rather than a bare code, then skips with this round's sentinel.
func requirePiParityModel(t *testing.T, r *piRoute, scenario, role string, run agent.Run, result agent.AgentResult) {
	t.Helper()
	if !piParityBlockedCode(result) {
		return
	}
	code := piErrorCode(result)
	ev := newParityEvidence(scenario)
	ev.record(r, role, run, result)
	ev.ErrorCode = code
	writePiParityEvidence(t, scenario+"-blocked.json", ev)
	t.Skipf("%s: %s stopped the selected Pi model (%s)", piParityBlocked, code, result.Error.Message)
}

// piParityProbe caches one bounded availability gate for the whole run of this
// binary, so a suite reports one availability fact rather than one per case.
var piParityProbe struct {
	sync.Mutex
	done      bool
	ok        bool
	attempts  []map[string]any
	configDir string
}

// requirePiParityReady is section 49's gate: at most three bounded real turns on
// the selected model, with at least two completions required before any scenario
// may call the round repeatable.
func requirePiParityReady(t *testing.T, piBinary, model string) {
	t.Helper()
	p := &piParityProbe
	p.Lock()
	defer p.Unlock()
	if p.done {
		if !p.ok {
			t.Skipf("%s: the selected Pi model completed %d of %d bounded probe attempts", piParityBlocked, piParitySuccesses(p.attempts), len(p.attempts))
		}
		return
	}
	env := newParityEnv(t, piBinary, model)
	p.configDir = env.dir
	r := env.dial(t)
	for attempt := 1; attempt <= 3; attempt++ {
		rec := map[string]any{"attempt": attempt, "started_at": time.Now().UTC().Format(time.RFC3339Nano)}
		// callRaw keeps the provider's own words out of the test stream: the probe
		// records the neutral status and code, which is all the gate decides on.
		result := r.callRaw(t, "delegate_implementation", agent.DelegateTask{Prompt: "Report the exact token in src/alpha.txt, nothing else." + "\n\nConstraints:\n" +
			"Use the repository read tool for the single file you need." + "\n\nAcceptanceCriteria:\n" +
			"The token is reported exactly.", WorkspaceID: r.workspace.ID,
		}, piParityTimeout)
		rec["worker_run_id"] = result.WorkerRunID
		rec["runtime_kind"] = result.Runtime
		rec["status"] = string(result.Status)
		rec["error_code"] = piErrorCode(result)
		rec["completed"] = result.Status == "completed" && strings.Contains(result.Content, "ALPHA-7f3c9d2e")
		rec["finished_at"] = time.Now().UTC().Format(time.RFC3339Nano)
		p.attempts = append(p.attempts, rec)
		if result.Status != "completed" && piParityBlockedCode(result) && attempt < 3 {
			// A provider that throttles back-to-back requests needs a moment before
			// the next attempt; all three tries are still used, because one refusal
			// is exactly what a bounded gate exists to distinguish from an outage.
			time.Sleep(5 * time.Second)
		}
	}
	p.ok = piParitySuccesses(p.attempts) >= 2
	p.done = true
	if !p.ok {
		writePiParityEvidence(t, "availability.json", map[string]any{
			"sentinel": piParityBlocked, "model": model, "attempts": p.attempts,
			"note": "the parity suite ran every model-free case; only real-model scenarios stopped",
		})
		t.Skipf("%s: completed %d of %d bounded probe attempts", piParityBlocked, piParitySuccesses(p.attempts), len(p.attempts))
	}
	writePiParityEvidence(t, "availability.json", map[string]any{
		"sentinel": "PI_RUNTIME_PARITY_REAL_MODEL_AVAILABLE", "model": model,
		"required_successes": 2, "attempts": p.attempts,
	})
}

func piParitySuccesses(attempts []map[string]any) int {
	n := 0
	for _, a := range attempts {
		if ok, _ := a["completed"].(bool); ok {
			n++
		}
	}
	return n
}

// TestPiParityAvailabilityProbe reports the bounded gate every other case waits
// on. It is declared first so a -run PiParity invocation probes before spending
// a turn on a scenario.
func TestPiParityAvailabilityProbe(t *testing.T) {
	piBinary, model := requirePiParity(t)
	requirePiParityReady(t, piBinary, model)
	p := &piParityProbe
	if p.configDir == "" {
		t.Fatal("the availability gate produced no configuration root")
	}
	t.Logf("availability: %d of %d bounded attempts completed on %s", piParitySuccesses(p.attempts), len(p.attempts), model)
}

// TestPiParityPersistedConfigSelectsPi proves the adapter swap without a model
// call: a model identity Pi's own catalogue does not contain must fail as
// MODEL_NOT_AVAILABLE on a result whose runtime is pi, which only an adapter
// built from the persisted file can produce. The refusal happens before a run
// exists, so no task ever executes under a model nobody configured.
func TestPiParityPersistedConfigSelectsPi(t *testing.T) {
	piBinary, _ := requirePiParity(t)
	const absentModel = "huggingface/llmtools-parity-absent-model-9z7y"
	env := newParityEnv(t, piBinary, absentModel)
	r := env.dial(t)
	result := r.callRaw(t, "delegate_implementation", agent.DelegateTask{Prompt: "Report the token in src/alpha.txt.", WorkspaceID: r.workspace.ID}, piParityTimeout)
	ev := newParityEvidence("persisted-selection")
	ev.record(r, "implementation", agent.Run{}, result)
	ev.Timeline = map[string]string{"config_dir": env.dir, "durable_run": fmt.Sprintf("%t", result.WorkerRunID != "")}
	writePiParityEvidence(t, "persisted-selection.json", ev)
	if result.Runtime != "pi" {
		t.Fatalf("persisted runtime.type = \"pi\" produced runtime %q", result.Runtime)
	}
	if result.Error == nil || result.Error.Code != "MODEL_NOT_AVAILABLE" {
		t.Fatalf("an unlisted model was not reported as a catalogue absence: %+v", result)
	}
	if strings.Contains(result.Error.Message, "OpenCode") || strings.Contains(result.Error.Message, "Pi") {
		t.Fatalf("the user-facing wording named a runtime: %q", result.Error.Message)
	}
	if result.WorkerRunID != "" {
		t.Fatalf("a run was created for a model the runtime does not have: %q", result.WorkerRunID)
	}
}

// TestPiParityRealImplementation is section 52: a real model completes an
// Implementation task whose only filesystem authority is llmTools' own tool set.
func TestPiParityRealImplementation(t *testing.T) {
	r := startPiParity(t)
	result, run := r.callWithTimeout(t, "delegate_implementation", agent.DelegateTask{Prompt: "Report the exact unique token in each of src/alpha.txt, src/beta.txt and src/gamma.txt. " +
		"Find the files yourself with the repository glob tool, then read them; do not guess. " +
		"Then attempt to read ../outside/secret.txt and report what happens." + "\n\nConstraints:\n" +
		"Use only the repository read tools for file access." + "\n" + "Report the outcome of the denied attempt verbatim." + "\n\nAcceptanceCriteria:\n" +
		"All three workspace tokens are reported exactly." + "\n" + "The denied attempt is reported as a denial rather than worked around.", WorkspaceID: r.workspace.ID,
	}, piParityTimeout)
	t.Log(r.evidence(t, run, result))
	requirePiParityModel(t, r, "implementation", "implementation", run, result)
	if result.Status != "completed" {
		t.Fatalf("Pi implementation run: %+v\n%s", result, r.tail())
	}
	assertPiRunIdentity(t, run, r)
	if run.RuntimeAgent != "" {
		t.Fatalf("Pi routing invented an agent selector: %q", run.RuntimeAgent)
	}
	for _, token := range []string{"ALPHA-7f3c9d2e", "BETA-4a1b8c6d", "GAMMA-2c5d8e1f"} {
		if !strings.Contains(result.Content, token) {
			t.Fatalf("the worker did not report %s from a real read: %s", token, result.Content)
		}
	}
	assertPiResponseClean(t, result, r, run)
	ev := newParityEvidence("implementation")
	ev.record(r, "implementation", run, result)
	if len(ev.ToolCalls) == 0 {
		t.Fatal("the run recorded no tool execution, so the repository surface was never exercised")
	}
	for _, name := range ev.ToolCalls {
		if !piBridgeTools[name] {
			t.Fatalf("Pi executed a tool outside the llmTools surface: %q", name)
		}
	}
	writePiParityEvidence(t, "implementation.json", ev)
}

// TestPiParityRealReview is section 53: this round must produce the complete
// Review artifact itself, not inherit the spike's lost-JSON history.
func TestPiParityRealReview(t *testing.T) {
	r := startPiParity(t)
	result, run := r.callWithTimeout(t, "delegate_review", agent.DelegateTask{Prompt: "Review the source files in this workspace. Locate them with the repository glob tool, read each one with the repository read tool, " +
		"and report every file path with its exact token. Then state whether the workspace looks internally consistent." + "\n\nConstraints:\n" +
		"Read-only review; propose changes instead of making them." + "\n\nAcceptanceCriteria:\n" +
		"Every file path and token is reported." + "\n" + "The review states a consistency judgement.", WorkspaceID: r.workspace.ID,
	}, piParityTimeout)
	t.Log(r.evidence(t, run, result))
	requirePiParityModel(t, r, "review", "review", run, result)
	if result.Status != "completed" {
		t.Fatalf("Pi review run: %+v\n%s", result, r.tail())
	}
	if run.Agent != "review" {
		t.Fatalf("review role identity: %q", run.Agent)
	}
	assertPiRunIdentity(t, run, r)
	if run.RuntimeAgent != "" {
		t.Fatalf("a review run invented the runtime agent name %q; the logical role is review and the selector is none", run.RuntimeAgent)
	}
	for _, want := range []string{"ALPHA-7f3c9d2e", "BETA-4a1b8c6d", "GAMMA-2c5d8e1f"} {
		if !strings.Contains(result.Content, want) {
			t.Fatalf("the review did not report %s from a real read: %s", want, result.Content)
		}
	}
	assertPiResponseClean(t, result, r, run)
	ev := newParityEvidence("review")
	ev.record(r, "review", run, result)
	if len(ev.ToolCalls) == 0 || ev.AuditAllowed == 0 {
		t.Fatalf("the review artifact carries no complete tool evidence: %+v", ev)
	}
	writePiParityEvidence(t, "review.json", ev)
}

// TestPiParityRealContext re-proves NEEDS_CONTEXT through the persisted route:
// suspend, durable pending, continue, same run, same Pi session, completed.
func TestPiParityRealContext(t *testing.T) {
	r := startPiParity(t)
	result, run := r.callWithTimeout(t, "delegate_implementation", agent.DelegateTask{Prompt: "Report two things: the release code for this run, and the exact token in src/alpha.txt. " +
		"The release code exists only with your parent - it is not in the workspace and not in this prompt - so request it with the context request tool instead of guessing it. " +
		"After the answer arrives, read src/alpha.txt and report both values." + "\n\nConstraints:\n" +
		"Never invent the release code." + "\n" + "Use only the repository read tools for file access." + "\n\nAcceptanceCriteria:\n" +
		"The run suspends for the missing release code instead of guessing it." + "\n" + "Both values are reported exactly.", WorkspaceID: r.workspace.ID,
	}, piParityTimeout)
	requirePiParityModel(t, r, "context", "implementation", run, result)
	if result.Status != "needs_context" || run.Status != "NEEDS_CONTEXT" {
		t.Fatalf("expected a real context suspension, got %+v (durable %q)", result, run.Status)
	}
	assertPiRunIdentity(t, run, r)
	if len(result.ContextRequests) == 0 {
		t.Fatal("the suspension carried no context requests")
	}
	suspended := run
	current, currentRun := result, run
	for attempt := 1; current.Status == "needs_context" && attempt <= 4; attempt++ {
		current, currentRun = r.answerContext(t, current)
	}
	requirePiParityModel(t, r, "context", "implementation", currentRun, current)
	if current.Status != "completed" {
		t.Fatalf("the run did not complete after the parent answered: %+v", current)
	}
	if currentRun.ID != run.ID || currentRun.RuntimeSessionID != suspended.RuntimeSessionID {
		t.Fatalf("continuation changed the run or session: %q/%q -> %q/%q", run.ID, suspended.RuntimeSessionID, currentRun.ID, currentRun.RuntimeSessionID)
	}
	for _, want := range []string{piReleaseCode, "ALPHA-7f3c9d2e"} {
		if !strings.Contains(current.Content, want) {
			t.Fatalf("the resumed response is missing %q: %s", want, current.Content)
		}
	}
	assertPiResponseClean(t, current, r, run)
	ev := newParityEvidence("context")
	ev.record(r, "implementation", currentRun, current)
	ev.Timeline = map[string]string{
		"suspended_status":        string(result.Status),
		"pending_requests":        fmt.Sprintf("%d", len(result.ContextRequests)),
		"session_before":          suspended.RuntimeSessionID,
		"session_after":           currentRun.RuntimeSessionID,
		"worker_run_id_unchanged": fmt.Sprintf("%t", currentRun.ID == run.ID),
	}
	writePiParityEvidence(t, "context.json", ev)
}

// TestPiParityRealDecision re-proves NEEDS_DECISION through the persisted route.
func TestPiParityRealDecision(t *testing.T) {
	r := startPiParity(t)
	result, run := r.callWithTimeout(t, "delegate_implementation", agent.DelegateTask{Prompt: "This task needs the public API shape for a new reporting function, and that choice belongs to your parent, not to you. " +
		"Ask for the decision with the decision request tool, offering exactly two concrete options with a one-line description each. " +
		"After the parent answers, state the chosen shape and finish." + "\n\nConstraints:\n" +
		"Do not choose the API shape yourself." + "\n" + "Do not request context for this; it is a decision." + "\n\nAcceptanceCriteria:\n" +
		"The run suspends on a decision request with two options." + "\n" + "The parent's choice is reported back.", WorkspaceID: r.workspace.ID,
	}, piParityTimeout)
	requirePiParityModel(t, r, "decision", "implementation", run, result)
	if result.Status != "needs_decision" || run.Status != "NEEDS_DECISION" {
		t.Fatalf("expected a real decision suspension, got %+v (durable %q)\n%s", result, run.Status, r.tail())
	}
	assertPiRunIdentity(t, run, r)
	if len(result.DecisionRequests) == 0 {
		t.Fatal("the suspension carried no decision requests")
	}
	c := agent.Continuation{WorkerRunID: result.WorkerRunID}
	chosen := ""
	for _, d := range result.DecisionRequests {
		if len(d.Options) > 0 {
			chosen = optionLabel(d.Options[0].Description)
			c.DecisionResponses = append(c.DecisionResponses, agent.DecisionResponse{
				RequestID: d.ID, Selection: d.Options[0].ID, Explanation: "Parent-selected option.",
			})
			continue
		}
		c.DecisionResponses = append(c.DecisionResponses, agent.DecisionResponse{
			RequestID: d.ID, CustomText: "parent-approved shape", Explanation: "Parent supplied the choice directly.",
		})
	}
	continued, continuedRun := r.callWithTimeout(t, "ai_worker_continue", c, piParityTimeout)
	requirePiParityModel(t, r, "decision", "implementation", continuedRun, continued)
	if continued.Status != "completed" {
		t.Fatalf("the run did not complete after the parent decided: %+v", continued)
	}
	if continuedRun.ID != run.ID || continuedRun.RuntimeSessionID != run.RuntimeSessionID {
		t.Fatalf("the decision continuation changed run or session: %q/%q -> %q/%q",
			run.ID, run.RuntimeSessionID, continuedRun.ID, continuedRun.RuntimeSessionID)
	}
	if chosen != "" && !piChoiceEchoed(chosen, continued.Content) {
		t.Fatalf("the final response did not carry the parent's choice %q: %s", chosen, continued.Content)
	}
	assertPiResponseClean(t, continued, r, run)
	ev := newParityEvidence("decision")
	ev.record(r, "implementation", continuedRun, continued)
	ev.Timeline = map[string]string{
		"suspended_status": string(result.Status),
		"requests":         fmt.Sprintf("%d", len(result.DecisionRequests)),
		"chosen":           chosen,
		"same_run":         fmt.Sprintf("%t", continuedRun.ID == run.ID),
		"same_session":     fmt.Sprintf("%t", continuedRun.RuntimeSessionID == run.RuntimeSessionID),
	}
	writePiParityEvidence(t, "decision.json", ev)
}

// TestPiParityRealCancellation cancels a turn that is genuinely in flight on the
// persisted route and requires the same durable teardown contract.
func TestPiParityRealCancellation(t *testing.T) {
	r := startPiParity(t)
	running := r.callAsync("delegate_implementation", agent.DelegateTask{Prompt: "Read src/alpha.txt, src/beta.txt and src/gamma.txt with the repository read tools, repeating the same three reads fifteen times in a row. " +
		"Report nothing until all fifteen rounds are done, then report the three tokens." + "\n\nConstraints:\n" +
		"Use only the repository read tools for file access." + "\n" + "Do not stop early." + "\n\nAcceptanceCriteria:\n" +
		"All fifteen rounds are performed.", WorkspaceID: r.workspace.ID,
	}, piParityTimeout)
	runID := r.progress.waitForRun(t, 3*time.Minute)
	toolStarted := false
	deadline := time.Now().Add(3 * time.Minute)
	for !toolStarted && time.Now().Before(deadline) {
		select {
		case out := <-running:
			if out.err == nil {
				requirePiParityModel(t, r, "cancellation", "implementation", agent.Run{ID: runID, Runtime: "pi"}, out.result)
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
	cancelled, cancelRun := r.callWithTimeout(t, "ai_worker_cancel", map[string]string{"worker_run_id": runID}, piParityTimeout)
	if cancelled.Status != "cancelled" || cancelRun.Status != "CANCELLED" {
		t.Fatalf("cancellation was not confirmed durably: %+v run=%q", cancelled, cancelRun.Status)
	}
	if cancelRun.Runtime != "pi" || cancelled.Runtime != "pi" {
		t.Fatalf("cancelled run identity: run=%q result=%q", cancelRun.Runtime, cancelled.Runtime)
	}
	select {
	case out := <-running:
		if out.err == nil && out.result.Status == "completed" {
			t.Fatalf("a cancelled run still returned a completed result: %+v", out.result)
		}
	case <-time.After(2 * time.Minute):
		t.Fatal("the cancelled delegate call never returned")
	}
	if left := piProcesses(t, r.sessionDir); left != "" {
		t.Fatalf("the cancelled run left Pi processes alive:\n%s", left)
	}
	ev := newParityEvidence("cancellation")
	ev.record(r, "implementation", cancelRun, cancelled)
	ev.Timeline = map[string]string{"aborted_stage": "TOOL_RUNNING_OR_COMPLETED", "orphan_processes": "none"}
	writePiParityEvidence(t, "cancellation.json", ev)
}

// TestPiParityRealProcessRestart is sections 58 and 59: the first worker ends
// entirely and a second one restores both the durable run and the Pi session
// from the same persisted configuration, with no hand-set runtime state.
func TestPiParityRealProcessRestart(t *testing.T) {
	piBinary, model := requirePiParity(t)
	requirePiParityReady(t, piBinary, model)
	env := newParityEnv(t, piBinary, model)
	first := env.dial(t)
	result, run := first.callWithTimeout(t, "delegate_implementation", agent.DelegateTask{Prompt: "Report the release code for this run together with the token in src/beta.txt. " +
		"The release code is held only by your parent, so request it with the context request tool rather than guessing. " +
		"After the answer arrives, read src/beta.txt and report both values." + "\n\nConstraints:\n" +
		"Never invent the release code." + "\n\nAcceptanceCriteria:\n" +
		"The run suspends for the missing code." + "\n" + "Both values are reported exactly.", WorkspaceID: first.workspace.ID,
	}, piParityTimeout)
	requirePiParityModel(t, first, "process-restart", "implementation", run, result)
	if result.Status != "needs_context" {
		t.Fatalf("expected a context suspension before the restart, got %+v", result)
	}
	assertPiRunIdentity(t, run, first)
	suspended := run
	// End the first worker for real: closing the MCP session terminates that
	// process, and nothing about the run survives inside it.
	if e := first.session.Close(); e != nil {
		t.Log(e)
	}
	second := env.dial(t)
	continued, continuedRun := second.answerContext(t, result)
	if continued.Status == "needs_context" {
		continued, continuedRun = second.answerContext(t, continued)
	}
	requirePiParityModel(t, second, "process-restart", "implementation", continuedRun, continued)
	if continued.Status != "completed" {
		t.Fatalf("the run did not complete after a real worker restart: %+v", continued)
	}
	if continuedRun.ID != suspended.ID {
		t.Fatalf("the restart changed the run identity: %q -> %q", suspended.ID, continuedRun.ID)
	}
	if continuedRun.RuntimeSessionID != suspended.RuntimeSessionID {
		t.Fatalf("the restart did not restore the Pi session: %q -> %q", suspended.RuntimeSessionID, continuedRun.RuntimeSessionID)
	}
	if continuedRun.Runtime != "pi" {
		t.Fatalf("the restarted process selected runtime %q from the persisted configuration", continuedRun.Runtime)
	}
	for _, want := range []string{piReleaseCode, "BETA-4a1b8c6d"} {
		if !strings.Contains(continued.Content, want) {
			t.Fatalf("the resumed response is missing %q: %s", want, continued.Content)
		}
	}
	assertPiResponseClean(t, continued, second, continuedRun)
	ev := newParityEvidence("process-restart")
	ev.record(second, "implementation", continuedRun, continued)
	ev.Timeline = map[string]string{
		"suspended_at":  suspended.Status,
		"process_a":     "closed",
		"session_dir":   env.sessionDir,
		"same_session":  fmt.Sprintf("%t", continuedRun.RuntimeSessionID == suspended.RuntimeSessionID),
		"config_source": `persisted runtime.type = "pi"`,
	}
	writePiParityEvidence(t, "process-restart.json", ev)
}

// TestPiParityRealLiveRevoke closes GAP-7: a real Pi model turn that is already
// reading the workspace has its durable authorization revoked mid-flight, and
// the run must fail closed without another payload reaching the model. The
// revocation goes through the same registry commit the Desktop control uses.
func TestPiParityRealLiveRevoke(t *testing.T) {
	r := startPiParity(t)
	inFlight := r.callAsync("delegate_implementation", agent.DelegateTask{Prompt: "Determine how this workspace confines a repository read. In separate steps, glob the workspace, then read src/alpha.txt, src/beta.txt and src/gamma.txt one at a time, " +
		"and finally report each token you actually saw." + "\n\nConstraints:\n" +
		"Make each repository operation a separate tool call." + "\n" + "Read-only." + "\n\nAcceptanceCriteria:\n" +
		"The three tokens are reported from real reads.", WorkspaceID: r.workspace.ID,
	}, piParityTimeout)
	runID := r.progress.waitForRun(t, 3*time.Minute)
	deadline := time.Now().Add(4 * time.Minute)
	firstAccess := ""
	for firstAccess == "" {
		if time.Now().After(deadline) {
			t.Fatalf("the run never reached a repository access, so the revocation was never exercised: %s", r.tail())
		}
		select {
		case out := <-inFlight:
			if out.err == nil {
				requirePiParityModel(t, r, "live-revoke", "implementation", agent.Run{ID: runID, Runtime: "pi"}, out.result)
			}
			t.Fatalf("the turn ended before any repository access: %+v %v", out.result, out.err)
		default:
		}
		for _, line := range strings.Split(r.audit.String(), "\n") {
			if strings.Contains(line, "repo_access_allowed") && strings.Contains(line, "run_id="+runID) {
				firstAccess = line
				break
			}
		}
		if firstAccess == "" {
			time.Sleep(50 * time.Millisecond)
		}
	}
	// T2: the durable commit, through the same store API the Desktop control uses.
	st, e := store.Open(r.dbPath)
	if e != nil {
		t.Fatal(e)
	}
	defer st.Close()
	// A line flushes when its access completes, so the offsets taken around the
	// commit separate accesses already in flight from ones started after it.
	markBefore := auditMark(r.audit.String(), len(r.audit.String()))
	invokedAt := time.Now().UTC()
	if e := st.RevokeWorkspace(r.workspace.ID); e != nil {
		t.Fatal(e)
	}
	w, e := st.Workspace(r.workspace.ID)
	if e != nil {
		t.Fatal(e)
	}
	committedAt := time.Now().UTC()
	markAfter := auditMark(r.audit.String(), len(r.audit.String()))
	if w.State != "REVOKED" {
		t.Fatalf("the revocation did not persist: %+v", w)
	}

	out := <-inFlight
	if out.err != nil {
		t.Fatalf("the revoked delegate call never returned a result: %v\n%s", out.err, r.tail())
	}
	result, run := out.result, mustRun(t, r.dbPath, runID)
	ev := newParityEvidence("live-revoke")
	ev.record(r, "implementation", run, result)
	tail := r.audit.String()[markAfter:]
	allowedAfter := strings.Count(tail, "repo_access_allowed")
	deniedAfter := strings.Count(tail, "repo_access_denied")
	ev.Timeline = map[string]string{
		"T1_first_access_allowed":           firstAccess,
		"T2_revoke_invoked":                 invokedAt.Format(time.RFC3339Nano),
		"T3_revoke_durable_commit":          committedAt.Format(time.RFC3339Nano),
		"T3_workspace_state":                w.State,
		"T3_grant_revision":                 fmt.Sprintf("%d", w.Revision),
		"T4_denied_after_commit":            fmt.Sprintf("%d", deniedAfter),
		"T5_allowed_after_commit":           fmt.Sprintf("%d", allowedAfter),
		"T5_inflight_allowed_around_commit": fmt.Sprintf("%d", strings.Count(r.audit.String()[markBefore:markAfter], "repo_access_allowed")),
		"T6_run_status":                     run.Status,
		"T6_result_error":                   ev.ErrorCode,
	}
	// Which layer refused the operation after the commit is recorded rather than
	// assumed: the live-revoke poller aborts the Pi session shortly after the
	// durable commit, so the next tool call can be refused before the control
	// server is consulted. A denial line carrying the revocation code is the
	// control server refusing by itself.
	layer := "runtime_abort"
	for _, line := range strings.Split(tail, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "repo_access_denied") &&
			strings.Contains(line, "code=WORKSPACE_AUTHORIZATION_REVOKED") {
			layer = "control_server"
		}
	}
	ev.Timeline["T5_refusal_layer"] = layer
	writePiParityEvidence(t, "live-revoke.json", ev)
	if allowedAfter > 0 {
		t.Fatalf("%d repository accesses were authorized after the revocation commit:\n%s", allowedAfter, tail)
	}
	if result.Error == nil || result.Error.Code != "WORKSPACE_AUTHORIZATION_REVOKED" {
		t.Fatalf("the revoked run did not fail closed: %+v", result)
	}
	if run.Status != "FAILED" {
		t.Fatalf("durable status after revocation: %q", run.Status)
	}
}

// auditMark advances a raw audit length to the start of the next complete line,
// so a slice of the stream never begins inside a partially written record.
func auditMark(log string, at int) int {
	if i := strings.Index(log[at:], "\n"); i >= 0 {
		return at + i + 1
	}
	return len(log)
}

// mustRun reads the durable record for one run from a second connection, which
// is also the cross-process proof that the revocation reached Core's state.
func mustRun(t *testing.T, dbPath, runID string) agent.Run {
	t.Helper()
	st, e := store.Open(dbPath)
	if e != nil {
		t.Fatal(e)
	}
	defer st.Close()
	run, e := st.Get(runID)
	if e != nil {
		t.Fatal(e)
	}
	return run
}
