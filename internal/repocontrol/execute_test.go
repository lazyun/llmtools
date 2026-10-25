package repocontrol

import (
	"ai-worker-mcp/internal/agent"
	"ai-worker-mcp/internal/store"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func executionGrant(t *testing.T, role string, policy agent.CapabilityPolicy) (*store.Store, store.RepoAccess) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "runs.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	w, _, err := st.AddWorkspace(t.TempDir(), "fixture", "mcp_client_grant")
	if err != nil {
		t.Fatal(err)
	}
	a := store.RepoAccess{Token: "ctl_" + strings.Repeat("b", 32), RunID: "wrk_" + strings.Repeat("a", 32), SessionID: "ses_fixture", WorkspaceID: w.ID, CanonicalRoot: w.CanonicalRoot, GrantRevision: w.Revision}
	r := agent.Run{ID: a.RunID, Status: "RUNNING", LogicalRole: role, Capabilities: policy, Workspace: agent.WorkspaceSnapshot{WorkspaceID: w.ID, CanonicalRoot: w.CanonicalRoot, GrantRevision: w.Revision}}
	if err := st.Create(r); err != nil {
		t.Fatal(err)
	}
	if err := st.RegisterRepoAccess(a); err != nil {
		t.Fatal(err)
	}
	return st, a
}
func TestExecutionRequiresDeveloperAndLiveGrant(t *testing.T) {
	for _, role := range []string{"implementation", "review", "research", ""} {
		st, a := executionGrant(t, role, agent.DeveloperCapabilities())
		raw, _ := json.Marshal(WriteInput{ControlToken: a.Token, Path: "new", Content: "x"})
		if _, err := Call(context.Background(), st, "repo_write", raw); agent.Safe(err).Code != "EXECUTION_NOT_ALLOWED" {
			t.Fatalf("%s: %v", role, err)
		}
	}
	st, a := executionGrant(t, "developer", agent.DeveloperCapabilities())
	raw, _ := json.Marshal(WriteInput{ControlToken: a.Token, Path: "new", Content: "x"})
	if _, err := Call(context.Background(), st, "repo_write", raw); err != nil {
		t.Fatal(err)
	}
	if err := st.RevokeWorkspace(a.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	if _, err := Call(context.Background(), st, "repo_write", raw); agent.Safe(err).Code != "WORKSPACE_AUTHORIZATION_REVOKED" {
		t.Fatal(err)
	}
}
func TestRevocationStopsRunningCommand(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS sandbox required")
	}
	st, a := executionGrant(t, "developer", agent.DeveloperCapabilities())
	raw, _ := json.Marshal(ExecInput{ControlToken: a.Token, Command: "printf started > started; sleep 10; printf bad > late", TimeoutSeconds: 20})
	done := make(chan error, 1)
	go func() { _, err := Call(context.Background(), st, "repo_exec", raw); done <- err }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(a.CanonicalRoot, "started")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("command never started")
		}
		time.Sleep(20 * time.Millisecond)
	}
	st.RevokeWorkspace(a.WorkspaceID)
	select {
	case err := <-done:
		if agent.Safe(err).Code != "WORKSPACE_AUTHORIZATION_REVOKED" {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("command not stopped")
	}
	if _, err := os.Stat(filepath.Join(a.CanonicalRoot, "late")); err == nil {
		t.Fatal("revoked command wrote")
	}
}

func TestEditWaitsForProjectCommand(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS sandbox required")
	}
	st, a := executionGrant(t, "developer", agent.DeveloperCapabilities())
	os.WriteFile(filepath.Join(a.CanonicalRoot, "file"), []byte("original"), 0600)
	command, _ := json.Marshal(ExecInput{ControlToken: a.Token, Command: "printf start > started; sleep 0.3; printf formatted > file", TimeoutSeconds: 5})
	done := make(chan error, 1)
	go func() { _, err := Call(context.Background(), st, "repo_exec", command); done <- err }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(a.CanonicalRoot, "started")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("command never started")
		}
		time.Sleep(10 * time.Millisecond)
	}
	edit, _ := json.Marshal(EditInput{ControlToken: a.Token, Path: "file", OldText: "formatted", NewText: "edited"})
	if _, err := Call(context.Background(), st, "repo_edit", edit); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(filepath.Join(a.CanonicalRoot, "file"))
	if string(body) != "edited" {
		t.Fatal(string(body))
	}
}
