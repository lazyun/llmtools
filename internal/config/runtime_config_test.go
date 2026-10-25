package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writePi is the documented minimal Pi document: a runtime selector plus the
// two paths Pi's lifecycle actually needs.
func piDocument(dir string) string {
	return "[runtime]\ntype = 'pi'\nbinary = '/usr/local/bin/pi'\nsession_dir = '" + filepath.Join(dir, "pi-sessions") + "'\n"
}

func TestPersistedConfigExpressesPi(t *testing.T) {
	dir := t.TempDir()
	c, e := Decode([]byte(piDocument(dir)))
	if e != nil {
		t.Fatal(e)
	}
	want := Runtime{Type: "pi", Binary: "/usr/local/bin/pi", SessionDir: filepath.Join(dir, "pi-sessions")}
	if c.Runtime != want {
		t.Fatalf("pi runtime: %+v want %+v", c.Runtime, want)
	}
	// A Pi section must not quietly keep OpenCode's inherited defaults: they read
	// like a live HTTP route for a runtime that has no endpoint.
	if c.Runtime.Mode != "" || c.Runtime.Host != "" || c.Runtime.BaseURL != "" || c.Runtime.Port != 0 {
		t.Fatalf("OpenCode transport survived a Pi decode: %+v", c.Runtime)
	}
	path := filepath.Join(dir, "config.toml")
	if e := Save(path, c); e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(path)
	text := string(b)
	// What the user did not write must not come back as a live value. port and
	// mode serialize their zero because every runtime shares one schema, and
	// Validate is what guarantees a zero really means "not an OpenCode route"
	// rather than an inherited default.
	for _, absent := range []string{"host", "base_url", "'managed'", "'external'"} {
		if strings.Contains(text, absent) {
			t.Fatalf("saved Pi configuration carries OpenCode state %s:\n%s", absent, text)
		}
	}
	if !strings.Contains(text, "mode = ''") || !strings.Contains(text, "port = 0") {
		t.Fatalf("Pi configuration did not save the inert OpenCode zeros its schema requires:\n%s", text)
	}
	reloaded, e := Load(path)
	if e != nil || reloaded.Runtime != want {
		t.Fatalf("Pi configuration did not round-trip: %+v %v", reloaded.Runtime, e)
	}
}

// TestLegacyConfigStillSelectsOpenCode is the backward-compatibility freeze:
// adding a second runtime may not move an existing user.
func TestDefaultIsPiAndExplicitLegacyConfigRemainsReadable(t *testing.T) {
	for name, doc := range map[string]string{
		"no runtime section":  "[general]\nlog_level = 'info'\n",
		"empty runtime table": "[runtime]\n",
		"explicit opencode":   "[runtime]\ntype = 'opencode'\nmode = 'managed'\nbinary = 'opencode'\nhost = '127.0.0.1'\n",
	} {
		c, e := Decode([]byte(doc))
		if e != nil {
			t.Fatalf("%s: %v", name, e)
		}
		want := "pi"
		if name == "explicit opencode" {
			want = "opencode"
		}
		if c.Runtime.Type != want {
			t.Fatalf("%s: type %q", name, c.Runtime.Type)
		}
	}
}

// TestUnknownRuntimeFailsClosed forbids the fallback in both directions: an
// unrecognized selector never runs OpenCode, and never runs Pi either.
func TestUnknownRuntimeFailsClosed(t *testing.T) {
	for _, doc := range []string{
		"[runtime]\ntype = 'foo'\n",
		"[runtime]\ntype = 'PI'\n",
		"[runtime]\ntype = ''\nbinary = 'pi'\nsession_dir = '/tmp/s'\n",
		"[runtime]\ntype = 'opencode'\nmode = 'pi'\n",
	} {
		if _, e := Decode([]byte(doc)); e == nil {
			t.Fatalf("accepted an unknown runtime selector:\n%s", doc)
		}
	}
}

func TestPiConfigRejectsIncompleteOrForeignSections(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]string{
		"no binary":       "[runtime]\ntype = 'pi'\nsession_dir = '" + dir + "'\n",
		"blank binary":    "[runtime]\ntype = 'pi'\nbinary = '  '\nsession_dir = '" + dir + "'\n",
		"no session dir":  "[runtime]\ntype = 'pi'\nbinary = 'pi'\n",
		"relative dir":    "[runtime]\ntype = 'pi'\nbinary = 'pi'\nsession_dir = 'sessions'\n",
		"unclean dir":     "[runtime]\ntype = 'pi'\nbinary = 'pi'\nsession_dir = '/tmp/./sessions'\n",
		"filesystem root": "[runtime]\ntype = 'pi'\nbinary = 'pi'\nsession_dir = '/'\n",
		"inherited mode":  "[runtime]\ntype = 'pi'\nbinary = 'pi'\nsession_dir = '" + dir + "'\nmode = 'managed'\n",
		"endpoint":        "[runtime]\ntype = 'pi'\nbinary = 'pi'\nsession_dir = '" + dir + "'\nbase_url = 'http://127.0.0.1:4096'\n",
		"port":            "[runtime]\ntype = 'pi'\nbinary = 'pi'\nsession_dir = '" + dir + "'\nport = 4096\n",
		"host":            "[runtime]\ntype = 'pi'\nbinary = 'pi'\nsession_dir = '" + dir + "'\nhost = '127.0.0.1'\n",
	}
	for name, doc := range cases {
		if _, e := Decode([]byte(doc)); e == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
}

// TestPiRoutingMayOmitTheAgentSelector covers a runtime without an agent
// catalogue, while an OpenCode route keeps its real agent name unchanged.
func TestPiRoutingMayOmitTheAgentSelector(t *testing.T) {
	c, e := Decode([]byte(piDocument(t.TempDir()) + "\n[agents.implementation]\nagent = ''\nmodel = 'huggingface/deepseek-ai/DeepSeek-V4-Flash'\n"))
	if e != nil {
		t.Fatal(e)
	}
	if got := c.Agents["implementation"]; got.Agent != "" || !got.Exposed || got.Model == "" {
		t.Fatalf("pi routing: %+v", got)
	}
	legacy, e := Decode([]byte("[agents.implementation]\nagent = 'build'\nmodel = ''\n"))
	if e != nil || legacy.Agents["implementation"].Agent != "build" {
		t.Fatalf("OpenCode routing changed: %+v %v", legacy.Agents, e)
	}
	if _, e := Decode([]byte("[agents.implementation]\nagent = 'not a name'\n")); e == nil {
		t.Fatal("malformed agent name accepted")
	}
}

// TestOpenCodeRuntimeJSONIsUnchanged protects the run-identity hash: a persisted
// run stores the digest of its runtime block, so a new field that serializes for
// OpenCode would orphan every historical run's continuation.
func TestOpenCodeRuntimeJSONIsUnchanged(t *testing.T) {
	b, e := json.Marshal(Runtime{Type: "opencode", Mode: "managed", Binary: "opencode", Host: "127.0.0.1"})
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(b), "session_dir") {
		t.Fatalf("Pi's field leaked into the OpenCode runtime identity: %s", b)
	}
	pi, e := json.Marshal(Runtime{Type: "pi", Binary: "pi", SessionDir: "/tmp/s"})
	if e != nil || !strings.Contains(string(pi), "session_dir") {
		t.Fatalf("Pi session directory is not persisted identity: %s %v", pi, e)
	}
}
