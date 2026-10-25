package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigrationNeverReadsOrCopiesSecrets(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	old := []byte("[general]\nlog_level='warn'\nmax_context_bytes=4096\n[providers.old]\napi_key_env='SECRET_TEST'\nbase_url='https://provider.invalid'\n[roles.implementer]\nsystem_prompt='old'\n")
	if e := os.WriteFile(path, old, 0600); e != nil {
		t.Fatal(e)
	}
	secret := filepath.Join(dir, "secrets.toml")
	if e := os.WriteFile(secret, []byte("not even valid TOML"), 0600); e != nil {
		t.Fatal(e)
	}
	c, e := Load(path)
	if e != nil || len(c.Warnings) != 1 || c.General.LogLevel != "warn" || len(c.Agents) != 0 {
		t.Fatal(c, e)
	}
	if e = Save(path, c); e != nil {
		t.Fatal(e)
	}
	data, _ := os.ReadFile(secret)
	if string(data) != "not even valid TOML" {
		t.Fatal("legacy secret touched")
	}
	data, _ = os.ReadFile(path)
	if string(data) == string(old) {
		t.Fatal("migration not saved")
	}
	if _, e = Load(path); e != nil {
		t.Fatal(e)
	}
}
func TestUnsafeConfigRejected(t *testing.T) {
	for _, url := range []string{"https://example.com", "http://user:pass@localhost:4096", "http://127.0.0.1:4096/path", "http://127.0.0.1:4096?secret=x"} {
		c := Default()
		c.Runtime.Mode = "external"
		c.Runtime.BaseURL = url
		if c.Validate() == nil {
			t.Fatal(url)
		}
	}
	if _, e := Decode([]byte("[runtime]\nunknown='x'")); e == nil {
		t.Fatal("unknown field accepted")
	}
}

func TestManagedLegacyEndpointIsNotSaved(t *testing.T) {
	c := Default()
	c.Runtime = Runtime{Type: "opencode", Mode: "managed", Binary: "opencode", Host: "127.0.0.1"}
	c.Runtime.BaseURL = "http://127.0.0.1:4096"
	path := filepath.Join(t.TempDir(), "config.toml")
	if e := Save(path, c); e != nil {
		t.Fatal(e)
	}
	loaded, e := Load(path)
	if e != nil || loaded.Runtime.Port != 0 || loaded.Runtime.Host != "127.0.0.1" || loaded.Runtime.BaseURL != "" {
		t.Fatal(loaded, e)
	}
}

func TestLegacyAndExplicitAgentRouting(t *testing.T) {
	legacy, e := Decode([]byte("[agents]\nimplementation='build'\n"))
	if e != nil || legacy.Agents["implementation"] != (AgentRouting{Exposed: true, Agent: "build"}) {
		t.Fatal(legacy, e)
	}
	explicit, e := Decode([]byte("[agents.implementation]\nagent='build'\nmodel='omni_router/gpt-5.6-terra'\n"))
	if e != nil || explicit.Agents["implementation"] != (AgentRouting{Exposed: true, Agent: "build", Model: "omni_router/gpt-5.6-terra"}) {
		t.Fatal(explicit, e)
	}
	if nested, nestedErr := Decode([]byte("[agents.research]\nagent='explore'\nmodel='gateway/openai/gpt-5.6-luna'\n")); nestedErr != nil || nested.Agents["research"].Model != "gateway/openai/gpt-5.6-luna" {
		t.Fatal(nested, nestedErr)
	}
	path := filepath.Join(t.TempDir(), "config.toml")
	if e = Save(path, legacy); e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "[agents.implementation]") || !strings.Contains(string(b), "agent = 'build'") {
		t.Fatalf("legacy routing was not canonically saved: %s", b)
	}
}

func TestAgentExposureMigrationAndExplicitHidden(t *testing.T) {
	old, e := Decode([]byte("[agents.review]\nagent='build'\nmodel=''\n"))
	if e != nil || !old.Agents["review"].Exposed {
		t.Fatal(old, e)
	}
	hidden, e := Decode([]byte("[agents.research]\nexposed=false\nagent='explore'\nmodel=''\n"))
	if e != nil || hidden.Agents["research"].Exposed {
		t.Fatal(hidden, e)
	}
	if _, exists := hidden.Agents["implementation"]; exists {
		t.Fatal("absent role was created")
	}
}
