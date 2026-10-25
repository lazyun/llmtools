//go:build !darwin

package projectexec

import (
	"context"

	"ai-worker-mcp/internal/agent"
)

// Run fails closed on platforms without the macOS sandbox-exec confinement.
// The Developer command surface is a macOS-only contract: Linux and Windows
// never execute a project command unconstrained instead of sandboxed.
func Run(ctx context.Context, root, scratch, command string, timeoutSeconds int) (Result, error) {
	return Result{Command: command, ExitCode: -1}, agent.E("EXECUTION_SANDBOX_UNAVAILABLE")
}
