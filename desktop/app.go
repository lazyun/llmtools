package main

import (
	"ai-worker-mcp/internal/agent"
	"ai-worker-mcp/internal/config"
	app "ai-worker-mcp/internal/desktop"
	"context"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type App struct {
	service *app.Service
	ctx     context.Context
	ready   chan struct{}
	initErr error
}

func (a *App) backend() (*app.Service, error) { <-a.ready; return a.service, a.initErr }
func (a *App) State() (app.State, error) {
	s, e := a.backend()
	if e != nil {
		return app.State{}, e
	}
	return s.State(), nil
}
func (a *App) InstallSkill() (app.SkillStatus, error) {
	s, e := a.backend()
	if e != nil {
		return app.SkillStatus{}, e
	}
	return s.InstallSkill()
}
func (a *App) RemoveSkill() (app.SkillStatus, error) {
	s, e := a.backend()
	if e != nil {
		return app.SkillStatus{}, e
	}
	return s.RemoveSkill()
}
func (a *App) SaveRuntime(r config.Runtime) error {
	s, e := a.backend()
	if e != nil {
		return e
	}
	return s.SaveRuntime(r)
}
func (a *App) FindRuntimeBinary(name string) (string, error) {
	s, e := a.backend()
	if e != nil {
		return "", e
	}
	return s.FindBinary(name), nil
}
func (a *App) SelectRuntimeBinary() (string, error) {
	path, e := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title:   "Select Runtime Binary",
		Filters: []runtime.FileFilter{{DisplayName: "Executable", Pattern: "*"}},
	})
	if e != nil {
		return "", e
	}
	return path, nil
}
func (a *App) SelectRuntimeDirectory() (string, error) {
	path, e := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title:                "Select Session Directory",
		CanCreateDirectories: true,
	})
	if e != nil {
		return "", e
	}
	return path, nil
}
func (a *App) SaveSettings(g config.General) error {
	s, e := a.backend()
	if e != nil {
		return e
	}
	return s.SaveSettings(g)
}
func (a *App) DiscoverAgents() ([]agent.AgentInfo, error) {
	s, e := a.backend()
	if e != nil {
		return nil, e
	}
	return s.DiscoverAgents(a.ctx)
}
func (a *App) DiscoverRouting() (app.RoutingDiscovery, error) {
	s, e := a.backend()
	if e != nil {
		return app.RoutingDiscovery{}, e
	}
	return s.DiscoverRouting(a.ctx)
}
func (a *App) SaveRoutings(routings map[string]config.AgentRouting) error {
	s, e := a.backend()
	if e != nil {
		return e
	}
	return s.SaveRoutings(a.ctx, routings)
}
func (a *App) DeleteMapping(name string) error {
	s, e := a.backend()
	if e != nil {
		return e
	}
	return s.DeleteMapping(name)
}
func (a *App) StartRuntime() error {
	s, e := a.backend()
	if e != nil {
		return e
	}
	return s.StartRuntime(a.ctx)
}
func (a *App) StopRuntime() error {
	s, e := a.backend()
	if e != nil {
		return e
	}
	return s.StopRuntime()
}
func (a *App) CodexConfig() (string, error) {
	s, e := a.backend()
	if e != nil {
		return "", e
	}
	return s.CodexConfig()
}
func (a *App) SetBinary(path string) error {
	s, e := a.backend()
	if e != nil {
		return e
	}
	return s.SetBinary(path)
}

// AddWorkspace is the only workspace authorization entry point. It opens the
// native folder picker and grants the user-chosen canonical directory. No path
// argument crosses the binding, so there is no text-path authorization route.
func (a *App) AddWorkspace() (app.WorkspaceGrant, error) {
	s, e := a.backend()
	if e != nil {
		return app.WorkspaceGrant{}, e
	}
	path, e := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{Title: "Authorize Workspace", CanCreateDirectories: false})
	if e != nil {
		return app.WorkspaceGrant{}, e
	}
	if path == "" {
		// User cancellation is not an error and changes nothing.
		return app.WorkspaceGrant{}, nil
	}
	return s.AddWorkspace(path)
}
func (a *App) RenameWorkspace(id, label string) error {
	s, e := a.backend()
	if e != nil {
		return e
	}
	return s.RenameWorkspace(id, label)
}
func (a *App) RevokeWorkspace(id string) error {
	s, e := a.backend()
	if e != nil {
		return e
	}
	return s.RevokeWorkspace(id)
}
func (a *App) Diagnostics() ([]app.Check, error) {
	s, e := a.backend()
	if e != nil {
		return nil, e
	}
	return s.Diagnostics(a.ctx), nil
}
func (a *App) ProbeRuntime() ([]app.Check, error) {
	s, e := a.backend()
	if e != nil {
		return nil, e
	}
	return s.ProbeRuntime(a.ctx), nil
}
func (a *App) ProbeMCP() ([]app.Check, error) {
	s, e := a.backend()
	if e != nil {
		return nil, e
	}
	return s.ProbeMCP(a.ctx), nil
}
