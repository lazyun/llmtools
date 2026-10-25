package store

import (
	"ai-worker-mcp/internal/agent"
	"database/sql"
	"os"
	"strings"
	"time"
)

// RepoAccess is the durable read-only repository authorization of one live
// run. The control token is the only handle the execution agent ever sees;
// it maps to a frozen workspace grant, never to a caller-chosen path.
type RepoAccess struct {
	Token         string
	RunID         string
	SessionID     string
	WorkspaceID   string
	CanonicalRoot string
	GrantRevision int64
}

func validRepoAccess(a RepoAccess) bool {
	return strings.HasPrefix(a.Token, "ctl_") && len(a.Token) == 36 &&
		strings.HasPrefix(a.RunID, "wrk_") && len(a.RunID) == 36 &&
		validWorkspaceID(a.WorkspaceID) && validRuntimeSessionID(a.SessionID) &&
		strings.HasPrefix(a.CanonicalRoot, "/") && a.GrantRevision > 0
}

// validRuntimeSessionID accepts the durable runtime session identity in generic
// form. The store must not know which runtime produced it: OpenCode issues
// "ses_…" ids and an experimental runtime issues its own, while the control
// token — not this id — remains the only authorization handle. The charset is
// still confined so an id can never carry a path, query or URL fragment.
func validRuntimeSessionID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for i := 0; i < len(id); i++ {
		if c := id[i]; !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-' || c == '.') {
			return false
		}
	}
	return true
}

const repoAccessColumns = "token,run_id,session_id,workspace_id,canonical_root,grant_revision"

func scanRepoAccess(row interface{ Scan(...any) error }) (RepoAccess, error) {
	var a RepoAccess
	e := row.Scan(&a.Token, &a.RunID, &a.SessionID, &a.WorkspaceID, &a.CanonicalRoot, &a.GrantRevision)
	return a, e
}

// RegisterRepoAccess replaces any prior record for the run so a continuation
// can never observe a stale token or revision.
func (s *Store) RegisterRepoAccess(a RepoAccess) error {
	if !validRepoAccess(a) {
		return agent.E("INTERNAL_ERROR")
	}
	res, e := s.db.Exec(`INSERT INTO repo_access(token,run_id,session_id,workspace_id,canonical_root,grant_revision,updated)
		VALUES(?,?,?,?,?,?,?)
		ON CONFLICT(run_id) DO UPDATE SET token=excluded.token,session_id=excluded.session_id,workspace_id=excluded.workspace_id,canonical_root=excluded.canonical_root,grant_revision=excluded.grant_revision,updated=excluded.updated`,
		a.Token, a.RunID, a.SessionID, a.WorkspaceID, a.CanonicalRoot, a.GrantRevision, time.Now().UnixMilli())
	if e != nil {
		return agent.E("INTERNAL_ERROR")
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return agent.E("INTERNAL_ERROR")
	}
	return nil
}

func (s *Store) RepoAccessByRun(runID string) (RepoAccess, error) {
	a, e := scanRepoAccess(s.db.QueryRow("SELECT "+repoAccessColumns+" FROM repo_access WHERE run_id=?", runID))
	if e == sql.ErrNoRows {
		return RepoAccess{}, agent.E("WORKSPACE_ACCESS_DENIED")
	}
	if e != nil {
		return RepoAccess{}, agent.E("INTERNAL_ERROR")
	}
	return a, nil
}

// DeleteRepoAccess ends the read capability of a terminal run.
func (s *Store) DeleteRepoAccess(runID string) {
	if strings.HasPrefix(runID, "wrk_") {
		_, _ = s.db.Exec("DELETE FROM repo_access WHERE run_id=?", runID)
	}
}

// LiveRepoRoot revalidates the frozen grant behind a control token against the
// durable registry and the filesystem. A revoked grant, a bumped revision, a
// changed root, or a missing directory all deny the access it is asked about.
// The snapshot alone never authorizes anything. A run that is no longer
// executing, or that already carries a cancellation request, also denies: the
// repository capability is bound to the live execution, not to the token.
// Authorization loss is evaluated first so the caller sees the specific code
// rather than a generic denial.
func (s *Store) LiveRepoRoot(token string) (RepoAccess, string, error) {
	var a RepoAccess
	if !strings.HasPrefix(token, "ctl_") || len(token) != 36 {
		return a, "", agent.E("WORKSPACE_ACCESS_DENIED")
	}
	stored, e := scanRepoAccess(s.db.QueryRow("SELECT "+repoAccessColumns+" FROM repo_access WHERE token=?", token))
	if e == sql.ErrNoRows {
		return a, "", agent.E("WORKSPACE_ACCESS_DENIED")
	}
	if e != nil {
		return a, "", agent.E("INTERNAL_ERROR")
	}
	w, e := s.Workspace(stored.WorkspaceID)
	if e != nil {
		return stored, "", e
	}
	if w.State != "ACTIVE" || w.Revision != stored.GrantRevision || w.CanonicalRoot != stored.CanonicalRoot {
		return stored, "", agent.E("WORKSPACE_AUTHORIZATION_REVOKED")
	}
	st, e := os.Stat(w.CanonicalRoot)
	if e != nil || !st.IsDir() {
		return stored, "", agent.E("WORKSPACE_UNAVAILABLE")
	}
	var status string
	var cancel int
	if e := s.db.QueryRow("SELECT status,cancel_requested FROM runs WHERE id=?", stored.RunID).Scan(&status, &cancel); e != nil || status != "RUNNING" || cancel != 0 {
		return stored, "", agent.E("WORKSPACE_ACCESS_DENIED")
	}
	return stored, w.CanonicalRoot, nil
}

// AbortReason reports why another process asked this run to stop. An unknown
// run reads as no request, which keeps the cancellation path unchanged.
func (s *Store) AbortReason(runID string) string {
	var reason string
	if !strings.HasPrefix(runID, "wrk_") || s.db.QueryRow("SELECT abort_reason FROM runs WHERE id=?", runID).Scan(&reason) != nil {
		return ""
	}
	return reason
}
