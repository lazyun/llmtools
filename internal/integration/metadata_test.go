package integration_test

import (
	"ai-worker-mcp/internal/config"
	"ai-worker-mcp/internal/store"
	"context"
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestWorkspaceSelectionSchemaAndInstructions(t *testing.T) {
	dir := t.TempDir()
	c := config.Default()
	c.Directory = dir
	c.Agents = map[string]config.AgentRouting{"implementation": {Exposed: true, Agent: "build"}}
	if err := config.Save(filepath.Join(dir, "config.toml"), c); err != nil {
		t.Fatal(err)
	}
	st, e := store.Open(filepath.Join(dir, c.Storage.Database))
	if e != nil {
		t.Fatal(e)
	}
	root := t.TempDir()
	w, _, e := st.AddWorkspace(root, "repo", "desktop_user_grant")
	if e != nil {
		t.Fatal(e)
	}
	st.Close()
	s := connect(t, binary(t), dir, nil)
	defer s.Close()
	instructions := s.InitializeResult().Instructions
	if !strings.Contains(instructions, w.ID) || !strings.Contains(instructions, "repo") {
		t.Fatalf("authorized workspace not advertised: %s", instructions)
	}
	if strings.Contains(instructions, root) {
		t.Fatalf("full canonical path leaked to the model: %s", instructions)
	}
	if !strings.Contains(instructions, "repository read-only (read, glob, grep)") {
		t.Fatalf("workspace instructions hide the granted read access: %s", instructions)
	}
	if !strings.Contains(instructions, "not requestable by a caller") || !strings.Contains(instructions, "never points at a path") {
		t.Fatalf("instructions do not bound workspace_id authority: %s", instructions)
	}
	for _, overclaim := range []string{"full repository access", "write access", "coding permission"} {
		if strings.Contains(instructions, overclaim) {
			t.Fatalf("instructions overclaim %q: %s", overclaim, instructions)
		}
	}
	listed, e := s.ListTools(context.Background(), nil)
	if e != nil {
		t.Fatal(e)
	}
	for _, tool := range listed.Tools {
		if tool.Name != "delegate_implementation" {
			continue
		}
		b, _ := json.Marshal(tool.InputSchema)
		var schema map[string]any
		if json.Unmarshal(b, &schema) != nil {
			t.Fatal("invalid schema")
		}
		props := schema["properties"].(map[string]any)
		if _, ok := props["workspace_id"]; !ok {
			t.Fatalf("%s missing workspace_id", tool.Name)
		}
		if _, ok := props["workspace_root"]; ok {
			t.Fatalf("%s exposes workspace_root", tool.Name)
		}
		if _, ok := props["capabilities"]; ok {
			t.Fatalf("%s exposes caller capabilities", tool.Name)
		}
	}
}

func TestDynamicRoleNativeMetadataOverStdio(t *testing.T) {
	cases := []struct {
		name      string
		routes    map[string]config.AgentRouting
		want      []string
		mentioned []string
		absent    []string
	}{
		{"implementation-review", map[string]config.AgentRouting{
			"implementation": {Exposed: true, Agent: "build"},
			"review":         {Exposed: true, Agent: "reviewer"},
			"research":       {Exposed: false, Agent: "explore"},
		}, []string{"ai_worker_add_workspace", "ai_worker_cancel", "ai_worker_continue", "ai_worker_list_workspaces", "ai_worker_revoke_workspace", "delegate_implementation", "delegate_review"}, []string{"implementation", "review"}, []string{"research"}},
		{"research-only", map[string]config.AgentRouting{
			"research": {Exposed: true, Agent: "explore"},
		}, []string{"ai_worker_add_workspace", "ai_worker_cancel", "ai_worker_continue", "ai_worker_list_workspaces", "ai_worker_revoke_workspace", "delegate_research"}, []string{"research"}, []string{"implementation", "review"}},
		{"developer-only", map[string]config.AgentRouting{"developer": {Exposed: true, Agent: "build"}}, []string{"ai_worker_add_workspace", "ai_worker_cancel", "ai_worker_continue", "ai_worker_list_workspaces", "ai_worker_revoke_workspace", "delegate_developer"}, []string{"developer", "sandboxed", "Codex"}, []string{"review", "research"}},
		{"none", map[string]config.AgentRouting{}, []string{"ai_worker_add_workspace", "ai_worker_cancel", "ai_worker_continue", "ai_worker_list_workspaces", "ai_worker_revoke_workspace"}, []string{"No new external worker roles"}, []string{"implementation", "review", "research"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			c := config.Default()
			c.Directory, c.Agents = dir, tc.routes
			if err := config.Save(filepath.Join(dir, "config.toml"), c); err != nil {
				t.Fatal(err)
			}
			s := connect(t, binary(t), dir, nil)
			defer s.Close()
			instructions := s.InitializeResult().Instructions
			for _, term := range tc.mentioned {
				if !strings.Contains(strings.ToLower(instructions), strings.ToLower(term)) {
					t.Errorf("instructions missing %q: %s", term, instructions)
				}
			}
			for _, term := range tc.absent {
				if strings.Contains(strings.ToLower(instructions), strings.ToLower(term)) {
					t.Errorf("instructions mention hidden role %q: %s", term, instructions)
				}
			}
			listed, err := s.ListTools(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			names := make([]string, 0, len(listed.Tools))
			for _, tool := range listed.Tools {
				names = append(names, tool.Name)
			}
			sort.Strings(names)
			if strings.Join(names, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("tools=%v want=%v", names, tc.want)
			}
			for _, name := range names {
				if name == "ai_worker" {
					t.Fatal("legacy ai_worker was publicly listed")
				}
			}
		})
	}
}

func TestRoleToolSchemaIsSharedSimpleAndHasNoAgent(t *testing.T) {
	dir := t.TempDir()
	c := config.Default()
	c.Directory = dir
	c.Agents = map[string]config.AgentRouting{
		"implementation": {Exposed: true, Agent: "build"},
		"review":         {Exposed: true, Agent: "reviewer"},
		"research":       {Exposed: true, Agent: "explore"},
	}
	if err := config.Save(filepath.Join(dir, "config.toml"), c); err != nil {
		t.Fatal(err)
	}
	s := connect(t, binary(t), dir, nil)
	defer s.Close()
	listed, err := s.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var canonical string
	for _, tool := range listed.Tools {
		if !strings.HasPrefix(tool.Name, "delegate_") {
			continue
		}
		if tool.Title == "" || !strings.Contains(tool.Description, "configured external") {
			t.Fatalf("weak role metadata: %#v", tool)
		}
		b, _ := json.Marshal(tool.InputSchema)
		var schema map[string]any
		if json.Unmarshal(b, &schema) != nil {
			t.Fatal("invalid schema")
		}
		props := schema["properties"].(map[string]any)
		if _, ok := props["agent"]; ok {
			t.Fatalf("%s exposes agent", tool.Name)
		}
		for _, field := range []string{"prompt", "workspace_id"} {
			if _, ok := props[field]; !ok {
				t.Errorf("%s missing %s", tool.Name, field)
			}
		}
		if _, ok := props["capabilities"]; ok {
			t.Errorf("%s exposes caller-controlled capabilities", tool.Name)
		}
		if !strings.Contains(tool.Description, "configured llmTools role policy") || !strings.Contains(tool.Description, "not requested by the caller") {
			t.Errorf("%s does not describe server-owned execution policy: %s", tool.Name, tool.Description)
		}
		for _, overclaim := range []string{"full repository access", "write access", "coding permission"} {
			if strings.Contains(tool.Description, overclaim) {
				t.Errorf("%s overclaims %q: %s", tool.Name, overclaim, tool.Description)
			}
		}
		// The published description states the same policy the server enforces.
		if tool.Name == "delegate_research" {
			if !strings.Contains(tool.Description, "context-only") || strings.Contains(tool.Description, "read-only tools") {
				t.Errorf("%s does not describe context-only execution: %s", tool.Name, tool.Description)
			}
		} else if strings.HasPrefix(tool.Name, "delegate_") {
			for _, phrase := range []string{"server-controlled read-only", "cannot expand", "required when more than one grant is active"} {
				if !strings.Contains(tool.Description, phrase) {
					t.Errorf("%s description missing %q: %s", tool.Name, phrase, tool.Description)
				}
			}
		}
		if len(props) != 2 {
			t.Fatalf("legacy task fields remain: %v", props)
		}
		text := string(b)
		if canonical == "" {
			canonical = text
		} else if text != canonical {
			t.Error("role tools do not share a prompt contract")
		}

	}
}
