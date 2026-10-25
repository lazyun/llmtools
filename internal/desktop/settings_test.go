package desktop

import (
	"ai-worker-mcp/internal/config"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// General limits belong to llmTools and must remain editable for either runtime.
func TestGeneralSettingsPersistForPi(t *testing.T) {
	for _, kind := range []string{"pi"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			s, e := New(dir, "/tmp/mcp")
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close()
			if kind == "pi" {
				if e := s.SaveRuntime(config.Runtime{Type: "pi", Binary: "/nonexistent/pi", SessionDir: filepath.Join(dir, "sessions")}); e != nil {
					t.Fatal(e)
				}
			}
			runtime := s.config.Runtime
			settings := s.config.General
			settings.TaskTimeoutSeconds = 1800
			settings.MaxContextBytes = 2 * 1024 * 1024
			if e := s.SaveSettings(settings); e != nil {
				t.Fatalf("saving general settings for %s: %v", kind, e)
			}
			persisted, e := config.Load(filepath.Join(dir, "config.toml"))
			if e != nil || persisted.General != settings || s.config.General != settings {
				t.Fatalf("settings did not persist: %+v %v", persisted.General, e)
			}
			if persisted.Runtime != runtime || s.State().RuntimeOwned {
				t.Fatalf("settings changed or started the runtime: %+v", persisted.Runtime)
			}
			codex, e := s.CodexConfig()
			if e != nil || !strings.Contains(codex, "tool_timeout_sec = 3615") {
				t.Fatalf("MCP timeout does not cover live task-timeout changes: %s %v", codex, e)
			}
			before, e := os.ReadFile(filepath.Join(dir, "config.toml"))
			if e != nil {
				t.Fatal(e)
			}
			for _, invalid := range []config.General{
				{LogLevel: "info", TaskTimeoutSeconds: 0, MaxContextBytes: 1048576},
				{LogLevel: "info", TaskTimeoutSeconds: 3601, MaxContextBytes: 1048576},
				{LogLevel: "info", TaskTimeoutSeconds: 1800, MaxContextBytes: 1023},
				{LogLevel: "info", TaskTimeoutSeconds: 1800, MaxContextBytes: 4194305},
			} {
				if e := s.SaveSettings(invalid); e == nil {
					t.Fatalf("invalid settings accepted: %+v", invalid)
				}
			}
			after, e := os.ReadFile(filepath.Join(dir, "config.toml"))
			if e != nil || string(before) != string(after) || s.config.General != settings {
				t.Fatalf("rejected settings changed saved values: %v", e)
			}
		})
	}
}
