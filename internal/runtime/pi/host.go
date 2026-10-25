package pi

import (
	"ai-worker-mcp/internal/agent"
	"ai-worker-mcp/internal/repocontrol"
	"ai-worker-mcp/internal/store"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

// maxControlInput matches the public context bound for one tool argument set.
const maxControlInput = 4 * 1024 * 1024

// ControlMain is the one-shot repository gateway used by the Pi runtime
// extension. It is the same authoritative implementation the OpenCode control
// MCP server exposes, so a second Pi path can never grow its own workspace
// authorization, path confinement or revoke rules.
func ControlMain(args []string) (bool, int) {
	if len(args) != 4 || args[1] != "--llmtools-pi-control" {
		return false, 0
	}
	dbPath, tool := args[2], args[3]
	fail := func(code string) int {
		_, _ = io.WriteString(os.Stderr, code)
		return 1
	}
	if !filepath.IsAbs(dbPath) || filepath.Base(dbPath) == "." || !knownRepoTool(tool) {
		return true, fail("REPO_INPUT_INVALID")
	}
	input, e := io.ReadAll(io.LimitReader(os.Stdin, maxControlInput+1))
	if e != nil || len(input) > maxControlInput {
		return true, fail("REPO_INPUT_INVALID")
	}
	st, e := store.Open(dbPath)
	if e != nil {
		return true, fail("RUNTIME_UNAVAILABLE")
	}
	defer st.Close()
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(signalCtx, 3600*time.Second)
	defer cancel()
	out, e := repocontrol.Call(ctx, st, tool, json.RawMessage(input))
	if e != nil {
		return true, fail(agent.Safe(e).Code)
	}
	os.Stdout.Write(out)
	return true, 0
}

func knownRepoTool(tool string) bool {
	for _, name := range repocontrol.Tools {
		if name == tool {
			return true
		}
	}
	return false
}
