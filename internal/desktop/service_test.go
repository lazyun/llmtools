package desktop

import (
	"ai-worker-mcp/internal/agent"
	"ai-worker-mcp/internal/config"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSkillStatusObservation(t *testing.T) {
	missing := skillStatus(filepath.Join(t.TempDir(), "SKILL.md"))
	if missing.Status != "missing" || missing.ExpectedVersion != expectedSkillVersion || missing.ExpectedFingerprint != expectedSkillFingerprint {
		t.Fatal(missing)
	}
	path := filepath.Join(t.TempDir(), "SKILL.md")
	write := func(version, fingerprint string) {
		body := "# Skill\n\nVersion: `" + version + "`\nFingerprint: `" + fingerprint + "`\n"
		if e := os.WriteFile(path, []byte(body), 0o644); e != nil {
			t.Fatal(e)
		}
	}
	write(expectedSkillVersion, expectedSkillFingerprint)
	if got := skillStatus(path); got.Status != "installed" || got.Version != expectedSkillVersion || got.Fingerprint != expectedSkillFingerprint {
		t.Fatal(got)
	}
	write("0.9.0", expectedSkillFingerprint)
	if got := skillStatus(path); got.Status != "outdated" {
		t.Fatal(got)
	}
}

func TestDesktopWorkspaceGrantCanonicalizesAndDeduplicates(t *testing.T) {
	s, e := New(t.TempDir(), "/tmp/mcp")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if e := os.Symlink(real, link); e != nil {
		t.Fatal(e)
	}
	grant, e := s.AddWorkspace(link)
	if e != nil || !grant.Created {
		t.Fatalf("symlink grant: %+v err=%v", grant, e)
	}
	want, _ := filepath.EvalSymlinks(real)
	if grant.Workspace.CanonicalRoot != want {
		t.Fatalf("canonical %q want %q", grant.Workspace.CanonicalRoot, want)
	}
	again, e := s.AddWorkspace(real)
	if e != nil || again.Created || again.Workspace.ID != grant.Workspace.ID {
		t.Fatalf("duplicate grant: %+v err=%v", again, e)
	}
	if e := s.RenameWorkspace(grant.Workspace.ID, "renamed"); e != nil {
		t.Fatal(e)
	}
	if e := s.RevokeWorkspace(grant.Workspace.ID); e != nil {
		t.Fatal(e)
	}
	revoked, _ := s.storage.Workspace(grant.Workspace.ID)
	if revoked.State != "REVOKED" || revoked.Revision != 2 {
		t.Fatalf("revocation not durable: %+v", revoked)
	}
	if _, e := s.AddWorkspace(filepath.Join(t.TempDir(), strings.Repeat("missing", 4))); e == nil {
		t.Fatal("missing directory authorized")
	}
}

func TestDesktopRolePolicyDisplayIsServerOwned(t *testing.T) {
	s, e := New(t.TempDir(), "/tmp/mcp")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	want := map[string]string{"developer": "Project implementation and tests", "implementation": "Repository read-only", "review": "Repository read-only", "research": "Context-only"}
	got := s.State().RolePolicies
	if len(got) != len(want) {
		t.Fatalf("state role policies: %+v", got)
	}
	for role, label := range want {
		if got[role] != label {
			t.Fatalf("role %s displayed %q want %q", role, got[role], label)
		}
	}
	find := func(checks []Check, name string) (Check, bool) {
		for _, c := range checks {
			if c.Name == name {
				return c, true
			}
		}
		return Check{}, false
	}
	diagnostics := s.Diagnostics(context.Background())
	for _, role := range []string{"implementation", "developer", "review", "research"} {
		c, ok := find(diagnostics, "Role Execution Policy: "+role)
		if !ok || c.Status != "PASS" || c.Message != want[role] {
			t.Fatalf("role %s policy check: %+v ok=%v", role, c, ok)
		}
	}
	enforcement, ok := find(diagnostics, "Repository Read Enforcement")
	if !ok || enforcement.Status != "PASS" {
		t.Fatalf("repository read enforcement check: %+v ok=%v", enforcement, ok)
	}
	for _, phrase := range []string{"read/glob/grep enabled", "Developer alone adds confined write/edit", "sandboxed project commands", "write: denied", "shell: denied", "git command: denied", "web: denied", "subagent: denied", "live workspace grant"} {
		if !strings.Contains(enforcement.Message, phrase) {
			t.Fatalf("enforcement message %q missing %q", enforcement.Message, phrase)
		}
	}
	if strings.Contains(enforcement.Message, "write: enabled") || strings.Contains(enforcement.Message, "shell: enabled") {
		t.Fatalf("enforcement message overclaims: %q", enforcement.Message)
	}
	workspace, ok := find(diagnostics, "Workspace Authorization")
	if !ok || !strings.Contains(workspace.Message, "Sources:") {
		t.Fatalf("workspace authorization check: %+v ok=%v", workspace, ok)
	}
	policy, ok := find(diagnostics, "Runtime health")
	if ok {
		t.Fatalf("passive diagnostics probed a runtime: %+v", policy)
	}
	// Passive diagnostics must never start a runtime or call a model.
	if s.State().RuntimeOwned {
		t.Fatal("diagnostics started the managed runtime")
	}
}

func TestDesktopRejectsOpenCodeAndPreservesLegacyHistory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	original := []byte("[runtime]\ntype='opencode'\nmode='managed'\nbinary='opencode'\n[agents.implementation]\nexposed=true\nagent='build'\nmodel='legacy/model'\n")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	s, err := New(dir, "/tmp/mcp")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	state := s.State()
	if state.Config.Runtime.Type != "pi" || state.RuntimeOwned || len(state.Config.Warnings) == 0 {
		t.Fatal(state.Config)
	}
	route := state.Config.Agents["implementation"]
	if route.Exposed || route.Agent != "" || route.Model != "" {
		t.Fatal("legacy routing was silently activated", route)
	}
	if err := s.SaveRuntime(config.Runtime{Type: "opencode", Mode: "managed", Binary: "opencode"}); err == nil {
		t.Fatal("OpenCode accepted")
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(original) {
		t.Fatal("legacy file rewritten before saving Pi")
	}
	if s.StartRuntime(context.Background()) == nil || s.StopRuntime() == nil {
		t.Fatal("server lifecycle enabled")
	}
	if _, err := s.DiscoverAgents(context.Background()); err == nil {
		t.Fatal("OpenCode discovery enabled")
	}
	if err := s.SaveRuntime(config.Runtime{Type: "pi", Binary: "/nonexistent/pi", SessionDir: filepath.Join(dir, "sessions")}); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load(path)
	if err != nil || loaded.Runtime.Type != "pi" {
		t.Fatal(loaded, err)
	}
	if err := s.storage.Create(agent.Run{ID: "wrk_legacy", Runtime: "opencode", LogicalRole: "implementation", RuntimeAgent: "build", Status: "COMPLETED"}); err != nil {
		t.Fatal(err)
	}
	history := s.State().Runs
	if len(history) != 1 || history[0].Runtime != "opencode" || history[0].RuntimeAgent != "build" {
		t.Fatal("historical identity changed", history)
	}
}
