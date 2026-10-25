//go:build !windows

package pi

import (
	"context"
	"os/exec"
)

func piCommand(ctx context.Context, binary string, args ...string) (*exec.Cmd, error) {
	return exec.CommandContext(ctx, binary, args...), nil
}
