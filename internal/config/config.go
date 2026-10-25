package config

import (
	"bytes"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"ai-worker-mcp/internal/localfs"
	"github.com/pelletier/go-toml/v2"
)

type General struct {
	LogLevel           string `toml:"log_level" json:"log_level"`
	TaskTimeoutSeconds int    `toml:"task_timeout_seconds" json:"task_timeout_seconds"`
	MaxContextBytes    int    `toml:"max_context_bytes" json:"max_context_bytes"`
}

const MaxTaskTimeoutSeconds = 3600

// Runtime describes the selected runtime. Type is the only field that chooses
// behaviour: OpenCode reads Mode/Host/BaseURL/Port as its transport settings,
// while Pi reads Binary/SessionDir and has no transport endpoint at all. The
// fields belonging to the other runtime are inert, never guessed at, and never
// used to infer which runtime is configured.
type Runtime struct {
	Type       string `toml:"type" json:"type"`
	Mode       string `toml:"mode" json:"mode"`
	Binary     string `toml:"binary" json:"binary"`
	Host       string `toml:"host,omitempty" json:"host"`
	BaseURL    string `toml:"base_url,omitempty" json:"base_url"`
	Port       int    `toml:"port" json:"port"`
	SessionDir string `toml:"session_dir,omitempty" json:"session_dir,omitempty"`
}
type Storage struct {
	Database string `toml:"database" json:"database"`
}
type AgentRouting struct {
	Exposed bool   `toml:"exposed" json:"exposed"`
	Agent   string `toml:"agent" json:"agent"`
	Model   string `toml:"model" json:"model"`
}
type Config struct {
	General   General                 `toml:"general" json:"general"`
	Runtime   Runtime                 `toml:"runtime" json:"runtime"`
	Agents    map[string]AgentRouting `toml:"agents" json:"agents"`
	Storage   Storage                 `toml:"storage" json:"storage"`
	Directory string                  `toml:"-" json:"-"`
	Warnings  []string                `toml:"-" json:"warnings"`
}

var Name = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,63}$`)

func Default() Config {
	dir, _ := localfs.DefaultDirectory()
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".llmtools")
	}
	return DefaultAt(dir)
}

func DefaultAt(dir string) Config {
	return Config{General: General{"info", 180, 1048576}, Runtime: Runtime{Type: "pi", Binary: "pi", SessionDir: filepath.Join(dir, "pi-sessions")}, Agents: map[string]AgentRouting{}, Storage: Storage{"llmtools.db"}}
}
func (c Config) Validate() error {
	bad := errors.New("CONFIG_ERROR: invalid runtime configuration")
	if c.Runtime.Port < 0 || c.Runtime.Port > 65535 {
		return bad
	}
	switch c.Runtime.Type {
	case "opencode":
		if c.Runtime.Mode != "managed" && c.Runtime.Mode != "external" {
			return bad
		}
		if c.Runtime.Mode == "managed" && (c.Runtime.Binary == "" || (c.Runtime.Host != "" && c.Runtime.Host != "127.0.0.1")) {
			return bad
		}
		if c.Runtime.Mode == "external" {
			u, e := url.Parse(c.Runtime.BaseURL)
			if e != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost" && u.Hostname() != "::1") {
				return errors.New("CONFIG_ERROR: external runtime must use a loopback HTTP URL without credentials")
			}
		}
	case "pi":
		// Pi is a per-turn process, not an HTTP server, so it has no transport
		// endpoint to configure. A written-out zero is the shape this program's own
		// serializer produces; anything else would make the file read like a live
		// server route that never starts.
		if c.Runtime.Binary == "" || c.Runtime.Mode != "" || c.Runtime.Host != "" || c.Runtime.BaseURL != "" || c.Runtime.Port != 0 {
			return errors.New(`CONFIG_ERROR: runtime.type = "pi" sets no OpenCode transport fields`)
		}
		if !filepath.IsAbs(c.Runtime.SessionDir) || filepath.Clean(c.Runtime.SessionDir) != c.Runtime.SessionDir || c.Runtime.SessionDir == string(filepath.Separator) {
			return errors.New(`CONFIG_ERROR: runtime.type = "pi" requires an absolute, canonical runtime.session_dir`)
		}
	default:
		// An unknown runtime is a configuration error, not a cue to run something
		// else. Falling back to OpenCode here could execute a task on a runtime the
		// user explicitly stopped selecting.
		return bad
	}
	if c.General.TaskTimeoutSeconds < 1 || c.General.TaskTimeoutSeconds > MaxTaskTimeoutSeconds || c.General.MaxContextBytes < 1024 || c.General.MaxContextBytes > 4194304 {
		return bad
	}
	if c.General.LogLevel != "debug" && c.General.LogLevel != "info" && c.General.LogLevel != "warn" && c.General.LogLevel != "error" {
		return bad
	}
	if c.Storage.Database == "" || filepath.Base(c.Storage.Database) != c.Storage.Database || c.Storage.Database == "." || c.Storage.Database == ".." {
		return bad
	}
	for k, v := range c.Agents {
		if !Name.MatchString(k) || !validAgent(v.Agent) || !validModel(v.Model) {
			return bad
		}
	}
	return nil
}

// validAgent accepts a runtime-native agent name, or none at all. A runtime
// without an agent catalogue has no selector to name, so an empty agent is a
// valid route rather than a broken one; runtimes that do have a catalogue still
// reject it when the route is resolved.
func validAgent(agent string) bool { return agent == "" || Name.MatchString(agent) }
func Decode(b []byte) (Config, error) {
	c := Default()
	if len(b) > 65536 {
		return c, errors.New("CONFIG_ERROR: configuration too large")
	}
	var raw map[string]any
	if toml.Unmarshal(b, &raw) != nil {
		return c, errors.New("CONFIG_ERROR: invalid TOML")
	}
	// Decode old files for migration/history only. Production factory never
	// constructs an OpenCode runtime from these values.
	rt, _ := raw["runtime"].(map[string]any)
	legacy := rt["type"] == "opencode"
	if rt["type"] == nil {
		for _, key := range []string{"mode", "host", "base_url", "port"} {
			if !inertValue(rt[key]) {
				legacy = true
			}
		}
		if rt["binary"] == "opencode" {
			legacy = true
		}
		if agents, ok := raw["agents"].(map[string]any); ok {
			for _, value := range agents {
				if name, ok := value.(string); ok && name != "" {
					legacy = true
				}
				if route, ok := value.(map[string]any); ok {
					if name, _ := route["agent"].(string); name != "" {
						legacy = true
					}
				}
			}
		}
	}
	if legacy {
		c.Runtime = Runtime{Type: "opencode", Mode: "managed", Binary: "opencode", Host: "127.0.0.1"}
	}
	if raw["providers"] != nil || raw["roles"] != nil { // Explicit one-way migration; never read secrets.toml.
		c.Warnings = []string{"Legacy direct-provider configuration detected. Provider/model/credential/role settings are ignored. Configure Pi separately and select Pi models; existing secrets.toml is untouched."}
		migrateGeneral := func(g map[string]any) {
			if l, ok := g["log_level"].(string); ok {
				c.General.LogLevel = l
			}
			if n, ok := g["max_context_bytes"].(int64); ok && n >= 1024 && n <= 4194304 {
				c.General.MaxContextBytes = int(n)
			}
			if n, ok := g["request_timeout_seconds"].(int64); ok && n >= 1 && n <= 3600 {
				c.General.TaskTimeoutSeconds = int(n)
			}
		}
		migrateGeneral(raw)
		if g, ok := raw["general"].(map[string]any); ok {
			migrateGeneral(g)
		}
	} else {
		// Existing configured roles migrate to exposed=true. Missing roles remain
		// absent and therefore are not exposed.
		if agents, ok := raw["agents"].(map[string]any); ok {
			for role, value := range agents {
				if name, legacy := value.(string); legacy {
					agents[role] = map[string]any{"exposed": true, "agent": name, "model": ""}
				} else if routing, structured := value.(map[string]any); structured {
					if _, present := routing["exposed"]; !present {
						routing["exposed"] = true
					}
				}
			}
		}
		normalized, marshalErr := toml.Marshal(raw)
		if marshalErr != nil || toml.NewDecoder(bytes.NewReader(normalized)).DisallowUnknownFields().Decode(&c) != nil {
			return c, errors.New("CONFIG_ERROR: unsupported configuration fields")
		}
	}
	if e := c.normalizeRuntime(raw); e != nil {
		return c, e
	}
	if c.Runtime.Type == "opencode" {
		c.Warnings = append(c.Warnings, "OpenCode is no longer supported. Save a Pi runtime configuration, select Pi models and re-enable roles. Historical task records are retained.")
	}
	return c, c.Validate()
}

// normalizeRuntime applies the defaults a partial document cannot express. Every
// field decodes onto Default(), so a runtime that owns fewer fields than another
// must shed the values it did not set instead of inheriting them: a Pi section
// that quietly kept binary = "opencode" or mode = "managed" would launch, or
// appear to launch, the wrong runtime.
func (c *Config) normalizeRuntime(raw map[string]any) error {
	if c.Runtime.Type != "pi" {
		return nil
	}
	rt, _ := raw["runtime"].(map[string]any)
	if len(rt) == 0 {
		return nil
	}
	// The serializer writes every field that has no TOML omitempty, so a Pi file
	// this program saved itself carries the OpenCode transport keys at their zero
	// values. Only a value that actually describes an endpoint can route work to
	// OpenCode, so that is the case that fails closed.
	for _, key := range []string{"mode", "host", "base_url", "port"} {
		v, present := rt[key]
		if present && !inertValue(v) {
			return errors.New(`CONFIG_ERROR: runtime.type = "pi" cannot set the OpenCode transport field runtime.` + key)
		}
	}
	p := Runtime{Type: "pi"}
	for _, key := range []string{"binary", "session_dir"} {
		v, ok := rt[key].(string)
		if !ok || strings.TrimSpace(v) == "" {
			return errors.New(`CONFIG_ERROR: runtime.type = "pi" requires a non-empty runtime.` + key)
		}
		if key == "binary" {
			p.Binary = v
		} else {
			p.SessionDir = v
		}
	}
	c.Runtime = p
	return nil
}

// inertValue reports whether a decoded TOML value is a zero: the shape our own
// writer produces for a field the runtime does not use.
func inertValue(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return x == ""
	case bool:
		return !x
	case int:
		return x == 0
	case int64:
		return x == 0
	case float64:
		return x == 0
	default:
		return false
	}
}

func validModel(model string) bool {
	if model == "" || len(model) > 256 || !strings.Contains(model, "/") {
		return model == ""
	}
	for _, part := range strings.Split(model, "/") {
		if !Name.MatchString(part) {
			return false
		}
	}
	return true
}
func Load(path string) (Config, error) {
	absolute, e := filepath.Abs(path)
	if e != nil {
		return Config{}, errors.New("CONFIG_ERROR: invalid configuration path")
	}
	path = absolute
	b, e := localfs.ReadFile(path)
	if e != nil {
		return Config{}, errors.New("CONFIG_ERROR: cannot read configuration")
	}
	c, e := Decode(b)
	c.Directory = filepath.Dir(path)
	if c.Runtime.Type == "pi" && !bytes.Contains(b, []byte("session_dir")) {
		c.Runtime.SessionDir = filepath.Join(c.Directory, "pi-sessions")
	}
	return c, e
}
func Save(path string, c Config) error {
	if c.Runtime.Mode == "managed" {
		c.Runtime.BaseURL = ""
		c.Runtime.Host = "127.0.0.1"
	}
	if e := c.Validate(); e != nil {
		return e
	}
	b, e := toml.Marshal(c)
	if e != nil {
		return errors.New("CONFIG_ERROR: cannot encode configuration")
	}
	return localfs.AtomicWrite(path, b)
}
func Ensure(dir string) (Config, error) {
	if e := localfs.EnsureDirectory(dir); e != nil {
		return Config{}, e
	}
	p := filepath.Join(dir, "config.toml")
	if _, e := os.Lstat(p); os.IsNotExist(e) {
		c := DefaultAt(dir)
		c.Directory = dir
		if e = Save(p, c); e != nil {
			return c, e
		}
	}
	return Load(p)
}
