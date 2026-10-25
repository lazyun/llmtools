package service

import (
	"ai-worker-mcp/internal/config"
	"ai-worker-mcp/internal/store"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func settingsService(t *testing.T) (*Service, config.Config) {
	t.Helper()
	c := config.Default()
	c.Directory = t.TempDir()
	if e := config.Save(filepath.Join(c.Directory, "config.toml"), c); e != nil {
		t.Fatal(e)
	}
	st, e := store.Open(filepath.Join(c.Directory, c.Storage.Database))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { st.Close() })
	return New(c, st, nil), c
}

func assertTaskDeadline(t *testing.T, ctx context.Context, want time.Duration) time.Time {
	t.Helper()
	deadline, ok := ctx.Deadline()
	remaining := time.Until(deadline)
	if !ok || remaining > want || remaining < want-time.Second {
		t.Fatalf("deadline remaining %v, want %v", remaining, want)
	}
	return deadline
}

func TestTaskTimeoutReloadsForExistingServiceWithoutChangingActiveCalls(t *testing.T) {
	s, c := settingsService(t)
	active, cancelActive := s.taskContext(context.Background(), "active")
	defer cancelActive()
	before := assertTaskDeadline(t, active, 180*time.Second)
	c.General.TaskTimeoutSeconds = 1800
	if e := config.Save(filepath.Join(c.Directory, "config.toml"), c); e != nil {
		t.Fatal(e)
	}
	next, cancelNext := s.taskContext(context.Background(), "next")
	defer cancelNext()
	assertTaskDeadline(t, next, 1800*time.Second)
	after, _ := active.Deadline()
	if before != after || s.Config.General.TaskTimeoutSeconds != 180 {
		t.Fatal("active deadline or startup routing snapshot changed")
	}
	parent, cancelParent := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelParent()
	bounded, cancelBounded := s.taskContext(parent, "parent-bounded")
	defer cancelBounded()
	assertTaskDeadline(t, bounded, 5*time.Second)
	c.General.TaskTimeoutSeconds = 30
	if e := config.Save(filepath.Join(c.Directory, "config.toml"), c); e != nil {
		t.Fatal(e)
	}
	shorter, cancelShorter := s.taskContext(context.Background(), "shorter")
	defer cancelShorter()
	assertTaskDeadline(t, shorter, 30*time.Second)
}

func TestTaskTimeoutKeepsLastValidSettingOnInvalidOrReplacedConfiguration(t *testing.T) {
	for _, kind := range []string{"invalid", "missing", "runtime", "storage"} {
		t.Run(kind, func(t *testing.T) {
			s, c := settingsService(t)
			c.General.TaskTimeoutSeconds = 1800
			path := filepath.Join(c.Directory, "config.toml")
			if e := config.Save(path, c); e != nil {
				t.Fatal(e)
			}
			valid, cancelValid := s.taskContext(context.Background(), "valid")
			assertTaskDeadline(t, valid, 1800*time.Second)
			cancelValid()
			switch kind {
			case "invalid":
				if e := os.WriteFile(path, []byte("invalid = ["), 0o600); e != nil {
					t.Fatal(e)
				}
			case "missing":
				if e := os.Remove(path); e != nil {
					t.Fatal(e)
				}
			default:
				c.General.TaskTimeoutSeconds = 10
				if kind == "runtime" {
					c.Runtime.Binary = "different-runtime"
				} else {
					c.Storage.Database = "different.db"
				}
				if e := config.Save(path, c); e != nil {
					t.Fatal(e)
				}
			}
			next, cancelNext := s.taskContext(context.Background(), "next")
			defer cancelNext()
			assertTaskDeadline(t, next, 1800*time.Second)
		})
	}
}
