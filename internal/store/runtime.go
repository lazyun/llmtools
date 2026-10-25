package store

import (
	"ai-worker-mcp/internal/agent"
	"database/sql"
	"encoding/json"
	"time"
)

// RuntimeInstance is observed state, never user configuration. Generation is a
// monotonic SQLite epoch; identity and owner are immutable within that epoch.
type RuntimeInstance struct {
	ID         string    `json:"runtime_instance_id"`
	Type       string    `json:"runtime_type"`
	Mode       string    `json:"mode"`
	BaseURL    string    `json:"base_url"`
	Host       string    `json:"host"`
	Port       int       `json:"port"`
	OwnerID    string    `json:"owner_instance_id"`
	OwnerPID   int       `json:"owner_pid"`
	PID        int       `json:"pid"`
	Generation int64     `json:"generation"`
	Status     string    `json:"status"`
	Version    string    `json:"version"`
	StartedAt  time.Time `json:"started_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func (s *Store) RuntimeInstance() (RuntimeInstance, error) {
	var r RuntimeInstance
	var body string
	e := s.db.QueryRow("SELECT body FROM runtime_instances ORDER BY generation DESC LIMIT 1").Scan(&body)
	if e == sql.ErrNoRows {
		return r, nil
	}
	if e != nil || json.Unmarshal([]byte(body), &r) != nil {
		return r, agent.E("INTERNAL_ERROR")
	}
	return r, nil
}

// ClaimRuntime requires the caller to hold runtime.lock until its supervised
// child has exited. This non-expiring kernel lease prevents takeover of a paused
// live owner. SQLite provides durable single-writer publication and CAS fencing.
func (s *Store) ClaimRuntime(r RuntimeInstance) (RuntimeInstance, error) {
	tx, e := s.db.Begin()
	if e != nil {
		return r, e
	}
	defer tx.Rollback()
	rows, e := tx.Query("SELECT body FROM runtime_instances WHERE status IN ('STARTING','READY','STOPPING')")
	if e != nil {
		return r, e
	}
	var stale []RuntimeInstance
	for rows.Next() {
		var b string
		var old RuntimeInstance
		if e = rows.Scan(&b); e != nil {
			rows.Close()
			return r, e
		}
		if e = json.Unmarshal([]byte(b), &old); e != nil {
			rows.Close()
			return r, e
		}
		stale = append(stale, old)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return r, e
	}
	for _, old := range stale {
		old.Status = "STALE"
		old.UpdatedAt = time.Now().UTC()
		b, _ := json.Marshal(old)
		if _, e = tx.Exec("UPDATE runtime_instances SET status='STALE',body=? WHERE generation=?", string(b), old.Generation); e != nil {
			return r, e
		}
	}
	r.Status = "STARTING"
	r.StartedAt = time.Now().UTC()
	r.UpdatedAt = r.StartedAt
	res, e := tx.Exec("INSERT INTO runtime_instances(instance_id,owner_id,status,body) VALUES(?,?,'STARTING','{}')", r.ID, r.OwnerID)
	if e != nil {
		return r, e
	}
	r.Generation, e = res.LastInsertId()
	if e != nil {
		return r, e
	}
	b, _ := json.Marshal(r)
	if _, e = tx.Exec("UPDATE runtime_instances SET body=? WHERE generation=?", string(b), r.Generation); e != nil {
		return r, e
	}
	return r, tx.Commit()
}

func (s *Store) UpdateRuntime(r RuntimeInstance, expected string) error {
	r.UpdatedAt = time.Now().UTC()
	b, _ := json.Marshal(r)
	res, e := s.db.Exec("UPDATE runtime_instances SET status=?,body=? WHERE generation=? AND instance_id=? AND owner_id=? AND status=? AND generation=(SELECT MAX(generation) FROM runtime_instances)", r.Status, string(b), r.Generation, r.ID, r.OwnerID, expected)
	if e != nil {
		return agent.E("INTERNAL_ERROR")
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return agent.E("RUNTIME_UNAVAILABLE")
	}
	return nil
}
