package pi

import (
	"ai-worker-mcp/internal/agent"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"
)

// newControlToken mints the same opaque per-run handle shape the durable
// workspace grant already accepts. The canonical workspace root is never an
// authorization credential, for Pi or any other runtime.
func newControlToken() string {
	return "ctl_" + randomHex(16)
}

func randomHex(n int) string {
	buf := make([]byte, n)
	if _, e := rand.Read(buf); e != nil {
		return strings.TrimPrefix(fmt.Sprintf("%032x", time.Now().UnixNano()), "0")[:2*n]
	}
	return hex.EncodeToString(buf)
}

func shortID() string {
	return randomHex(6)
}

// piDebugf writes one operator diagnostic. It is off unless the spike harness
// asks for it, and never carries content, tokens or paths.
func piDebugf(format string, args ...any) {
	if os.Getenv("LLMTOOLS_PI_DEBUG") == "1" {
		fmt.Fprint(os.Stderr, "LLMTOOLS_PI "+fmt.Sprintf(format, args...)+"\n")
	}
}

// piAuditf writes an operator-visible runtime evidence line. It carries
// identities and names only, never tool arguments, file content or credentials.
func piAuditf(format string, args ...any) {
	fmt.Fprint(os.Stderr, "LLMTOOLS_PI "+fmt.Sprintf(format, args...)+"\n")
}

// jsonField reads one top-level string field from an event payload. An event
// that does not decode is evidence of nothing, so it reports "".
func jsonField(raw []byte, field string) string {
	var probe map[string]json.RawMessage
	if json.Unmarshal(raw, &probe) != nil {
		return ""
	}
	var out string
	if json.Unmarshal(probe[field], &out) != nil {
		return ""
	}
	return out
}

// splitModel maps llmTools' provider/model routing identity onto Pi's flags.
// The two halves are checked with the same name rules the model catalogue is
// parsed with, so a malformed identity can never reach Pi as a launch argument.
func splitModel(model string) (string, string, error) {
	parts := strings.SplitN(model, "/", 2)
	if len(parts) != 2 || !looksLikeProvider(parts[0]) || !looksLikeModel(parts[1]) {
		return "", "", agent.E("MODEL_NOT_AVAILABLE")
	}
	return parts[0], parts[1], nil
}

func workspaceRoot(t agent.AgentTask) string {
	if t.Workspace == nil {
		return ""
	}
	return t.Workspace.CanonicalRoot
}

// parseModelTable reads `pi --list-models`, which prints a header row followed
// by one row per model. Only the first two columns are llmTools' business: the
// row is turned into the same provider/model routing identity Core already
// carries. Pi's own catalogue stays the only model source; llmTools never
// registers a provider or a credential.
func parseModelTable(out string) []agent.RuntimeModel {
	var models []agent.RuntimeModel
	seen := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] == "provider" {
			continue
		}
		provider, model := fields[0], fields[1]
		if !looksLikeProvider(provider) || !looksLikeModel(model) {
			continue
		}
		id := provider + "/" + model
		if seen[id] {
			continue
		}
		seen[id] = true
		models = append(models, agent.RuntimeModel{ID: id, ProviderID: provider, ModelID: model, DisplayName: id})
	}
	return models
}

func looksLikeProvider(v string) bool { return looksLikeName(v, false) }

func looksLikeModel(v string) bool { return looksLikeName(v, true) }

// looksLikeName rejects anything a real id cannot contain and requires at least
// one letter or digit, so table borders, bullets and counts never enter Core's
// routing space. Only a model id may carry "/", because Pi's catalogue ids do.
func looksLikeName(v string, allowSlash bool) bool {
	named := false
	for _, r := range v {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
			named = true
		case r == '-' || r == '.' || r == '_' || (allowSlash && r == '/'):
		default:
			return false
		}
	}
	return named
}

// mapProcessError turns a transport failure into the runtime-neutral error
// vocabulary, using Pi's stderr only to classify, never to report verbatim.
//
// A missing executable is a runtime fault; a provider that answers 401/402/429
// is an upstream one, and only a pre-dispatch protocol violation says nothing
// about either. The three boundaries stay distinguishable in Core state.
func mapProcessError(p *process, err error) error {
	var coded *agent.Error
	if err == nil {
		return agent.E("INTERNAL_ERROR")
	}
	if asAgentError(err, &coded) {
		return coded
	}
	tail := ""
	if p != nil {
		tail = strings.ToLower(p.stderrTail())
	}
	switch {
	case strings.Contains(tail, "no such file"), strings.Contains(tail, "enoent"):
		return agent.E("RUNTIME_NOT_FOUND")
	case agent.MentionsUpstreamBlock(tail):
		return agent.E("UPSTREAM_UNAVAILABLE")
	default:
		return agent.E("RUNTIME_UNAVAILABLE")
	}
}

func asAgentError(err error, out **agent.Error) bool {
	if e, ok := err.(*agent.Error); ok {
		*out = e
		return true
	}
	return false
}

// errorText reads the failure text Pi attaches to an errored assistant record.
// Pi may report it as a string or as a structured object, and either form may
// echo a credential back from the provider, so long opaque runs are redacted
// and the result is bounded before it reaches an operator stream.
func errorText(candidates ...json.RawMessage) string {
	for _, raw := range candidates {
		if len(raw) == 0 || string(raw) == "null" {
			continue
		}
		var text string
		if json.Unmarshal(raw, &text) != nil {
			text = string(raw)
		}
		if strings.TrimSpace(text) == "" {
			continue
		}
		return redactSecrets(text)
	}
	return ""
}

var opaqueRun = regexp.MustCompile(`[A-Za-z0-9_\-]{16,}`)

func redactSecrets(v string) string {
	v = opaqueRun.ReplaceAllString(v, "[redacted]")
	return truncate(strings.Join(strings.Fields(v), " "), 240)
}

// mapAssistantError classifies an errored assistant turn into llmTools'
// runtime-neutral vocabulary. Pi's own text selects the code and is never
// reported as Core state.
//
// A model the runtime catalogue does not contain is MODEL_NOT_AVAILABLE. A model
// that exists but whose provider refuses the request — depleted quota, expired
// auth, rate limit, an upstream outage or a pre-dispatch filter — is
// UPSTREAM_UNAVAILABLE, because pretending the model is missing would blame the
// catalogue for an execution limit, and blaming the runtime would hide which
// boundary actually answered.
func mapAssistantError(text string) error {
	lower := strings.ToLower(text)
	if agent.MentionsUpstreamBlock(lower) {
		return agent.E("UPSTREAM_UNAVAILABLE")
	}
	for _, marker := range []string{"not found", "unknown model", "no such model", "not available", "unsupported model"} {
		if strings.Contains(lower, marker) && strings.Contains(lower, "model") {
			return agent.E("MODEL_NOT_AVAILABLE")
		}
	}
	return agent.E("RUNTIME_UNAVAILABLE")
}

type sessionInfo struct {
	SessionID   string `json:"sessionId"`
	SessionFile string `json:"sessionFile"`
	IsStreaming bool   `json:"isStreaming"`
	Model       struct {
		Provider string `json:"provider"`
		ID       string `json:"id"`
	} `json:"model"`
}

func (p *process) sessionState(ctx context.Context) (sessionInfo, error) {
	callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	data, e := p.request(callCtx, "state-"+shortID(), map[string]any{"type": "get_state"})
	if e != nil {
		return sessionInfo{}, e
	}
	var info sessionInfo
	if json.Unmarshal(data, &info) != nil {
		return sessionInfo{}, agent.E("RUNTIME_PROTOCOL_ERROR")
	}
	return info, nil
}

// waitIdle confirms Pi stopped streaming. An aborted turn never emits
// agent_settled, so cancellation detection cannot rely on events alone.
func (p *process) waitIdle(ctx context.Context) bool {
	deadline := time.Now().Add(15 * time.Second)
	for {
		info, e := p.sessionState(ctx)
		if e == nil && !info.IsStreaming {
			return true
		}
		if !time.Now().Before(deadline) || ctx.Err() != nil {
			return false
		}
		time.Sleep(150 * time.Millisecond)
	}
}

// lastAssistantText is Pi's authoritative final assistant response for the
// current session. Nothing else becomes AgentResult content.
func (p *process) lastAssistantText(ctx context.Context) (string, error) {
	data, e := p.request(ctx, "last-"+shortID(), map[string]any{"type": "get_last_assistant_text"})
	if e != nil {
		return "", e
	}
	var out struct {
		Text *string `json:"text"`
	}
	if json.Unmarshal(data, &out) != nil {
		return "", agent.E("RUNTIME_PROTOCOL_ERROR")
	}
	if out.Text == nil {
		return "", nil
	}
	return *out.Text, nil
}
