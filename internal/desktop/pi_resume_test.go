//go:build !windows

package desktop

import (
	"ai-worker-mcp/internal/agent"
	"ai-worker-mcp/internal/config"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
)

func writePiHistory(t *testing.T, dir, name, id string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	header, _ := json.Marshal(map[string]any{"type": "session", "version": 3, "id": id, "cwd": dir})
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, append(header, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func resumeArguments(t *testing.T, command string) []string {
	t.Helper()
	// set -- tokenizes the generated command without executing Pi or any argument.
	out, err := exec.Command("/bin/sh", "-c", "set -- "+command+"; printf '%s\\000' \"$@\"").Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
}

func TestPiResumeCommandUsesFrozenPathsAndExactSessionFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions ' $(printf unsafe) <history>")
	path := writePiHistory(t, dir, "renamed history.jsonl", "id-123")
	binary := "/Applications/Pi ' $(printf unsafe)/pi"
	run := agent.Run{ID: "wrk_pi", Runtime: "pi", RuntimeSessionID: "id-123", RuntimeBinary: binary, RuntimeSessionDir: dir, RuntimeSessionFile: path}
	cfg := config.DefaultAt(t.TempDir())
	cfg.Runtime.Binary = "/different/pi"
	got := piResumeCommands([]agent.Run{run}, cfg)[run.ID]
	if got == "" {
		t.Fatal("missing command")
	}
	want := []string{binary, "--resume", "--session-dir", dir, "--session", path}
	if args := resumeArguments(t, got); !reflect.DeepEqual(args, want) {
		t.Fatalf("unsafe quoting or wrong session: %q", args)
	}
}

func TestPiResumeCommandOldRunsResolveOnlyVerifiedCurrentHistory(t *testing.T) {
	cfg := config.DefaultAt(t.TempDir())
	dir := cfg.Runtime.SessionDir
	path := writePiHistory(t, dir, "2026-10-26_legacy-id.jsonl", "legacy-id")
	run := agent.Run{ID: "wrk_old_pi", Runtime: "pi", RuntimeSessionID: "legacy-id"}
	got := piResumeCommands([]agent.Run{run}, cfg)
	if len(got) != 1 || !reflect.DeepEqual(resumeArguments(t, got[run.ID]), []string{"pi", "--resume", "--session-dir", dir, "--session", path}) {
		t.Fatal(got)
	}
	os.Remove(path)
	if got := piResumeCommands([]agent.Run{run}, cfg); len(got) != 0 {
		t.Fatal("missing history produced a command", got)
	}
}

func TestPiResumeCommandRejectsWrongEscapingAndAmbiguousHistory(t *testing.T) {
	cfg := config.DefaultAt(t.TempDir())
	dir := cfg.Runtime.SessionDir
	os.MkdirAll(dir, 0700)
	outside := writePiHistory(t, t.TempDir(), "outside.jsonl", "outside")
	os.Symlink(outside, filepath.Join(dir, "date_outside.jsonl"))
	os.WriteFile(filepath.Join(dir, "date_wrong.jsonl"), []byte("{\"type\":\"session\",\"id\":\"other\"}\n"), 0600)
	writePiHistory(t, dir, "one_duplicate.jsonl", "duplicate")
	writePiHistory(t, dir, "two_duplicate.jsonl", "duplicate")
	if err := syscall.Mkfifo(filepath.Join(dir, "date_fifo.jsonl"), 0600); err != nil {
		t.Fatal(err)
	}
	runs := []agent.Run{
		{ID: "legacy", Runtime: "opencode", RuntimeSessionID: "duplicate"},
		{ID: "missing-id", Runtime: "pi"},
		{ID: "escape", Runtime: "pi", RuntimeSessionID: "outside", RuntimeSessionFile: outside},
		{ID: "wrong-header", Runtime: "pi", RuntimeSessionID: "wrong"},
		{ID: "ambiguous", Runtime: "pi", RuntimeSessionID: "duplicate"},
		{ID: "fifo", Runtime: "pi", RuntimeSessionID: "fifo"},
		{ID: "invalid-id", Runtime: "pi", RuntimeSessionID: "../duplicate"},
	}
	if got := piResumeCommands(runs, cfg); len(got) != 0 {
		t.Fatal(got)
	}
}

func TestStateIncludesResumeCommandsWithoutChangingHistory(t *testing.T) {
	dir := t.TempDir()
	cfg := config.DefaultAt(dir)
	cfg.Directory = dir
	writePiHistory(t, cfg.Runtime.SessionDir, "date_state-session.jsonl", "state-session")
	if err := config.Save(filepath.Join(dir, "config.toml"), cfg); err != nil {
		t.Fatal(err)
	}
	s, err := New(dir, "/tmp/mcp")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	run := agent.Run{ID: "wrk_state", Runtime: "pi", RuntimeSessionID: "state-session", Status: "COMPLETED"}
	if err := s.storage.Create(run); err != nil {
		t.Fatal(err)
	}
	state := s.State()
	if state.PiResumeCommands[run.ID] == "" {
		t.Fatal("resume command absent from Desktop state")
	}
	saved, _ := s.storage.Get(run.ID)
	if saved.RuntimeSessionDir != "" || saved.RuntimeSessionID != run.RuntimeSessionID {
		t.Fatal("state mutated history", saved)
	}
}
