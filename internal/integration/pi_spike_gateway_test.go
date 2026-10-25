package integration_test

import (
	"ai-worker-mcp/internal/agent"
	"ai-worker-mcp/internal/store"
	"bytes"
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

// The Pi runtime bridge reaches repository tools through this one-shot gateway,
// which is the same authoritative implementation the OpenCode control MCP
// server calls. These cases cross a real process boundary and re-read the
// durable store on every call, with no model in the loop, so the enforcement
// evidence does not depend on a provider being reachable.

// piGateway runs one llmTools repository tool in a real child process and
// returns its stdout, stderr and exit code.
func piGateway(t *testing.T, b, dbPath, tool string, args map[string]any) (string, string, int) {
	t.Helper()
	input, e := json.Marshal(args)
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, b, "--llmtools-pi-control", dbPath, tool)
	cmd.Stdin = bytes.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	code := 0
	var exit *exec.ExitError
	if e = cmd.Run(); e != nil && !errors.As(e, &exit) {
		t.Fatalf("%s %s: %v", tool, input, e)
	} else if e != nil {
		code = exit.ExitCode()
	}
	return stdout.String(), stderr.String(), code
}

// seedPiGrant creates a fixture workspace, one live run and its opaque per-run
// token exactly as Core registers them before a turn is dispatched. The session
// identity is a Pi-shaped id, which proves the durable grant validation is
// runtime-neutral rather than tied to one runtime's id prefix.
func seedPiGrant(t *testing.T, dir string) (dbPath, workspaceID, token, root string) {
	t.Helper()
	root = filepath.Join(dir, "workspace")
	if e := os.MkdirAll(filepath.Join(root, "src"), 0o755); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(root, "src", "alpha.txt"), []byte("ALPHA-7f3c9d2e\n"), 0o644); e != nil {
		t.Fatal(e)
	}
	outside := filepath.Join(dir, "outside")
	if e := os.MkdirAll(outside, 0o755); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("OUTSIDE-9e8d7c6b\n"), 0o644); e != nil {
		t.Fatal(e)
	}
	dbPath = filepath.Join(dir, "llmtools.db")
	st, e := store.Open(dbPath)
	if e != nil {
		t.Fatal(e)
	}
	defer st.Close()
	w, _, e := st.AddWorkspace(root, "pi-gateway", "desktop_user_grant")
	if e != nil {
		t.Fatal(e)
	}
	runID := "wrk_" + strings.Repeat("7", 32)
	if e = st.Create(agent.Run{ID: runID, Status: "RUNNING"}); e != nil {
		t.Fatal(e)
	}
	token = "ctl_" + strings.Repeat("7", 32)
	if e = st.RegisterRepoAccess(store.RepoAccess{Token: token, RunID: runID, SessionID: "01934b8f-2f6c-7000-8000-abcdef012345", WorkspaceID: w.ID, CanonicalRoot: w.CanonicalRoot, GrantRevision: w.Revision}); e != nil {
		t.Fatal(e)
	}
	return dbPath, w.ID, token, root
}

func TestPiControlGatewayConfinesEveryTool(t *testing.T) {
	dir := t.TempDir()
	dbPath, workspaceID, token, root := seedPiGrant(t, dir)
	b := binary(t)

	body, stderr, code := piGateway(t, b, dbPath, "repo_read", map[string]any{"control_token": token, "path": "src/alpha.txt"})
	if code != 0 || !strings.Contains(body, "ALPHA-7f3c9d2e") {
		t.Fatalf("confined read: code=%d out=%s err=%s", code, body, stderr)
	}
	if !strings.Contains(body, `"path":"src/alpha.txt"`) {
		t.Fatalf("read result is not root-relative: %s", body)
	}
	if _, _, code = piGateway(t, b, dbPath, "repo_glob", map[string]any{"control_token": token, "pattern": "**/*.txt"}); code != 0 {
		t.Fatalf("confined glob denied")
	}
	if _, _, code = piGateway(t, b, dbPath, "repo_grep", map[string]any{"control_token": token, "pattern": "ALPHA"}); code != 0 {
		t.Fatalf("confined grep denied")
	}
	// An absolute path is accepted only inside the bound workspace.
	if _, _, code = piGateway(t, b, dbPath, "repo_read", map[string]any{"control_token": token, "path": filepath.Join(root, "src", "alpha.txt")}); code != 0 {
		t.Fatalf("absolute in-workspace read denied")
	}

	denials := []struct{ name, tool, expect string }{
		{"traversal", "repo_read", "WORKSPACE_ACCESS_DENIED"},
		{"absolute outside", "repo_read", "WORKSPACE_ACCESS_DENIED"},
		{"unknown token", "repo_read", "WORKSPACE_ACCESS_DENIED"},
		{"escape glob", "repo_glob", "WORKSPACE_ACCESS_DENIED"},
		{"escape grep", "repo_grep", "WORKSPACE_ACCESS_DENIED"},
		{"invalid regex", "repo_grep", "REPO_INPUT_INVALID"},
	}
	args := []map[string]any{
		{"control_token": token, "path": "../outside/secret.txt"},
		{"control_token": token, "path": filepath.Join(dir, "outside", "secret.txt")},
		{"control_token": "ctl_" + strings.Repeat("f", 32), "path": "src/alpha.txt"},
		{"control_token": token, "pattern": "../outside/**"},
		{"control_token": token, "pattern": "OUTSIDE", "path": "../outside"},
		{"control_token": token, "pattern": "([("},
	}
	for i, d := range denials {
		out, errOut, code := piGateway(t, b, dbPath, d.tool, args[i])
		if code == 0 {
			t.Fatalf("%s was allowed: %s", d.name, out)
		}
		if !strings.Contains(errOut, d.expect) {
			t.Fatalf("%s: want %s in %q", d.name, d.expect, errOut)
		}
		if strings.Contains(out, "OUTSIDE-9e8d7c6b") || strings.Contains(out, "ALPHA-7f3c9d2e") {
			t.Fatalf("%s leaked file content: %s", d.name, out)
		}
	}
	// The gateway recognizes write tools, but a read-only run cannot use them.
	if _, errOut, code := piGateway(t, b, dbPath, "repo_write", map[string]any{"control_token": token, "path": "src/alpha.txt"}); code == 0 || !strings.Contains(errOut, "EXECUTION_NOT_ALLOWED") {
		t.Fatalf("a read-only run reached project write: %s", errOut)
	}
	// A malformed or oversized-by-shape argument cannot reach the filesystem.
	if _, _, code = piGateway(t, b, dbPath, "repo_read", map[string]any{"control_token": token}); code == 0 {
		t.Fatal("read without a path succeeded")
	}

	// Live revocation reaches a gateway process that started before it.
	st, e := store.Open(dbPath)
	if e != nil {
		t.Fatal(e)
	}
	if e = st.RevokeWorkspace(workspaceID); e != nil {
		t.Fatal(e)
	}
	st.Close()
	if _, errOut, code := piGateway(t, b, dbPath, "repo_read", map[string]any{"control_token": token, "path": "src/alpha.txt"}); code == 0 || !strings.Contains(errOut, "WORKSPACE_AUTHORIZATION_REVOKED") {
		t.Fatalf("revoked grant still readable: %s", errOut)
	}
}
