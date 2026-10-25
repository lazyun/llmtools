package opencode

import (
	"ai-worker-mcp/internal/agent"
	"ai-worker-mcp/internal/repocontrol"
	"ai-worker-mcp/internal/store"
	"context"
	"encoding/json"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// The repository tools are llmTools-owned. Exposed names follow the control
// MCP server prefix, which is also what the session permission ruleset allows.
const (
	repoReadToolName = controlMCPName + "_repo_read"
	repoGlobToolName = controlMCPName + "_repo_glob"
	repoGrepToolName = controlMCPName + "_repo_grep"
)

func text(result any) *sdk.CallToolResult {
	b, _ := json.Marshal(result)
	return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: string(b)}}}
}

// addRepoTools is the OpenCode transport for the shared control implementation;
// the tool bodies live in internal/repocontrol so no runtime owns a second copy
// of workspace authorization.
func addRepoTools(server *sdk.Server, st *store.Store, boundToken ...string) error {
	readSchema, e := jsonschema.For[repocontrol.ReadInput](nil)
	if e != nil {
		return e
	}
	globSchema, e := jsonschema.For[repocontrol.GlobInput](nil)
	if e != nil {
		return e
	}
	grepSchema, e := jsonschema.For[repocontrol.GrepInput](nil)
	if e != nil {
		return e
	}
	add := func(name, description string, schema *jsonschema.Schema) {
		if len(boundToken) > 0 && boundToken[0] != "" {
			hideControlToken(schema)
		}
		server.AddTool(&sdk.Tool{Name: name, Description: description, InputSchema: schema}, func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			raw := req.Params.Arguments
			if len(boundToken) > 0 && boundToken[0] != "" {
				var args map[string]json.RawMessage
				if json.Unmarshal(raw, &args) != nil || args == nil {
					return nil, agent.E("CONFIG_ERROR")
				}
				args["control_token"], _ = json.Marshal(boundToken[0])
				raw, _ = json.Marshal(args)
			}
			done := make(chan struct{})
			defer close(done)
			if name == "repo_exec" {
				go func() {
					tick := time.NewTicker(5 * time.Second)
					defer tick.Stop()
					for {
						select {
						case <-done:
							return
						case <-ctx.Done():
							return
						case <-tick.C:
							if token := req.Params.GetProgressToken(); token != nil {
								_ = req.Session.NotifyProgress(ctx, &sdk.ProgressNotificationParams{ProgressToken: token, Progress: 0, Message: "Project command running"})
							}
						}
					}
				}()
			}
			out, e := repocontrol.Call(ctx, st, name, raw)
			if e != nil {
				return nil, e
			}
			return text(json.RawMessage(out)), nil
		})
	}
	add("repo_read", repocontrol.DescriptionRead, readSchema)
	add("repo_glob", repocontrol.DescriptionGlob, globSchema)
	add("repo_grep", repocontrol.DescriptionGrep, grepSchema)
	writeSchema, err := jsonschema.For[repocontrol.WriteInput](nil)
	if err != nil {
		return err
	}
	editSchema, err := jsonschema.For[repocontrol.EditInput](nil)
	if err != nil {
		return err
	}
	execSchema, err := jsonschema.For[repocontrol.ExecInput](nil)
	if err != nil {
		return err
	}
	add("repo_write", repocontrol.DescriptionWrite, writeSchema)
	add("repo_edit", repocontrol.DescriptionEdit, editSchema)
	add("repo_exec", repocontrol.DescriptionExec, execSchema)
	return nil
}
