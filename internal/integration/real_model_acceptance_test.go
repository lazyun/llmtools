package integration_test

import (
	"context"

	"ai-worker-mcp/internal/agent"
	"ai-worker-mcp/internal/store"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// Real-model acceptance: a live model must do the repository work through the
// llmTools control tools on real production source, and the evidence has to come
// from the runtime and the durable store rather than from the final answer.
//
//	LLMTOOLS_TEST_OPENCODE=$(command -v opencode) LLMTOOLS_REAL_E2E=1 \
//	  go test ./internal/integration -run 'TestRealImplementationCodeChainE2E|TestRealReviewCodeChainE2E|TestRealModelLiveRevocation' -timeout 40m -v

// acceptanceEvidence is one recorded scenario, written to .smoke so the run can
// be re-checked without a second model call.
type acceptanceEvidence struct {
	Scenario     string                 `json:"scenario"`
	StartedAt    string                 `json:"started_at"`
	FinishedAt   string                 `json:"finished_at"`
	Role         string                 `json:"role"`
	Agent        string                 `json:"agent"`
	Model        string                 `json:"model"`
	WorkerRunID  string                 `json:"worker_run_id"`
	SessionID    string                 `json:"session_id"`
	WorkspaceID  string                 `json:"workspace_id"`
	GrantRoot    string                 `json:"workspace_label"`
	GrantRev     int64                  `json:"grant_revision"`
	Capabilities agent.CapabilityPolicy `json:"capabilities"`
	RepoCalls    []repoCall             `json:"repository_tool_calls"`
	RepoSource   string                 `json:"repository_tool_call_source"`
	SessionParts []sessionPart          `json:"session_parts"`
	Status       string                 `json:"status"`
	ErrorCode    string                 `json:"error_code,omitempty"`
	ContentChars int                    `json:"content_chars"`
	ContentHead  string                 `json:"content_excerpt"`
	RunStatus    string                 `json:"run_status"`
	Timeline     map[string]string      `json:"timeline,omitempty"`
}

// repoCall is one repository access record. In the deployed route the control
// server is a child process of the managed runtime, so its stderr audit stream
// is not reachable from the harness; the runtime's own session record is used
// instead, with the control tool prefix as the proof that the access went
// through llmTools rather than through a native runtime tool.
type repoCall struct {
	Kind   string `json:"kind"`
	Tool   string `json:"tool,omitempty"`
	Target string `json:"target,omitempty"`
	Code   string `json:"code,omitempty"`
	At     string `json:"at"`
	AtMs   int64  `json:"at_ms,omitempty"`
	EndMs  int64  `json:"end_ms,omitempty"`
}

// sessionPart is a tool invocation as the runtime itself recorded it. Only
// whitelisted, non-content fields are kept: the tool name, its status, the
// requested relative target, timing, and a redacted error code. Input payloads
// are never copied because they carry the run's control token, and output
// payloads are never copied because they carry repository content.
type sessionPart struct {
	Type    string `json:"type"`
	Tool    string `json:"tool,omitempty"`
	Status  string `json:"status,omitempty"`
	Role    string `json:"role,omitempty"`
	Target  string `json:"target,omitempty"`
	StartMs int64  `json:"start_ms,omitempty"`
	EndMs   int64  `json:"end_ms,omitempty"`
	ErrCode string `json:"error_code,omitempty"`
}

// controlToolPrefix is the MCP server prefix the runtime exposes llmTools'
// own repository tools under.
const controlToolPrefix = "llmtools_control_"

func isControlRepoTool(name string) bool {
	return strings.HasPrefix(name, controlToolPrefix) && (strings.HasSuffix(name, "_repo_read") || strings.HasSuffix(name, "_repo_glob") || strings.HasSuffix(name, "_repo_grep"))
}

func parseKV(s string) map[string]string {
	out := map[string]string{}
	for i := 0; i < len(s); {
		j := strings.IndexByte(s[i:], '=')
		if j < 0 {
			return out
		}
		k := s[i : i+j]
		i += j + 1
		switch {
		case i < len(s) && s[i] == '"':
			end := strings.IndexByte(s[i+1:], '"')
			if end < 0 {
				return out
			}
			out[k] = s[i+1 : i+1+end]
			i += end + 2
		default:
			end := strings.IndexByte(s[i:], ' ')
			if end < 0 {
				end = len(s) - i
			}
			out[k] = strings.TrimSuffix(s[i:i+end], `"`)
			i += end + 1
		}
	}
	return out
}

// repoCallsFor returns the audit records for one run in call order. The audit
// stream is only visible when the control server is a direct child of the
// harness, which is the case for the process-level tests, not for the deployed
// route the real-model tests use.
func repoCallsFor(audit, runID string) []repoCall {
	var out []repoCall
	for _, line := range strings.Split(audit, "\n") {
		kind, rest, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok || !strings.HasPrefix(kind, "repo_access_") {
			continue
		}
		f := parseKV(rest)
		if f["run_id"] != runID {
			continue
		}
		c := repoCall{
			Kind:   strings.TrimPrefix(kind, "repo_access_"),
			Tool:   f["tool"],
			Target: f["target"],
			Code:   f["code"],
			At:     f["at"],
		}
		if at, e := time.Parse(time.RFC3339, f["at"]); e == nil {
			c.AtMs = at.UnixMilli()
		}
		out = append(out, c)
	}
	return out
}

// repoCallsFromSession derives the same repository call sequence from the
// runtime's own record of the session.
func repoCallsFromSession(parts []sessionPart) []repoCall {
	var out []repoCall
	for _, p := range parts {
		if p.Type != "tool" || !isControlRepoTool(p.Tool) {
			continue
		}
		c := repoCall{
			Tool:   "repo_" + p.Tool[strings.LastIndex(p.Tool, "_repo_")+6:],
			Target: p.Target,
			At:     time.UnixMilli(p.StartMs).UTC().Format(time.RFC3339),
			AtMs:   p.StartMs,
			EndMs:  p.EndMs,
		}
		switch p.Status {
		case "completed":
			c.Kind = "allowed"
		case "error":
			c.Kind = "denied"
			c.Code = p.ErrCode
		default:
			continue
		}
		out = append(out, c)
	}
	return out
}

// controlErrorCodes are the authorization outcomes the control server reports.
var controlErrorCodes = []string{
	"WORKSPACE_AUTHORIZATION_REVOKED",
	"WORKSPACE_ACCESS_DENIED",
	"WORKSPACE_UNAVAILABLE",
	"REPO_INPUT_INVALID",
	"RUNTIME_UNAVAILABLE",
}

// errorCode extracts the authorization outcome from a runtime tool error,
// keeping only a code so no payload or token can be carried into evidence. A
// call the runtime refused because the session was already aborted reports as
// TOOL_EXECUTION_ABORTED rather than as a server denial.
func errorCode(s string) string {
	for _, c := range controlErrorCodes {
		if strings.Contains(s, c) {
			return c
		}
	}
	if strings.Contains(s, "abort") {
		return "TOOL_EXECUTION_ABORTED"
	}
	return ""
}

// runtimeSession fetches one session's message parts from the live runtime.
func runtimeSession(t *testing.T, baseURL, sessionID string) []sessionPart {
	t.Helper()
	if baseURL == "" || sessionID == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	req, e := http.NewRequestWithContext(ctx, "GET", baseURL+"/session/"+url.PathEscape(sessionID)+"/message?limit=100", nil)
	if e != nil {
		t.Fatal(e)
	}
	res, e := http.DefaultClient.Do(req)
	if e != nil {
		t.Logf("session message query: %v", e)
		return nil
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Logf("session message query status %d", res.StatusCode)
		return nil
	}
	var messages []struct {
		Info struct {
			Role string `json:"role"`
		} `json:"info"`
		Parts []struct {
			Type  string          `json:"type"`
			Tool  string          `json:"tool"`
			State json.RawMessage `json:"state"`
		} `json:"parts"`
	}
	if e = json.NewDecoder(res.Body).Decode(&messages); e != nil {
		t.Logf("session message body: %v", e)
		return nil
	}
	var out []sessionPart
	for _, m := range messages {
		for _, p := range m.Parts {
			if p.Type != "tool" {
				continue
			}
			sp := sessionPart{Type: p.Type, Tool: p.Tool, Role: m.Info.Role}
			var state struct {
				Status string          `json:"status"`
				Input  json.RawMessage `json:"input"`
				Error  json.RawMessage `json:"error"`
				Time   struct {
					Start int64 `json:"start"`
					End   int64 `json:"end"`
				} `json:"time"`
			}
			if len(p.State) > 0 {
				_ = json.Unmarshal(p.State, &state)
			}
			sp.Status = state.Status
			sp.StartMs = state.Time.Start
			sp.EndMs = state.Time.End
			// Only the requested target is kept, and only from the whitelisted
			// path-shaped keys, so the control token and the tool output never
			// enter the evidence file.
			var input map[string]any
			if len(state.Input) > 0 {
				_ = json.Unmarshal(state.Input, &input)
			}
			for _, key := range []string{"path", "pattern", "glob"} {
				if v, ok := input[key].(string); ok && v != "" {
					sp.Target = v
					break
				}
			}
			if len(state.Error) > 0 {
				sp.ErrCode = errorCode(string(state.Error))
			}
			out = append(out, sp)
		}
	}
	return out
}

// repoEvidenceFor returns one run's repository call sequence. It prefers the
// control server's audit stream, which the harness owns in the process-level
// tests, and otherwise reads the runtime's session record, which is what the
// deployed route leaves available.
func repoEvidenceFor(t *testing.T, r *realRoute, run agent.Run) []repoCall {
	t.Helper()
	if calls := repoCallsFor(r.audit.String(), run.ID); len(calls) > 0 {
		return calls
	}
	return repoCallsFromSession(sessionParts(t, r.dir, run.RuntimeSessionID))
}

// sessionParts reads the runtime's own record of what the session executed, so
// a claim about native tool use is backed by the runtime rather than by the
// worker's prose.
func sessionParts(t *testing.T, dir, sessionID string) []sessionPart {
	t.Helper()
	st, e := store.Open(filepath.Join(dir, "llmtools.db"))
	if e != nil {
		t.Fatal(e)
	}
	instance, e := st.RuntimeInstance()
	st.Close()
	if e != nil || instance.BaseURL == "" {
		t.Logf("no live runtime instance for session evidence: %v", e)
		return nil
	}
	return runtimeSession(t, instance.BaseURL, sessionID)
}

// nativeToolNames are the runtime's own filesystem and execution tools. None of
// them may execute in a repository-read session.
var nativeToolNames = []string{"read", "glob", "grep", "bash", "edit", "write", "patch", "apply_patch", "webfetch", "websearch", "task"}

func assertNoNativeExecution(t *testing.T, parts []sessionPart) {
	t.Helper()
	for _, p := range parts {
		for _, n := range nativeToolNames {
			if p.Tool == n && p.Status == "completed" {
				t.Fatalf("native runtime tool %q completed in the session", n)
			}
		}
	}
}

// treeDigest fingerprints every path, byte and symlink target under root.
func treeDigest(t *testing.T, root string) string {
	t.Helper()
	type ent struct{ rel, kind, sum string }
	var ents []ent
	e := filepath.WalkDir(root, func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		rel, _ := filepath.Rel(root, p)
		switch {
		case d.IsDir():
			ents = append(ents, ent{rel, "dir", ""})
		case d.Type()&os.ModeSymlink != 0:
			target, e := os.Readlink(p)
			if e != nil {
				return e
			}
			ents = append(ents, ent{rel, "link", target})
		default:
			b, e := os.ReadFile(p)
			if e != nil {
				return e
			}
			sum := sha256.Sum256(b)
			ents = append(ents, ent{rel, "file", hex.EncodeToString(sum[:])})
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	sort.Slice(ents, func(i, j int) bool { return ents[i].rel < ents[j].rel })
	h := sha256.New()
	for _, x := range ents {
		fmt.Fprintf(h, "%s\x00%s\x00%s\x00", x.rel, x.kind, x.sum)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// scanStoreFor proves the durable store kept no repository payload. The needles
// have to be verbatim blocks that only a file body can produce: a worker's own
// report is persisted by design, and it legitimately names functions and paths.
func scanStoreFor(t *testing.T, dir string, needles []string) {
	t.Helper()
	for _, name := range []string{"llmtools.db", "llmtools.db-wal"} {
		b, e := os.ReadFile(filepath.Join(dir, name))
		if e != nil {
			continue
		}
		for _, n := range needles {
			if strings.Contains(string(b), n) {
				t.Fatalf("%s persisted repository payload %q", name, n)
			}
		}
	}
}

// assertCapabilityReleased is the persistence claim that actually holds at the
// product boundary: the control handle is durable while the run lives because
// the control server is a separate process, and it must resolve to nothing once
// the run is terminal. Byte-level scans of the database files cannot show this,
// because a released row leaves its bytes behind until the WAL is checkpointed.
func assertCapabilityReleased(t *testing.T, r *realRoute, run agent.Run) {
	t.Helper()
	st, e := store.Open(filepath.Join(r.dir, "llmtools.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer st.Close()
	if run.RuntimeControl == "" {
		t.Fatal("the run carried no control handle to release")
	}
	if _, _, e := st.LiveRepoRoot(run.RuntimeControl); e == nil {
		t.Fatal("a terminal run still resolves a repository grant")
	}
	if _, e := st.RepoAccessByRun(run.ID); e == nil {
		t.Fatal("repo_access still carries the terminal run")
	}
}

// verbatimRepositoryBlock returns a contiguous mid-file block of one source
// file, long enough that no plausible summary reproduces it byte for byte.
func verbatimRepositoryBlock(t *testing.T, root, rel string) string {
	t.Helper()
	b, e := os.ReadFile(filepath.Join(root, rel))
	if e != nil {
		t.Fatal(e)
	}
	const size = 400
	if len(b) < size {
		t.Fatalf("%s is too small to fingerprint", rel)
	}
	return string(b[len(b)/2 : len(b)/2+size])
}

// persistenceNeedles fingerprint the two files a read-path task must have opened
// and the shape of a raw repository tool payload.
func persistenceNeedles(t *testing.T, src string) []string {
	t.Helper()
	return []string{
		verbatimRepositoryBlock(t, src, "repoaccess/repoaccess.go"),
		verbatimRepositoryBlock(t, src, "runtime/opencode/repo_tools.go"),
	}
}

// redact keeps the evidence free of the run's control handle and of the user's
// home layout, which §42 forbids recording.
func redact(s string) string {
	if h := os.Getenv("HOME"); h != "" {
		s = strings.ReplaceAll(s, h, "~")
	}
	// A control token is a live repository capability; it never belongs in a
	// report, so a partial match is refused rather than trimmed.
	if i := strings.Index(s, "ctl_"); i >= 0 {
		return s[:i] + "<redacted-token>"
	}
	return s
}

// tokenPaths names the JSON locations whose values carry the token prefix, which
// is what a refused evidence record has to report without copying the value.
func tokenPaths(b []byte) []string {
	var v any
	if e := json.Unmarshal(b, &v); e != nil {
		return []string{"<unparseable>"}
	}
	var paths []string
	var walk func(path string, v any)
	walk = func(path string, v any) {
		switch t := v.(type) {
		case map[string]any:
			for k, x := range t {
				walk(path+"."+k, x)
			}
		case []any:
			for i, x := range t {
				walk(fmt.Sprintf("%s[%d]", path, i), x)
			}
		case string:
			if strings.Contains(t, "ctl_") {
				paths = append(paths, path)
			}
		}
	}
	walk("$", v)
	sort.Strings(paths)
	return paths
}

func writeEvidence(t *testing.T, name string, v acceptanceEvidence) {
	t.Helper()
	v.ContentHead = redact(v.ContentHead)
	for i := range v.RepoCalls {
		v.RepoCalls[i].Target = redact(v.RepoCalls[i].Target)
	}
	// A real model reviewing the containment code can grep for the token prefix
	// itself, so the runtime's own record of each tool input needs the same
	// treatment as the derived call list.
	for i := range v.SessionParts {
		v.SessionParts[i].Target = redact(v.SessionParts[i].Target)
	}
	b, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(b), "ctl_") {
		// Only the JSON paths are reported, never the value, so a refusal cannot
		// turn the test log into a second copy of the capability it just refused.
		t.Fatalf("evidence carried a control token in %v", tokenPaths(b))
	}
	path := filepath.Join("..", "..", ".smoke", "read-only-repo", name)
	f, e := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if e != nil {
		t.Log(e)
		return
	}
	defer f.Close()
	if _, e := f.Write(append(b, '\n')); e != nil {
		t.Log(e)
	}
}

// recordRun fills the run-shaped evidence fields from the durable record, so the
// snapshot claims come from the store rather than from the harness's own input.
func (ev *acceptanceEvidence) recordRun(run agent.Run, result agent.AgentResult, workspace store.Workspace) {
	ev.WorkerRunID = run.ID
	ev.SessionID = run.RuntimeSessionID
	ev.Agent = run.RuntimeAgent
	ev.Model = run.RuntimeModel
	ev.Capabilities = run.Capabilities
	ev.WorkspaceID = run.Workspace.WorkspaceID
	ev.GrantRev = run.Workspace.GrantRevision
	ev.GrantRoot = workspace.Label
	ev.Status = result.Status
	ev.RunStatus = run.Status
	if result.Error != nil {
		ev.ErrorCode = result.Error.Code
	}
	ev.ContentChars = len(result.Content)
	ev.ContentHead = result.Content
	if len(ev.ContentHead) > 400 {
		ev.ContentHead = ev.ContentHead[:400]
	}
}

func (ev *acceptanceEvidence) finish(t *testing.T, r *realRoute, name string) {
	t.Helper()
	ev.FinishedAt = time.Now().UTC().Format(time.RFC3339)
	ev.SessionParts = sessionParts(t, r.dir, ev.SessionID)
	// Prefer the control server's own audit stream when the harness can see it;
	// in the deployed route the control server is the runtime's child, so the
	// runtime session record carries the same tool attribution instead.
	if ev.RepoCalls = repoCallsFor(r.audit.String(), ev.WorkerRunID); len(ev.RepoCalls) > 0 {
		ev.RepoSource = "control_server_audit"
	} else {
		ev.RepoCalls = repoCallsFromSession(ev.SessionParts)
		ev.RepoSource = "runtime_session_record"
	}
	// Evidence is written before any assertion so a failed acceptance still
	// records what the runtime actually did.
	writeEvidence(t, name, *ev)
	assertNoNativeExecution(t, ev.SessionParts)
	if len(ev.RepoCalls) == 0 {
		t.Fatalf("no llmTools repository tool call was recorded for %s", ev.WorkerRunID)
	}
}

// assertConfinement checks that every recorded target is workspace-relative and
// that neither the evidence nor the audit carried a payload, token or the
// absolute root.
func assertConfinement(t *testing.T, r *realRoute, ev acceptanceEvidence, workspace store.Workspace) {
	t.Helper()
	tools := map[string]bool{}
	for _, c := range ev.RepoCalls {
		if c.Kind != "allowed" {
			continue
		}
		tools[c.Tool] = true
		if strings.HasPrefix(c.Target, "/") || strings.HasPrefix(c.Target, "..") {
			t.Fatalf("recorded target escaped the workspace: %q", c.Target)
		}
	}
	if !tools["repo_read"] {
		t.Fatal("no repository read was served through the control tool")
	}
	if !tools["repo_glob"] && !tools["repo_grep"] {
		t.Fatal("no repository discovery was served through the control tools")
	}
	for _, leak := range []string{workspace.CanonicalRoot, "ctl_"} {
		if strings.Contains(r.audit.String(), leak) {
			t.Fatalf("audit leaked %q", leak)
		}
	}
}

func implementationChainTask(workspaceID string) agent.DelegateTask {
	return agent.DelegateTask{Prompt: "Map the production path a repository read takes inside this workspace: start from the control tool entry points that receive a repository request, follow the chain at least one level deeper until the bytes are produced, and name the files and functions of every hop. " +
		"List each authorization or containment check the chain performs before it returns content, using the identifiers exactly as the source spells them. " +
		"First glob for the candidate packages, then grep for the function and its call sites, then read the files that answer the question." + "\n\nConstraints:\n" +

		"Read-only: do not create, edit, rename or delete anything, and do not run commands." + "\n" +
		"Name only files you actually opened with a repository tool." + "\n" +
		"Every claim must be supported by a tool result you received in this run." + "\n\nDecisions:\n" +

		"The workspace bound to this run is the only source of truth; nothing outside it is in scope." + "\n\nAcceptanceCriteria:\n" +
		"At least two production files are named with the role each plays in the chain." + "\n" + "The listed checks are ones the code really performs.", WorkspaceID: workspaceID,
	}
}

func reviewChainTask(workspaceID string) agent.DelegateTask {
	return agent.DelegateTask{Prompt: "Review this workspace's workspace-authorization and repository-read path for an obvious bypass: locate the implementation, grep its call sites, read what decides whether an access is allowed, and follow at least one call layer. " +
		"Report the concrete files you inspected and the reason each is or is not a bypass risk. An empty finding list is an acceptable result if the inspection supports it." + "\n\nConstraints:\n" +

		"Read-only review: no edits, no commands, no redesign." + "\n" +
		"Cite file paths relative to the workspace root." + "\n" +
		"Do not report on code you did not open in this run." + "\n\nDecisions:\n" +

		"Review only; the frozen security model is not up for reinterpretation." + "\n\nAcceptanceCriteria:\n" +
		"At least two files are named with a specific reason." + "\n" + "Each statement traces to an inspected file.", WorkspaceID: workspaceID,
	}
}

func TestRealImplementationCodeChainE2E(t *testing.T) {
	r := setupRealRoute(t)
	src := r.srcRoot()
	before := treeDigest(t, src)
	ev := acceptanceEvidence{Scenario: "implementation", StartedAt: time.Now().UTC().Format(time.RFC3339), Role: "implementation"}
	result, run := r.delegate(t, "delegate_implementation", implementationChainTask(r.wsSrc.ID))
	ev.recordRun(run, result, r.wsSrc)
	ev.finish(t, r, "real-implementation-e2e.jsonl")
	if result.Status != "completed" {
		t.Fatalf("implementation run: %+v", result)
	}
	assertReadPolicy(t, run, r.wsSrc)
	assertConfinement(t, r, ev, r.wsSrc)
	// The report must reflect files it really opened, not a plausible guess.
	named := 0
	for _, f := range []string{"repo_tools.go", "repoaccess.go", "context.go", "client.go", "repoaccess", "runtime/opencode"} {
		if strings.Contains(result.Content, f) {
			named++
		}
	}
	if named < 2 {
		t.Fatalf("implementation report does not name the files it had to read: %s", result.Content)
	}
	if treeDigest(t, src) != before {
		t.Fatal("the run modified the bound workspace tree")
	}
	scanStoreFor(t, r.dir, persistenceNeedles(t, src))
	assertCapabilityReleased(t, r, run)
}

func TestRealReviewCodeChainE2E(t *testing.T) {
	r := setupRealRoute(t)
	src := r.srcRoot()
	before := treeDigest(t, src)
	ev := acceptanceEvidence{Scenario: "review", StartedAt: time.Now().UTC().Format(time.RFC3339), Role: "review"}
	result, run := r.delegate(t, "delegate_review", reviewChainTask(r.wsSrc.ID))
	ev.recordRun(run, result, r.wsSrc)
	ev.finish(t, r, "real-review-e2e.jsonl")
	if result.Status != "completed" {
		t.Fatalf("review run: %+v", result)
	}
	assertReadPolicy(t, run, r.wsSrc)
	assertConfinement(t, r, ev, r.wsSrc)
	if !strings.Contains(result.Content, ".go") {
		t.Fatalf("review cites no source file: %s", result.Content)
	}
	if treeDigest(t, src) != before {
		t.Fatal("the run modified the bound workspace tree")
	}
	scanStoreFor(t, r.dir, persistenceNeedles(t, src))
	assertCapabilityReleased(t, r, run)
}

// realReleaseCode is known only to this harness, so a run that needs it can get
// it in exactly one way: by suspending for parent context.
const realReleaseCode = "RELEASE-8b3e6d1a"

// TestRealOpenCodeContextContinuation is the production-route NEEDS_CONTEXT case:
// a real model run suspends for a value only the parent holds, the parent answers
// through ai_worker_continue, and the same durable run and runtime session
// complete. It is the regression that matters for the shared-contract work,
// because the suspension contract is what both runtimes now implement.
func TestRealOpenCodeContextContinuationE2E(t *testing.T) {
	r := setupRealRoute(t)
	before := treeDigest(t, r.wsA.CanonicalRoot)
	ev := acceptanceEvidence{Scenario: "context-continuation", StartedAt: time.Now().UTC().Format(time.RFC3339), Role: "implementation"}
	result, run := r.delegate(t, "delegate_implementation", contextContinuationTask(r.wsA.ID))
	if result.Status != "needs_context" || run.Status != "NEEDS_CONTEXT" {
		ev.recordRun(run, result, r.wsA)
		writeEvidence(t, "real-context-continuation-e2e.jsonl", ev)
		t.Fatalf("the run did not suspend for parent-only context: %+v (durable %q)", result, run.Status)
	}
	suspended := run
	if len(result.ContextRequests) == 0 {
		t.Fatal("the suspension carried no context requests")
	}
	continued, continuedRun := result, run
	for attempt := 1; continued.Status == "needs_context" && attempt <= 3; attempt++ {
		c := agent.Continuation{WorkerRunID: continued.WorkerRunID}
		for _, q := range continued.ContextRequests {
			answer := agent.AdditionalContext{RequestID: q.ID, Type: "contract", Content: realReleaseCode}
			if q.Kind == "file" {
				// Core requires a file answer to name the file that was requested.
				answer.Type, answer.Path, answer.Content = "file", q.Target, realReleaseCode+"\n"
			}
			c.AdditionalContext = append(c.AdditionalContext, answer)
		}
		continued, continuedRun = r.call(t, "ai_worker_continue", c)
	}
	ev.recordRun(continuedRun, continued, r.wsA)
	ev.Timeline = map[string]string{
		"T1_suspended_status": string(result.Status),
		"T1_pending_requests": fmt.Sprintf("%d", len(result.ContextRequests)),
		"T2_run_status":       run.Status,
		"T3_final_status":     string(continued.Status),
		"T3_same_run":         fmt.Sprintf("%t", continuedRun.ID == suspended.ID),
		"T3_same_session":     fmt.Sprintf("%t", continuedRun.RuntimeSessionID == suspended.RuntimeSessionID),
		"T3_final_run_status": continuedRun.Status,
	}
	ev.finish(t, r, "real-context-continuation-e2e.jsonl")
	if continued.Status != "completed" {
		t.Fatalf("the run did not complete after the parent answered: %+v", continued)
	}
	if continuedRun.ID != suspended.ID || continuedRun.RuntimeSessionID != suspended.RuntimeSessionID {
		t.Fatalf("continuation changed run or session: %q/%q -> %q/%q",
			suspended.ID, suspended.RuntimeSessionID, continuedRun.ID, continuedRun.RuntimeSessionID)
	}
	for _, want := range []string{realReleaseCode, "ALPHA-7f3c9d2e"} {
		if !strings.Contains(continued.Content, want) {
			t.Fatalf("the resumed response is missing %q: %s", want, continued.Content)
		}
	}
	if treeDigest(t, r.wsA.CanonicalRoot) != before {
		t.Fatal("the continuation modified the bound workspace tree")
	}
	assertConfinement(t, r, ev, r.wsA)
	assertCapabilityReleased(t, r, continuedRun)
}

// TestRealOpenCodeDecisionContinuation is the same lifecycle for NEEDS_DECISION on
// the production route: the model must not choose an unauthorized option, the
// parent's selection reaches it through the durable continuation, and the run
// completes with the choice and a real repository read in the same session.
func TestRealOpenCodeDecisionContinuationE2E(t *testing.T) {
	r := setupRealRoute(t)
	before := treeDigest(t, r.wsA.CanonicalRoot)
	ev := acceptanceEvidence{Scenario: "decision-continuation", StartedAt: time.Now().UTC().Format(time.RFC3339), Role: "implementation"}
	result, run := r.delegate(t, "delegate_implementation", decisionContinuationTask(r.wsA.ID))
	if result.Status != "needs_decision" || run.Status != "NEEDS_DECISION" {
		ev.recordRun(run, result, r.wsA)
		writeEvidence(t, "real-decision-continuation-e2e.jsonl", ev)
		t.Fatalf("the run did not suspend for a parent decision: %+v (durable %q)", result, run.Status)
	}
	suspended := run
	if len(result.DecisionRequests) == 0 {
		t.Fatal("the suspension carried no decision requests")
	}
	c := agent.Continuation{WorkerRunID: result.WorkerRunID}
	chosen := ""
	for _, d := range result.DecisionRequests {
		if len(d.Options) == 0 {
			t.Fatalf("decision %s offers no selection to answer with", d.ID)
		}
		chosen = optionLabel(d.Options[0].Description)
		c.DecisionResponses = append(c.DecisionResponses, agent.DecisionResponse{
			RequestID: d.ID, Selection: d.Options[0].ID, Explanation: "The parent takes the first offered option.",
		})
	}
	continued, continuedRun := r.call(t, "ai_worker_continue", c)
	ev.recordRun(continuedRun, continued, r.wsA)
	ev.Timeline = map[string]string{
		"T1_suspended_status": string(result.Status),
		"T1_pending_requests": fmt.Sprintf("%d", len(result.DecisionRequests)),
		"T2_run_status":       run.Status,
		"T3_final_status":     string(continued.Status),
		"T3_same_run":         fmt.Sprintf("%t", continuedRun.ID == suspended.ID),
		"T3_same_session":     fmt.Sprintf("%t", continuedRun.RuntimeSessionID == suspended.RuntimeSessionID),
		"T3_final_run_status": continuedRun.Status,
	}
	ev.finish(t, r, "real-decision-continuation-e2e.jsonl")
	if continued.Status != "completed" {
		t.Fatalf("the run did not complete after the parent decided: %+v", continued)
	}
	if continuedRun.ID != suspended.ID || continuedRun.RuntimeSessionID != suspended.RuntimeSessionID {
		t.Fatalf("decision continuation changed run or session: %q/%q -> %q/%q",
			suspended.ID, suspended.RuntimeSessionID, continuedRun.ID, continuedRun.RuntimeSessionID)
	}
	if !piChoiceEchoed(chosen, continued.Content) {
		t.Fatalf("the final response did not carry the parent's choice %q: %s", chosen, continued.Content)
	}
	if !strings.Contains(continued.Content, "ALPHA-7f3c9d2e") {
		t.Fatalf("the resumed response carries no result of a real read: %s", continued.Content)
	}
	if treeDigest(t, r.wsA.CanonicalRoot) != before {
		t.Fatal("the decision continuation modified the bound workspace tree")
	}
	assertConfinement(t, r, ev, r.wsA)
	assertCapabilityReleased(t, r, continuedRun)
}

// contextContinuationTask forces one capability the workspace cannot supply.
func contextContinuationTask(workspaceID string) agent.DelegateTask {
	return agent.DelegateTask{Prompt: "Report two values: the release code for this run, and the exact unique token in src/alpha.txt. " +
		"The release code exists only with your parent - it is not in the workspace and not in this prompt - so request it with the context request tool instead of guessing it. " +
		"After the answer arrives, find src/alpha.txt with the repository glob tool, read it with the repository read tool, and report both values." + "\n\nConstraints:\n" +

		"Never invent the release code." + "\n" +
		"Use only the repository read tools for file access, and read only the one file you need." + "\n" +
		"Read-only: no edits and no commands." + "\n\nAcceptanceCriteria:\n" +

		"The run suspends for the missing release code." + "\n" + "Both values are reported exactly.", WorkspaceID: workspaceID,
	}
}

// decisionContinuationTask withholds the one choice the role is not allowed to make.
func decisionContinuationTask(workspaceID string) agent.DelegateTask {
	return agent.DelegateTask{Prompt: "A new reporting helper must either change the public lookup return type or preserve it and add an internal adapter. " +
		"Neither option is authorized, and this shape decision belongs to your parent, so request it immediately with the decision request tool, offering exactly those two options with a one-line description each. " +
		"After the parent answers, find src/alpha.txt with the repository glob tool, read it with the repository read tool, and report the chosen option together with the token." + "\n\nConstraints:\n" +

		"Do not choose the API shape yourself." + "\n" +
		"This is a decision, not missing context." + "\n" +
		"Read-only: no edits and no commands." + "\n\nAcceptanceCriteria:\n" +

		"The run suspends on a decision request with two options." + "\n" + "The parent's choice and a real read result are both reported.", WorkspaceID: workspaceID,
	}
}

// waitForLiveRepoAccess watches the durable registry and the runtime's own
// session record until a real model has one repository access served, then
// revokes the workspace grant through the registry API and reports the
// ordering. It runs while the delegated call is still blocked, so the
// revocation lands during execution rather than between runs.
func waitForLiveRepoAccess(t *testing.T, r *realRoute, workspaceID string) (revokeObservation, error) {
	t.Helper()
	st, e := store.Open(filepath.Join(r.dir, "llmtools.db"))
	if e != nil {
		return revokeObservation{}, e
	}
	defer st.Close()
	deadline := time.Now().Add(realE2EModelTimeout)
	var obs revokeObservation
	for obs.SessionID == "" {
		if time.Now().After(deadline) {
			return obs, fmt.Errorf("no run bound to %s started", workspaceID)
		}
		runs, e := st.Running()
		if e != nil {
			return obs, e
		}
		for _, run := range runs {
			if run.Workspace.WorkspaceID == workspaceID {
				obs.SessionID, obs.RunID = run.RuntimeSessionID, run.ID
				break
			}
		}
		if obs.SessionID == "" {
			time.Sleep(25 * time.Millisecond)
		}
	}
	if instance, e := st.RuntimeInstance(); e == nil {
		obs.BaseURL = instance.BaseURL
	}
	for obs.FirstAccess.Tool == "" {
		if time.Now().After(deadline) {
			return obs, fmt.Errorf("run %s never reached a repository access", obs.RunID)
		}
		for _, c := range repoCallsFromSession(runtimeSession(t, obs.BaseURL, obs.SessionID)) {
			if c.Kind == "allowed" {
				obs.FirstAccess = c
				break
			}
		}
		if obs.FirstAccess.Tool == "" {
			time.Sleep(25 * time.Millisecond)
		}
	}
	obs.InvokedAt = time.Now().UTC()
	if e := st.RevokeWorkspace(workspaceID); e != nil {
		return obs, e
	}
	// The read-back on a separate transaction is the durable commit proof: the
	// revocation is visible to another connection before any later access is
	// evaluated.
	w, e := st.Workspace(workspaceID)
	if e != nil {
		return obs, e
	}
	obs.CommittedAt = time.Now().UTC()
	obs.State, obs.Revision = w.State, w.Revision
	return obs, nil
}

// revokeObservation is the measured revocation timeline.
type revokeObservation struct {
	RunID       string
	SessionID   string
	BaseURL     string
	FirstAccess repoCall
	InvokedAt   time.Time
	CommittedAt time.Time
	State       string
	Revision    int64
}

func liveRevokeTask(workspaceID string) agent.DelegateTask {
	return agent.DelegateTask{Prompt: "Inspect how this workspace confines a repository read to the directory it was granted. " +
		"In a single step, issue the repository glob that finds the candidate files, the grep that locates the containment helper, and the reads of the two files that answer the question. " +
		"Then report which check stops a path that resolves outside the granted root, naming the files and identifiers you actually saw." + "\n\nConstraints:\n" +

		"Read-only: no edits, no commands." + "\n" +
		"Make each repository operation a separate tool call." + "\n" +
		"Only the bound workspace is in scope." + "\n\nDecisions:\n" +

		"The bound workspace is the only source of truth." + "\n\nAcceptanceCriteria:\n" +
		"The answer names the containment check and the file it lives in.", WorkspaceID: workspaceID,
	}
}

// TestRealModelLiveRevocation revokes the durable grant while a real model is
// still exploring, and records the ordering rather than only the end state.
func TestRealModelLiveRevocation(t *testing.T) {
	r := setupRealRoute(t)
	src := r.srcRoot()
	before := treeDigest(t, src)
	ev := acceptanceEvidence{
		Scenario:  "live_revoke",
		StartedAt: time.Now().UTC().Format(time.RFC3339),
		Role:      "implementation",
	}
	type outcome struct {
		obs revokeObservation
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		obs, e := waitForLiveRepoAccess(t, r, r.wsSrc.ID)
		done <- outcome{obs, e}
	}()
	result, run := r.delegate(t, "delegate_implementation", liveRevokeTask(r.wsSrc.ID))
	o := <-done
	if o.err != nil {
		t.Fatalf("live revocation was not exercised: %v", o.err)
	}
	if o.obs.State != "REVOKED" {
		t.Fatalf("revocation did not persist: %+v", o.obs)
	}
	ev.recordRun(run, result, r.wsSrc)
	ev.SessionParts = runtimeSession(t, o.obs.BaseURL, o.obs.SessionID)
	ev.RepoCalls = repoCallsFromSession(ev.SessionParts)
	ev.RepoSource = "runtime_session_record"
	assertNoNativeExecution(t, ev.SessionParts)

	commitMs := o.obs.CommittedAt.UnixMilli()
	var denied []repoCall
	authorizedAfter, inFlightReturned := 0, 0
	for _, c := range ev.RepoCalls {
		if c.AtMs >= commitMs {
			if c.Kind == "allowed" {
				authorizedAfter++
			} else {
				denied = append(denied, c)
			}
			continue
		}
		// The grant is revalidated when an access starts, so an access that
		// started before the commit must also have finished before it: any
		// payload landing afterwards would reach the model past its revocation.
		if c.Kind == "allowed" && c.EndMs > commitMs {
			inFlightReturned++
		}
	}
	ev.Timeline = map[string]string{
		"T1_first_repo_access_allowed":     o.obs.FirstAccess.At,
		"T1_tool":                          o.obs.FirstAccess.Tool,
		"T1_target":                        o.obs.FirstAccess.Target,
		"T2_revoke_invoked":                o.obs.InvokedAt.Format(time.RFC3339Nano),
		"T3_revoke_durable_commit":         o.obs.CommittedAt.Format(time.RFC3339Nano),
		"T3_committed_epoch_ms":            fmt.Sprintf("%d", commitMs),
		"T3_workspace_state":               o.obs.State,
		"T3_grant_revision":                fmt.Sprintf("%d", o.obs.Revision),
		"accesses_authorized_after_commit": fmt.Sprintf("%d", authorizedAfter),
		"payloads_returned_after_commit":   fmt.Sprintf("%d", inFlightReturned),
		"T6_run_status":                    run.Status,
		"T6_result_error":                  ev.ErrorCode,
	}
	if len(denied) > 0 {
		ev.Timeline["T4_T5_next_operation_denied"] = denied[0].At
		ev.Timeline["T5_error_code"] = denied[0].Code
		layer := "runtime_abort"
		for _, c := range denied {
			if c.Code == "WORKSPACE_AUTHORIZATION_REVOKED" {
				layer = "control_server"
			}
		}
		ev.Timeline["T5_refusal_layer"] = layer
	}
	ev.ContentChars = len(result.Content)
	writeEvidence(t, "real-live-revoke-e2e.jsonl", ev)
	t.Logf("live revoke timeline: %v", ev.Timeline)

	if authorizedAfter > 0 {
		t.Fatalf("%d repository accesses were authorized after the revocation commit", authorizedAfter)
	}
	if inFlightReturned > 0 {
		t.Fatalf("%d repository payloads returned to the model after the revocation commit", inFlightReturned)
	}
	if len(denied) == 0 {
		t.Fatalf("no repository operation was attempted and refused after the revocation: %+v", ev.Timeline)
	}
	// Whichever layer refused, the requirement is the same: the operation that
	// followed the revocation produced no repository access. Which layer did it
	// is recorded rather than assumed, because the live-revoke poller aborts the
	// session about twenty milliseconds after the commit and can therefore
	// refuse the next call before the control server is ever consulted. The
	// server-side per-access denial for the same commit is proven by
	// TestControlRepositoryToolsInRealProcess and the service-level tests.
	for _, c := range denied {
		if c.Code != "WORKSPACE_AUTHORIZATION_REVOKED" && c.Code != "TOOL_EXECUTION_ABORTED" {
			t.Fatalf("post-revoke refusal carried an unexpected code %q: %+v", c.Code, c)
		}
	}
	if ev.Timeline["T5_refusal_layer"] == "control_server" {
		t.Log("the control server refused the post-revoke access by itself")
	} else {
		t.Log("the runtime abort refused the post-revoke access before the control server")
	}
	if result.Error == nil || result.Error.Code != "WORKSPACE_AUTHORIZATION_REVOKED" {
		t.Fatalf("revoked run did not fail closed: %+v", result)
	}
	if run.Status != "FAILED" {
		t.Fatalf("revoked run terminal status: %s", run.Status)
	}
	// No rebind: the run still names the revoked workspace, and the only other
	// grants in this registry stayed untouched.
	if run.Workspace.WorkspaceID != r.wsSrc.ID || run.Workspace.CanonicalRoot != r.wsSrc.CanonicalRoot {
		t.Fatalf("run rebound after revocation: %+v", run.Workspace)
	}
	st, e := store.Open(filepath.Join(r.dir, "llmtools.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer st.Close()
	for _, id := range []string{r.wsA.ID, r.wsB.ID} {
		if w, e := st.Workspace(id); e != nil || w.State != "ACTIVE" {
			t.Fatalf("revoking one workspace disturbed another (%s): %+v %v", id, w, e)
		}
	}
	if treeDigest(t, src) != before {
		t.Fatal("the revoked run modified the workspace tree")
	}
	scanStoreFor(t, r.dir, persistenceNeedles(t, src))
	assertCapabilityReleased(t, r, run)
}
