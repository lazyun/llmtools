package store

import (
	"path/filepath"
	"testing"
)

func TestRuntimeDurabilityAndGenerationFencing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	a, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer a.Close()
	first, e := a.ClaimRuntime(RuntimeInstance{ID: "rt_old", OwnerID: "owner_old"})
	if e != nil {
		t.Fatal(e)
	}
	first.Status = "READY"
	first.Port = 55115
	first.BaseURL = "http://127.0.0.1:55115"
	if e = a.UpdateRuntime(first, "STARTING"); e != nil {
		t.Fatal(e)
	}
	b, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer b.Close()
	got, e := b.RuntimeInstance()
	if e != nil || got.ID != first.ID || got.Port != 55115 {
		t.Fatal(got, e)
	}
	// Simulate recovery after the caller has acquired the released kernel lease.
	second, e := b.ClaimRuntime(RuntimeInstance{ID: "rt_new", OwnerID: "owner_new"})
	if e != nil {
		t.Fatal(e)
	}
	if second.Generation <= first.Generation {
		t.Fatal("generation did not advance")
	}
	second.Status = "READY"
	if e = b.UpdateRuntime(second, "STARTING"); e != nil {
		t.Fatal(e)
	}
	for _, status := range []string{"READY", "STOPPING", "STOPPED"} {
		first.Status = status
		if a.UpdateRuntime(first, "READY") == nil {
			t.Fatal("old owner was not fenced", status)
		}
	}
	got, _ = a.RuntimeInstance()
	if got.ID != second.ID || got.Status != "READY" {
		t.Fatal(got)
	}
	var oldStatus string
	_ = a.db.QueryRow("SELECT status FROM runtime_instances WHERE generation=?", first.Generation).Scan(&oldStatus)
	if oldStatus != "STALE" {
		t.Fatal(oldStatus)
	}
	var count int
	_ = a.db.QueryRow("SELECT count(*) FROM runtime_instances WHERE status='READY'").Scan(&count)
	if count != 1 {
		t.Fatal(count)
	}
}
