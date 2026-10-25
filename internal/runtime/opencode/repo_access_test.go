package opencode

import (
	"ai-worker-mcp/internal/agent"
	"ai-worker-mcp/internal/repoaccess"
	"ai-worker-mcp/internal/repocontrol"
	"ai-worker-mcp/internal/store"
	"ai-worker-mcp/internal/testutil"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// The execution surface is deny-all by construction. Only the llmTools control
// tools are re-allowed, and the repository tools appear for a read role alone.
func TestPermissionRulesetIsDenyAllPlusAllowlistedTools(t *testing.T) {
	denyAll := permissions(false)
	if denyAll[0].Permission != "*" || denyAll[0].Pattern != "*" || denyAll[0].Action != "deny" {
		t.Fatalf("first rule must deny everything: %+v", denyAll[0])
	}
	for _, r := range denyAll[1:] {
		if r.Action != "allow" || r.Permission == "*" {
			t.Fatalf("only specific tool allows may follow the deny-all: %+v", r)
		}
	}
	for _, name := range []string{"question", contextToolName} {
		if !hasPermission(denyAll, name) {
			t.Fatalf("control tool %s not allowed", name)
		}
	}
	if hasPermission(denyAll, repoReadToolName) || hasPermission(denyAll, repoGlobToolName) || hasPermission(denyAll, repoGrepToolName) {
		t.Fatal("context-only session received repository tools")
	}
	read := permissions(true)
	if !reflect.DeepEqual(read[:len(denyAll)], denyAll) {
		t.Fatalf("read ruleset changed the deny-all prefix: %+v vs %+v", read, denyAll)
	}
	for _, name := range []string{repoReadToolName, repoGlobToolName, repoGrepToolName} {
		if !hasPermission(read, name) {
			t.Fatalf("read session missing %s", name)
		}
	}
	if len(read) != len(denyAll)+3 {
		t.Fatalf("read ruleset widened beyond the three repository tools: %+v", read)
	}
	// Nothing that implies mutation or shell access may appear anywhere.
	for _, r := range read {
		for _, forbidden := range []string{"write", "edit", "patch", "bash", "shell", "webfetch", "websearch", "task", "create", "delete"} {
			if strings.Contains(strings.ToLower(r.Permission), forbidden) {
				t.Fatalf("forbidden capability present: %+v", r)
			}
		}
	}
}

func hasPermission(rules []permission, name string) bool {
	for _, r := range rules {
		if r.Permission == name && r.Action == "allow" {
			return true
		}
	}
	return false
}

// A session whose persisted ruleset differs from the intended one in any way
// (missing rule, extra rule, reordered, widened) is rejected.
func TestValidSessionRejectsRulesetDrift(t *testing.T) {
	want := permissions(true)
	if !validSession(session{ID: "ses_1", Permission: want}, true) {
		t.Fatal("exact ruleset rejected")
	}
	if validSession(session{ID: "ses_1", Permission: want}, false) {
		t.Fatal("read ruleset accepted for a context-only run")
	}
	if validSession(session{ID: "ses_1", Permission: permissions(true)[:1]}, true) {
		t.Fatal("allow rules removed")
	}
	if validSession(session{ID: "ses_1", Permission: permissions(false)}, true) {
		t.Fatal("read run accepted on a context-only ruleset")
	}
	reordered := []permission{want[1], want[0], want[2], want[3], want[4], want[5]}
	if validSession(session{ID: "ses_1", Permission: reordered}, true) {
		t.Fatal("reordered ruleset accepted")
	}
	widened := append(append([]permission{}, want...), permission{"*", "*", "allow"})
	if validSession(session{ID: "ses_1", Permission: widened}, true) {
		t.Fatal("widened ruleset accepted")
	}
	if validSession(session{ID: "other", Permission: want}, true) || validSession(session{ID: "ses_bad/../x", Permission: want}, true) {
		t.Fatal("session identity not validated")
	}
}

func TestScopedClientChangesOnlyTheDirectory(t *testing.T) {
	m := NewManager(legacyFixtureConfig())
	base := &Client{BaseURL: "http://127.0.0.1:1", Directory: filepath.Join(t.TempDir(), "scratch"), ControlDB: "/fixed/llmtools.db", InstanceID: "inst_1", Generation: 4}
	scoped := m.Scoped(base, "/tmp/workspace-root")
	if scoped == base || scoped.Directory != "/tmp/workspace-root" {
		t.Fatalf("scope not applied: %+v", scoped)
	}
	if scoped.BaseURL != base.BaseURL || scoped.ControlDB != base.ControlDB || scoped.InstanceID != base.InstanceID || scoped.Generation != base.Generation {
		t.Fatalf("scoping changed ownership or control state: %+v", scoped)
	}
	if base.Directory != filepath.Join(filepath.Dir(base.Directory), "scratch") {
		t.Fatal("scoping mutated the shared client")
	}
	if m.Scoped(base, base.Directory) != base {
		t.Fatal("identical scope should reuse the client")
	}
	if m.Scoped(base, "") != base || m.Scoped(nil, "/x") != nil {
		t.Fatal("degenerate scoping must be a no-op")
	}
}

func TestStartTaskRequiresItsOwnWorkspaceDirectory(t *testing.T) {
	dir := t.TempDir()
	mock := testutil.New("", agent.AgentResult{Status: "completed", Content: "done"})
	defer mock.Close()
	c := New(mock.Server.URL, filepath.Join(dir, "scratch"))
	c.ControlDB = filepath.Join(dir, "llmtools.db")
	workspace := filepath.Join(dir, "repo")
	if e := os.MkdirAll(workspace, 0o755); e != nil {
		t.Fatal(e)
	}
	task := agent.AgentTask{Agent: "implementation", Objective: "read", Capabilities: agent.CapabilityPolicy{RepoRead: true}}
	if _, e := c.StartTask(context.Background(), task, "fixture", ""); agent.Safe(e).Code != "WORKSPACE_ACCESS_DENIED" {
		t.Fatalf("unbound read run started: %v", e)
	}
	elsewhere := task
	elsewhere.Workspace = &agent.WorkspaceSnapshot{WorkspaceID: "ws_" + strings.Repeat("0", 32), CanonicalRoot: filepath.Join(dir, "other"), GrantRevision: 1}
	if _, e := c.StartTask(context.Background(), elsewhere, "fixture", ""); agent.Safe(e).Code != "WORKSPACE_ACCESS_DENIED" {
		t.Fatalf("read run started outside its workspace: %v", e)
	}
	// The workspace must be the real scope of every request, not merely recorded.
	bound := task
	bound.Workspace = &agent.WorkspaceSnapshot{WorkspaceID: "ws_" + strings.Repeat("0", 32), CanonicalRoot: workspace, GrantRevision: 1}
	if _, e := c.StartTask(context.Background(), bound, "fixture", ""); agent.Safe(e).Code != "WORKSPACE_ACCESS_DENIED" {
		t.Fatalf("read run started with an unscoped client: %v", e)
	}
	scoped := NewManager(legacyFixtureConfig()).Scoped(c, workspace)
	h, e := scoped.StartTask(context.Background(), bound, "fixture", "")
	if e != nil {
		t.Fatalf("bound read run refused: %v", e)
	}
	if !h.RepoRead || h.WorkspaceRoot != workspace {
		t.Fatalf("handle lost its binding: %+v", h)
	}
	sessions, _, _ := mock.Snapshot()
	if len(sessions) != 1 || sessions[0].Directory != workspace || !sameRuleset(sessions[0].Permission, permissions(true, h.ControlToken)) {
		t.Fatalf("session scope or ruleset: %+v", sessions)
	}
	// A context-only run is unchanged and gets no repository tools.
	mock2 := testutil.New("", agent.AgentResult{Status: "completed", Content: "done"})
	defer mock2.Close()
	c2 := New(mock2.Server.URL, filepath.Join(dir, "scratch"))
	c2.ControlDB = filepath.Join(dir, "llmtools.db")
	if _, e := c2.StartTask(context.Background(), agent.AgentTask{Agent: "research", Objective: "x"}, "fixture", ""); e != nil {
		t.Fatal(e)
	}
	s2, _, _ := mock2.Snapshot()
	if len(s2) != 1 || !sameRuleset(s2[0].Permission, permissions(false, s2[0].ControlToken)) {
		t.Fatalf("context-only ruleset widened: %+v", s2[0].Permission)
	}
}

// sameRuleset compares a ruleset as recorded by the mock (decoded JSON) with
// the intended one, so a wire-level rewrite cannot silently widen it.
func sameRuleset(recorded any, want []permission) bool {
	raw, e := json.Marshal(recorded)
	if e != nil {
		return false
	}
	var got []permission
	if json.Unmarshal(raw, &got) != nil {
		return false
	}
	return reflect.DeepEqual(got, want)
}

// Every repository tool call re-derives the live grant from the durable store;
// a frozen snapshot never authorizes a read by itself.
func TestRepoToolAuthorizationFollowsLiveGrant(t *testing.T) {
	st, token, granted := openRepoGrant(t)
	// readCode runs one confined read through the shared implementation and
	// returns its mapped failure code, or "" when the read was allowed.
	readCode := func(tok string) string {
		t.Helper()
		args, e := json.Marshal(repocontrol.ReadInput{ControlToken: tok, Path: "notes.md"})
		if e != nil {
			t.Fatal(e)
		}
		_, e = repocontrol.Call(context.Background(), st, "repo_read", args)
		if e == nil {
			return ""
		}
		return agent.Safe(e).Code
	}
	if code := readCode(token); code != "" {
		t.Fatalf("live grant denied a read: %s", code)
	}
	if e := st.RevokeWorkspace(workspaceID(t, st, granted)); e != nil {
		t.Fatal(e)
	}
	if code := readCode(token); code != "WORKSPACE_AUTHORIZATION_REVOKED" {
		t.Fatalf("revoked grant authorized a read: %s", code)
	}
	if code := readCode("ctl_" + strings.Repeat("c", 32)); code != "WORKSPACE_ACCESS_DENIED" {
		t.Fatalf("unknown token: %s", code)
	}
}

// openRepoGrant wires a live RUNNING run, an ACTIVE workspace and its token.
func openRepoGrant(t *testing.T) (*store.Store, string, string) {
	t.Helper()
	dir := t.TempDir()
	st, e := store.Open(filepath.Join(dir, "llmtools.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = st.Close() })
	root := t.TempDir()
	if e = os.WriteFile(filepath.Join(root, "notes.md"), []byte("ALPHA-7f3c9d2e\n"), 0o644); e != nil {
		t.Fatal(e)
	}
	w, _, e := st.AddWorkspace(root, "repo", "desktop_user_grant")
	if e != nil {
		t.Fatal(e)
	}
	if e = st.Create(agent.Run{ID: "wrk_" + strings.Repeat("a", 32), Status: "RUNNING"}); e != nil {
		t.Fatal(e)
	}
	token := "ctl_" + strings.Repeat("b", 32)
	if e = st.RegisterRepoAccess(store.RepoAccess{Token: token, RunID: "wrk_" + strings.Repeat("a", 32), SessionID: "ses_1", WorkspaceID: w.ID, CanonicalRoot: w.CanonicalRoot, GrantRevision: w.Revision}); e != nil {
		t.Fatal(e)
	}
	return st, token, w.CanonicalRoot
}

func workspaceID(t *testing.T, st *store.Store, canonicalRoot string) string {
	t.Helper()
	list, e := st.Workspaces()
	if e != nil {
		t.Fatal(e)
	}
	for _, w := range list {
		if w.CanonicalRoot == canonicalRoot {
			return w.ID
		}
	}
	t.Fatalf("no workspace for %s", canonicalRoot)
	return ""
}

func callRepo(t *testing.T, cs *sdk.ClientSession, name string, args map[string]any) (string, error) {
	t.Helper()
	res, e := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: args})
	if e != nil {
		return "", e
	}
	if res.IsError {
		return "", agent.E(strings.TrimSpace(textOf(res)))
	}
	return textOf(res), nil
}

func textOf(res *sdk.CallToolResult) string {
	parts := []string{}
	for _, c := range res.Content {
		if tc, ok := c.(*sdk.TextContent); ok {
			parts = append(parts, tc.Text)
		}
	}
	return strings.Join(parts, "")
}

// The control MCP server really exposes the three repository tools, and their
// schemas carry only the per-run token and a navigation argument — never a
// root, directory or cwd the model could choose itself.
func TestControlServerServesRepositoryTools(t *testing.T) {
	st, token, granted := openRepoGrant(t)
	server := sdk.NewServer(&sdk.Implementation{Name: controlMCPName, Version: "0.3.0"}, nil)
	if e := addRepoTools(server, st); e != nil {
		t.Fatal(e)
	}
	client := sdk.NewClient(&sdk.Implementation{Name: "repo-access-test", Version: "0.3.0"}, nil)
	clientTransport, serverTransport := sdk.NewInMemoryTransports()
	if _, e := server.Connect(context.Background(), serverTransport, nil); e != nil {
		t.Fatal(e)
	}
	cs, e := client.Connect(context.Background(), clientTransport, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer cs.Close()
	list, e := cs.ListTools(context.Background(), nil)
	if e != nil {
		t.Fatal(e)
	}
	got := map[string]*sdk.Tool{}
	for _, tool := range list.Tools {
		got[tool.Name] = tool
	}
	if len(got) != 6 {
		t.Fatalf("repository tool names: %v", got)
	}
	for _, name := range []string{"repo_read", "repo_glob", "repo_grep"} {
		tool := got[name]
		if tool == nil {
			t.Fatalf("%s not registered", name)
		}
		raw, e := json.Marshal(tool.InputSchema)
		if e != nil {
			t.Fatal(e)
		}
		schema := strings.ToLower(string(raw))
		if !strings.Contains(schema, "control_token") {
			t.Fatalf("%s does not require the run token: %s", name, schema)
		}
		for _, forbidden := range []string{"\"root\"", "\"directory\"", "\"cwd\"", "workspace_root"} {
			if strings.Contains(schema, forbidden) {
				t.Fatalf("%s schema exposes %q: %s", name, forbidden, schema)
			}
		}
	}
	// Reads through the MCP boundary observe the same confinement as the store.
	body, e := callRepo(t, cs, "repo_read", map[string]any{"control_token": token, "path": "notes.md"})
	if e != nil {
		t.Fatalf("confined read denied: %v", e)
	}
	var file repoaccess.File
	if json.Unmarshal([]byte(body), &file) != nil || file.Path != "notes.md" || !strings.Contains(file.Content, "ALPHA-7f3c9d2e") {
		t.Fatalf("repo_read result: %s", body)
	}
	// An absolute path is accepted only for the bound workspace itself.
	if _, e = callRepo(t, cs, "repo_read", map[string]any{"control_token": token, "path": filepath.Join(granted, "notes.md")}); e != nil {
		t.Fatalf("absolute in-workspace read denied: %v", e)
	}
	if _, e = callRepo(t, cs, "repo_read", map[string]any{"control_token": token, "path": filepath.Join(filepath.Dir(granted), "outside", "secret.txt")}); e == nil || !strings.Contains(e.Error(), "WORKSPACE_ACCESS_DENIED") {
		t.Fatalf("absolute outside read allowed: %v", e)
	}
	if _, e = callRepo(t, cs, "repo_read", map[string]any{"control_token": token, "path": "../../etc/passwd"}); e == nil || !strings.Contains(e.Error(), "WORKSPACE_ACCESS_DENIED") {
		t.Fatalf("traversal read: %v", e)
	}
	if _, e = callRepo(t, cs, "repo_read", map[string]any{"control_token": "ctl_" + strings.Repeat("9", 32), "path": "notes.md"}); e == nil || !strings.Contains(e.Error(), "WORKSPACE_ACCESS_DENIED") {
		t.Fatalf("unknown token accepted: %v", e)
	}
	if _, e = callRepo(t, cs, "repo_glob", map[string]any{"control_token": token, "pattern": "**/*"}); e != nil {
		t.Fatalf("confined glob denied: %v", e)
	}
	if _, e = callRepo(t, cs, "repo_grep", map[string]any{"control_token": token, "pattern": "ALPHA"}); e != nil {
		t.Fatalf("confined grep denied: %v", e)
	}
	if _, e = callRepo(t, cs, "repo_grep", map[string]any{"control_token": token, "pattern": "([("}); e == nil || !strings.Contains(e.Error(), "REPO_INPUT_INVALID") {
		t.Fatalf("invalid regex: %v", e)
	}
}

func repoToolSessions(t *testing.T, st *store.Store, token string, name string) (*sdk.ClientSession, string) {
	t.Helper()
	server := sdk.NewServer(&sdk.Implementation{Name: controlMCPName, Version: "0.3.0"}, nil)
	if e := addRepoTools(server, st); e != nil {
		t.Fatal(e)
	}
	client := sdk.NewClient(&sdk.Implementation{Name: name, Version: "0.3.0"}, nil)
	clientTransport, serverTransport := sdk.NewInMemoryTransports()
	if _, e := server.Connect(context.Background(), serverTransport, nil); e != nil {
		t.Fatal(e)
	}
	cs, e := client.Connect(context.Background(), clientTransport, nil)
	if e != nil {
		t.Fatal(e)
	}
	return cs, token
}

// The audit is the operator's evidence that a workspace was read and how. It
// records the run, workspace, tool and navigation target, and never the bytes
// that were read, the absolute root, or the control token itself.
func TestRepositoryAuditRecordsAccessWithoutContents(t *testing.T) {
	st, token, granted := openRepoGrant(t)
	cs, _ := repoToolSessions(t, st, token, "repo-audit-test")
	defer cs.Close()

	pipeRead, pipeWrite, e := os.Pipe()
	if e != nil {
		t.Fatal(e)
	}
	original := os.Stderr
	os.Stderr = pipeWrite
	captured := make(chan string, 1)
	go func() {
		body, _ := io.ReadAll(pipeRead)
		captured <- string(body)
	}()
	if _, e = callRepo(t, cs, "repo_read", map[string]any{"control_token": token, "path": "notes.md"}); e != nil {
		t.Fatal(e)
	}
	if _, e = callRepo(t, cs, "repo_glob", map[string]any{"control_token": token, "pattern": "**/*.md"}); e != nil {
		t.Fatal(e)
	}
	if _, e = callRepo(t, cs, "repo_grep", map[string]any{"control_token": token, "pattern": "ALPHA"}); e != nil {
		t.Fatal(e)
	}
	if _, e = callRepo(t, cs, "repo_read", map[string]any{"control_token": token, "path": filepath.Join(filepath.Dir(granted), "secret.txt")}); e == nil {
		t.Fatal("escape allowed")
	}
	if _, e = callRepo(t, cs, "repo_read", map[string]any{"control_token": "ctl_" + strings.Repeat("0", 32), "path": "notes.md"}); e == nil {
		t.Fatal("unknown token allowed")
	}
	_ = pipeWrite.Close()
	os.Stderr = original
	logged := <-captured
	lines := strings.Split(strings.TrimSpace(logged), "\n")
	allowed, denied := 0, 0
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "repo_access_allowed"):
			allowed++
			if !strings.Contains(line, "run_id=wrk_") || !strings.Contains(line, "workspace_id=ws_") {
				t.Fatalf("audit line lost its subject: %q", line)
			}
		case strings.HasPrefix(line, "repo_access_denied"):
			denied++
			if !strings.Contains(line, "code=WORKSPACE_ACCESS_DENIED") {
				t.Fatalf("denial lost its code: %q", line)
			}
		}
	}
	if allowed != 3 || denied != 2 {
		t.Fatalf("audit counts allowed=%d denied=%d:\n%s", allowed, denied, logged)
	}
	if !strings.Contains(logged, `tool=repo_read target="notes.md"`) {
		t.Fatalf("audit lost the read target: %s", logged)
	}
	for _, leak := range []string{"ALPHA-7f3c9d2e", granted, token} {
		if strings.Contains(logged, leak) {
			t.Fatalf("audit leaked %q", leak)
		}
	}
}
