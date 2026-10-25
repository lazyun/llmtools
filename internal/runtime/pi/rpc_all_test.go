package pi

import (
	"ai-worker-mcp/internal/agent"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// captureStdin stands in for the Pi child so the protocol layer can be asserted
// without a runtime: llmTools must produce exactly the records Pi reads.
type captureStdin struct {
	mu      sync.Mutex
	writes  []string
	written chan struct{}
}

func newCaptureStdin() *captureStdin {
	return &captureStdin{written: make(chan struct{}, 1)}
}

func (c *captureStdin) Write(p []byte) (int, error) {
	c.mu.Lock()
	c.writes = append(c.writes, string(p))
	c.mu.Unlock()
	select {
	case c.written <- struct{}{}:
	default:
	}
	return len(p), nil
}

func (c *captureStdin) Close() error { return nil }

func (c *captureStdin) last(t *testing.T) string {
	t.Helper()
	select {
	case <-c.written:
	case <-time.After(2 * time.Second):
		t.Fatal("no command reached the runtime stdin")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return strings.TrimSuffix(c.writes[len(c.writes)-1], "\n")
}

func newTestProcess() (*process, *captureStdin) {
	c := newCaptureStdin()
	return &process{
		stdin:   c,
		events:  make(chan Event, 16),
		closed:  make(chan struct{}),
		pending: map[string]chan response{},
	}, c
}

// TestRequestCarriesItsOwnCorrelationID pins the Pi protocol rule that the
// correlation id lives inside the command object, because Pi echoes that value
// on the matching response.
func TestRequestCarriesItsOwnCorrelationID(t *testing.T) {
	p, stdin := newTestProcess()
	got := make(chan json.RawMessage, 1)
	go func() {
		data, e := p.request(context.Background(), "prompt-1", map[string]any{"type": "prompt", "message": "hello"})
		if e != nil {
			got <- nil
			return
		}
		got <- data
	}()
	line := stdin.last(t)
	var sent map[string]any
	if e := json.Unmarshal([]byte(line), &sent); e != nil {
		t.Fatalf("command is not one JSON record: %q", line)
	}
	if sent["id"] != "prompt-1" || sent["type"] != "prompt" || sent["message"] != "hello" {
		t.Fatalf("command payload: %v", sent)
	}
	p.mu.Lock()
	ch := p.pending["prompt-1"]
	p.mu.Unlock()
	if ch == nil {
		t.Fatal("request did not register its pending response")
	}
	ch <- response{ID: "prompt-1", Success: true, Data: json.RawMessage(`{"ok":true}`)}
	select {
	case data := <-got:
		if string(data) != `{"ok":true}` {
			t.Fatalf("response payload: %s", data)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("request never returned")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.pending) != 0 {
		t.Fatalf("pending map leaked: %v", p.pending)
	}
}

func TestRequestMapsProtocolFailureToRuntimeNeutralCode(t *testing.T) {
	p, stdin := newTestProcess()
	fail := make(chan error, 1)
	go func() {
		_, e := p.request(context.Background(), "state-1", map[string]any{"type": "get_state"})
		fail <- e
	}()
	stdin.last(t)
	p.mu.Lock()
	ch := p.pending["state-1"]
	p.mu.Unlock()
	ch <- response{ID: "state-1", Success: false, Error: "No assistant message is streaming"}
	select {
	case e := <-fail:
		var coded *agent.Error
		if !errors.As(e, &coded) || coded.Code != "RUNTIME_PROTOCOL_ERROR" {
			t.Fatalf("mapped error: %v", e)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("request never returned")
	}
}

func TestRequestRefusesIncompleteCommands(t *testing.T) {
	p, _ := newTestProcess()
	if _, e := p.request(context.Background(), "", map[string]any{"type": "prompt"}); e == nil {
		t.Fatal("an uncorrelated command was sent")
	}
	if _, e := p.request(context.Background(), "x", map[string]any{"message": "hi"}); e == nil {
		t.Fatal("a command without a type was sent")
	}
}

// TestReadStdoutFramesOnLFOnly proves the client is protocol-compliant where a
// generic line reader is not: U+2028 is legal inside a JSON string.
func TestReadStdoutFramesOnLFOnly(t *testing.T) {
	p, _ := newTestProcess()
	stream := `{"type":"response","id":"a","command":"prompt","success":true,"data":{"text":"linecontinues"}}` + "\n" +
		`{"type":"message_end","message":{"role":"assistant"}}` + "\n"
	resolved := make(chan response, 1)
	p.mu.Lock()
	p.pending["a"] = resolved
	p.mu.Unlock()
	done := make(chan struct{})
	go func() { p.readStdout(context.Background(), strings.NewReader(stream)); close(done) }()
	select {
	case res := <-resolved:
		if !res.Success || !strings.Contains(string(res.Data), "continues") {
			t.Fatalf("response routing: %+v", res)
		}
		var payload struct {
			Text string `json:"text"`
		}
		if e := json.Unmarshal(res.Data, &payload); e != nil {
			t.Fatalf("a string separator broke the record: %s (%v)", res.Data, e)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("response was not correlated to its id")
	}
	select {
	case ev := <-p.events:
		if ev.Type != "message_end" || !strings.Contains(string(ev.Raw), "assistant") {
			t.Fatalf("record after the separator was mangled: %s", ev.Raw)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stream event never reached the observer")
	}
	<-done
}

func TestReadStdoutIgnoresNonRecords(t *testing.T) {
	p, _ := newTestProcess()
	go p.readStdout(context.Background(), strings.NewReader("not json\n\n{\"type\":\"agent_settled\"}\n"))
	select {
	case ev := <-p.events:
		if ev.Type != "agent_settled" {
			t.Fatalf("decoded %q", ev.Type)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("valid record after noise was dropped")
	}
}

func TestStderrTailStaysBounded(t *testing.T) {
	p, _ := newTestProcess()
	go p.readStderr(strings.NewReader(strings.Repeat("x", stderrKeep*3)))
	deadline := time.Now().Add(3 * time.Second)
	for {
		p.mu.Lock()
		n := len(p.stderr)
		p.mu.Unlock()
		if n == stderrKeep {
			break
		}
		if n > stderrKeep {
			t.Fatalf("stderr buffer grew past the bound: %d", n)
		}
		if time.Now().After(deadline) {
			t.Fatalf("stderr reader stopped at %d bytes", n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestMapProtocolErrorCoversThePiVocabulary(t *testing.T) {
	cases := map[string]string{
		"No assistant message is streaming":      "RUNTIME_PROTOCOL_ERROR",
		"model not found: deepseek-ai/X":         "MODEL_NOT_AVAILABLE",
		"provider authentication failed":         "UPSTREAM_UNAVAILABLE",
		"402 for model deepseek-ai/DeepSeek-V4":  "UPSTREAM_UNAVAILABLE",
		"session file is missing":                "SESSION_LOST",
		"something unexpected in the transcript": "RUNTIME_PROTOCOL_ERROR",
	}
	for in, want := range cases {
		var coded *agent.Error
		if !errors.As(mapProtocolError(in), &coded) || coded.Code != want {
			t.Fatalf("mapProtocolError(%q) = %v, want %s", in, coded, want)
		}
	}
}
