package main

import (
	app "ai-worker-mcp/internal/desktop"
	"context"
	"errors"
	"testing"
	"time"
)

func TestBridgeWaitsForStartup(t *testing.T) {
	a := &App{ready: make(chan struct{})}
	done := make(chan error, 1)
	go func() { _, err := a.State(); done <- err }()
	select {
	case <-done:
		t.Fatal("bridge bypassed startup gate")
	case <-time.After(10 * time.Millisecond):
	}
	service, err := app.New(t.TempDir(), "/no/binary")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Close)
	a.service = service
	a.ctx = context.Background()
	close(a.ready)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("bridge did not unblock")
	}
}
func TestBridgeStartupFailureIsSafe(t *testing.T) {
	a := &App{ready: make(chan struct{}), initErr: errors.New("CONFIG_ERROR: cannot initialize Desktop storage")}
	close(a.ready)
	if _, err := a.State(); err == nil {
		t.Fatal("startup failure hidden")
	}
	if _, err := a.InstallSkill(); err == nil {
		t.Fatal("Skill injection bypassed failed startup")
	}
	if _, err := a.RemoveSkill(); err == nil {
		t.Fatal("Skill removal bypassed failed startup")
	}
	if _, err := a.DiscoverAgents(); err == nil {
		t.Fatal("discovery bypassed failed startup")
	}
}
