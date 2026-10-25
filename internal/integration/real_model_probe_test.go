package integration_test

import (
	"ai-worker-mcp/internal/agent"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The real-model acceptance round must not spend a full task on an upstream that
// still refuses to serve, so this probe makes one minimal call through the
// unchanged production routing and classifies the outcome.
//
//	LLMTOOLS_TEST_OPENCODE=$(command -v opencode) LLMTOOLS_REAL_E2E=1 \
//	  go test ./internal/integration -run TestRealRouteAvailabilityProbe -timeout 15m -v

// probeLog is the managed runtime's own log. Only the signatures below are
// extracted from it, because the file also carries provider response bodies.
func probeLog() string {
	home := os.Getenv("HOME")
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".local", "share", "opencode", "log", "opencode.log")
}

// upstreamSignatures are the failure shapes that mean "the route's provider
// cannot serve this request", as opposed to an llmTools or harness defect.
var upstreamSignatures = []string{
	"all targets were skipped by pre-dispatch filters",
	"Service temporarily unavailable",
	"Budget pool quota",
	"AI_APICallError",
}

// probeUpstream returns the upstream signatures that appear in the runtime log
// after `since`, plus one truncated copy of the provider error message. Log
// lines look like `timestamp=<RFC3339> level=… … error.error="…"`; only the
// timestamp and the matched signature are read, never the bodies around them.
func probeUpstream(since time.Time) ([]string, string) {
	path := probeLog()
	if path == "" {
		return nil, ""
	}
	b, e := os.ReadFile(path)
	if e != nil {
		return nil, ""
	}
	var hits []string
	var message string
	for _, line := range strings.Split(string(b), "\n") {
		i := strings.Index(line, "timestamp=")
		if i < 0 {
			continue
		}
		fields := strings.Fields(line[i:])
		var ts string
		for _, f := range fields {
			if k, v, _ := strings.Cut(f, "="); k == "timestamp" {
				ts = v
			}
		}
		parsed, e := time.Parse(time.RFC3339Nano, ts)
		if e != nil || parsed.Before(since) {
			continue
		}
		for _, s := range upstreamSignatures {
			if strings.Contains(line, s) {
				seen := false
				for _, h := range hits {
					seen = seen || h == s
				}
				if !seen {
					hits = append(hits, s)
				}
			}
		}
		if message == "" {
			if _, v, ok := strings.Cut(line, `error.error="`); ok {
				v, _, _ = strings.Cut(v, `"`)
				if len(v) > 200 {
					v = v[:200]
				}
				message = v
			}
		}
	}
	return hits, message
}

func TestRealRouteAvailabilityProbe(t *testing.T) {
	r := setupRealRoute(t)
	task := agent.DelegateTask{Prompt: "Reply with exactly the single token PROBE_OK and nothing else." + "\n\nConstraints:\n" +
		"Do not call any tool." + "\n" + "Do not read the repository." + "\n\nDecisions:\n" +
		"This is a transport availability probe, not a work item." + "\n\nAcceptanceCriteria:\n" +
		"The reply is PROBE_OK.", WorkspaceID: r.wsA.ID,
	}
	since := time.Now().UTC()
	result, run := r.delegate(t, "delegate_implementation", task)
	signatures, message := probeUpstream(since)
	code := ""
	if result.Error != nil {
		code = result.Error.Code
	}
	verdict := "PRODUCT_DEFECT"
	switch {
	case result.Status == "completed" && strings.Contains(result.Content, "PROBE_OK"):
		verdict = "UPSTREAM_AVAILABLE"
	case code == "RUNTIME_UNAVAILABLE" && len(signatures) > 0:
		verdict = "UPSTREAM_BLOCKED"
	}
	evidence := map[string]any{
		"started_at":          since.Format(time.RFC3339),
		"finished_at":         time.Now().UTC().Format(time.RFC3339),
		"role":                "implementation",
		"agent":               result.Agent,
		"model":               result.Model,
		"runtime":             result.Runtime,
		"worker_run_id":       result.WorkerRunID,
		"session":             run.RuntimeSessionID,
		"workspace_id":        r.wsA.ID,
		"status":              result.Status,
		"error_code":          code,
		"upstream_signatures": signatures,
		"upstream_error":      message,
		"verdict":             verdict,
	}
	path := filepath.Join("..", "..", ".smoke", "read-only-repo", "availability-probe.jsonl")
	// §6 allows a bounded number of probe attempts; the evidence file counts them.
	attempt := 1
	if prev, e := os.ReadFile(path); e == nil {
		attempt += strings.Count(string(prev), `"verdict":`)
	}
	evidence["attempt"] = attempt
	// One compact object per line keeps the whole attempt history JSONL-readable.
	body, e := json.Marshal(evidence)
	if e != nil {
		t.Fatal(e)
	}
	body = append(body, '\n')
	f, e := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if e != nil {
		t.Log(e)
	} else {
		if _, e := f.Write(body); e != nil {
			t.Log(e)
		}
		f.Close()
	}
	t.Logf("availability probe evidence: %s", body)
	if verdict != "UPSTREAM_AVAILABLE" {
		t.Fatalf("availability probe: %+v", evidence)
	}
	t.Logf("availability probe %s on %s/%s", verdict, result.Agent, result.Model)
}
