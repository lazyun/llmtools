//go:build windows

package pi

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Windows cannot CreateProcess an npm .cmd shim. Invoke its Node entry point
// directly with an argv vector, keeping shell metacharacters literal.
func piCommand(ctx context.Context, binary string, args ...string) (*exec.Cmd, error) {
	resolved, err := exec.LookPath(binary)
	if err != nil {
		return nil, err
	}
	ext := strings.ToLower(filepath.Ext(resolved))
	if ext != ".cmd" && ext != ".bat" && ext != ".js" && ext != ".mjs" && ext != ".cjs" {
		return exec.CommandContext(ctx, resolved, args...), nil
	}
	script := resolved
	if ext == ".cmd" || ext == ".bat" {
		script, err = npmNodeTarget(resolved)
		if err != nil {
			return nil, err
		}
	}
	node := filepath.Join(filepath.Dir(resolved), "node.exe")
	if info, e := os.Stat(node); e != nil || !info.Mode().IsRegular() {
		node, err = exec.LookPath("node.exe")
		if err != nil {
			return nil, err
		}
	}
	return exec.CommandContext(ctx, node, append([]string{script}, args...)...), nil
}
