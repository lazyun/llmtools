package pi

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Covers version detection, the RPC handshake and model discovery when Pi's
// interpreter is installed beside its shim but absent from the GUI PATH.
func TestPiLaunchFindsAdjacentInterpreterOutsidePATH(t *testing.T) {
	t.Setenv("PATH", "/usr/bin:/bin")
	t.Setenv("FAKE_PI_MODEL_TABLE", "provider model\nopenai gpt-5-mini\n")
	binary := fakePi(t)
	interpreter := filepath.Join(filepath.Dir(binary), "llmtools-pi-test-interpreter")
	if e := os.Symlink("/bin/sh", interpreter); e != nil {
		t.Fatal(e)
	}
	script := strings.Replace(fakePiScript, "#!/bin/sh", "#!/usr/bin/env llmtools-pi-test-interpreter", 1)
	if e := os.WriteFile(binary, []byte(script), 0o700); e != nil {
		t.Fatal(e)
	}
	dir := t.TempDir()
	adapter, e := New(Spec{Binary: binary, ControlDB: filepath.Join(dir, "db"), SessionDir: filepath.Join(dir, "sessions")})
	if e != nil {
		t.Fatal(e)
	}
	defer adapter.Close()
	if health, e := adapter.Health(context.Background()); e != nil || !health.Healthy {
		t.Fatalf("GUI health: %+v %v", health, e)
	}
	if models, e := adapter.ListModels(context.Background()); e != nil || len(models) != 1 || models[0].ID != "openai/gpt-5-mini" {
		t.Fatalf("GUI models: %+v %v", models, e)
	}
	if path := os.Getenv("PATH"); path != "/usr/bin:/bin" {
		t.Fatalf("parent PATH changed: %s", path)
	}
}
