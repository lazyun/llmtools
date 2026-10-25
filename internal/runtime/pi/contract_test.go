package pi

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestHealthProbeNeverAsksForAGeneration proves the passive-health freeze: the
// handshake may start Pi and speak its protocol, but it must never send a
// prompt, select a model or reach a provider.
func TestHealthProbeNeverAsksForAGeneration(t *testing.T) {
	r := fakeRuntimeSetup(t)
	dir := t.TempDir()
	commands, argv := filepath.Join(dir, "commands.jsonl"), filepath.Join(dir, "argv.txt")
	t.Setenv("FAKE_PI_LOG", commands)
	t.Setenv("FAKE_PI_ARGV", argv)
	if _, e := r.adapter.Health(context.Background()); e != nil {
		t.Fatal(e)
	}
	sent, e := os.ReadFile(commands)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(string(sent), `"type":"get_state"`) {
		t.Fatalf("health completed without a protocol round trip: %s", sent)
	}
	started, e := os.ReadFile(argv)
	if e != nil {
		t.Fatal(e)
	}
	// --offline is what makes "no provider traffic" a property of the launch
	// rather than a promise about the command list.
	for _, want := range []string{"--offline", "--no-session", "--no-tools", "--no-builtin-tools"} {
		if !strings.Contains(string(started), "\n"+want+"\n") {
			t.Fatalf("health probe launched without %s: %s", want, started)
		}
	}
	for _, forbidden := range []string{"--provider", "--model", "--api-key", `"type":"prompt"`} {
		if strings.Contains(string(started)+string(sent), forbidden) {
			t.Fatalf("health asked the runtime to select or serve a model (%s): %s / %s", forbidden, started, sent)
		}
	}
}
