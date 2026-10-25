// Package factory is the runtime dispatch boundary: it turns the persisted
// runtime.type into exactly one agent.RuntimeAdapter. It is the only place
// allowed to know which runtimes exist; Core, storage, security and the Desktop
// bindings all keep working with runtime-neutral values.
package factory

import (
	"ai-worker-mcp/internal/agent"
	"ai-worker-mcp/internal/config"
	"ai-worker-mcp/internal/runtime/pi"
	"errors"
	"os"
	"path/filepath"
)

// spikeRuntimeEnv selects the Pi adapter for a test or development process
// without touching any configuration file. It is an override, not the supported
// route: persisted selection is runtime.type = "pi".
const spikeRuntimeEnv = "LLMTOOLS_RUNTIME_SPIKE"

// spikeBinaryEnv points at a Pi executable that is not on this process' PATH,
// and spikeSessionDirEnv relocates the Pi session tree so a harness can reopen
// the same sessions after a restart. Both are only read with the override above.
const (
	spikeBinaryEnv     = "LLMTOOLS_PI_BINARY"
	spikeSessionDirEnv = "LLMTOOLS_PI_SESSION_DIR"
)

// Runtime is a constructed adapter plus the shutdown that releases every process
// or managed server it owns. closer is always safe to call, including when no
// runtime was started.
type Runtime struct {
	Adapter agent.RuntimeAdapter
	closer  func()
}

func (r Runtime) Close() {
	if r.closer != nil {
		r.closer()
	}
}

// New builds the runtime the configuration selects. An unrecognized type fails
// closed instead of falling back: silently running OpenCode for a configuration
// that asked for something else would execute tasks under tools nobody chose.
//
// Pi's provider credentials, model catalogue and session content stay inside
// Pi's own configuration and session directory; llmTools stores only the
// executable name and where its sessions live, and never installs or edits
// either.
func New(c config.Config) (Runtime, error) {
	if e := c.Validate(); e != nil {
		return Runtime{}, e
	}
	if c.Runtime.Type != "pi" {
		return Runtime{}, errors.New("CONFIG_ERROR: only Pi is supported; save a Pi runtime configuration and select Pi models")
	}
	switch override := os.Getenv(spikeRuntimeEnv); override {
	case "":
	case pi.Kind:
		return newPi(c, spikePaths())
	default:
		// An unrecognized override fails closed rather than running the formal
		// route under a label that suggests otherwise.
		return Runtime{}, errors.New("CONFIG_ERROR: unknown runtime override")
	}
	switch c.Runtime.Type {
	case pi.Kind:
		return newPi(c, piPaths{})
	default:
		return Runtime{}, errors.New("CONFIG_ERROR: unsupported runtime type")
	}
}

// piPaths are the locations a harness may relocate, bypassing the persisted
// configuration. An empty struct means configuration decides everything, which
// is the supported route.
type piPaths struct {
	binary     string
	sessionDir string
	workDir    string
}

func spikePaths() piPaths {
	binary := os.Getenv(spikeBinaryEnv)
	if binary == "" {
		// The override configures nothing else, so it defaults to Pi's own command
		// name rather than the OpenCode binary the surrounding config still names.
		binary = pi.Kind
	}
	base := os.Getenv(spikeSessionDirEnv)
	if base == "" {
		base = filepath.Join(os.TempDir(), "llmtools-pi-spike")
	}
	return piPaths{binary: binary, sessionDir: filepath.Join(base, "sessions"), workDir: filepath.Join(base, "work")}
}

// newPi assembles the adapter from configuration plus this process's own
// identity, which the control bridge needs to call back into llmTools. The
// working directory is llmTools scratch and never a user workspace: Pi reaches
// repository files only through the confined read tools.
func newPi(c config.Config, paths piPaths) (Runtime, error) {
	self, e := os.Executable()
	if e != nil {
		return Runtime{}, errors.New("RUNTIME_UNAVAILABLE: runtime control bridge unavailable")
	}
	binary, sessionDir := c.Runtime.Binary, c.Runtime.SessionDir
	if paths.binary != "" {
		binary = paths.binary
	}
	if paths.sessionDir != "" {
		sessionDir = paths.sessionDir
	}
	workDir := filepath.Join(c.Directory, "pi-work")
	if paths.workDir != "" {
		workDir = paths.workDir
	}
	adapter, e := pi.New(pi.Spec{
		Binary:        binary,
		ControlBinary: self,
		ControlDB:     filepath.Join(c.Directory, c.Storage.Database),
		SessionDir:    sessionDir,
		WorkingDir:    workDir,
	})
	if e != nil {
		return Runtime{}, e
	}
	return Runtime{Adapter: adapter, closer: adapter.Close}, nil
}
