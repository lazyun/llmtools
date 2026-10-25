package main

import (
	"ai-worker-mcp/internal/config"
	server "ai-worker-mcp/internal/mcp"
	"ai-worker-mcp/internal/runtime/factory"
	"ai-worker-mcp/internal/runtime/pi"
	"ai-worker-mcp/internal/service"
	"ai-worker-mcp/internal/store"
	"context"
	"flag"
	"fmt"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
)

func main() {
	if handled, code := pi.ControlMain(os.Args); handled {
		os.Exit(code)
	}
	os.Exit(run())
}

func run() int {
	path := flag.String("config", "", "configuration file")
	dir := flag.String("config-dir", "", "application data directory")
	flag.Parse()
	if (*path == "") == (*dir == "") || flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "CONFIG_ERROR: use --config FILE or --config-dir DIRECTORY")
		return 2
	}
	if *dir != "" {
		*path = filepath.Join(*dir, "config.toml")
	}
	c, e := config.Load(*path)
	if e != nil {
		fmt.Fprintln(os.Stderr, "CONFIG_ERROR: cannot load configuration")
		return 2
	}
	for _, w := range c.Warnings {
		fmt.Fprintln(os.Stderr, w)
	}
	st, e := store.Open(filepath.Join(c.Directory, c.Storage.Database))
	if e != nil {
		fmt.Fprintln(os.Stderr, "CONFIG_ERROR: cannot open run storage")
		return 2
	}
	defer st.Close()
	// The persisted configuration alone decides which runtime serves tasks.
	runtime, e := factory.New(c)
	if e != nil {
		fmt.Fprintln(os.Stderr, "CONFIG_ERROR: only Pi is supported; verify the Pi runtime configuration")
		return 2
	}
	defer runtime.Close()
	s := service.New(c, st, runtime.Adapter)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	s.Recover()
	if e = server.New(s).Run(ctx, &mcp.StdioTransport{MaxLineLength: c.General.MaxContextBytes*2 + 131072}); e != nil && ctx.Err() == nil {
		fmt.Fprintln(os.Stderr, "INTERNAL_ERROR: MCP transport stopped")
		return 1
	}
	return 0
}
