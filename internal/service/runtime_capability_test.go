package service_test

// Core branches on a runtime's declared capability, never on its name: a runtime
// with no runtime-native agent catalogue has no selector to validate, so an
// empty route agent must run and an empty catalogue must not read as an
// unavailable agent. The mirror case — a catalogue-holding runtime routing to a
// name it does not list — is TestMissingRoutedAgentFailsBeforeRunCreation.

import (
	"ai-worker-mcp/internal/agent"
	"ai-worker-mcp/internal/config"
	"ai-worker-mcp/internal/runtime/opencode"
	"ai-worker-mcp/internal/service"
	"ai-worker-mcp/internal/store"
	"ai-worker-mcp/internal/testutil"
	"context"
	"path/filepath"
	"testing"
)

// noCatalogueAdapter reports a runtime without an agent catalogue while the
// shared fixture transport below it stays the one these tests already exercise.
type noCatalogueAdapter struct {
	agent.RuntimeAdapter
	agentsAsked int
}

func (a *noCatalogueAdapter) AgentCatalogueSupported() bool { return false }

func (a *noCatalogueAdapter) ListAgents(context.Context) ([]agent.AgentInfo, error) {
	a.agentsAsked++
	return nil, nil
}

func TestCataloguelessRuntimeRoutesWithoutAnAgentSelector(t *testing.T) {
	mock := testutil.New("", agent.AgentResult{Status: "completed", Content: "Completed with no selector"})
	t.Cleanup(mock.Close)
	c := config.Default()
	c.Directory = t.TempDir()
	// No agent name on any route: that is how a runtime without an agent
	// selector is configured.
	c.Agents = map[string]config.AgentRouting{
		"implementation": {Exposed: true, Model: "mock/fixture"},
		"review":         {Exposed: true, Model: "mock/fixture"},
		"research":       {Exposed: true, Model: "mock/fixture"},
	}
	if e := config.Save(filepath.Join(c.Directory, "config.toml"), c); e != nil {
		t.Fatal(e)
	}
	st, e := store.Open(filepath.Join(c.Directory, c.Storage.Database))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = st.Close() })
	adapter := &noCatalogueAdapter{RuntimeAdapter: opencode.New(mock.Server.URL, filepath.Join(c.Directory, "runtime-workspace"))}
	s := service.New(c, st, adapter)
	result := s.Run(context.Background(), raw(task()), nil)
	if result.Error != nil && result.Error.Code == "AGENT_NOT_FOUND" {
		t.Fatalf("an empty agent selector was read as an unavailable agent: %+v", result)
	}
	if result.Status != "completed" {
		t.Fatalf("catalogue-less routing failed: %+v", result)
	}
	if adapter.agentsAsked != 0 {
		t.Fatalf("Core consulted a catalogue the runtime does not have: %d calls", adapter.agentsAsked)
	}
	stored, e := s.Store.Get(result.WorkerRunID)
	if e != nil {
		t.Fatal(e)
	}
	if stored.RuntimeAgent != "" {
		t.Fatalf("a selector was invented for a runtime without a catalogue: %q", stored.RuntimeAgent)
	}
	if stored.LogicalRole != "implementation" {
		t.Fatalf("the logical role is the only agent identity a catalogue-less run has: %+v", stored)
	}
}
