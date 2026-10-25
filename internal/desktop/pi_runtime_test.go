package desktop

import (
	"ai-worker-mcp/internal/config"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDesktopSupportsPiRuntimeConfiguration verifies that Desktop can read and
// write Pi runtime configurations and probe Pi, while OpenCode-specific
// operations (agent discovery, server start) remain refused when Pi is active.
func TestDesktopSupportsPiRuntimeConfiguration(t *testing.T) {
	dir := t.TempDir()
	sessionDir := filepath.Join(dir, "pi-sessions")
	path := filepath.Join(dir, "config.toml")
	body := []byte("[runtime]\ntype = 'pi'\nbinary = '/usr/local/bin/pi'\nsession_dir = '" + sessionDir + "'\n")
	if e := os.WriteFile(path, body, 0o600); e != nil {
		t.Fatal(e)
	}
	s, e := New(dir, "/tmp/mcp")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	ctx := context.Background()

	state := s.State()
	if state.Config.Runtime.Type != "pi" || state.Config.Runtime.Binary != "/usr/local/bin/pi" {
		t.Fatalf("Pi configuration was not loaded: %+v", state.Config.Runtime)
	}
	if state.Config.Runtime.SessionDir != sessionDir {
		t.Fatalf("Pi session_dir was not loaded: %q", state.Config.Runtime.SessionDir)
	}
	// No OpenCode snapshot is presented for a Pi configuration.
	if state.RuntimeOwned || state.Runtime.Health != "" || state.Runtime.Instance.Type != "" {
		t.Fatalf("Desktop presented OpenCode runtime metadata for a Pi configuration: %+v", state)
	}
	// No "read-only" warning: Pi is now configurable from Desktop.
	for _, w := range state.Config.Warnings {
		if w == "CONFIG_ERROR: Desktop manages the OpenCode runtime only; this configuration is read-only here." {
			t.Fatalf("unexpected read-only warning for Pi: %v", state.Config.Warnings)
		}
	}

	// SaveRuntime accepts a Pi configuration.
	if err := s.SaveRuntime(config.Runtime{Type: "pi", Binary: "/usr/local/bin/pi", SessionDir: sessionDir}); err != nil {
		t.Fatalf("SaveRuntime refused a valid Pi configuration: %v", err)
	}

	// OpenCode is no longer a configurable runtime.
	if err := s.SaveRuntime(config.Runtime{Type: "opencode", Mode: "managed", Binary: "opencode", Host: "127.0.0.1"}); err == nil {
		t.Fatal("SaveRuntime accepted OpenCode")
	}
	// And back to Pi.
	if err := s.SaveRuntime(config.Runtime{Type: "pi", Binary: "/usr/local/bin/pi", SessionDir: sessionDir}); err != nil {
		t.Fatalf("SaveRuntime refused switching back to Pi: %v", err)
	}

	// SaveRuntime rejects an unknown runtime type.
	if err := s.SaveRuntime(config.Runtime{Type: "unknown"}); err == nil {
		t.Fatal("SaveRuntime accepted an unknown runtime type")
	}

	// OpenCode-specific operations remain refused while Pi is configured.
	if err := s.StartRuntime(ctx); err == nil {
		t.Fatal("StartRuntime should not work for Pi")
	}
	if _, err := s.DiscoverAgents(ctx); err == nil {
		t.Fatal("DiscoverAgents reached the OpenCode catalogue for a Pi configuration")
	}
	if _, err := s.DiscoverRouting(ctx); err == nil {
		t.Fatal("Pi discovery accepted a missing executable")
	}
	probe := s.ProbeRuntime(ctx)
	if len(probe) != 1 || probe[0].Name != "Pi protocol" || probe[0].Code != "RUNTIME_NOT_FOUND" {
		t.Fatalf("Pi probe did not report its missing executable: %+v", probe)
	}

	// Diagnostics must not observe the OpenCode server for a Pi configuration.
	for _, c := range s.Diagnostics(ctx) {
		if c.Name == "Runtime connection" || c.Name == "Runtime health" || c.Name == "Agent discovery" {
			t.Fatalf("Diagnostics observed the OpenCode server for a Pi configuration: %+v", c)
		}
	}
}

// The Pi check is a transport probe, never a prompt or a persistent server.
func TestDesktopPiProbeUsesOfflineRPC(t *testing.T) {
	t.Setenv("LLMTOOLS_RUNTIME_SPIKE", "")
	dir := t.TempDir()
	binary := filepath.Join(dir, "pi")
	argv := filepath.Join(dir, "argv")
	commands := filepath.Join(dir, "commands")
	t.Setenv("PI_PROBE_ARGV", argv)
	t.Setenv("PI_PROBE_COMMANDS", commands)
	script := `#!/bin/sh
if [ "$1" = "--version" ]; then echo "1.0.2-test"; exit 0; fi
printf '%s\n' "$@" > "$PI_PROBE_ARGV"
while IFS= read -r line; do
 printf '%s\n' "$line" >> "$PI_PROBE_COMMANDS"
 id=$(printf '%s' "$line" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')
 printf '{"type":"response","id":"%s","success":true,"data":{"sessionId":"probe-session"}}\n' "$id"
done
`
	if e := os.WriteFile(binary, []byte(script), 0o700); e != nil {
		t.Fatal(e)
	}
	s, e := New(dir, "/tmp/mcp")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if e := s.SaveRuntime(config.Runtime{Type: "pi", Binary: binary, SessionDir: filepath.Join(dir, "sessions")}); e != nil {
		t.Fatal(e)
	}
	probe := s.ProbeRuntime(context.Background())
	if len(probe) != 1 || probe[0].Name != "Pi protocol" || probe[0].Status != "PASS" || !strings.Contains(probe[0].Message, "1.0.2-test") {
		t.Fatalf("Pi probe failed: %+v", probe)
	}
	args, e := os.ReadFile(argv)
	if e != nil {
		t.Fatal(e)
	}
	for _, flag := range []string{"--offline", "--no-session", "--no-tools", "--no-builtin-tools", "--no-extensions"} {
		if !strings.Contains("\n"+string(args), "\n"+flag+"\n") {
			t.Fatalf("probe omitted %s: %s", flag, args)
		}
	}
	body, e := os.ReadFile(commands)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(string(body), `"type":"get_state"`) || strings.Contains(string(body), `"type":"prompt"`) {
		t.Fatalf("unexpected commands: %s", body)
	}
	state := s.State()
	if state.RuntimeOwned || state.Runtime.Instance.Type != "" {
		t.Fatalf("Pi was presented as an OpenCode server: %+v", state.Runtime)
	}
}

// TestDesktopFindBinary verifies the auto-detect helper searches PATH.
func TestDesktopFindBinary(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	body := []byte("[runtime]\ntype = 'opencode'\nmode = 'managed'\nbinary = 'opencode'\nhost = '127.0.0.1'\nport = 0\n")
	if e := os.WriteFile(path, body, 0o600); e != nil {
		t.Fatal(e)
	}
	s, e := New(dir, "/tmp/mcp")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()

	// "sh" is always available on macOS/Linux.
	found := s.FindBinary("sh")
	if found == "" {
		t.Fatal("FindBinary did not locate 'sh'")
	}
	if !filepath.IsAbs(found) {
		t.Fatalf("FindBinary returned a non-absolute path: %s", found)
	}

	// A non-existent binary returns empty.
	if got := s.FindBinary("definitely-not-a-real-binary-xyz"); got != "" {
		t.Fatalf("FindBinary found a non-existent binary: %s", got)
	}

	// Empty name returns empty.
	if got := s.FindBinary(""); got != "" {
		t.Fatalf("FindBinary returned something for empty name: %s", got)
	}
}

// Pi routes logical roles directly to models without a named agent selector.
func desktopPiModels(t *testing.T) (*Service, string) {
	t.Helper()
	t.Setenv("LLMTOOLS_RUNTIME_SPIKE", "")
	dir := t.TempDir()
	binary := filepath.Join(dir, "pi")
	argv := filepath.Join(dir, "argv")
	t.Setenv("PI_DESKTOP_ARGV", argv)
	t.Setenv("PI_DESKTOP_MODELS", "provider model context max-out thinking images\nhuggingface deepseek-ai/DeepSeek-V4-Flash 1M 384K yes no\nopenai gpt-5-mini 400K 128K yes no")
	script := `#!/bin/sh
printf '%s\n' "$@" >> "$PI_DESKTOP_ARGV"
if [ "$1" = "--offline" ] && [ "$2" = "--list-models" ]; then
 [ "$PI_DESKTOP_FAIL" = "1" ] && exit 3
 printf '%s\n' "$PI_DESKTOP_MODELS"
 exit 0
fi
exit 9
`
	if e := os.WriteFile(binary, []byte(script), 0o700); e != nil {
		t.Fatal(e)
	}
	s, e := New(dir, "/tmp/mcp")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(s.Close)
	if e = s.SaveRuntime(config.Runtime{Type: "pi", Binary: binary, SessionDir: filepath.Join(dir, "sessions")}); e != nil {
		t.Fatal(e)
	}
	return s, argv
}

func TestDesktopPiModelRouting(t *testing.T) {
	s, argv := desktopPiModels(t)
	ctx := context.Background()
	discovery, e := s.DiscoverRouting(ctx)
	if e != nil || len(discovery.Agents) != 0 || len(discovery.Models) != 2 {
		t.Fatalf("Pi catalogue: %+v %v", discovery, e)
	}
	model := "huggingface/deepseek-ai/DeepSeek-V4-Flash"
	if discovery.Models[0].ID != model {
		t.Fatalf("model identity changed: %+v", discovery.Models)
	}
	routes := map[string]config.AgentRouting{
		"implementation": {Exposed: true, Model: model},
		"review":         {Exposed: true, Model: "openai/gpt-5-mini"},
		"research":       {Exposed: false},
	}
	if e := s.SaveRoutings(ctx, routes); e != nil {
		t.Fatal(e)
	}
	persisted, e := config.Load(filepath.Join(s.config.Directory, "config.toml"))
	if e != nil {
		t.Fatal(e)
	}
	for role, want := range routes {
		if got := persisted.Agents[role]; got != want {
			t.Fatalf("%s: %+v, want %+v", role, got, want)
		}
	}
	before, e := os.ReadFile(filepath.Join(s.config.Directory, "config.toml"))
	if e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct {
		route config.AgentRouting
		code  string
	}{
		{config.AgentRouting{Exposed: true, Model: "openai/missing"}, "MODEL_NOT_AVAILABLE"},
		{config.AgentRouting{Exposed: true}, "MODEL_NOT_AVAILABLE"},
		{config.AgentRouting{Exposed: true, Agent: "build", Model: model}, "AGENT_NOT_FOUND"},
	} {
		e := s.SaveRoutings(ctx, map[string]config.AgentRouting{"implementation": tc.route})
		if e == nil || !strings.Contains(e.Error(), tc.code) {
			t.Fatalf("invalid route: %v, want %s", e, tc.code)
		}
	}
	after, e := os.ReadFile(filepath.Join(s.config.Directory, "config.toml"))
	if e != nil || string(before) != string(after) {
		t.Fatalf("failed save modified configuration: %v", e)
	}
	if e := s.DeleteMapping("review"); e != nil {
		t.Fatal(e)
	}
	if _, ok := s.State().Config.Agents["review"]; ok {
		t.Fatal("Pi route was not deleted")
	}
	args, e := os.ReadFile(argv)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(args), "--mode") || strings.Contains(string(args), "--version") {
		t.Fatalf("model discovery launched a task or health probe: %s", args)
	}
	if s.State().RuntimeOwned {
		t.Fatal("Pi routing started an OpenCode server")
	}
}

func TestDesktopPiModelDiscoveryFailurePreservesRouting(t *testing.T) {
	for _, name := range []string{"empty catalogue", "command failure"} {
		t.Run(name, func(t *testing.T) {
			s, _ := desktopPiModels(t)
			before, e := os.ReadFile(filepath.Join(s.config.Directory, "config.toml"))
			if e != nil {
				t.Fatal(e)
			}
			if name == "empty catalogue" {
				t.Setenv("PI_DESKTOP_MODELS", "provider model")
			} else {
				t.Setenv("PI_DESKTOP_FAIL", "1")
			}
			if _, e := s.DiscoverRouting(context.Background()); e == nil {
				t.Fatal("unusable model catalogue accepted")
			}
			if e := s.SaveRoutings(context.Background(), map[string]config.AgentRouting{"review": {Exposed: true}}); e == nil {
				t.Fatal("routes saved without a model catalogue")
			}
			after, e := os.ReadFile(filepath.Join(s.config.Directory, "config.toml"))
			if e != nil || string(before) != string(after) {
				t.Fatalf("failed discovery changed configuration: %v", e)
			}
		})
	}
}

// Opt-in verification uses the installed Pi catalogue without any model call.
// Both routing and sessions are isolated from the user's live configuration.
func TestDesktopPiInstalledCatalogueRouting(t *testing.T) {
	binary := os.Getenv("LLMTOOLS_TEST_PI")
	if binary == "" {
		t.Skip("set LLMTOOLS_TEST_PI for an installed Pi offline check")
	}
	t.Setenv("LLMTOOLS_RUNTIME_SPIKE", "")
	t.Setenv("PATH", "/usr/bin:/bin")
	dir := t.TempDir()
	s, e := New(dir, "/tmp/mcp")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if e := s.SaveRuntime(config.Runtime{Type: "pi", Binary: binary, SessionDir: filepath.Join(dir, "sessions")}); e != nil {
		t.Fatal(e)
	}
	discovery, e := s.DiscoverRouting(context.Background())
	if e != nil || len(discovery.Agents) != 0 || len(discovery.Models) == 0 {
		t.Fatalf("installed catalogue: %d models, %v", len(discovery.Models), e)
	}
	model := os.Getenv("LLMTOOLS_TEST_PI_MODEL")
	if model == "" {
		model = discovery.Models[0].ID
	}
	if e := s.SaveRoutings(context.Background(), map[string]config.AgentRouting{"implementation": {Exposed: true, Model: model}}); e != nil {
		t.Fatal(e)
	}
	persisted, e := config.Load(filepath.Join(dir, "config.toml"))
	if e != nil || persisted.Agents["implementation"].Model != model {
		t.Fatalf("installed model did not persist: %v", e)
	}
	t.Logf("Discovered %d Pi models and persisted %s with a GUI-style PATH", len(discovery.Models), model)
}
