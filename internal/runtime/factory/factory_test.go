package factory

import (
	"ai-worker-mcp/internal/agent"
	"ai-worker-mcp/internal/config"
	"ai-worker-mcp/internal/runtime/pi"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// openCodeConfig is the legacy shape: a document that names no runtime at all.
func openCodeConfig(t *testing.T) config.Config {
	t.Helper()
	c, e := config.Decode([]byte("[runtime]\ntype='opencode'\n"))
	if e != nil {
		t.Fatal(e)
	}
	c.Directory = t.TempDir()
	return c
}

// piConfig is the supported Pi selection: the persisted file decides, and no
// environment variable is involved.
func piConfig(t *testing.T, binary string) config.Config {
	t.Helper()
	dir := t.TempDir()
	doc := "[runtime]\ntype = 'pi'\nbinary = '" + binary + "'\nsession_dir = '" + filepath.Join(dir, "pi-sessions") + "'\n"
	c, e := config.Decode([]byte(doc))
	if e != nil {
		t.Fatal(e)
	}
	c.Directory = dir
	return c
}

func TestFactorySelectsThePersistedRuntime(t *testing.T) {
	// The spike selector must not be what makes the formal route work.
	t.Setenv("LLMTOOLS_RUNTIME_SPIKE", "")

	oc := openCodeConfig(t)
	if r, e := New(oc); e == nil || r.Adapter != nil {
		t.Fatal("OpenCode production runtime accepted", e)
	}
	pr := piConfig(t, "/nonexistent/pi-under-test")
	rr, e := New(pr)
	if e != nil {
		t.Fatalf("pi selection: %v", e)
	}
	defer rr.Close()
	if _, isAdapter := rr.Adapter.(*pi.Adapter); !isAdapter {
		t.Fatalf("persisted runtime.type = \"pi\" built %T", rr.Adapter)
	}
	if got := rr.Adapter.RuntimeKind(); got != "pi" {
		t.Fatalf("runtime kind: %q", got)
	}
	if rr.Adapter.AgentCatalogueSupported() {
		t.Fatal("Pi claimed an agent catalogue it does not have")
	}
	// Pi's own files live under the configuration directory. A workspace is never
	// passed to the runtime, so nothing here can write into a user repository.
	if e := os.MkdirAll(pr.Runtime.SessionDir, 0o700); e != nil {
		t.Fatal(e)
	}
	if !strings.HasPrefix(pr.Runtime.SessionDir, pr.Directory) {
		t.Fatalf("Pi session tree escaped its configuration directory: %s", pr.Runtime.SessionDir)
	}
	if _, e := os.Stat(filepath.Join(pr.Directory, "pi-work")); e != nil {
		t.Fatalf("Pi working directory was not created from configuration: %v", e)
	}
}

// TestFactoryFailsClosedOnAnUnknownRuntime is the §9 rule: an unrecognized
// selector is an error, never a cue to run OpenCode anyway.
func TestFactoryFailsClosedOnAnUnknownRuntime(t *testing.T) {
	t.Setenv("LLMTOOLS_RUNTIME_SPIKE", "")
	for _, doc := range []string{
		"[runtime]\ntype = 'claude'\n",
		"[runtime]\ntype = 'Pi'\nbinary = 'pi'\nsession_dir = '/tmp/llmtools-factory-test'\n",
		"[runtime]\ntype = 'opencode'\nmode = 'pi'\n",
	} {
		c, e := config.Decode([]byte(doc))
		if e == nil {
			// A document the decoder already rejects cannot be built either.
			if _, e := New(c); e == nil {
				t.Fatalf("accepted %s", doc)
			}
			continue
		}
		c.Directory = t.TempDir()
		r, e := New(c)
		if e == nil || r.Adapter != nil {
			t.Fatalf("unknown runtime %s built an adapter: %v", doc, e)
		}
		if !strings.Contains(e.Error(), "CONFIG_ERROR") {
			t.Fatalf("unknown runtime %s failed as %v", doc, e)
		}
	}
	// The defensive branch matters only if Validate ever lets a type through.
	c := openCodeConfig(t)
	c.Runtime.Type = "unknown-but-valid"
	if r, e := New(c); e == nil || r.Adapter != nil {
		t.Fatalf("an unknown type reached dispatch and did not fail closed: %v", e)
	}
}

// TestMissingPiBinaryIsNotFoundNotAnOpenCodeFallback proves selection cannot
// degrade: an unusable Pi executable is reported about Pi.
func TestMissingPiBinaryIsNotFoundNotAnOpenCodeFallback(t *testing.T) {
	t.Setenv("LLMTOOLS_RUNTIME_SPIKE", "")
	r, e := New(piConfig(t, "/nonexistent/pi-under-test"))
	if e != nil {
		t.Fatal(e)
	}
	defer r.Close()
	if _, e := r.Adapter.Health(context.Background()); e == nil {
		t.Fatal("a missing runtime reported itself healthy")
	} else if code := safeCode(e); code != "RUNTIME_NOT_FOUND" {
		t.Fatalf("missing Pi binary reported as %s", code)
	}
	// The adapter is still Pi's: no OpenCode server was substituted underneath.
	if _, isPi := r.Adapter.(*pi.Adapter); !isPi {
		t.Fatalf("fallback replaced the selected runtime with %T", r.Adapter)
	}
}

// TestSpikeOverrideRemainsATestRoute keeps the environment selector working for
// the existing harness without letting it silently retarget an unknown name.
func TestSpikeOverrideRemainsATestRoute(t *testing.T) {
	t.Setenv("LLMTOOLS_RUNTIME_SPIKE", "pi")
	t.Setenv("LLMTOOLS_PI_BINARY", "/nonexistent/pi-override")
	base := t.TempDir()
	t.Setenv("LLMTOOLS_PI_SESSION_DIR", base)
	r, e := New(piConfig(t, "/nonexistent/pi-config"))
	if e != nil {
		t.Fatal(e)
	}
	defer r.Close()
	a, isPi := r.Adapter.(*pi.Adapter)
	if !isPi {
		t.Fatalf("override built %T", r.Adapter)
	}
	if _, e := a.Health(context.Background()); safeCode(e) != "RUNTIME_NOT_FOUND" {
		t.Fatalf("override health: %v", e)
	}
	// The override relocated the session tree instead of inheriting the
	// OpenCode binary the surrounding configuration still names.
	if _, e := os.Stat(filepath.Join(base, "sessions")); e != nil {
		t.Fatalf("spike session directory: %v", e)
	}

	t.Setenv("LLMTOOLS_RUNTIME_SPIKE", "gemini")
	if rr, e := New(piConfig(t, "/nonexistent/pi-config")); e == nil || rr.Adapter != nil {
		t.Fatalf("an unknown override ran the formal route: %v", e)
	}
}

func safeCode(e error) string {
	if e == nil {
		return ""
	}
	return agent.Safe(e).Code
}
