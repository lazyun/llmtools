// Headless lifecycle acceptance harness. Calls the production Desktop backend;
// it never starts Wails or operates the user's UI.
package main

import (
	"ai-worker-mcp/internal/desktop"
	"ai-worker-mcp/internal/runtime/opencode"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	if handled, code := opencode.HostMain(os.Args); handled {
		os.Exit(code)
	}
	dir := flag.String("config-dir", "", "existing data directory")
	binary := flag.String("binary", "", "MCP binary")
	verifyNonOwner := flag.Bool("verify-non-owner", false, "assert attached backend cannot stop runtime")
	start := flag.Bool("start", false, "start through Desktop backend")
	mapBuild := flag.Bool("map-build", false, "map implementation to discovered build agent")
	normalize := flag.Bool("normalize", false, "save canonical managed configuration")
	stopFile := flag.String("stop-file", "", "exit when this file exists")
	hold := flag.Duration("hold", 10*time.Minute, "maximum owner lifetime")
	flag.Parse()
	s, e := desktop.New(*dir, *binary)
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
	defer s.Close()
	if *normalize {
		r := s.State().Config.Runtime
		if e = s.SaveRuntime(r); e != nil {
			fmt.Fprintln(os.Stderr, e)
			return
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	if *start {
		if e = s.StartRuntime(ctx); e != nil {
			fmt.Fprintln(os.Stderr, e)
			return
		}
	}
	if *mapBuild {
		if e = s.SaveMapping(ctx, "implementation", "build"); e != nil {
			fmt.Fprintln(os.Stderr, e)
			return
		}
	}
	_ = json.NewEncoder(os.Stdout).Encode(s.State().Runtime)
	_ = json.NewEncoder(os.Stdout).Encode(s.Diagnostics(ctx))
	if *verifyNonOwner {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]bool{"non_owner_stop_rejected": s.StopRuntime() != nil})
	}
	if !*start {
		return
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	timer := time.NewTimer(*hold)
	defer timer.Stop()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-signals:
			return
		case <-timer.C:
			return
		case <-ticker.C:
			if *stopFile != "" {
				if _, e = os.Stat(*stopFile); e == nil {
					return
				}
			}
		}
	}
}
