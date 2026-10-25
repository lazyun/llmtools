package store_test

import (
	"ai-worker-mcp/internal/store"
	"path/filepath"
	"testing"
)

func TestWorkspaceAuthorizationSources(t *testing.T) {
	dir := t.TempDir()
	st, e := store.Open(filepath.Join(dir, "test.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer st.Close()

	root1 := t.TempDir()
	root2 := t.TempDir()

	// Desktop user grant
	w1, created1, e := st.AddWorkspace(root1, "desktop-project", "desktop_user_grant")
	if e != nil {
		t.Fatal(e)
	}
	if !created1 {
		t.Fatal("expected new workspace to be created")
	}
	if w1.Source != "desktop_user_grant" {
		t.Fatalf("expected source desktop_user_grant, got %s", w1.Source)
	}

	// MCP client grant
	w2, created2, e := st.AddWorkspace(root2, "mcp-project", "mcp_client_grant")
	if e != nil {
		t.Fatal(e)
	}
	if !created2 {
		t.Fatal("expected new workspace to be created")
	}
	if w2.Source != "mcp_client_grant" {
		t.Fatalf("expected source mcp_client_grant, got %s", w2.Source)
	}

	// Invalid source should fail
	_, _, e = st.AddWorkspace(t.TempDir(), "bad-source", "invalid_source")
	if e == nil {
		t.Fatal("expected error for invalid source")
	}

	// List all workspaces
	all, e := st.Workspaces()
	if e != nil {
		t.Fatal(e)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 workspaces, got %d", len(all))
	}

	// Active workspaces
	active, e := st.ActiveWorkspaces()
	if e != nil {
		t.Fatal(e)
	}
	if len(active) != 2 {
		t.Fatalf("expected 2 active workspaces, got %d", len(active))
	}

	// Revoke one
	if e := st.RevokeWorkspace(w1.ID); e != nil {
		t.Fatal(e)
	}

	// Check counts
	activeCount, revokedCount, e := st.WorkspaceStats()
	if e != nil {
		t.Fatal(e)
	}
	if activeCount != 1 {
		t.Fatalf("expected 1 active, got %d", activeCount)
	}
	if revokedCount != 1 {
		t.Fatalf("expected 1 revoked, got %d", revokedCount)
	}

	// List all should still show both
	all, e = st.Workspaces()
	if e != nil {
		t.Fatal(e)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 workspaces after revoke, got %d", len(all))
	}

	// Active should only show one
	active, e = st.ActiveWorkspaces()
	if e != nil {
		t.Fatal(e)
	}
	if len(active) != 1 {
		t.Fatalf("expected 1 active workspace after revoke, got %d", len(active))
	}
	if active[0].ID != w2.ID {
		t.Fatalf("expected remaining active workspace to be %s, got %s", w2.ID, active[0].ID)
	}
	if active[0].Source != "mcp_client_grant" {
		t.Fatalf("expected remaining workspace source mcp_client_grant, got %s", active[0].Source)
	}
}

func TestWorkspaceDuplicateGrantReturnsExisting(t *testing.T) {
	dir := t.TempDir()
	st, e := store.Open(filepath.Join(dir, "test.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer st.Close()

	root := t.TempDir()

	// First grant via Desktop
	w1, created1, e := st.AddWorkspace(root, "project", "desktop_user_grant")
	if e != nil {
		t.Fatal(e)
	}
	if !created1 {
		t.Fatal("expected first grant to be created")
	}

	// Second grant for same root via MCP should return existing
	w2, created2, e := st.AddWorkspace(root, "project-mcp", "mcp_client_grant")
	if e != nil {
		t.Fatal(e)
	}
	if created2 {
		t.Fatal("expected duplicate grant to not create new workspace")
	}
	if w2.ID != w1.ID {
		t.Fatalf("expected same workspace ID, got %s vs %s", w1.ID, w2.ID)
	}
	// The source should remain the original
	if w2.Source != "desktop_user_grant" {
		t.Fatalf("expected original source desktop_user_grant, got %s", w2.Source)
	}
}
