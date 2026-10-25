package integration_test

import (
	"ai-worker-mcp/internal/config"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// callWorkspaceTool calls a workspace management MCP tool and returns the raw JSON text.
func callWorkspaceTool(t *testing.T, s *sdk.ClientSession, name string, args any) (string, bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	r, e := s.CallTool(ctx, &sdk.CallToolParams{Name: name, Arguments: args})
	if e != nil {
		t.Fatalf("CallTool(%s): %v", name, e)
	}
	if len(r.Content) == 0 {
		t.Fatalf("CallTool(%s): no content", name)
	}
	text, ok := r.Content[0].(*sdk.TextContent)
	if !ok {
		t.Fatalf("CallTool(%s): content is not TextContent", name)
	}
	return text.Text, r.IsError
}

func TestMCPWorkspaceAuthorizationTools(t *testing.T) {
	dir := t.TempDir()
	c := config.Default()
	c.Directory = dir
	c.Agents = map[string]config.AgentRouting{}
	if err := config.Save(filepath.Join(dir, "config.toml"), c); err != nil {
		t.Fatal(err)
	}
	s := connect(t, binary(t), dir, nil)
	defer s.Close()

	// Verify workspace tools are present
	listed, e := s.ListTools(context.Background(), nil)
	if e != nil {
		t.Fatal(e)
	}
	toolNames := map[string]bool{}
	for _, tool := range listed.Tools {
		toolNames[tool.Name] = true
	}
	for _, expected := range []string{"ai_worker_add_workspace", "ai_worker_list_workspaces", "ai_worker_revoke_workspace"} {
		if !toolNames[expected] {
			t.Fatalf("missing workspace tool: %s", expected)
		}
	}

	// List workspaces (should be empty)
	listText, isErr := callWorkspaceTool(t, s, "ai_worker_list_workspaces", map[string]any{})
	if isErr {
		t.Fatalf("list_workspaces returned error: %s", listText)
	}
	var listResp map[string]any
	if e := json.Unmarshal([]byte(listText), &listResp); e != nil {
		t.Fatalf("failed to parse list response: %v, text: %s", e, listText)
	}
	if count, ok := listResp["count"].(float64); !ok || count != 0 {
		t.Fatalf("expected count=0, got %v", listResp["count"])
	}

	// Add a workspace
	workspaceDir := t.TempDir()
	addText, isErr := callWorkspaceTool(t, s, "ai_worker_add_workspace", map[string]any{
		"path":  workspaceDir,
		"label": "test-project",
	})
	if isErr {
		t.Fatalf("add_workspace returned error: %s", addText)
	}
	var addResp map[string]any
	if e := json.Unmarshal([]byte(addText), &addResp); e != nil {
		t.Fatalf("failed to parse add response: %v, text: %s", e, addText)
	}
	if addResp["created"] != true {
		t.Fatalf("expected created=true, got %v", addResp["created"])
	}
	workspace, ok := addResp["workspace"].(map[string]any)
	if !ok {
		t.Fatalf("expected workspace object in response, got %T", addResp["workspace"])
	}
	workspaceID, ok := workspace["id"].(string)
	if !ok || !strings.HasPrefix(workspaceID, "ws_") {
		t.Fatalf("expected valid workspace ID, got %v", workspace["id"])
	}
	if workspace["source"] != "mcp_client_grant" {
		t.Fatalf("expected source mcp_client_grant, got %v", workspace["source"])
	}

	// Adding the same path again should return existing (not created)
	addText2, isErr := callWorkspaceTool(t, s, "ai_worker_add_workspace", map[string]any{
		"path": workspaceDir,
	})
	if isErr {
		t.Fatalf("second add_workspace returned error: %s", addText2)
	}
	var addResp2 map[string]any
	if e := json.Unmarshal([]byte(addText2), &addResp2); e != nil {
		t.Fatalf("failed to parse second add response: %v", e)
	}
	if addResp2["created"] != false {
		t.Fatalf("expected created=false for duplicate, got %v", addResp2["created"])
	}

	// List workspaces should now show one
	listText2, isErr := callWorkspaceTool(t, s, "ai_worker_list_workspaces", map[string]any{})
	if isErr {
		t.Fatalf("second list_workspaces returned error: %s", listText2)
	}
	var listResp2 map[string]any
	if e := json.Unmarshal([]byte(listText2), &listResp2); e != nil {
		t.Fatalf("failed to parse second list response: %v", e)
	}
	if count, ok := listResp2["count"].(float64); !ok || count != 1 {
		t.Fatalf("expected count=1, got %v", listResp2["count"])
	}

	// Revoke the workspace
	revokeText, isErr := callWorkspaceTool(t, s, "ai_worker_revoke_workspace", map[string]any{
		"workspace_id": workspaceID,
	})
	if isErr {
		t.Fatalf("revoke_workspace returned error: %s", revokeText)
	}
	var revokeResp map[string]any
	if e := json.Unmarshal([]byte(revokeText), &revokeResp); e != nil {
		t.Fatalf("failed to parse revoke response: %v", e)
	}
	if revokeResp["revoked"] != true {
		t.Fatalf("expected revoked=true, got %v", revokeResp["revoked"])
	}

	// List should still show one (revoked, not deleted)
	listText3, isErr := callWorkspaceTool(t, s, "ai_worker_list_workspaces", map[string]any{})
	if isErr {
		t.Fatalf("third list_workspaces returned error: %s", listText3)
	}
	var listResp3 map[string]any
	if e := json.Unmarshal([]byte(listText3), &listResp3); e != nil {
		t.Fatalf("failed to parse third list response: %v", e)
	}
	if count, ok := listResp3["count"].(float64); !ok || count != 1 {
		t.Fatalf("expected count=1 after revoke, got %v", listResp3["count"])
	}

	// Adding an invalid path should fail
	_, isErr = callWorkspaceTool(t, s, "ai_worker_add_workspace", map[string]any{
		"path": "/nonexistent/path/that/does/not/exist",
	})
	if !isErr {
		t.Fatal("expected error for invalid path")
	}
}

func TestMCPWorkspaceAuthorizationInstructions(t *testing.T) {
	dir := t.TempDir()
	c := config.Default()
	c.Directory = dir
	c.Agents = map[string]config.AgentRouting{}
	if err := config.Save(filepath.Join(dir, "config.toml"), c); err != nil {
		t.Fatal(err)
	}
	s := connect(t, binary(t), dir, nil)
	defer s.Close()

	instructions := s.InitializeResult().Instructions

	// Should mention workspace authorization tools
	for _, tool := range []string{"ai_worker_add_workspace", "ai_worker_list_workspaces", "ai_worker_revoke_workspace"} {
		if !strings.Contains(instructions, tool) {
			t.Fatalf("instructions missing workspace tool %q: %s", tool, instructions)
		}
	}

	// Should mention both authorization methods
	if !strings.Contains(instructions, "Desktop") {
		t.Fatalf("instructions missing Desktop authorization method: %s", instructions)
	}
	if !strings.Contains(instructions, "ai_worker_add_workspace") {
		t.Fatalf("instructions missing MCP authorization method: %s", instructions)
	}
}
