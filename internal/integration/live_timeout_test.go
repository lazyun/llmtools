package integration_test

import (
	"ai-worker-mcp/internal/agent"
	"ai-worker-mcp/internal/config"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestExistingMCPConnectionReloadsContinuationTimeout(t *testing.T) {
	c := piProcessConfig(t)
	c.General.TaskTimeoutSeconds = 1
	path := filepath.Join(c.Directory, "config.toml")
	config.Save(path, c)
	delay := filepath.Join(c.Directory, "delay")
	t.Setenv("TEST_PI_DELAY", delay)
	client := connect(t, binary(t), c.Directory, nil)
	defer client.Close()
	first := call(t, client, "delegate_implementation", agent.DelegateTask{Prompt: "NEED_CONTEXT"})
	if first.Status != "needs_context" {
		t.Fatal(first)
	}
	c.General.TaskTimeoutSeconds = 3
	c.Agents["implementation"] = config.AgentRouting{Exposed: true, Model: "openai/gpt-4.1"}
	if err := config.Save(path, c); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(delay, []byte("delay"), 0600)
	start := time.Now()
	final := call(t, client, "ai_worker_continue", agent.Continuation{WorkerRunID: first.WorkerRunID, AdditionalContext: []agent.AdditionalContext{{Type: "file", Path: "contract.txt", Content: "answer"}}})
	if time.Since(start) < time.Second || final.Status != "completed" || final.Model != "openai/gpt-5-mini" {
		t.Fatal(final)
	}
}
