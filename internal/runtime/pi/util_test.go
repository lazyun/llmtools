package pi

import (
	"ai-worker-mcp/internal/agent"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func codeOf(t *testing.T, err error) string {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error")
	}
	var coded *agent.Error
	if !errors.As(err, &coded) {
		t.Fatalf("error is not in llmTools' code vocabulary: %T %v", err, err)
	}
	return coded.Code
}

// splitModel must keep a Pi catalogue id intact: those ids contain "/" themselves
// while llmTools' routing identity is provider + "/" + id.
func TestSplitModelSplitsOnce(t *testing.T) {
	provider, model, e := splitModel("huggingface/deepseek-ai/DeepSeek-V4-Flash")
	if e != nil {
		t.Fatal(e)
	}
	if provider != "huggingface" || model != "deepseek-ai/DeepSeek-V4-Flash" {
		t.Fatalf("split: %q %q", provider, model)
	}
}

func TestSplitModelRejectsUnusableIdentities(t *testing.T) {
	for _, v := range []string{"", "deepseek", "/DeepSeek", "huggingface/", "huggingface /x"} {
		if _, _, e := splitModel(v); e == nil {
			t.Fatalf("%q was accepted as a routing identity", v)
		} else if codeOf(t, e) != "MODEL_NOT_AVAILABLE" {
			t.Fatalf("%q mapped to %v", v, e)
		}
	}
}

// Pi's catalogue is only useful to llmTools if it re-enters Core as the same
// provider/model identity Core already routes on.
func TestParseModelTableBuildsRoutingIdentities(t *testing.T) {
	table := `provider  model  context  input
huggingface  deepseek-ai/DeepSeek-V4-Flash  128000  $0.25/M
openai  gpt-5-mini  400000  $0.25/M
huggingface  deepseek-ai/DeepSeek-V4-Flash  128000  $0.25/M

│ no table borders here
anthropic  claude-opus-4-1
`
	models := parseModelTable(table)
	if len(models) != 3 {
		t.Fatalf("parsed %+v", models)
	}
	want := []string{"huggingface/deepseek-ai/DeepSeek-V4-Flash", "openai/gpt-5-mini", "anthropic/claude-opus-4-1"}
	for i, id := range want {
		if models[i].ID != id {
			t.Fatalf("model %d id: %q want %q", i, models[i].ID, id)
		}
		if models[i].DisplayName != id {
			t.Fatalf("model %d display: %q", i, models[i].DisplayName)
		}
		provider, model, e := splitModel(models[i].ID)
		if e != nil || provider != models[i].ProviderID || model != models[i].ModelID {
			t.Fatalf("identity does not round-trip through splitModel: %+v %v %v", models[i], provider, model)
		}
	}
}

// TestParseModelTableAcceptsNothingElse covers the rows a fixed-width table
// renderer puts around real data: headers, borders, bullets and counts.
func TestParseModelTableAcceptsNothingElse(t *testing.T) {
	if got := parseModelTable(""); got != nil {
		t.Fatalf("empty output parsed as %v", got)
	}
	for _, line := range []string{
		"provider  model  context  max-out  thinking  images",
		"Total: 3 models",
		"│  huggingface  deepseek-ai/DeepSeek-V4-Flash",
		"-  gpt-5",
		"huggingface",
	} {
		if got := parseModelTable(line); len(got) != 0 {
			t.Fatalf("%q parsed as %+v", line, got)
		}
	}
}

// TestParseModelTableMatchesRealPiOutput pins the parser to the measured shape
// of `pi --offline --list-models` (0.85.1), including ids that contain "/".
func TestParseModelTableMatchesRealPiOutput(t *testing.T) {
	out := parseModelTable(`provider     model                                     context  max-out  thinking  images
huggingface  deepseek-ai/DeepSeek-V4-Flash             1.0M     384K     yes       no    
openai       gpt-5-mini                                400K     128K     yes       no
`)
	if len(out) != 2 {
		t.Fatalf("real table parsed as %+v", out)
	}
	if out[0].ID != "huggingface/deepseek-ai/DeepSeek-V4-Flash" || out[1].ID != "openai/gpt-5-mini" {
		t.Fatalf("routing identities: %+v", out)
	}
}

func TestNewControlTokenShape(t *testing.T) {
	first, second := newControlToken(), newControlToken()
	if !strings.HasPrefix(first, "ctl_") || len(first) != len("ctl_")+32 {
		t.Fatalf("token shape: %q", first)
	}
	if first == second {
		t.Fatal("two runs share one control token")
	}
	if strings.Contains(first, " ") || strings.Contains(first, "/") {
		t.Fatalf("token is not a safe single argument: %q", first)
	}
	for _, r := range strings.TrimPrefix(first, "ctl_") {
		if !strings.ContainsRune("0123456789abcdef", r) {
			t.Fatalf("token is not hex: %q", first)
		}
	}
}

func TestJSONFieldReadsOnlyTopLevelStrings(t *testing.T) {
	raw := json.RawMessage(`{"type":"tool_execution_start","toolName":"llmtools_control_repo_read","args":{"toolName":"nested"}}`)
	if got := jsonField(raw, "toolName"); got != "llmtools_control_repo_read" {
		t.Fatalf("jsonField: %q", got)
	}
	if got := jsonField(raw, "missing"); got != "" {
		t.Fatalf("missing field: %q", got)
	}
	if got := jsonField([]byte(`{"args":1}`), "args"); got != "" {
		t.Fatalf("a non-string field was reported as text: %q", got)
	}
	if got := jsonField([]byte("not json"), "toolName"); got != "" {
		t.Fatalf("undecodable record: %q", got)
	}
}

func TestErrorTextPrefersTheFirstUsableCandidate(t *testing.T) {
	if got := errorText(json.RawMessage(`null`), json.RawMessage(`"boom"`)); got != "boom" {
		t.Fatalf("errorText: %q", got)
	}
	if got := errorText(); got != "" {
		t.Fatalf("no candidates: %q", got)
	}
	// Pi also reports failures as an object; the raw JSON is still operator
	// evidence as long as it is bounded and redacted below.
	if got := errorText(json.RawMessage(`{"code":"provider_error"}`)); !strings.Contains(got, "provider_error") {
		t.Fatalf("structured failure text: %q", got)
	}
}

// Pi echoes provider payloads that can contain an API key, so nothing reaches an
// operator stream unredacted or unbounded.
func TestRedactSecretsBoundsAndMasks(t *testing.T) {
	long := strings.Repeat("a", 40)
	got := redactSecrets("failed with   key\n" + long + " and sk-hf_0123456789abcdef0123456789")
	if strings.Contains(got, long) || strings.Contains(got, "sk-hf_0123456789") {
		t.Fatalf("secret survived redaction: %q", got)
	}
	if !strings.Contains(got, "[redacted]") {
		t.Fatalf("nothing was redacted: %q", got)
	}
	if len(got) > 240 {
		t.Fatalf("redacted text is unbounded: %d", len(got))
	}
	if strings.Contains(got, "\n") || strings.Contains(got, "  ") {
		t.Fatalf("redacted text kept raw whitespace: %q", got)
	}
}

// mapAssistantError must never return Pi's text as Core state, and a model-side
// block must not be reported as an adapter failure.
func TestMapAssistantErrorUsesTheRuntimeNeutralVocabulary(t *testing.T) {
	for _, tc := range []struct {
		text, code string
	}{
		{`401 Unauthorized`, "UPSTREAM_UNAVAILABLE"},
		{`Request to provider huggingface failed: 429`, "UPSTREAM_UNAVAILABLE"},
		{`402 "You have depleted your monthly included credits."`, "UPSTREAM_UNAVAILABLE"},
		{`invalid api key`, "UPSTREAM_UNAVAILABLE"},
		{`unknown model: deepseek-ai/Absent`, "MODEL_NOT_AVAILABLE"},
		{"connection reset by peer", "RUNTIME_UNAVAILABLE"},
		{"", "RUNTIME_UNAVAILABLE"},
	} {
		if got := codeOf(t, mapAssistantError(tc.text)); got != tc.code {
			t.Fatalf("%q mapped to %s want %s", tc.text, got, tc.code)
		}
	}
}

func TestMapProcessErrorClassifiesWithoutLeaking(t *testing.T) {
	if got := codeOf(t, mapProcessError(nil, nil)); got != "INTERNAL_ERROR" {
		t.Fatalf("nil error: %s", got)
	}
	p := &process{stderr: []byte("open /nonexistent/pi: no such file or directory")}
	if got := codeOf(t, mapProcessError(p, errors.New("exit status 1"))); got != "RUNTIME_NOT_FOUND" {
		t.Fatalf("missing binary: %s", got)
	}
	p = &process{stderr: []byte("HTTP 401 invalid api key")}
	if got := codeOf(t, mapProcessError(p, errors.New("exit status 1"))); got != "UPSTREAM_UNAVAILABLE" {
		t.Fatalf("credential failure: %s", got)
	}
	p = &process{stderr: []byte("upstream 429 rate limit")}
	if got := codeOf(t, mapProcessError(p, errors.New("exit status 1"))); got != "UPSTREAM_UNAVAILABLE" {
		t.Fatalf("throttle: %s", got)
	}
	// An error that is already in the shared vocabulary passes through unchanged.
	if got := codeOf(t, mapProcessError(nil, agent.E("SESSION_LOST"))); got != "SESSION_LOST" {
		t.Fatalf("coded error pass-through: %s", got)
	}
}

func TestWorkspaceRootNeverInventsAPath(t *testing.T) {
	if got := workspaceRoot(agent.AgentTask{}); got != "" {
		t.Fatalf("task without a workspace: %q", got)
	}
	if got := workspaceRoot(agent.AgentTask{Workspace: &agent.WorkspaceSnapshot{CanonicalRoot: "/tmp/x"}}); got != "/tmp/x" {
		t.Fatalf("workspace root: %q", got)
	}
}
