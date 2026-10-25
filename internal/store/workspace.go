package store

import (
	"ai-worker-mcp/internal/agent"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"path/filepath"
	"strings"
	"time"
)

// Workspace is a durable user authorization over one canonical directory.
// ID is the public selection handle; the path itself is never the identity.
type Workspace struct {
	ID            string `json:"id"`
	Label         string `json:"label"`
	CanonicalRoot string `json:"canonical_root"`
	State         string `json:"state"`
	Revision      int64  `json:"revision"`
	Source        string `json:"source"`
	CreatedAt     int64  `json:"created_at"`
	UpdatedAt     int64  `json:"updated_at"`
}

const workspaceColumns = "id,canonical_root,label,state,revision,source,created_at,updated_at"

func newWorkspaceID() string {
	b := make([]byte, 16)
	if _, e := rand.Read(b); e != nil {
		panic("secure randomness unavailable")
	}
	return "ws_" + hex.EncodeToString(b)
}

func validWorkspaceID(id string) bool {
	return strings.HasPrefix(id, "ws_") && len(id) == 35
}

func validWorkspaceLabel(label string) bool {
	label = strings.TrimSpace(label)
	if label == "" || len(label) > 120 {
		return false
	}
	for _, r := range label {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func validWorkspaceSource(source string) bool {
	// Desktop user grant is the interactive path; MCP client grant is the
	// programmatic path. Both are first-class authority sources recorded in
	// the durable audit trail so the user can see which method was used.
	return source == "desktop_user_grant" || source == "mcp_client_grant"
}

func scanWorkspace(row interface{ Scan(...any) error }) (Workspace, error) {
	var w Workspace
	e := row.Scan(&w.ID, &w.CanonicalRoot, &w.Label, &w.State, &w.Revision, &w.Source, &w.CreatedAt, &w.UpdatedAt)
	return w, e
}

// AddWorkspace records one ACTIVE grant. Re-adding the same canonical root
// returns the existing ACTIVE grant instead of creating a second authority.
func (s *Store) AddWorkspace(canonicalRoot, label, source string) (Workspace, bool, error) {
	if !filepath.IsAbs(canonicalRoot) || filepath.Clean(canonicalRoot) != canonicalRoot || !validWorkspaceLabel(label) || !validWorkspaceSource(source) {
		return Workspace{}, false, agent.E("WORKSPACE_INVALID")
	}
	label = strings.TrimSpace(label)
	tx, e := s.db.Begin()
	if e != nil {
		return Workspace{}, false, agent.E("INTERNAL_ERROR")
	}
	defer tx.Rollback()
	existing, e := scanWorkspace(tx.QueryRow("SELECT "+workspaceColumns+" FROM workspaces WHERE canonical_root=? AND state='ACTIVE'", canonicalRoot))
	if e == nil {
		if tx.Commit() != nil {
			return Workspace{}, false, agent.E("INTERNAL_ERROR")
		}
		return existing, false, nil
	}
	if e != sql.ErrNoRows {
		return Workspace{}, false, agent.E("INTERNAL_ERROR")
	}
	now := time.Now().UnixMilli()
	w := Workspace{ID: newWorkspaceID(), Label: label, CanonicalRoot: canonicalRoot, State: "ACTIVE", Revision: 1, Source: source, CreatedAt: now, UpdatedAt: now}
	if _, e = tx.Exec("INSERT INTO workspaces("+workspaceColumns+") VALUES(?,?,?,?,?,?,?,?)", w.ID, w.CanonicalRoot, w.Label, w.State, w.Revision, w.Source, w.CreatedAt, w.UpdatedAt); e != nil {
		// A concurrent identical grant loses the race on the partial unique
		// index. Return the now-canonical ACTIVE row rather than a duplicate.
		existing, readErr := scanWorkspace(tx.QueryRow("SELECT "+workspaceColumns+" FROM workspaces WHERE canonical_root=? AND state='ACTIVE'", canonicalRoot))
		if readErr == nil {
			if tx.Commit() != nil {
				return Workspace{}, false, agent.E("INTERNAL_ERROR")
			}
			return existing, false, nil
		}
		return Workspace{}, false, agent.E("INTERNAL_ERROR")
	}
	if tx.Commit() != nil {
		return Workspace{}, false, agent.E("INTERNAL_ERROR")
	}
	return w, true, nil
}

func (s *Store) Workspaces() ([]Workspace, error) {
	rows, e := s.db.Query("SELECT " + workspaceColumns + " FROM workspaces ORDER BY created_at, id")
	if e != nil {
		return nil, agent.E("INTERNAL_ERROR")
	}
	defer rows.Close()
	out := []Workspace{}
	for rows.Next() {
		w, e := scanWorkspace(rows)
		if e != nil {
			return nil, agent.E("INTERNAL_ERROR")
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

func (s *Store) ActiveWorkspaces() ([]Workspace, error) {
	rows, e := s.db.Query("SELECT " + workspaceColumns + " FROM workspaces WHERE state='ACTIVE' ORDER BY created_at, id")
	if e != nil {
		return nil, agent.E("INTERNAL_ERROR")
	}
	defer rows.Close()
	out := []Workspace{}
	for rows.Next() {
		w, e := scanWorkspace(rows)
		if e != nil {
			return nil, agent.E("INTERNAL_ERROR")
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

func (s *Store) Workspace(id string) (Workspace, error) {
	if !validWorkspaceID(id) {
		return Workspace{}, agent.E("WORKSPACE_NOT_FOUND")
	}
	w, e := scanWorkspace(s.db.QueryRow("SELECT "+workspaceColumns+" FROM workspaces WHERE id=?", id))
	if e == sql.ErrNoRows {
		return Workspace{}, agent.E("WORKSPACE_NOT_FOUND")
	}
	if e != nil {
		return Workspace{}, agent.E("INTERNAL_ERROR")
	}
	return w, nil
}

// RenameWorkspace changes only the user-facing label. It never changes the
// authorization revision, so it cannot invalidate an existing run binding.
func (s *Store) RenameWorkspace(id, label string) error {
	if !validWorkspaceID(id) || !validWorkspaceLabel(label) {
		return agent.E("WORKSPACE_INVALID")
	}
	res, e := s.db.Exec("UPDATE workspaces SET label=?,updated_at=? WHERE id=?", strings.TrimSpace(label), time.Now().UnixMilli(), id)
	if e != nil {
		return agent.E("INTERNAL_ERROR")
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return agent.E("WORKSPACE_NOT_FOUND")
	}
	return nil
}

// RevokeWorkspace durably revokes the grant. The identity is retained for
// audit; the revision increments so stale snapshots fail closed. Revocation is
// also an instruction to every run that is actively reading this workspace, so
// the durable abort happens in the same transaction: no window exists where
// the grant is REVOKED while a bound run may still issue a new read.
func (s *Store) RevokeWorkspace(id string) error {
	if !validWorkspaceID(id) {
		return agent.E("WORKSPACE_NOT_FOUND")
	}
	tx, e := s.db.Begin()
	if e != nil {
		return agent.E("INTERNAL_ERROR")
	}
	defer tx.Rollback()
	res, e := tx.Exec("UPDATE workspaces SET state='REVOKED',revision=revision+1,updated_at=? WHERE id=? AND state='ACTIVE'", time.Now().UnixMilli(), id)
	if e != nil {
		return agent.E("INTERNAL_ERROR")
	}
	if n, _ := res.RowsAffected(); n != 1 {
		if _, e = scanWorkspace(tx.QueryRow("SELECT "+workspaceColumns+" FROM workspaces WHERE id=?", id)); e != nil {
			return agent.E("WORKSPACE_NOT_FOUND")
		}
		// Idempotent for an already-revoked grant.
		return nil
	}
	if _, e = tx.Exec(`UPDATE runs SET cancel_requested=1, abort_reason=?
		WHERE status='RUNNING' AND id IN (SELECT run_id FROM repo_access WHERE workspace_id=?)`, "WORKSPACE_AUTHORIZATION_REVOKED", id); e != nil {
		return agent.E("INTERNAL_ERROR")
	}
	if tx.Commit() != nil {
		return agent.E("INTERNAL_ERROR")
	}
	return nil
}

func (s *Store) WorkspaceStats() (active, revoked int, err error) {
	rows, e := s.db.Query("SELECT state,COUNT(*) FROM workspaces GROUP BY state")
	if e != nil {
		return 0, 0, agent.E("INTERNAL_ERROR")
	}
	defer rows.Close()
	for rows.Next() {
		var state string
		var count int
		if rows.Scan(&state, &count) != nil {
			return 0, 0, agent.E("INTERNAL_ERROR")
		}
		switch state {
		case "ACTIVE":
			active = count
		case "REVOKED":
			revoked = count
		}
	}
	return active, revoked, rows.Err()
}
