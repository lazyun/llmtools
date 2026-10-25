package pi

import (
	"ai-worker-mcp/internal/agent"
	_ "embed"
	"os"
	"path/filepath"
)

// extensionSource is the llmTools-owned Pi bridge. It ships inside the binary
// and is materialised next to the run store, so a Pi checkout, a user
// extension directory, or a project-local .pi directory can never supply the
// tool surface a run executes against.
//
//go:embed extension/llmtools-control.ts
var extensionSource string

const extensionName = "llmtools-control.ts"

// materializeExtension writes the bridge into a directory only this user can
// enter and returns its absolute path. Content is compared first so repeated
// runs do not rewrite a file a live Pi process may still have open.
func materializeExtension(dir string) (string, error) {
	if dir == "" || !filepath.IsAbs(dir) {
		return "", agent.E("CONFIG_ERROR")
	}
	if e := os.MkdirAll(filepath.Join(dir, "extension"), 0o700); e != nil {
		return "", agent.E("RUNTIME_UNAVAILABLE")
	}
	path := filepath.Join(dir, "extension", extensionName)
	if current, e := os.ReadFile(path); e == nil && string(current) == extensionSource {
		return path, nil
	}
	// A previous build's file is read-only and may still be open in a live Pi
	// process, so the new bridge is written beside it and renamed into place:
	// the swap is atomic and never truncates a file another process reads.
	tmp, e := os.CreateTemp(filepath.Dir(path), ".bridge-*.ts")
	if e != nil {
		return "", agent.E("RUNTIME_UNAVAILABLE")
	}
	defer os.Remove(tmp.Name())
	if _, e = tmp.WriteString(extensionSource); e == nil {
		e = tmp.Chmod(0o400)
	}
	if cErr := tmp.Close(); e == nil {
		e = cErr
	}
	if e != nil {
		return "", agent.E("RUNTIME_UNAVAILABLE")
	}
	if e = os.Rename(tmp.Name(), path); e != nil {
		return "", agent.E("RUNTIME_UNAVAILABLE")
	}
	return path, nil
}
