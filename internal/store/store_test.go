package store

import (
	"ai-worker-mcp/internal/agent"
	"database/sql"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestConcurrentStoresAndDurability(t *testing.T) {
	p := filepath.Join(t.TempDir(), "runs.db")
	a, e := Open(p)
	if e != nil {
		t.Fatal(e)
	}
	b, e := Open(p)
	if e != nil {
		t.Fatal(e)
	}
	if e = a.Create(agent.Run{ID: "wrk_one", Status: "RUNNING"}); e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, e := b.Get("wrk_one"); e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	r, _ := a.Get("wrk_one")
	r.Status = "COMPLETED"
	if e = a.Save(r, "RUNNING"); e != nil {
		t.Fatal(e)
	}
	if e = b.Save(r, "RUNNING"); e == nil {
		t.Fatal("CAS missed conflict")
	}
	a.Close()
	b.Close()
	c, e := Open(p)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	r, e = c.Get("wrk_one")
	if e != nil || r.Status != "COMPLETED" {
		t.Fatal(r, e)
	}
	st, _ := os.Stat(p)
	if st.Mode().Perm() != 0600 {
		t.Fatal("unsafe DB permissions")
	}
	var v int
	if c.db.QueryRow("PRAGMA user_version").Scan(&v) != nil || v != 7 {
		t.Fatal("migration version")
	}
}

func TestVersionOneUpgrade(t *testing.T) {
	p := filepath.Join(t.TempDir(), "old.db")
	db, e := sql.Open("sqlite3", p)
	if e != nil {
		t.Fatal(e)
	}
	_, e = db.Exec("CREATE TABLE runs(id TEXT PRIMARY KEY,status TEXT NOT NULL,body TEXT NOT NULL); CREATE TABLE run_requests(run_id TEXT NOT NULL,id TEXT NOT NULL,kind TEXT NOT NULL,resolved INTEGER NOT NULL DEFAULT 0,PRIMARY KEY(run_id,id)); PRAGMA user_version=1;")
	db.Close()
	if e != nil {
		t.Fatal(e)
	}
	s, e := Open(p)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if e = s.Create(agent.Run{ID: "wrk_migrated", Status: "RUNNING"}); e != nil {
		t.Fatal(e)
	}
	if s.CancelRequested("wrk_migrated") {
		t.Fatal("unexpected cancellation")
	}
	if e = s.RequestCancel("wrk_migrated"); e != nil || !s.CancelRequested("wrk_migrated") {
		t.Fatal(e)
	}
}
