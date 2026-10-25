package pi

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNpmShimResolvesLiteralNodeScriptWithoutShellExecution(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "npm prefix & spaces")
	target := filepath.Join(dir, "node_modules", "@earendil-works", "pi-coding-agent", "dist", "cli.js")
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("// fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	shim := filepath.Join(dir, "pi.cmd")
	source := `@ECHO off
SET dp0=%~dp0
"%_prog%" "%dp0%\node_modules\@earendil-works\pi-coding-agent\dist\cli.js" %*
`
	if err := os.WriteFile(shim, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := npmNodeTarget(shim)
	if err != nil || got != target {
		t.Fatalf("target %q: %v", got, err)
	}
	for _, body := range []string{"echo arbitrary batch content", source + source} {
		os.WriteFile(shim, []byte(body), 0600)
		if got, err := npmNodeTarget(shim); err == nil {
			t.Fatalf("ambiguous/non-npm batch accepted: %q", got)
		}
	}
}
