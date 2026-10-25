package opencode

import (
	"ai-worker-mcp/internal/agent"
	"ai-worker-mcp/internal/config"
	"ai-worker-mcp/internal/localfs"
	"ai-worker-mcp/internal/store"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"
)

type RuntimeSnapshot struct {
	Instance  store.RuntimeInstance `json:"instance"`
	Ownership string                `json:"ownership"`
	Health    string                `json:"health"`
}

// SQLite is the durable authority. The inherited flock is the liveness lease:
// it cannot expire while a paused owner or its child could still be alive.
// A per-instance supervisor socket proves identity without trusting a PID or a
// reused HTTP port. All process signals target our own exec.Cmd only.
type Manager struct {
	mu       sync.Mutex
	Config   config.Config
	client   *Client
	cmd      *exec.Cmd
	lock     *os.File
	done     chan struct{}
	pipe     *os.File
	instance store.RuntimeInstance
}

func NewManager(c config.Config) *Manager { return &Manager{Config: c} }
func (m *Manager) newClient(base, workspace string) *Client {
	c := New(base, workspace)
	c.ControlDB = filepath.Join(m.Config.Directory, m.Config.Storage.Database)
	return c
}

// Scoped derives a per-run client from an established connection. It changes
// only the request scope (OpenCode's `directory`), never which instance is
// owned, which lease is held, or which control database is used.
func (m *Manager) Scoped(c *Client, root string) *Client {
	if c == nil || root == "" || c.Directory == root {
		return c
	}
	scoped := *c
	scoped.Directory = root
	return &scoped
}
func randomID(prefix string) string {
	b := make([]byte, 16)
	if _, e := rand.Read(b); e != nil {
		panic("secure randomness unavailable")
	}
	return prefix + hex.EncodeToString(b)
}
func identitySocket(dir, id string) string {
	sum := sha256.Sum256([]byte(dir + "\x00" + id))
	return filepath.Join("/tmp", "llmtools-"+hex.EncodeToString(sum[:20])+".sock")
}
func (m *Manager) database() (*store.Store, error) {
	return store.Open(filepath.Join(m.Config.Directory, m.Config.Storage.Database))
}
func (m *Manager) Owned() bool { m.mu.Lock(); defer m.mu.Unlock(); return m.cmd != nil }
func (m *Manager) Snapshot(ctx context.Context) RuntimeSnapshot {
	client, connectionErr := m.Connect(ctx, false)
	m.mu.Lock()
	defer m.mu.Unlock()
	out := RuntimeSnapshot{Ownership: "UNAVAILABLE", Health: "FAIL"}
	if connectionErr == nil {
		out.Health = "PASS"
	}
	if m.Config.Runtime.Mode == "external" {
		out.Ownership = "EXTERNAL"
		out.Instance.BaseURL = m.Config.Runtime.BaseURL
		out.Instance.Type = "opencode"
		out.Instance.Mode = "external"
		out.Instance.Status = "UNAVAILABLE"
		out.Health = "FAIL"
		if connectionErr == nil {
			if health, e := client.Health(ctx); e == nil {
				out.Health = "PASS"
				out.Instance.Status = "READY"
				out.Instance.Version = health.Version
			}
		}
		return out
	}
	st, e := m.database()
	if e != nil {
		return out
	}
	defer st.Close()
	out.Instance, _ = st.RuntimeInstance()
	if m.instance.ID == out.Instance.ID && m.instance.Generation == out.Instance.Generation {
		if m.cmd != nil {
			out.Ownership = "OWNER"
		} else if connectionErr == nil && m.client != nil && out.Instance.Status == "READY" {
			out.Ownership = "ATTACHED_NON_OWNER"
		}
	}
	return out
}
func supervisorIdentity(ctx context.Context, dir string, r store.RuntimeInstance) (store.RuntimeInstance, error) {
	var actual store.RuntimeInstance
	d := net.Dialer{Timeout: time.Second}
	c, e := d.DialContext(ctx, "unix", identitySocket(dir, r.ID))
	if e != nil {
		return actual, e
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(time.Second))
	e = json.NewDecoder(io.LimitReader(c, 8192)).Decode(&actual)
	if e != nil || actual.ID != r.ID || actual.Generation != r.Generation || actual.OwnerID != r.OwnerID || actual.OwnerPID != r.OwnerPID || actual.BaseURL != r.BaseURL || actual.Type != "opencode" || actual.PID <= 0 {
		return actual, agent.E("RUNTIME_UNAVAILABLE")
	}
	return actual, nil
}
func (m *Manager) validate(ctx context.Context, r store.RuntimeInstance, workspace string) (*Client, error) {
	if r.Status != "READY" || r.Host != "127.0.0.1" || r.Port <= 0 || r.BaseURL != "http://127.0.0.1:"+strconv.Itoa(r.Port) {
		return nil, agent.E("RUNTIME_UNAVAILABLE")
	}
	actual, e := supervisorIdentity(ctx, m.Config.Directory, r)
	if e != nil || actual.PID != r.PID {
		return nil, agent.E("RUNTIME_UNAVAILABLE")
	}
	client := m.newClient(r.BaseURL, workspace)
	h, e := client.Health(ctx)
	if e != nil || h.Version != r.Version {
		return nil, agent.E("RUNTIME_UNAVAILABLE")
	}
	return client, nil
}
func (m *Manager) Connect(ctx context.Context, start bool) (*Client, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := m.Config
	workspace := filepath.Join(c.Directory, "runtime-workspace")
	if e := localfs.EnsureDirectory(workspace); e != nil {
		return nil, agent.E("CONFIG_ERROR")
	}
	if c.Runtime.Mode == "external" {
		return m.newClient(c.Runtime.BaseURL, workspace), nil
	}
	st, e := m.database()
	if e != nil {
		return nil, e
	}
	defer st.Close()
	if m.cmd != nil {
		r, e := st.RuntimeInstance()
		if e != nil || r.ID != m.instance.ID || r.Generation != m.instance.Generation {
			return nil, agent.E("RUNTIME_UNAVAILABLE")
		}
		return m.validate(ctx, r, workspace) // never restart during an executing task
	}
	m.client = nil
	waitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	for {
		if waitCtx.Err() != nil {
			return nil, agent.E("RUNTIME_UNAVAILABLE")
		}
		lock, lockErr := localfs.Lock(filepath.Join(c.Directory, "runtime.lock"))
		if lockErr == nil {
			if !start {
				r, readErr := st.RuntimeInstance()
				if readErr == nil && (r.Status == "READY" || r.Status == "STARTING" || r.Status == "STOPPING") {
					expected := r.Status
					r.Status = "STALE"
					_ = st.UpdateRuntime(r, expected)
				}
				localfs.Unlock(lock)
				return nil, agent.E("RUNTIME_UNAVAILABLE")
			}
			r, e := st.ClaimRuntime(store.RuntimeInstance{ID: randomID("rt_"), Type: "opencode", Mode: "managed", Host: "127.0.0.1", OwnerID: randomID("owner_"), OwnerPID: os.Getpid()})
			if e != nil {
				localfs.Unlock(lock)
				return nil, agent.E("RUNTIME_START_FAILED")
			}
			client, e := m.start(waitCtx, st, lock, r, workspace)
			if e != nil {
				r.Status = "FAILED"
				_ = st.UpdateRuntime(r, "STARTING")
				lock.Close() // inherited lease survives if supervisor shutdown is delayed
			}
			return client, e
		}
		r, e := st.RuntimeInstance()
		if e != nil {
			return nil, e
		}
		if r.Status == "READY" {
			client, e := m.validate(waitCtx, r, workspace)
			if e == nil {
				latest, e := st.RuntimeInstance()
				if e == nil && latest.ID == r.ID && latest.Generation == r.Generation && latest.Status == "READY" {
					m.client = client
					m.instance = r
					return client, nil
				}
			}
			// Live but unhealthy owners are never preempted. Their kernel lease must
			// first be released, otherwise a second server would violate single-owner.
			if !start {
				return nil, agent.E("RUNTIME_UNAVAILABLE")
			}
		} else if !start && r.Status != "STARTING" {
			return nil, agent.E("RUNTIME_UNAVAILABLE")
		}
		select {
		case <-waitCtx.Done():
			return nil, agent.E("RUNTIME_UNAVAILABLE")
		case <-time.After(50 * time.Millisecond):
		}
	}
}
func (m *Manager) start(ctx context.Context, st *store.Store, lock *os.File, r store.RuntimeInstance, workspace string) (*Client, error) {
	binary, e := exec.LookPath(m.Config.Runtime.Binary)
	if e != nil {
		return nil, agent.E("RUNTIME_NOT_FOUND")
	}
	l, e := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(m.Config.Runtime.Port))
	if e != nil {
		return nil, agent.E("RUNTIME_START_FAILED")
	}
	r.Port = l.Addr().(*net.TCPAddr).Port
	l.Close()
	r.BaseURL = "http://127.0.0.1:" + strconv.Itoa(r.Port)
	host, e := os.Executable()
	if e != nil {
		return nil, agent.E("RUNTIME_START_FAILED")
	}
	readPipe, writePipe, e := os.Pipe()
	if e != nil {
		return nil, agent.E("RUNTIME_START_FAILED")
	}
	metadata, _ := json.Marshal(r)
	cmd := exec.Command(host, "--llmtools-runtime-host", binary, strconv.Itoa(r.Port), identitySocket(m.Config.Directory, r.ID), string(metadata))
	cmd.Dir = workspace
	cmd.Stdin = readPipe
	cmd.ExtraFiles = []*os.File{lock}
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if cmd.Start() != nil {
		readPipe.Close()
		writePipe.Close()
		return nil, agent.E("RUNTIME_START_FAILED")
	}
	readPipe.Close()
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	started := false
	defer func() {
		if !started {
			stopChild(cmd, writePipe, done)
		}
	}()
	client := m.newClient(r.BaseURL, workspace)
	readyCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	for {
		select {
		case <-done:
			return nil, agent.E("RUNTIME_START_FAILED")
		case <-readyCtx.Done():
			return nil, agent.E("RUNTIME_START_FAILED")
		default:
		}
		actual, e := supervisorIdentity(readyCtx, m.Config.Directory, r)
		if e == nil {
			if h, e := client.Health(readyCtx); e == nil {
				r.PID = actual.PID
				r.Version = h.Version
				break
			}
		}
		select {
		case <-readyCtx.Done():
		case <-time.After(100 * time.Millisecond):
		}
	}
	r.Status = "READY"
	if st.UpdateRuntime(r, "STARTING") != nil {
		return nil, agent.E("RUNTIME_START_FAILED")
	}
	m.cmd = cmd
	m.pipe = writePipe
	m.done = done
	m.lock = lock
	m.client = client
	m.instance = r
	started = true
	return client, nil
}
func stopChild(cmd *exec.Cmd, pipe *os.File, done chan struct{}) bool {
	_ = pipe.Close()
	_ = cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-done:
		return true
	case <-time.After(4 * time.Second):
		return false // do not orphan a child by killing its supervisor
	}
}
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cmd == nil {
		m.client = nil
		return nil
	}
	st, e := m.database()
	r := m.instance
	if e == nil {
		defer st.Close()
		current, readErr := st.RuntimeInstance()
		if readErr != nil || current.ID != r.ID || current.Generation != r.Generation || current.OwnerID != r.OwnerID {
			e = agent.E("RUNTIME_UNAVAILABLE")
		} else {
			r = current
			switch current.Status {
			case "READY":
				r.Status = "STOPPING"
				e = st.UpdateRuntime(r, "READY")
			case "STOPPING": // A previous stop timed out; retain its fenced claim.
			default:
				e = agent.E("RUNTIME_UNAVAILABLE")
			}
		}
	}
	// Even when fenced, only this manager's own supervisor can be signalled.
	if !stopChild(m.cmd, m.pipe, m.done) {
		return agent.E("ABORT_UNCONFIRMED")
	}
	if e == nil {
		r.Status = "STOPPED"
		if !m.cmd.ProcessState.Success() {
			r.Status = "FAILED"
		}
		e = st.UpdateRuntime(r, "STOPPING")
		if e == nil && r.Status == "FAILED" {
			e = agent.E("ABORT_UNCONFIRMED")
		}
	}
	m.cmd = nil
	m.client = nil
	// Close our copy; never unlock a lease still inherited by a live child.
	_ = m.lock.Close()
	m.lock = nil
	return e
}
