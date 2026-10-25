package store

import (
	"ai-worker-mcp/internal/agent"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func openRepoAccess(t *testing.T) (*Store, RepoAccess) {
	t.Helper()
	st, _ := openWorkspaces(t)
	root := t.TempDir()
	w, _, e := st.AddWorkspace(root, "repo", "desktop_user_grant")
	if e != nil {
		t.Fatal(e)
	}
	if e = st.Create(agent.Run{ID: "wrk_" + strings.Repeat("a", 32), Status: "RUNNING"}); e != nil {
		t.Fatal(e)
	}
	a := RepoAccess{Token: "ctl_" + strings.Repeat("b", 32), RunID: "wrk_" + strings.Repeat("a", 32), SessionID: "ses_1", WorkspaceID: w.ID, CanonicalRoot: root, GrantRevision: 1}
	if e = st.RegisterRepoAccess(a); e != nil {
		t.Fatal(e)
	}
	return st, a
}

func TestRepoAccessGrantsLiveRunOnly(t *testing.T) {
	st, a := openRepoAccess(t)
	got, root, e := st.LiveRepoRoot(a.Token)
	if e != nil || root != a.CanonicalRoot || got.RunID != a.RunID || got.WorkspaceID != a.WorkspaceID {
		t.Fatalf("live grant: %+v %v %v", got, root, e)
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("granted root unusable: %v", err)
	}
	// Only a run that is actively executing may read.
	if e = st.Save(agent.Run{ID: a.RunID, Status: "NEEDS_CONTEXT"}, "RUNNING"); e != nil {
		t.Fatal(e)
	}
	if _, _, e = st.LiveRepoRoot(a.Token); agent.Safe(e).Code != "WORKSPACE_ACCESS_DENIED" {
		t.Fatalf("suspended run kept read access: %v", e)
	}
	if e = st.Save(agent.Run{ID: a.RunID, Status: "RUNNING"}, "NEEDS_CONTEXT"); e != nil {
		t.Fatal(e)
	}
	if _, _, e = st.LiveRepoRoot(a.Token); e != nil {
		t.Fatalf("resumed run denied: %v", e)
	}
	// A cancellation request from any source denies immediately, before the
	// owning process has even observed it.
	if e = st.RequestCancel(a.RunID); e != nil {
		t.Fatal(e)
	}
	if _, _, e = st.LiveRepoRoot(a.Token); agent.Safe(e).Code != "WORKSPACE_ACCESS_DENIED" {
		t.Fatalf("cancelled run kept read access: %v", e)
	}
}

func TestRepoAccessRejectsUnknownAndMalformedTokens(t *testing.T) {
	st, a := openRepoAccess(t)
	for _, token := range []string{"", "ctl_", a.Token + "x", "wrk_" + strings.Repeat("b", 32), "ctl_" + strings.Repeat("c", 32)} {
		if _, _, e := st.LiveRepoRoot(token); agent.Safe(e).Code != "WORKSPACE_ACCESS_DENIED" {
			t.Fatalf("token %q accepted: %v", token, e)
		}
	}
	if _, e := st.RepoAccessByRun("wrk_" + strings.Repeat("d", 32)); agent.Safe(e).Code != "WORKSPACE_ACCESS_DENIED" {
		t.Fatalf("unknown run: %v", e)
	}
	bad := a
	bad.Token = "tok_" + strings.Repeat("b", 32)
	if e := st.RegisterRepoAccess(bad); agent.Safe(e).Code != "INTERNAL_ERROR" {
		t.Fatalf("unprefixed token registered: %v", e)
	}
	bad = a
	bad.GrantRevision = 0
	if e := st.RegisterRepoAccess(bad); agent.Safe(e).Code != "INTERNAL_ERROR" {
		t.Fatalf("zero revision registered: %v", e)
	}
}

// Revocation must cross the process boundary: the granting store marks the
// bound live run and denies the next read, and the abort reason is durable so
// the executing process can report the specific failure.
func TestRepoAccessRevocationDeniesAndAbortsBoundRun(t *testing.T) {
	st, a := openRepoAccess(t)
	if _, _, e := st.LiveRepoRoot(a.Token); e != nil {
		t.Fatal(e)
	}
	if e := st.RevokeWorkspace(a.WorkspaceID); e != nil {
		t.Fatal(e)
	}
	if _, _, e := st.LiveRepoRoot(a.Token); agent.Safe(e).Code != "WORKSPACE_AUTHORIZATION_REVOKED" {
		t.Fatalf("revoked grant still readable: %v", e)
	}
	if !st.CancelRequested(a.RunID) {
		t.Fatal("bound live run not marked for cancellation")
	}
	if got := st.AbortReason(a.RunID); got != "WORKSPACE_AUTHORIZATION_REVOKED" {
		t.Fatalf("abort reason: %q", got)
	}
	// A run that was only suspended carries no abort request; it is refused by
	// the binding check when an explicit continuation arrives.
	if got := st.AbortReason("wrk_" + strings.Repeat("e", 32)); got != "" {
		t.Fatalf("unknown run reported a reason: %q", got)
	}
}

func TestRepoAccessRevalidationCoversRevisionRootAndDirectory(t *testing.T) {
	st, a := openRepoAccess(t)
	// The same canonical root re-authorized after revocation is a new identity
	// at revision 1; the frozen grant must not silently follow it.
	if e := st.RevokeWorkspace(a.WorkspaceID); e != nil {
		t.Fatal(e)
	}
	if _, _, e := st.LiveRepoRoot(a.Token); agent.Safe(e).Code != "WORKSPACE_AUTHORIZATION_REVOKED" {
		t.Fatalf("stale revision accepted: %v", e)
	}
	// A directory that disappears fails closed rather than reading nothing.
	st2, a2 := openRepoAccess(t)
	if e := os.RemoveAll(a2.CanonicalRoot); e != nil {
		t.Fatal(e)
	}
	if _, _, e := st2.LiveRepoRoot(a2.Token); agent.Safe(e).Code != "WORKSPACE_UNAVAILABLE" {
		t.Fatalf("missing directory: %v", e)
	}
}

func TestRepoAccessRegistrationIsReplacedAndReleased(t *testing.T) {
	st, a := openRepoAccess(t)
	restarted := a
	restarted.Token = "ctl_" + strings.Repeat("f", 32)
	restarted.SessionID = "ses_2"
	restarted.GrantRevision = 1
	if e := st.RegisterRepoAccess(restarted); e != nil {
		t.Fatal(e)
	}
	bound, e := st.RepoAccessByRun(a.RunID)
	if e != nil || bound.Token != restarted.Token || bound.SessionID != restarted.SessionID {
		t.Fatalf("re-registration: %+v %v", bound, e)
	}
	if _, _, e := st.LiveRepoRoot(a.Token); agent.Safe(e).Code != "WORKSPACE_ACCESS_DENIED" {
		t.Fatalf("superseded token still live: %v", e)
	}
	st.DeleteRepoAccess(a.RunID)
	if _, _, e := st.LiveRepoRoot(restarted.Token); agent.Safe(e).Code != "WORKSPACE_ACCESS_DENIED" {
		t.Fatal("deleted grant still live")
	}
}

// A grant exists only for its run: reopening the store after the run is
// terminal drops the record, so a crashed or externally aborted run cannot
// leave a usable capability behind.
func TestRepoAccessSweptOnOpen(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sweep.db")
	st, e := Open(p)
	if e != nil {
		t.Fatal(e)
	}
	root := t.TempDir()
	w, _, e := st.AddWorkspace(root, "repo", "desktop_user_grant")
	if e != nil {
		t.Fatal(e)
	}
	runID := "wrk_" + strings.Repeat("1", 32)
	if e = st.Create(agent.Run{ID: runID, Status: "RUNNING"}); e != nil {
		t.Fatal(e)
	}
	a := RepoAccess{Token: "ctl_" + strings.Repeat("2", 32), RunID: runID, SessionID: "ses_1", WorkspaceID: w.ID, CanonicalRoot: root, GrantRevision: 1}
	if e = st.RegisterRepoAccess(a); e != nil {
		t.Fatal(e)
	}
	if e = st.Save(agent.Run{ID: runID, Status: "COMPLETED"}, "RUNNING"); e != nil {
		t.Fatal(e)
	}
	st.Close()
	reopened, e := Open(p)
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	if _, e := reopened.RepoAccessByRun(runID); agent.Safe(e).Code != "WORKSPACE_ACCESS_DENIED" {
		t.Fatalf("terminal grant survived reopen: %v", e)
	}
}

// A suspended run is not finished: its grant has to survive an MCP restart so
// an explicit continuation can resume reading the same workspace.
func TestRepoAccessSuspensionSurvivesReopen(t *testing.T) {
	p := filepath.Join(t.TempDir(), "suspend.db")
	st, e := Open(p)
	if e != nil {
		t.Fatal(e)
	}
	root := t.TempDir()
	w, _, e := st.AddWorkspace(root, "repo", "desktop_user_grant")
	if e != nil {
		t.Fatal(e)
	}
	runID := "wrk_" + strings.Repeat("3", 32)
	if e = st.Create(agent.Run{ID: runID, Status: "RUNNING"}); e != nil {
		t.Fatal(e)
	}
	token := "ctl_" + strings.Repeat("4", 32)
	if e = st.RegisterRepoAccess(RepoAccess{Token: token, RunID: runID, SessionID: "ses_1", WorkspaceID: w.ID, CanonicalRoot: root, GrantRevision: 1}); e != nil {
		t.Fatal(e)
	}
	if e = st.Save(agent.Run{ID: runID, Status: "NEEDS_CONTEXT"}, "RUNNING"); e != nil {
		t.Fatal(e)
	}
	st.Close()
	reopened, e := Open(p)
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	if _, _, e = reopened.LiveRepoRoot(token); agent.Safe(e).Code != "WORKSPACE_ACCESS_DENIED" {
		t.Fatalf("suspended run readable without execution: %v", e)
	}
	if _, e := reopened.RepoAccessByRun(runID); e != nil {
		t.Fatalf("suspended grant dropped: %v", e)
	}
	if e = reopened.Save(agent.Run{ID: runID, Status: "RUNNING"}, "NEEDS_CONTEXT"); e != nil {
		t.Fatal(e)
	}
	if got, resolved, e := reopened.LiveRepoRoot(token); e != nil || resolved != root || got.WorkspaceID != w.ID {
		t.Fatalf("continuation lost its grant: %+v %v %v", got, resolved, e)
	}
}
