package agent

import "testing"

func TestCompletedResultUsesOpaqueContent(t *testing.T) {
	content := "## Result\n\n```diff\n-old\n+new\n```"
	if e := ValidateResult(AgentResult{Status: "completed", Content: content}); e != nil {
		t.Fatal(e)
	}
	if ValidateResult(AgentResult{Status: "completed"}) == nil {
		t.Fatal("empty completion accepted")
	}
	if ValidateResult(AgentResult{Status: "completed", Summary: "legacy only"}) == nil {
		t.Fatal("legacy summary accepted as authoritative completion")
	}
}
