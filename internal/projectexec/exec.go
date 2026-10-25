// Package projectexec executes project commands under an OS-enforced workspace
// boundary. A working directory alone is never treated as a sandbox.
package projectexec

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"ai-worker-mcp/internal/agent"
)

const MaxOutputBytes = 256 * 1024

type Result struct {
	Command    string `json:"command"`
	ExitCode   int    `json:"exit_code"`
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	TimedOut   bool   `json:"timed_out"`
	Truncated  bool   `json:"truncated"`
	DurationMS int64  `json:"duration_ms"`
}

// Available reports whether this platform can run project commands under an
// OS-enforced sandbox. Only macOS provides the sandbox-exec confinement the
// Developer role is contracted to run under; Linux and Windows fail closed
// with EXECUTION_SANDBOX_UNAVAILABLE rather than running unconstrained.
func Available() error {
	if runtime.GOOS != "darwin" {
		return agent.E("EXECUTION_SANDBOX_UNAVAILABLE")
	}
	if _, err := os.Stat("/usr/bin/sandbox-exec"); err != nil {
		return agent.E("EXECUTION_SANDBOX_UNAVAILABLE")
	}
	return nil
}

func Scratch(directory, runID string) string {
	sum := sha256.Sum256([]byte(runID))
	return filepath.Join(directory, "execution-work", hex.EncodeToString(sum[:16]))
}

func Cleanup(directory, runID string) { _ = os.RemoveAll(Scratch(directory, runID)) }

// ValidateCommand applies the platform-neutral input bounds; the platform Run
// implementation re-checks them via prepareRun so behaviour stays identical.
func ValidateCommand(command string, timeoutSeconds int) error {
	if strings.TrimSpace(command) == "" || len(command) > 32768 || strings.ContainsRune(command, 0) || timeoutSeconds < 1 || timeoutSeconds > 3600 {
		return agent.E("REPO_INPUT_INVALID")
	}
	return nil
}

type limitedBuffer struct {
	mu        sync.Mutex
	buf       strings.Builder
	truncated bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	remaining := MaxOutputBytes - b.buf.Len()
	if remaining <= 0 {
		b.truncated = true
		return n, nil
	}
	if remaining < len(p) {
		p = p[:remaining]
		b.truncated = true
	}
	_, _ = b.buf.Write(p)
	return n, nil
}
func (b *limitedBuffer) value() (string, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String(), b.truncated
}

// runOptions is the validated, platform-neutral part of one command run.
type runOptions struct {
	command string
	timeout time.Duration
}

// prepareRun performs the shared validation and workspace preparation every
// platform needs before launching; the returned cancel releases the run
// context and must be called by the platform Run implementation.
func prepareRun(ctx context.Context, root, scratch, command string, timeoutSeconds int) (runOptions, context.Context, context.CancelFunc, error) {
	opts := runOptions{command: command}
	if err := ValidateCommand(command, timeoutSeconds); err != nil {
		return opts, ctx, func() {}, err
	}
	if err := Available(); err != nil {
		return opts, ctx, func() {}, err
	}
	var err error
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return opts, ctx, func() {}, agent.E("WORKSPACE_UNAVAILABLE")
	}
	for _, dir := range []string{scratch, filepath.Join(scratch, "tmp"), filepath.Join(scratch, "cache"), filepath.Join(scratch, "go-build"), filepath.Join(scratch, "npm")} {
		if err = ensureDir(dir); err != nil {
			return opts, ctx, func() {}, agent.E("INTERNAL_ERROR")
		}
	}
	scratch, err = filepath.EvalSymlinks(scratch)
	if err != nil {
		return opts, ctx, func() {}, agent.E("INTERNAL_ERROR")
	}
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSeconds)*time.Second)
	return runOptions{command: command, timeout: time.Duration(timeoutSeconds) * time.Second}, runCtx, cancel, nil
}

func ensureDir(dir string) error {
	if e := os.MkdirAll(dir, 0o700); e != nil {
		return e
	}
	s, e := os.Lstat(dir)
	if e != nil || !s.IsDir() || s.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("unsafe scratch directory")
	}
	return nil
}

func environment(scratch string) ([]string, []string) {
	// Explicit allowlist: model-provider credentials and llmTools control tokens
	// never enter a test process. HOME retains its real value; cache/temp writes
	// are redirected to this run's private scratch instead.
	var env []string
	for _, name := range []string{"PATH", "HOME", "LANG", "LC_ALL", "LC_CTYPE", "GOROOT", "GOPATH", "GOTOOLCHAIN", "SDKROOT", "DEVELOPER_DIR"} {
		if value, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+value)
		}
	}
	env = append(env, "TMPDIR="+filepath.Join(scratch, "tmp"), "TMP="+filepath.Join(scratch, "tmp"), "TEMP="+filepath.Join(scratch, "tmp"), "XDG_CACHE_HOME="+filepath.Join(scratch, "cache"), "GOCACHE="+filepath.Join(scratch, "go-build"), "npm_config_cache="+filepath.Join(scratch, "npm"), "PYTHONDONTWRITEBYTECODE=1", "CI=1", "NO_COLOR=1", "GOTOOLCHAIN=local")
	home, _ := os.UserHomeDir()
	reads := []string{runtime.GOROOT(), filepath.Join(home, "go", "pkg", "mod"), filepath.Join(home, ".nvm", "versions", "node"), filepath.Join(home, ".local", "share", "mise", "installs"), filepath.Join(home, ".rustup", "toolchains"), filepath.Join(home, ".cargo", "bin"), filepath.Join(home, ".cache", "codex-runtimes")}
	if value := os.Getenv("GOROOT"); value != "" {
		reads = append(reads, value)
	}
	if value := os.Getenv("GOPATH"); value != "" {
		for _, path := range filepath.SplitList(value) {
			reads = append(reads, filepath.Join(path, "pkg", "mod"))
		}
	}
	return env, reads
}
