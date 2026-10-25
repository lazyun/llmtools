package integration_test

import (
	"ai-worker-mcp/internal/config"
	"ai-worker-mcp/internal/desktop"
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestDesktopMCPExposureMatchesRealPiProcess(t *testing.T) {
	executable := binary(t)
	for _, kind := range []string{"pi"} {
		t.Run(kind, func(t *testing.T) {
			c := config.Default()
			c.Directory = t.TempDir()
			c.Runtime.Binary = "/nonexistent/runtime-must-not-start"
			if kind == "pi" {
				c.Runtime = config.Runtime{Type: "pi", Binary: c.Runtime.Binary, SessionDir: filepath.Join(c.Directory, "sessions")}
			}
			c.Agents = map[string]config.AgentRouting{"implementation": {Exposed: true}, "review": {Exposed: true}, "research": {Exposed: false}}
			if kind == "opencode" {
				c.Agents["implementation"] = config.AgentRouting{Exposed: true, Agent: "build"}
				c.Agents["review"] = config.AgentRouting{Exposed: true, Agent: "build"}
			}
			path := filepath.Join(c.Directory, "config.toml")
			if e := config.Save(path, c); e != nil {
				t.Fatal(e)
			}
			s, e := desktop.New(c.Directory, executable)
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close()
			check := func(wantImplementation string) {
				t.Helper()
				checks := s.ProbeMCP(context.Background())
				toolPass := false
				implementationPass := false
				for _, item := range checks {
					if item.Status == "FAIL" {
						t.Fatalf("diagnostics rejected real %s tool list: %+v", kind, checks)
					}
					toolPass = toolPass || item.Name == "MCP tools" && item.Status == "PASS"
					implementationPass = implementationPass || item.Name == "Role exposure consistency: implementation" && strings.Contains(item.Message, wantImplementation)
				}
				if !toolPass || !implementationPass {
					t.Fatalf("incomplete diagnostics: %+v", checks)
				}
			}
			check("Exposed: YES")
			// Another process may save routing after Desktop opened. Its probe must
			// compare against the same persisted configuration that the child loads.
			route := c.Agents["implementation"]
			route.Exposed = false
			c.Agents["implementation"] = route
			if e := config.Save(path, c); e != nil {
				t.Fatal(e)
			}
			check("Exposed: NO")
		})
	}
}
