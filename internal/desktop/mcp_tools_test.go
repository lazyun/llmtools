package desktop

import (
	"ai-worker-mcp/internal/config"
	"strings"
	"testing"
)

func TestMCPToolChecksUseTheSelectedRuntimeAgentRequirements(t *testing.T) {
	base := []string{"ai_worker_cancel", "ai_worker_continue", "ai_worker_add_workspace", "ai_worker_list_workspaces", "ai_worker_revoke_workspace"}
	for _, tc := range []struct {
		name, kind, selector string
		tools                []string
		wantFailure          bool
		wantRole             string
	}{
		{"Pi exposed roles without agent names", "pi", "", []string{"delegate_implementation", "delegate_review"}, false, "Exposed: YES"},
		{"Pi missing implementation tool", "pi", "", []string{"delegate_review"}, true, "missing from tools/list"},
		{"Pi unexpected tool", "pi", "", []string{"delegate_implementation", "delegate_review", "unknown_tool"}, true, ""},
		{"Pi duplicate tool", "pi", "", []string{"delegate_implementation", "delegate_review", "delegate_review"}, true, ""},
		{"OpenCode needs an agent name", "opencode", "", nil, false, "Exposed: NO"},
		{"OpenCode empty agent cannot expose tool", "opencode", "", []string{"delegate_implementation"}, true, "unexpectedly present"},
		{"OpenCode named agents", "opencode", "build", []string{"delegate_implementation", "delegate_review"}, false, "Exposed: YES"},
		{"Pi hidden research cannot expose tool", "pi", "", []string{"delegate_implementation", "delegate_review", "delegate_research"}, true, "unexpectedly present"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := config.Default()
			c.Runtime.Type = tc.kind
			c.Agents = map[string]config.AgentRouting{"implementation": {Exposed: true, Agent: tc.selector}, "review": {Exposed: true, Agent: tc.selector}, "research": {Exposed: false, Agent: tc.selector}}
			tools := append(append([]string{}, base...), tc.tools...)
			checks := mcpToolChecks(c, tools)
			failed := false
			messageFound := tc.wantRole == ""
			for _, check := range checks {
				failed = failed || check.Status == "FAIL"
				if strings.HasPrefix(check.Name, "Role exposure consistency:") && strings.Contains(check.Message, tc.wantRole) {
					messageFound = true
				}
			}
			if failed != tc.wantFailure || !messageFound {
				t.Fatalf("unexpected checks: %+v", checks)
			}
		})
	}
}
