// Package pi implements the experimental Pi runtime adapter.
//
// Pi is a per-turn managed process speaking Pi's own JSONL RPC protocol; it is
// deliberately not modelled as an HTTP server. Everything Pi-specific — the
// protocol, the session file layout, the extension bridge and the control tool
// payloads — stays inside this package. Core only ever sees agent.RuntimeAdapter
// values, and the OpenCode path is untouched.
package pi

import (
	"ai-worker-mcp/internal/agent"
	"ai-worker-mcp/internal/procsup"
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// maxRPCLine bounds one protocol record. Pi itself caps tool output well below
// this, so exceeding it is a protocol violation rather than a large result.
const maxRPCLine = 8 * 1024 * 1024

// stderrKeep is how much Pi stderr is retained for error classification. The
// audit lines llmTools writes are never secret, but the buffer is bounded so a
// chatty runtime cannot grow with the run.
const stderrKeep = 16 * 1024

// Event is one Pi stream event record, kept raw so only this package parses it.
type Event struct {
	Type string
	Raw  json.RawMessage
}

type response struct {
	ID      string          `json:"id"`
	Command string          `json:"command"`
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
	Error   string          `json:"error"`
}

// process owns one `pi --mode rpc` child for exactly one observation window.
// Pi persists its session tree on disk, so a turn may outlive the process; this
// type never pretends to be a long-lived server.
type process struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	events chan Event
	closed chan struct{}

	mu      sync.Mutex
	pending map[string]chan response
	stderr  []byte
	closed_ bool
}

// startProcess launches Pi and fails closed if the binary or the bridge is
// missing, so a misconfigured spike can never silently fall back to a tool
// surface llmTools does not control.
func startProcess(ctx context.Context, l launch, args []string) (*process, error) {
	if l.binary == "" {
		return nil, agent.E("RUNTIME_NOT_FOUND")
	}
	if l.extension == "" {
		return nil, agent.E("RUNTIME_UNAVAILABLE")
	}
	if _, e := os.Stat(l.extension); e != nil {
		return nil, agent.E("RUNTIME_UNAVAILABLE")
	}
	full := append([]string{"--mode", "rpc"}, args...)
	// Pi is started without built-in tools and with an exact allowlist, so the
	// only execution tools the model can see are the llmTools bridge tools.
	cmd, commandErr := piCommand(context.Background(), l.binary, full...)
	if commandErr != nil {
		return nil, agent.E("RUNTIME_NOT_FOUND")
	}
	cmd.Dir = l.workdir
	cmd.Env = commandEnvironment(l.binary, l.env)
	// The child runs in its own supervised tree (process group on Unix, Job
	// Object on Windows) so cancellation reaches Pi's own children too.
	procsup.Configure(cmd)
	stdin, e := cmd.StdinPipe()
	if e != nil {
		return nil, agent.E("RUNTIME_UNAVAILABLE")
	}
	stdout, e := cmd.StdoutPipe()
	if e != nil {
		return nil, agent.E("RUNTIME_UNAVAILABLE")
	}
	p := &process{cmd: cmd, stdin: stdin, events: make(chan Event, 256), closed: make(chan struct{}), pending: map[string]chan response{}}
	stderr, e := cmd.StderrPipe()
	if e != nil {
		return nil, agent.E("RUNTIME_UNAVAILABLE")
	}
	if e = cmd.Start(); e != nil {
		return nil, agent.E("RUNTIME_UNAVAILABLE")
	}
	if e = procsup.Started(cmd); e != nil {
		procsup.Terminate(cmd, 0)
		return nil, agent.E("RUNTIME_UNAVAILABLE")
	}
	go p.readStderr(stderr)
	go p.readStdout(ctx, stdout)
	return p, nil
}

func (p *process) readStderr(r io.Reader) {
	buf := make([]byte, 4096)
	for {
		n, e := r.Read(buf)
		if n > 0 {
			p.mu.Lock()
			p.stderr = append(p.stderr, buf[:n]...)
			if over := len(p.stderr) - stderrKeep; over > 0 {
				p.stderr = append(p.stderr[:0], p.stderr[over:]...)
			}
			p.mu.Unlock()
			// Pi writes its own warnings here; llmTools' bridge writes LLMTOOLS_PI
			// audit lines. Forwarding keeps operator evidence visible.
			for _, line := range strings.Split(strings.TrimRight(string(buf[:n]), "\n"), "\n") {
				if strings.Contains(line, "LLMTOOLS_PI") || strings.Contains(line, "repo_access_") {
					fmt.Fprintln(os.Stderr, line)
				}
			}
		}
		if e != nil {
			return
		}
	}
}

// readStdout splits records on LF only. Pi's RPC framing allows U+2028/U+2029
// inside JSON strings, so a generic line reader would corrupt records.
func (p *process) readStdout(ctx context.Context, r io.Reader) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), maxRPCLine)
	scanner.Split(splitLF)
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if line == "" {
			continue
		}
		var probe struct {
			Type string `json:"type"`
			ID   string `json:"id"`
		}
		if json.Unmarshal([]byte(line), &probe) != nil {
			continue
		}
		if probe.Type == "response" {
			var res response
			if json.Unmarshal([]byte(line), &res) == nil && res.ID != "" {
				p.mu.Lock()
				ch := p.pending[res.ID]
				delete(p.pending, res.ID)
				p.mu.Unlock()
				if ch != nil {
					select {
					case ch <- res:
					default:
					}
				}
			}
			continue
		}
		select {
		case p.events <- Event{Type: probe.Type, Raw: json.RawMessage(line)}:
		case <-ctx.Done():
			return
		case <-p.closed:
			return
		}
	}
	close(p.events)
}

func splitLF(data []byte, atEOF bool) (int, []byte, error) {
	if i := indexOf(data, '\n'); i >= 0 {
		return i + 1, data[:i], nil
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}

func indexOf(b []byte, c byte) int {
	for i := range b {
		if b[i] == c {
			return i
		}
	}
	return -1
}

var errRPCStopped = errors.New("runtime protocol stream stopped")

// request writes one command and waits for the matching response id. Pi's
// protocol carries the correlation id inside the command object, so it is added
// here rather than at every call site.
func (p *process) request(ctx context.Context, id string, command map[string]any) (json.RawMessage, error) {
	if id == "" || command == nil || command["type"] == nil {
		return nil, agent.E("RUNTIME_PROTOCOL_ERROR")
	}
	payload := maps.Clone(command)
	// Pi echoes the correlation id it read from the command object.
	payload["id"] = id
	body, e := json.Marshal(payload)
	if e != nil {
		return nil, agent.E("RUNTIME_PROTOCOL_ERROR")
	}
	ch := make(chan response, 1)
	p.mu.Lock()
	if p.closed_ {
		p.mu.Unlock()
		return nil, errRPCStopped
	}
	p.pending[id] = ch
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		delete(p.pending, id)
		p.mu.Unlock()
	}()
	if _, e = p.stdin.Write(append(body, '\n')); e != nil {
		return nil, errRPCStopped
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case res := <-ch:
		if !res.Success {
			return nil, mapProtocolError(res.Error)
		}
		return res.Data, nil
	case <-p.closed:
		return nil, errRPCStopped
	}
}

func (p *process) waitEvent(ctx context.Context) (Event, bool) {
	select {
	case <-ctx.Done():
		return Event{}, false
	case ev, ok := <-p.events:
		return ev, ok
	}
}

// kill terminates the whole process tree. Pi may itself spawn the control
// gateway child; an orphaned tree would keep a run's tools alive.
func (p *process) kill() {
	p.mu.Lock()
	if p.closed_ {
		p.mu.Unlock()
		return
	}
	p.closed_ = true
	close(p.closed)
	p.mu.Unlock()
	_ = p.stdin.Close()
	procsup.Terminate(p.cmd, 3*time.Second)
}

func (p *process) stderrTail() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return string(p.stderr)
}

// mapProtocolError turns a Pi RPC error string into the runtime-neutral error
// vocabulary; Pi's raw exception text never becomes Core state.
//
// A provider refusal carries the same markers here as in an assistant turn, so
// both paths report UPSTREAM_UNAVAILABLE for the same boundary. Ordering matters:
// a provider message usually names the model too, and reporting catalogue
// absence for it would point the operator at the wrong layer.
func mapProtocolError(raw string) error {
	lower := strings.ToLower(raw)
	switch {
	case strings.Contains(lower, "no assistant"), strings.Contains(lower, "not streaming"):
		return agent.E("RUNTIME_PROTOCOL_ERROR")
	case strings.Contains(lower, "session"):
		return agent.E("SESSION_LOST")
	case agent.MentionsUpstreamBlock(lower):
		return agent.E("UPSTREAM_UNAVAILABLE")
	case strings.Contains(lower, "model"):
		return agent.E("MODEL_NOT_AVAILABLE")
	default:
		return agent.E("RUNTIME_PROTOCOL_ERROR")
	}
}
