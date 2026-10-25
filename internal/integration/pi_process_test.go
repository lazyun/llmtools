package integration_test

import (
	"ai-worker-mcp/internal/agent"
	"ai-worker-mcp/internal/config"
	"ai-worker-mcp/internal/store"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// The actual shipped MCP and Pi factory run against this provider-free JSONL fixture.
const piProcessFixture = `#!/usr/bin/env python3
import sys,json,os,uuid,time,pathlib
a=sys.argv[1:]
if '--version' in a: print('1.0.2-fixture');sys.exit(0)
if '--list-models' in a:
 print('provider model context max-out thinking images\nopenai gpt-5-mini 400K 128K yes no\nopenai gpt-4.1 1M 32K no no');sys.exit(0)
def arg(k,d): return a[a.index(k)+1] if k in a else d
session=arg('--session','fixture-'+str(uuid.uuid4()))
provider=arg('--provider','openai');model=arg('--model','gpt-5-mini');streaming=False
session_file=str(pathlib.Path(arg('--session-dir',os.getcwd()))/('date_'+session+'.jsonl'))
def out(x): print(json.dumps(x),flush=True)
for line in sys.stdin:
 q=json.loads(line);kind=q.get('type');data={}
 if kind=='get_state':data={'sessionId':session,'sessionFile':session_file,'isStreaming':streaming,'messageCount':1}
 if kind=='get_last_assistant_text':data={'text':'fixture completed'}
 if kind=='abort':streaming=False
 out({'type':'response','id':q['id'],'command':kind,'success':True,'data':data})
 if kind!='prompt':continue
 prompt=q.get('message','')
 pathlib.Path(session_file).parent.mkdir(parents=True,exist_ok=True)
 if not pathlib.Path(session_file).exists():pathlib.Path(session_file).write_text(json.dumps({'type':'session','version':3,'id':session,'cwd':os.getcwd()})+'\n')
 if os.getenv('TEST_PI_LOG'):
  with open(os.environ['TEST_PI_LOG'],'a') as f:f.write(json.dumps(prompt)+'\n')
 if prompt=='WAIT':streaming=True;continue
 if os.getenv('TEST_PI_DELAY') and pathlib.Path(os.environ['TEST_PI_DELAY']).exists():time.sleep(1.2)
 if 'NEED_CONTEXT' in prompt and 'llmtools_control_response' not in prompt:
  out({'type':'tool_execution_start','toolName':'llmtools_control_request_context'})
  out({'type':'tool_execution_end','toolName':'llmtools_control_request_context','result':{'details':{'llmtools_control':'context','requests':[{'id':'ctx_1','kind':'file','target':'contract.txt','reason':'Missing contract'}]}}})
 if 'NEED_DECISION' in prompt and 'llmtools_control_response' not in prompt:
  out({'type':'tool_execution_start','toolName':'llmtools_control_request_decision'})
  out({'type':'tool_execution_end','toolName':'llmtools_control_request_decision','result':{'details':{'llmtools_control':'decision','questions':[{'id':'q1','question':'Choose a shape','options':[{'label':'flat','description':'a list'},{'label':'cursor','description':'paged'}]}]}}})
 out({'type':'message_end','message':{'role':'assistant','provider':provider,'model':model,'stopReason':'stop'}})
 out({'type':'agent_settled'})
`

func piProcessConfig(t *testing.T) config.Config {
	t.Helper()
	dir := t.TempDir()
	binary := filepath.Join(dir, "pi")
	if err := os.WriteFile(binary, []byte(piProcessFixture), 0700); err != nil {
		t.Fatal(err)
	}
	c := config.DefaultAt(dir)
	c.Directory = dir
	c.Runtime.Binary = binary
	for _, role := range []string{"implementation", "developer", "review", "research"} {
		c.Agents[role] = config.AgentRouting{Exposed: true, Model: "openai/gpt-5-mini"}
	}
	if err := config.Save(filepath.Join(dir, "config.toml"), c); err != nil {
		t.Fatal(err)
	}
	grantWorkspace(t, dir, c.Storage.Database)
	return c
}

func TestPiMCPRestartContinuationPreservesSessionRoutingAndPrompt(t *testing.T) {
	for _, kind := range []string{"context", "decision"} {
		t.Run(kind, func(t *testing.T) {
			c := piProcessConfig(t)
			log := filepath.Join(c.Directory, "prompts.jsonl")
			t.Setenv("TEST_PI_LOG", log)
			b := binary(t)
			client := connect(t, b, c.Directory, nil)
			prompt := "NEED_" + strings.ToUpper(kind)
			first := call(t, client, "delegate_implementation", agent.DelegateTask{Prompt: prompt})
			if first.Status != "needs_"+kind {
				t.Fatal(first)
			}
			client.Close()
			st, err := store.Open(filepath.Join(c.Directory, c.Storage.Database))
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			before, _ := st.Get(first.WorkerRunID)
			if before.RuntimeBinary != c.Runtime.Binary || before.RuntimeSessionDir != c.Runtime.SessionDir || before.RuntimeSessionFile == "" {
				t.Fatal("Pi session location was not frozen", before)
			}
			c.Agents["implementation"] = config.AgentRouting{Exposed: false, Model: "openai/gpt-4.1"}
			if err := config.Save(filepath.Join(c.Directory, "config.toml"), c); err != nil {
				t.Fatal(err)
			}
			client = connect(t, b, c.Directory, nil)
			defer client.Close()
			answer := agent.Continuation{WorkerRunID: first.WorkerRunID}
			if kind == "context" {
				answer.AdditionalContext = []agent.AdditionalContext{{Type: "file", Path: "contract.txt", Content: "supplied"}}
			} else {
				answer.DecisionResponses = []agent.DecisionResponse{{Selection: "option_1"}}
			}
			final := call(t, client, "ai_worker_continue", answer)
			if final.Status != "completed" || final.Model != "openai/gpt-5-mini" {
				t.Fatal(final)
			}
			after, _ := st.Get(first.WorkerRunID)
			if before.RuntimeSessionID != after.RuntimeSessionID || after.Runtime != "pi" {
				t.Fatal("session replaced", before, after)
			}
			raw, _ := os.ReadFile(log)
			lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
			if len(lines) != 2 {
				t.Fatal(string(raw))
			}
			var original string
			json.Unmarshal([]byte(lines[0]), &original)
			if original != prompt || strings.Contains(lines[1], prompt) {
				t.Fatal("prompt replayed", string(raw))
			}
		})
	}
}

func TestPiMCPPublicRuntimeCannotStartOpenCode(t *testing.T) {
	dir := t.TempDir()
	c := config.DefaultAt(dir)
	c.Directory = dir
	c.Runtime = config.Runtime{Type: "opencode", Mode: "managed", Binary: "opencode", Host: "127.0.0.1"}
	if err := config.Save(filepath.Join(dir, "config.toml"), c); err != nil {
		t.Fatal(err)
	}
	b := binary(t)
	out, err := exec.Command(b, "--config-dir", dir).CombinedOutput()
	if err == nil || !strings.Contains(string(out), "only Pi is supported") {
		t.Fatalf("legacy runtime accepted: %s %v", out, err)
	}
	for _, flag := range []string{"--llmtools-runtime-host", "--llmtools-context-control"} {
		if err := exec.Command(b, flag).Run(); err == nil {
			t.Fatalf("retired entry accepted: %s", flag)
		}
	}
}

func TestPiMCPWorkspaceRevocationBlocksPendingContinuation(t *testing.T) {
	c := piProcessConfig(t)
	client := connect(t, binary(t), c.Directory, nil)
	defer client.Close()
	first := call(t, client, "delegate_implementation", agent.DelegateTask{Prompt: "NEED_CONTEXT"})
	if first.Status != "needs_context" {
		t.Fatal(first)
	}
	st, err := store.Open(filepath.Join(c.Directory, c.Storage.Database))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	run, _ := st.Get(first.WorkerRunID)
	if err := st.RevokeWorkspace(run.Workspace.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	final := call(t, client, "ai_worker_continue", agent.Continuation{WorkerRunID: first.WorkerRunID, AdditionalContext: []agent.AdditionalContext{{Type: "file", Path: "contract.txt", Content: "answer"}}})
	if final.Error == nil || final.Error.Code != "WORKSPACE_AUTHORIZATION_REVOKED" {
		t.Fatal(final)
	}
}

func TestPiMCPExecutingTaskCanBeCancelledFromAnotherProcess(t *testing.T) {
	c := piProcessConfig(t)
	b := binary(t)
	started := make(chan string, 1)
	client := connect(t, b, c.Directory, func(p agent.Progress) {
		if p.Stage == "RUNNING" {
			select {
			case started <- p.WorkerRunID:
			default:
			}
		}
	})
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		params := &sdk.CallToolParams{Name: "delegate_implementation", Arguments: agent.DelegateTask{Prompt: "WAIT"}}
		params.SetProgressToken("running")
		_, err := client.CallTool(ctx, params)
		done <- err
	}()
	var id string
	select {
	case id = <-started:
	case <-ctx.Done():
		t.Fatal("Pi task did not start")
	}
	other := connect(t, b, c.Directory, nil)
	defer other.Close()
	result := call(t, other, "ai_worker_cancel", map[string]any{"worker_run_id": id})
	if result.Status != "cancelled" {
		t.Fatal(result)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("cancelled task did not finish")
	}
	st, err := store.Open(filepath.Join(c.Directory, c.Storage.Database))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	run, _ := st.Get(id)
	if run.Status != "CANCELLED" {
		t.Fatal(run.Status)
	}
}
