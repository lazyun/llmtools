package integration_test

import (
	"ai-worker-mcp/internal/agent"
	"ai-worker-mcp/internal/store"
	"context"
	"encoding/json"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func binary(t *testing.T) string {
	t.Helper()
	if p := os.Getenv("AI_WORKER_MCP_BINARY"); p != "" {
		return p
	}
	p := filepath.Join(t.TempDir(), "ai-worker-mcp")
	cmd := exec.Command("go", "build", "-o", p, "./cmd/ai-worker-mcp")
	cmd.Dir = "../.."
	if b, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("build: %v %s", e, b)
	}
	return p
}
func connect(t *testing.T, binary, dir string, progress func(agent.Progress)) *sdk.ClientSession {
	t.Helper()
	opts := &sdk.ClientOptions{}
	if progress != nil {
		opts.ProgressNotificationHandler = func(_ context.Context, r *sdk.ProgressNotificationClientRequest) {
			var p agent.Progress
			if json.Unmarshal([]byte(r.Params.Message), &p) == nil {
				progress(p)
			}
		}
	}
	client := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "1"}, opts)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s, e := client.Connect(ctx, &sdk.CommandTransport{Command: exec.Command(binary, "--config-dir", dir)}, nil)
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func call(t *testing.T, s *sdk.ClientSession, name string, args any) agent.AgentResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	p := &sdk.CallToolParams{Name: name, Arguments: args}
	p.SetProgressToken("progress")
	r, e := s.CallTool(ctx, p)
	if e != nil {
		t.Fatal(e)
	}
	b, _ := json.Marshal(r.StructuredContent)
	var result agent.AgentResult
	if json.Unmarshal(b, &result) != nil {
		t.Fatalf("invalid result %s", b)
	}
	if strings.Contains(string(b), "ses_mock") {
		t.Fatal("runtime session ID leaked to MCP")
	}
	return result
}
func grantWorkspace(t *testing.T, dir, dbName string) store.Workspace {
	t.Helper()
	st, e := store.Open(filepath.Join(dir, dbName))
	if e != nil {
		t.Fatal(e)
	}
	defer st.Close()
	w, _, e := st.AddWorkspace(t.TempDir(), "fixture repository", "desktop_user_grant")
	if e != nil {
		t.Fatal(e)
	}
	return w
}
