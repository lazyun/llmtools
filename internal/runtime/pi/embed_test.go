package pi

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The bridge is the tool surface a Pi run executes against, so it must be
// llmTools' own file, in a directory only this user can enter, and identical to
// what shipped inside the binary.
func TestMaterializeExtensionWritesTheBundledBridge(t *testing.T) {
	dir := t.TempDir()
	path, e := materializeExtension(dir)
	if e != nil {
		t.Fatal(e)
	}
	if filepath.Base(path) != extensionName {
		t.Fatalf("path: %q", path)
	}
	if !filepath.IsAbs(path) {
		t.Fatalf("Pi is launched with a relative extension path: %q", path)
	}
	body, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	if string(body) != extensionSource {
		t.Fatal("materialised bridge differs from the embedded source")
	}
	if info, e := os.Stat(path); e != nil || info.Mode().Perm() != 0o400 {
		t.Fatalf("bridge permissions: %v %v", info.Mode(), e)
	}
	if info, e := os.Stat(filepath.Dir(path)); e != nil || info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("extension directory is reachable by others: %v %v", info.Mode(), e)
	}
}

// A live Pi process may still hold the previous file open, so a rewrite swaps
// the path atomically instead of truncating, and an unchanged bridge is not
// rewritten at all.
func TestMaterializeExtensionIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	first, e := materializeExtension(dir)
	if e != nil {
		t.Fatal(e)
	}
	before, e := os.Stat(first)
	if e != nil {
		t.Fatal(e)
	}
	second, e := materializeExtension(dir)
	if e != nil {
		t.Fatal(e)
	}
	after, e := os.Stat(second)
	if e != nil {
		t.Fatal(e)
	}
	if first != second || !os.SameFile(before, after) {
		t.Fatalf("bridge rewritten on every run: %q/%q", first, second)
	}
	entries, e := os.ReadDir(filepath.Dir(first))
	if e != nil {
		t.Fatal(e)
	}
	if len(entries) != 1 {
		t.Fatalf("temporary bridge files left behind: %v", entries)
	}
}

// An unanchored directory would put the bridge wherever the process happens to
// be standing, so it is refused rather than resolved.
func TestMaterializeExtensionRefusesARelativeDirectory(t *testing.T) {
	for _, dir := range []string{"", "sessions", "../sessions"} {
		if _, e := materializeExtension(dir); codeOf(t, e) != "CONFIG_ERROR" {
			t.Fatalf("%q accepted: %v", dir, e)
		}
	}
}

// TestEmbeddedBridgeIsTheControlledSurface keeps the shipped bridge honest: the
// tool names Pi can register, the one process it may spawn, and the policy hook
// that denies anything else are asserted against the source that actually
// ships inside the binary.
func TestEmbeddedBridgeIsTheControlledSurface(t *testing.T) {
	for _, name := range bridgeToolNames {
		if !strings.Contains(extensionSource, `name: "`+name+`"`) {
			t.Fatalf("the bundled bridge never registers %s", name)
		}
	}
	// The only child process the bridge starts is llmTools' own control
	// entrypoint; no shell, no network client, no Pi-native capability appears.
	if !strings.Contains(extensionSource, `"--llmtools-pi-control", CONTROL_DB`) {
		t.Fatal("the bridge no longer routes repository reads through the llmTools gateway")
	}
	for _, forbidden := range []string{"shell:", "exec(", "fetch(", "writeFile", "edit("} {
		if strings.Contains(extensionSource, forbidden) {
			t.Fatalf("the bundled bridge reaches a native capability: %s", forbidden)
		}
	}
	// Reads exist only for a run whose frozen capability grants them, and every
	// call is denied again at tool_call time unless it is on the active list.
	if !strings.Contains(extensionSource, "if (REPO_READ) repoTools(pi);") {
		t.Fatal("repository reads are no longer gated by the run's capability")
	}
	if !strings.Contains(extensionSource, "ACTIVE.has(event.toolName)") || !strings.Contains(extensionSource, "block: true") {
		t.Fatal("the bridge stopped enforcing the tool allowlist at call time")
	}
	// The authority for a read is the injected per-run token, never a value the
	// model supplies and never the workspace root.
	if !strings.Contains(extensionSource, "control_token: CONTROL_TOKEN") {
		t.Fatal("the control token is no longer injected by the bridge")
	}
	if strings.Contains(extensionSource, "CanonicalRoot") || strings.Contains(extensionSource, "canonical_root") {
		t.Fatal("the bridge carries a workspace root as a credential")
	}
}
