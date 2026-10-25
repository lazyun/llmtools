package main

import (
	"context"
	"embed"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	app "ai-worker-mcp/internal/desktop"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/logger"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	dir := flag.String("config-dir", "", "custom application configuration directory")
	binary := flag.String("mcp-binary", "", "custom ai-worker-mcp binary path")
	flag.Parse()
	if *binary == "" {
		exe, err := os.Executable()
		if err != nil {
			fmt.Fprintln(os.Stderr, "CONFIG_ERROR: cannot locate app executable")
			return
		}
		name := "ai-worker-mcp"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		*binary = filepath.Join(filepath.Dir(exe), name)
	}
	a := &App{ready: make(chan struct{})}
	err := wails.Run(&options.App{
		Title: "AI Worker", Width: 1080, Height: 760, MinWidth: 860, MinHeight: 620,
		Logger: safeRuntimeLogger{output: os.Stderr}, LogLevel: logger.ERROR, LogLevelProduction: logger.ERROR,
		AssetServer: &assetserver.Options{Assets: assets},
		OnStartup: func(ctx context.Context) {
			defer close(a.ready)
			a.ctx = ctx
			service, err := app.New(*dir, *binary)
			if err != nil {
				fmt.Fprintln(os.Stderr, "CONFIG_ERROR: cannot initialize Desktop storage")
				a.initErr = errors.New("CONFIG_ERROR: cannot initialize Desktop storage; check directory permissions")
				return
			}
			a.service = service
		}, OnShutdown: func(ctx context.Context) {
			if a.service != nil {
				a.service.Close()
			}
		}, Bind: []interface{}{a},
		SingleInstanceLock: &options.SingleInstanceLock{UniqueId: "ai-worker-desktop-configuration-client"},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "DESKTOP_ERROR: application could not start")
	}
}
