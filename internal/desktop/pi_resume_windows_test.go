//go:build windows

package desktop

import (
	"ai-worker-mcp/internal/agent"
	"ai-worker-mcp/internal/config"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// On Windows the same session-header rules apply; only the Unix-only FIFO and
// /bin/sh tokenizing cases are excluded (see pi_resume_test.go).
func TestPiResumeCommandsResolveVerifiedSessionsOnWindows(t *testing.T) {
	cfg := config.DefaultAt(t.TempDir())
	dir := cfg.Runtime.SessionDir
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	id := "win-session"
	path := filepath.Join(dir, "2026-10-26_win-session.jsonl")
	header, _ := json.Marshal(map[string]any{"type": "session", "version": 3, "id": id, "cwd": dir})
	if err := os.WriteFile(path, append(header, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	run := agent.Run{ID: "wrk_win", Runtime: "pi", RuntimeSessionID: id}
	got := piResumeCommands([]agent.Run{run}, cfg)
	if len(got) != 1 {
		t.Fatal("verified session produced no command", got)
	}
	if cmd := got[run.ID]; !strings.Contains(cmd, "--resume") || !strings.Contains(cmd, path) {
		t.Fatalf("command missing resume/session: %q", cmd)
	}
}
