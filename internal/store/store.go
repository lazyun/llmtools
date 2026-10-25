// Package store persists only communication state, never runtime transcripts.
package store

import (
	"ai-worker-mcp/internal/agent"
	"ai-worker-mcp/internal/localfs"
	"database/sql"
	"encoding/json"
	_ "github.com/mattn/go-sqlite3"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Store struct {
	db        *sql.DB
	Directory string
}

func Open(path string) (*Store, error) {
	if e := localfs.EnsureDirectory(filepath.Dir(path)); e != nil {
		return nil, agent.E("CONFIG_ERROR")
	}
	// SQLite journal-mode negotiation can return SQLITE_BUSY before its busy
	// handler applies when two fresh processes open a brand-new database. Serialize
	// only connection/schema bootstrap; runtime ownership still uses SQL claims/CAS.
	var bootstrap *os.File
	deadline := time.Now().Add(5 * time.Second)
	for {
		var lockErr error
		bootstrap, lockErr = localfs.Lock(path + ".init.lock")
		if lockErr == nil {
			break
		}
		if time.Now().After(deadline) {
			return nil, agent.E("INTERNAL_ERROR")
		}
		time.Sleep(20 * time.Millisecond)
	}
	defer localfs.Unlock(bootstrap)
	f, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if e == nil {
		f.Close()
	} else if !os.IsExist(e) {
		return nil, agent.E("CONFIG_ERROR")
	}
	st, e := os.Lstat(path)
	if e != nil || !st.Mode().IsRegular() {
		return nil, agent.E("CONFIG_ERROR")
	}
	if os.Chmod(path, 0600) != nil {
		return nil, agent.E("CONFIG_ERROR")
	}
	abs, e := filepath.Abs(path)
	if e != nil {
		return nil, agent.E("CONFIG_ERROR")
	}
	u := url.URL{Scheme: "file", Path: abs, RawQuery: "_busy_timeout=5000&_journal_mode=WAL&_foreign_keys=on&_synchronous=FULL&_txlock=immediate"}
	db, e := sql.Open("sqlite3", u.String())
	if e != nil {
		return nil, agent.E("INTERNAL_ERROR")
	}
	db.SetMaxOpenConns(4)
	fail := func() (*Store, error) { db.Close(); return nil, agent.E("INTERNAL_ERROR") }
	tx, e := db.Begin()
	if e != nil {
		return fail()
	}
	defer tx.Rollback()
	var version int
	if tx.QueryRow("PRAGMA user_version").Scan(&version) != nil || version > 7 {
		return fail()
	}
	if version == 0 {
		_, e = tx.Exec(`CREATE TABLE runs(id TEXT PRIMARY KEY, status TEXT NOT NULL, body TEXT NOT NULL);
 CREATE TABLE run_requests(run_id TEXT NOT NULL REFERENCES runs(id), id TEXT NOT NULL, kind TEXT NOT NULL, resolved INTEGER NOT NULL DEFAULT 0, PRIMARY KEY(run_id,id));
 PRAGMA user_version=1;`)
		if e != nil {
			return fail()
		}
	}
	if version < 2 {
		if _, e = tx.Exec("ALTER TABLE runs ADD COLUMN cancel_requested INTEGER NOT NULL DEFAULT 0; PRAGMA user_version=2;"); e != nil {
			return fail()
		}
	}
	if version < 3 {
		if _, e = tx.Exec(`CREATE TABLE runtime_instances (generation INTEGER PRIMARY KEY AUTOINCREMENT, instance_id TEXT UNIQUE NOT NULL, owner_id TEXT NOT NULL, status TEXT NOT NULL, body TEXT NOT NULL); CREATE UNIQUE INDEX runtime_single_active ON runtime_instances ((1)) WHERE status IN ('STARTING','READY','STOPPING'); PRAGMA user_version=3;`); e != nil {
			return fail()
		}
	}
	if version < 4 {
		if _, e = tx.Exec(`CREATE TABLE runtime_context_controls (
 token TEXT PRIMARY KEY,
 session_id TEXT NOT NULL,
 state TEXT NOT NULL,
 request_json TEXT,
 response_json TEXT,
 updated INTEGER NOT NULL
); PRAGMA user_version=4;`); e != nil {
			return fail()
		}
	}
	// v5 records immutable routing identity inside each run's durable JSON body.
	// No duplicate relational columns or OpenCode catalog snapshot are needed.
	if version < 5 {
		if _, e = tx.Exec("PRAGMA user_version=5;"); e != nil {
			return fail()
		}
	}
	// v6 adds the user-authorized Workspace Registry. Workspace identity is a
	// server-generated id; the canonical root is an immutable attribute and a
	// partial unique index prevents duplicate ACTIVE grants for one root.
	if version < 6 {
		if _, e = tx.Exec(`CREATE TABLE workspaces (
	id TEXT PRIMARY KEY,
	canonical_root TEXT NOT NULL,
	label TEXT NOT NULL,
	state TEXT NOT NULL,
	revision INTEGER NOT NULL,
	source TEXT NOT NULL,
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL
);
CREATE UNIQUE INDEX workspace_active_root ON workspaces(canonical_root) WHERE state='ACTIVE';
PRAGMA user_version=6;`); e != nil {
			return fail()
		}
	}
	// v7 records the read-only repository authorization a live run holds, keyed
	// by both the control token (the only capability handle the execution agent
	// sees) and the workspace (so revocation can fan out to bound runs). It
	// stores identity and a frozen root only, never file contents. The abort
	// reason lets the revoking process tell the executing one why it must stop.
	if version < 7 {
		if _, e = tx.Exec(`CREATE TABLE repo_access (
	token TEXT PRIMARY KEY,
	run_id TEXT NOT NULL UNIQUE,
	session_id TEXT NOT NULL,
	workspace_id TEXT NOT NULL,
	canonical_root TEXT NOT NULL,
	grant_revision INTEGER NOT NULL,
	updated INTEGER NOT NULL
);
CREATE INDEX repo_access_workspace ON repo_access(workspace_id);
ALTER TABLE runs ADD COLUMN abort_reason TEXT NOT NULL DEFAULT '';
PRAGMA user_version=7;`); e != nil {
			return fail()
		}
	}
	// A read grant lasts exactly as long as its run can execute. Any record left
	// behind by a crash or by another process aborting the run is dropped here,
	// so no stale token can outlive the execution it authorized.
	if _, e = tx.Exec(`DELETE FROM repo_access WHERE run_id NOT IN
		(SELECT id FROM runs WHERE status IN ('RUNNING','NEEDS_CONTEXT','NEEDS_DECISION'))`); e != nil {
		return fail()
	}
	if tx.Commit() != nil {
		return fail()
	}
	return &Store{db, filepath.Dir(path)}, nil
}

type ContextControl struct {
	Token     string
	SessionID string
	State     string
	Requests  []agent.ContextRequest
	Responses []agent.AdditionalContext
}

func validControlToken(token string) bool {
	return strings.HasPrefix(token, "ctl_") && len(token) == 36
}

func (s *Store) RegisterContextControl(token, sessionID string) error {
	if !validControlToken(token) || !validRuntimeSessionID(sessionID) {
		return agent.E("INTERNAL_ERROR")
	}
	_, e := s.db.Exec("INSERT INTO runtime_context_controls(token,session_id,state,updated) VALUES(?,?,'ready',?)", token, sessionID, time.Now().UnixMilli())
	if e != nil {
		return agent.E("INTERNAL_ERROR")
	}
	return nil
}

func (s *Store) BeginContextControl(token string, requests []agent.ContextRequest) error {
	if !validControlToken(token) || len(requests) == 0 {
		return agent.E("INVALID_AGENT_RESULT")
	}
	b, _ := json.Marshal(requests)
	res, e := s.db.Exec("UPDATE runtime_context_controls SET state='pending',request_json=?,response_json=NULL,updated=? WHERE token=? AND state IN ('ready','delivered')", string(b), time.Now().UnixMilli(), token)
	if e != nil {
		return agent.E("INTERNAL_ERROR")
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return agent.E("INVALID_AGENT_RESULT")
	}
	return nil
}

func (s *Store) ContextControl(token string) (ContextControl, error) {
	var c ContextControl
	var requestJSON, responseJSON sql.NullString
	if !validControlToken(token) || s.db.QueryRow("SELECT session_id,state,request_json,response_json FROM runtime_context_controls WHERE token=?", token).Scan(&c.SessionID, &c.State, &requestJSON, &responseJSON) != nil {
		return c, agent.E("PENDING_CONTEXT_LOST")
	}
	c.Token = token
	if requestJSON.Valid && json.Unmarshal([]byte(requestJSON.String), &c.Requests) != nil {
		return c, agent.E("INTERNAL_ERROR")
	}
	if responseJSON.Valid && json.Unmarshal([]byte(responseJSON.String), &c.Responses) != nil {
		return c, agent.E("INTERNAL_ERROR")
	}
	return c, nil
}

func (s *Store) AnswerContextControl(token string, responses []agent.AdditionalContext) error {
	b, _ := json.Marshal(responses)
	res, e := s.db.Exec("UPDATE runtime_context_controls SET state='answered',response_json=?,updated=? WHERE token=? AND state='pending'", string(b), time.Now().UnixMilli(), token)
	if e != nil {
		return agent.E("INTERNAL_ERROR")
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return agent.E("PENDING_CONTEXT_LOST")
	}
	return nil
}

func (s *Store) DeliverContextControl(token string) error {
	res, e := s.db.Exec("UPDATE runtime_context_controls SET state='delivered',updated=? WHERE token=? AND state='answered'", time.Now().UnixMilli(), token)
	if e != nil {
		return agent.E("INTERNAL_ERROR")
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return agent.E("PENDING_CONTEXT_LOST")
	}
	return nil
}

func (s *Store) CancelContextControl(token string) {
	if validControlToken(token) {
		_, _ = s.db.Exec("UPDATE runtime_context_controls SET state='cancelled',updated=? WHERE token=?", time.Now().UnixMilli(), token)
	}
}

func (s *Store) DeleteContextControl(token string) {
	if validControlToken(token) {
		_, _ = s.db.Exec("DELETE FROM runtime_context_controls WHERE token=?", token)
	}
}
func (s *Store) Close() error { return s.db.Close() }
func (s *Store) RequestCancel(id string) error {
	res, e := s.db.Exec("UPDATE runs SET cancel_requested=1 WHERE id=? AND status IN ('RUNNING','NEEDS_CONTEXT','NEEDS_DECISION')", id)
	if e != nil {
		return agent.E("INTERNAL_ERROR")
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return agent.E("CONTINUATION_INVALID")
	}
	return nil
}
func (s *Store) CancelRequested(id string) bool {
	var flag int
	err := s.db.QueryRow("SELECT cancel_requested FROM runs WHERE id=?", id).Scan(&flag)
	return err != nil || flag != 0
}
func (s *Store) Create(r agent.Run) error {
	b, _ := json.Marshal(r)
	_, e := s.db.Exec("INSERT INTO runs(id,status,body) VALUES(?,?,?)", r.ID, r.Status, string(b))
	if e != nil {
		return agent.E("INTERNAL_ERROR")
	}
	return nil
}
func (s *Store) Get(id string) (agent.Run, error) {
	var b string
	if s.db.QueryRow("SELECT body FROM runs WHERE id=?", id).Scan(&b) != nil {
		return agent.Run{}, agent.E("CONTINUATION_INVALID")
	}
	var r agent.Run
	if json.Unmarshal([]byte(b), &r) != nil {
		return r, agent.E("INTERNAL_ERROR")
	}
	return r, nil
}
func (s *Store) List() ([]agent.Run, error) {
	return s.list("SELECT body FROM runs ORDER BY rowid DESC LIMIT 500")
}
func (s *Store) Running() ([]agent.Run, error) {
	return s.list("SELECT body FROM runs WHERE status='RUNNING'")
}
func (s *Store) list(query string) ([]agent.Run, error) {
	rows, e := s.db.Query(query)
	if e != nil {
		return nil, agent.E("INTERNAL_ERROR")
	}
	defer rows.Close()
	out := []agent.Run{}
	for rows.Next() {
		var b string
		var r agent.Run
		if rows.Scan(&b) != nil || json.Unmarshal([]byte(b), &r) != nil {
			return nil, agent.E("INTERNAL_ERROR")
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Save performs a compare-and-swap and writes pending requests in the same transaction.
func (s *Store) Save(r agent.Run, expected string) error {
	r.Updated = time.Now().UTC()
	b, _ := json.Marshal(r)
	tx, e := s.db.Begin()
	if e != nil {
		return agent.E("INTERNAL_ERROR")
	}
	defer tx.Rollback()
	query := "UPDATE runs SET status=?,body=? WHERE id=? AND status=?"
	if r.Status != "RUNNING" && r.Status != "CANCELLED" && r.Status != "FAILED" {
		query += " AND cancel_requested=0"
	}
	res, e := tx.Exec(query, r.Status, string(b), r.ID, expected)
	if e != nil {
		return agent.E("INTERNAL_ERROR")
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return agent.E("CONTINUATION_INVALID")
	}
	if r.Status == "NEEDS_CONTEXT" || r.Status == "NEEDS_DECISION" {
		for _, q := range r.Result.ContextRequests {
			if _, e = tx.Exec("INSERT INTO run_requests(run_id,id,kind) VALUES(?,?,?)", r.ID, q.ID, "context"); e != nil {
				return agent.E("INVALID_AGENT_RESULT")
			}
		}
		for _, q := range r.Result.DecisionRequests {
			if _, e = tx.Exec("INSERT INTO run_requests(run_id,id,kind) VALUES(?,?,?)", r.ID, q.ID, "decision"); e != nil {
				return agent.E("INVALID_AGENT_RESULT")
			}
		}
	}
	if r.Status == "RUNNING" && (expected == "NEEDS_CONTEXT" || expected == "NEEDS_DECISION") {
		if _, e = tx.Exec("UPDATE run_requests SET resolved=1 WHERE run_id=? AND resolved=0", r.ID); e != nil {
			return agent.E("INTERNAL_ERROR")
		}
	}
	if tx.Commit() != nil {
		return agent.E("INTERNAL_ERROR")
	}
	return nil
}
func (s *Store) Lock(id string) (*os.File, error) {
	if !strings.HasPrefix(id, "wrk_") || strings.ContainsAny(id, "/\\.") || len(id) > 80 {
		return nil, agent.E("CONTINUATION_INVALID")
	}
	f, e := localfs.Lock(filepath.Join(s.Directory, id+".lock"))
	if e != nil {
		return nil, agent.E("CONTINUATION_INVALID")
	}
	return f, nil
}
