//go:build darwin

package projectexec

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"ai-worker-mcp/internal/agent"
	"ai-worker-mcp/internal/procsup"
)

// Run's caller revalidates the live capability while the command runs and
// cancels ctx immediately on revocation, cancellation or a terminal run state.
func Run(ctx context.Context, root, scratch, command string, timeoutSeconds int) (Result, error) {
	result := Result{Command: command, ExitCode: -1}
	opts, runCtx, cancel, err := prepareRun(ctx, root, scratch, command, timeoutSeconds)
	if err != nil {
		return result, err
	}
	defer cancel()
	root, _ = filepath.EvalSymlinks(root)
	scratch, _ = filepath.EvalSymlinks(scratch)
	env, reads := environment(scratch)
	profile := sandboxProfile(root, scratch, reads)
	cmd := exec.CommandContext(runCtx, "/usr/bin/sandbox-exec", "-p", profile, "/bin/sh", "-c", opts.command)
	cmd.Dir = root
	cmd.Env = env
	// The sandboxed tree runs in its own process group; Kill reaps it as a
	// whole when the timeout fires or the caller cancels.
	procsup.Configure(cmd)
	kill := func() error { return procsup.Kill(cmd) }
	cmd.Cancel = kill
	cmd.WaitDelay = time.Second
	var stdout, stderr limitedBuffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	start := time.Now()
	if err = cmd.Start(); err != nil {
		return result, agent.E("EXECUTION_SANDBOX_UNAVAILABLE")
	}
	if err = procsup.Started(cmd); err != nil {
		_ = kill()
		return result, agent.E("EXECUTION_SANDBOX_UNAVAILABLE")
	}
	err = cmd.Wait()
	// Open pipe holders prove that this process group outlived its leader.
	// Do not unconditionally signal a reaped leader's ID after a normal exit.
	if errors.Is(err, exec.ErrWaitDelay) {
		_ = kill()
	}
	result.DurationMS = time.Since(start).Milliseconds()
	var outCut, errCut bool
	result.Stdout, outCut = stdout.value()
	result.Stderr, errCut = stderr.value()
	result.Truncated = outCut || errCut
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	if runCtx.Err() != nil {
		result.TimedOut = true
		return result, nil
	}
	if cmd.ProcessState != nil {
		result.ExitCode = cmd.ProcessState.ExitCode()
		if result.ExitCode < 0 && result.Stderr == "" {
			result.Stderr = cmd.ProcessState.String()
		}
	}
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) && !errors.Is(err, exec.ErrWaitDelay) {
		return result, agent.E("INTERNAL_ERROR")
	}
	return result, nil
}

func sandboxProfile(root, scratch string, reads []string) string {
	var p strings.Builder
	p.WriteString(`(version 1)
(deny default)
(allow process*)
(allow signal (target same-sandbox))
(allow sysctl-read)
(allow mach-lookup)
(allow file-read-metadata)
(allow file-read-data (literal "/"))
(allow file-read* (subpath "/System") (subpath "/usr") (subpath "/bin") (subpath "/sbin") (subpath "/Library/Developer") (subpath "/Library/Apple") (subpath "/Applications/Xcode.app") (subpath "/opt/homebrew/Cellar") (subpath "/opt/homebrew/opt") (subpath "/opt/homebrew/bin") (subpath "/opt/homebrew/lib") (subpath "/opt/homebrew/share") (subpath "/private/var/db/dyld") (subpath "/dev") (literal "/private/etc/passwd") (literal "/private/etc/group") (literal "/private/etc/hosts") (literal "/private/etc/localtime"))
(allow file-write* (literal "/dev/null"))
`)
	for _, path := range append(reads, root, scratch) {
		if path == "" || path == "/" || !filepath.IsAbs(path) {
			continue
		}
		if real, err := filepath.EvalSymlinks(path); err == nil {
			path = real
		}
		fmt.Fprintf(&p, "(allow file-read* (subpath %s))\n", strconv.Quote(path))
	}
	fmt.Fprintf(&p, "(allow file-write* (subpath %s) (subpath %s))\n", strconv.Quote(root), strconv.Quote(scratch))
	fmt.Fprintf(&p, "(deny file-write* (subpath %s))\n", strconv.Quote(filepath.Join(root, ".git")))
	// A test may bind a loopback listener, but outbound connections (including
	// localhost) are denied so commands cannot reach unconfined local services.
	p.WriteString("(allow network-bind (local ip \"localhost:*\"))\n(allow network-inbound (local ip \"localhost:*\"))\n")
	return p.String()
}
