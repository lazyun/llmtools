package store

import (
	"path/filepath"
	"strings"
	"testing"
)

func openWorkspaces(t *testing.T) (*Store, string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "workspaces.db")
	st, e := Open(p)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { st.Close() })
	return st, p
}

func TestWorkspaceRegistryLifecycle(t *testing.T) {
	st, _ := openWorkspaces(t)
	root := t.TempDir()
	w, created, e := st.AddWorkspace(root, "repo", "desktop_user_grant")
	if e != nil || !created || !strings.HasPrefix(w.ID, "ws_") || len(w.ID) != 35 || w.State != "ACTIVE" || w.Revision != 1 || w.CanonicalRoot != root {
		t.Fatalf("create: %+v created=%v err=%v", w, created, e)
	}
	again, createdAgain, e := st.AddWorkspace(root, "duplicate", "desktop_user_grant")
	if e != nil || createdAgain || again.ID != w.ID {
		t.Fatalf("duplicate root created a second grant: %+v created=%v err=%v", again, createdAgain, e)
	}
	if active, revoked, e := st.WorkspaceStats(); e != nil || active != 1 || revoked != 0 {
		t.Fatalf("stats after create: %d %d %v", active, revoked, e)
	}
	if e := st.RenameWorkspace(w.ID, "renamed"); e != nil {
		t.Fatal(e)
	}
	renamed, _ := st.Workspace(w.ID)
	if renamed.Label != "renamed" || renamed.State != "ACTIVE" || renamed.Revision != 1 {
		t.Fatalf("rename changed authority: %+v", renamed)
	}
	if e := st.RevokeWorkspace(w.ID); e != nil {
		t.Fatal(e)
	}
	revoked, _ := st.Workspace(w.ID)
	if revoked.State != "REVOKED" || revoked.Revision != 2 {
		t.Fatalf("revoke did not advance revision: %+v", revoked)
	}
	if active, revokedCount, _ := st.WorkspaceStats(); active != 0 || revokedCount != 1 {
		t.Fatalf("stats after revoke: %d %d", active, revokedCount)
	}
	if e := st.RevokeWorkspace(w.ID); e != nil {
		t.Fatalf("revoke not idempotent: %v", e)
	}
	// A revoked root may be authorized again as a new, distinct grant identity.
	fresh, createdFresh, e := st.AddWorkspace(root, "repo", "desktop_user_grant")
	if e != nil || !createdFresh || fresh.ID == w.ID || fresh.Revision != 1 {
		t.Fatalf("re-authorize after revoke: %+v created=%v err=%v", fresh, createdFresh, e)
	}
}

func TestWorkspaceRegistryRejectsUnknownAndInvalid(t *testing.T) {
	st, _ := openWorkspaces(t)
	unknown := "ws_" + strings.Repeat("0", 32)
	if _, e := st.Workspace(unknown); e == nil {
		t.Fatal("unknown workspace accepted")
	}
	if e := st.RenameWorkspace(unknown, "x"); e == nil {
		t.Fatal("unknown rename accepted")
	}
	if e := st.RevokeWorkspace(unknown); e == nil {
		t.Fatal("unknown revoke accepted")
	}
	if _, _, e := st.AddWorkspace("relative/path", "x", "desktop_user_grant"); e == nil {
		t.Fatal("relative root accepted")
	}
	if _, _, e := st.AddWorkspace(t.TempDir(), "", "desktop_user_grant"); e == nil {
		t.Fatal("empty label accepted")
	}
	if _, _, e := st.AddWorkspace(t.TempDir(), "x", "mcp_root"); e == nil {
		t.Fatal("untrusted source accepted")
	}
}

func TestWorkspaceRegistryCrossStorePersistence(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(t.TempDir(), "persist.db")
	a, e := Open(p)
	if e != nil {
		t.Fatal(e)
	}
	w, _, e := a.AddWorkspace(root, "persisted", "desktop_user_grant")
	if e != nil {
		t.Fatal(e)
	}
	a.Close()
	// Simulates a second process opening the same durable store.
	b, e := Open(p)
	if e != nil {
		t.Fatal(e)
	}
	defer b.Close()
	got, e := b.Workspace(w.ID)
	if e != nil || got.CanonicalRoot != root || got.State != "ACTIVE" || got.Revision != w.Revision {
		t.Fatalf("cross-store identity drift: %+v err=%v", got, e)
	}
	if e := b.RevokeWorkspace(w.ID); e != nil {
		t.Fatal(e)
	}
	c, e := Open(p)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	after, e := c.Workspace(w.ID)
	if e != nil || after.State != "REVOKED" || after.Revision != w.Revision+1 {
		t.Fatalf("revocation not visible cross-store: %+v err=%v", after, e)
	}
}
