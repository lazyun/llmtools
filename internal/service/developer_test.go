package service_test

import (
	"ai-worker-mcp/internal/agent"
	"ai-worker-mcp/internal/config"
	"context"
	"database/sql"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDeveloperReservesWorkspaceAndKeepsPolicyOnContinuation(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS sandbox required")
	}
	s, _ := setup(t, "", request("ctx_1"), agent.AgentResult{Status: "completed", Content: "tested"}, agent.AgentResult{Status: "completed", Content: "next"})
	c, _ := config.Load(filepath.Join(s.Config.Directory, "config.toml"))
	c.Agents["developer"] = config.AgentRouting{Exposed: true, Agent: "fixture"}
	config.Save(filepath.Join(c.Directory, "config.toml"), c)
	executableWorkspace(t, s, "repo")
	first := s.RunRole(context.Background(), "developer", raw(agent.DelegateTask{Prompt: "implement and test"}), nil)
	if first.Status != "needs_context" {
		t.Fatal(first)
	}
	blocked := s.RunRole(context.Background(), "developer", raw(agent.DelegateTask{Prompt: "conflicting writer"}), nil)
	if blocked.Error == nil || blocked.Error.Code != "WORKSPACE_BUSY" {
		t.Fatal(blocked)
	}
	done := s.Continue(context.Background(), raw(answer(first.WorkerRunID, "ctx_1")), nil)
	if done.Status != "completed" {
		t.Fatal(done)
	}
	run, _ := s.Store.Get(first.WorkerRunID)
	if run.Capabilities != agent.DeveloperCapabilities() {
		t.Fatalf("lost policy %+v", run)
	}
	next := s.RunRole(context.Background(), "developer", raw(agent.DelegateTask{Prompt: "next"}), nil)
	if next.Status != "needs_context" {
		t.Fatal(next)
	}
}

func TestReadOnlyRoleCannotResumeWithDeveloperPolicy(t *testing.T) {
	s, _ := setup(t, "", request("ctx_1"))
	executableWorkspace(t, s, "repo")
	first := s.RunRole(context.Background(), "implementation", raw(agent.DelegateTask{Prompt: "inspect"}), nil)
	db, err := sql.Open("sqlite3", filepath.Join(s.Config.Directory, s.Config.Storage.Database))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE runs SET body=json_set(body,'$.capabilities.repoWrite',json('true'),'$.capabilities.shell',json('true')) WHERE id=?`, first.WorkerRunID); err != nil {
		t.Fatal(err)
	}
	done := s.Continue(context.Background(), raw(answer(first.WorkerRunID, "ctx_1")), nil)
	if done.Error == nil || done.Error.Code != "ROLE_CAPABILITY_POLICY_INVALID" {
		t.Fatal(done)
	}
}
