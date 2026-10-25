package opencode

import (
	"ai-worker-mcp/internal/agent"
	"ai-worker-mcp/internal/localfs"
	"ai-worker-mcp/internal/store"
	"ai-worker-mcp/internal/testutil"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "--discovery-client" {
		c := legacyFixtureConfig()
		c.Directory = os.Args[2]
		c.Runtime.Binary = os.Args[0]
		manager := NewManager(c)
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if _, e := manager.Connect(ctx, true); e != nil {
			fmt.Fprintln(os.Stderr, e)
			os.Exit(2)
		}
		_ = json.NewEncoder(os.Stdout).Encode(manager.Snapshot(ctx))
		_, _ = io.Copy(io.Discard, os.Stdin)
		_ = manager.Close()
		os.Exit(0)
	}

	if handled, code := HostMain(os.Args); handled {
		os.Exit(code)
	}
	if len(os.Args) > 1 && os.Args[1] == "serve" {
		port := ""
		for i, a := range os.Args {
			if a == "--port" && i+1 < len(os.Args) {
				port = os.Args[i+1]
			}
		}
		mux := http.NewServeMux()
		mux.HandleFunc("/exit", func(http.ResponseWriter, *http.Request) { os.Exit(1) })
		mux.HandleFunc("/global/health", func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{"healthy": true, "version": "managed-test"})
		})
		_ = http.ListenAndServe("127.0.0.1:"+port, mux)
		os.Exit(1)
	}
	os.Exit(m.Run())
}
func TestManagedOwnerAttachShutdownCrash(t *testing.T) {
	binary, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	c := legacyFixtureConfig()
	c.Directory = t.TempDir()
	c.Runtime.Binary = binary
	first := NewManager(c)
	defer first.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	client, e := first.Connect(ctx, true)
	if e != nil || !first.Owned() {
		t.Fatal(e)
	}
	second := NewManager(c)
	attached, e := second.Connect(ctx, true)
	if e != nil || second.Owned() || attached.BaseURL != client.BaseURL {
		t.Fatal(e)
	}
	second.Close()
	if _, e = client.Health(ctx); e != nil {
		t.Fatal("borrower killed owner")
	}
	_, _ = http.Get(client.BaseURL + "/exit")
	<-first.done
	if _, e = first.Connect(ctx, true); e == nil {
		t.Fatal("crashed runtime silently restarted")
	}
	first.Close()
	third := NewManager(c)
	defer third.Close()
	if _, e = third.Connect(ctx, true); e != nil {
		t.Fatal("fresh explicit start failed", e)
	}
}
func TestExternalNeverOwnedAndMissingBinary(t *testing.T) {
	m := testutil.New("")
	defer m.Close()
	c := legacyFixtureConfig()
	c.Directory = t.TempDir()
	c.Runtime.Mode = "external"
	c.Runtime.BaseURL = m.Server.URL
	manager := NewManager(c)
	client, e := manager.Connect(context.Background(), true)
	if e != nil || manager.Owned() {
		t.Fatal(e)
	}
	manager.Close()
	if _, e = client.Health(context.Background()); e != nil {
		t.Fatal("external stopped")
	}
	c.Runtime.Mode = "managed"
	c.Runtime.Binary = "/does/not/exist/opencode"
	manager = NewManager(c)
	if _, e = manager.Connect(context.Background(), true); e == nil || !strings.Contains(e.Error(), "RUNTIME_NOT_FOUND") {
		t.Fatal(e)
	}
	c.Runtime.Binary = "/usr/bin/false"
	manager = NewManager(c)
	if _, e = manager.Connect(context.Background(), true); e == nil || !strings.Contains(e.Error(), "RUNTIME_START_FAILED") {
		t.Fatal(e)
	}
}

// Optional real binary protocol smoke: no model or provider configuration changes.
func TestRealManagedProtocol(t *testing.T) {
	binary := os.Getenv("LLMTOOLS_TEST_OPENCODE")
	if binary == "" {
		t.Skip("set LLMTOOLS_TEST_OPENCODE for real managed runtime protocol smoke")
	}
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	c := legacyFixtureConfig()
	c.Directory = t.TempDir()
	c.Runtime.Binary = binary
	m := NewManager(c)
	defer m.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	client, e := m.Connect(ctx, true)
	if e != nil {
		t.Fatal(e)
	}
	h, e := client.Health(ctx)
	if e != nil {
		t.Fatal(e)
	}
	t.Log("real OpenCode version", h.Version)
	list, e := client.ListAgents(ctx)
	if e != nil || len(list) == 0 {
		t.Fatal(e)
	}
	session, e := client.StartTask(ctx, agent.AgentTask{}, list[0].Name, "")
	if e != nil {
		t.Fatal(e)
	}
	streamCtx, stop := context.WithCancel(ctx)
	if _, e = client.SubscribeProgress(streamCtx, session, nil); e != nil {
		stop()
		t.Fatal(e)
	}
	stop()
	if e = client.CancelTask(ctx, session); e != nil {
		t.Fatal(e)
	}
	m.Close()
	if _, e = client.Health(ctx); e == nil {
		t.Fatal("managed server survived owner shutdown")
	}
}

func TestIndependentProcessesConcurrentDiscovery(t *testing.T) {
	dir := t.TempDir()
	binary, _ := os.Executable()
	type child struct {
		cmd      *exec.Cmd
		input    io.WriteCloser
		output   io.ReadCloser
		snapshot RuntimeSnapshot
	}
	children := make([]child, 2)
	for i := range children {
		c := &children[i]
		c.cmd = exec.Command(binary, "--discovery-client", dir)
		c.cmd.Stderr = os.Stderr
		c.input, _ = c.cmd.StdinPipe()
		c.output, _ = c.cmd.StdoutPipe()
		if e := c.cmd.Start(); e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { c.input.Close(); _ = c.cmd.Wait() })
	}
	for i := range children {
		if e := json.NewDecoder(children[i].output).Decode(&children[i].snapshot); e != nil {
			t.Fatal(e)
		}
	}
	a, b := children[0].snapshot, children[1].snapshot
	if a.Instance.ID != b.Instance.ID || a.Instance.PID != b.Instance.PID || a.Instance.BaseURL != b.Instance.BaseURL || a.Instance.Port <= 0 || a.Instance.Generation != b.Instance.Generation {
		t.Fatal(a, b)
	}
	owner := 0
	if a.Ownership != "OWNER" {
		owner = 1
	}
	attached := 1 - owner
	if children[attached].snapshot.Ownership != "ATTACHED_NON_OWNER" || children[owner].snapshot.Ownership != "OWNER" {
		t.Fatal(a, b)
	}
	children[attached].input.Close()
	if e := children[attached].cmd.Wait(); e != nil {
		t.Fatal(e)
	}
	client := New(a.Instance.BaseURL, dir)
	if _, e := client.Health(context.Background()); e != nil {
		t.Fatal("attached process shutdown killed server", e)
	}
	children[owner].input.Close()
	if e := children[owner].cmd.Wait(); e != nil {
		t.Fatal(e)
	}
	st, e := store.Open(filepath.Join(dir, "llmtools.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer st.Close()
	got, e := st.RuntimeInstance()
	if e != nil || got.Status != "STOPPED" {
		t.Fatal(got, e)
	}
	if _, e = client.Health(context.Background()); e == nil {
		t.Fatal("owned child survived")
	}
}

func TestStaleRecoveryDoesNotTrustPIDOrPort(t *testing.T) {
	c := legacyFixtureConfig()
	c.Directory = t.TempDir()
	c.Runtime.Binary, _ = os.Executable()
	st, e := store.Open(filepath.Join(c.Directory, c.Storage.Database))
	if e != nil {
		t.Fatal(e)
	}
	defer st.Close()
	// A live unrelated endpoint and the current live PID are not ownership proof.
	unrelated := testutil.New("")
	defer unrelated.Close()
	lock, e := localfs.Lock(filepath.Join(c.Directory, "runtime.lock"))
	if e != nil {
		t.Fatal(e)
	}
	old, e := st.ClaimRuntime(store.RuntimeInstance{ID: "rt_stale", OwnerID: "owner_stale", OwnerPID: os.Getpid(), BaseURL: unrelated.Server.URL, Type: "opencode"})
	if e != nil {
		t.Fatal(e)
	}
	old.Status = "READY"
	if e = st.UpdateRuntime(old, "STARTING"); e != nil {
		t.Fatal(e)
	}
	localfs.Unlock(lock)
	m := NewManager(c)
	defer m.Close()
	client, e := m.Connect(context.Background(), true)
	if e != nil {
		t.Fatal(e)
	}
	got, _ := st.RuntimeInstance()
	if got.Generation <= old.Generation || got.ID == old.ID || client.BaseURL == unrelated.Server.URL {
		t.Fatal(got)
	}
	old.Status = "STOPPED"
	if st.UpdateRuntime(old, "READY") == nil {
		t.Fatal("old callback not fenced")
	}
	if _, e = client.Health(context.Background()); e != nil {
		t.Fatal(e)
	}
}

func TestOwnerCrashReleasesSupervisedChildBeforeRecovery(t *testing.T) {
	dir := t.TempDir()
	binary, _ := os.Executable()
	cmd := exec.Command(binary, "--discovery-client", dir)
	input, _ := cmd.StdinPipe()
	defer input.Close()
	output, _ := cmd.StdoutPipe()
	if e := cmd.Start(); e != nil {
		t.Fatal(e)
	}
	var before RuntimeSnapshot
	if e := json.NewDecoder(output).Decode(&before); e != nil {
		t.Fatal(e)
	}
	// Kill precisely the test helper we created, never a PID obtained from SQLite.
	if e := cmd.Process.Kill(); e != nil {
		t.Fatal(e)
	}
	_ = cmd.Wait()
	c := legacyFixtureConfig()
	c.Directory = dir
	c.Runtime.Binary = binary
	m := NewManager(c)
	defer m.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, e := m.Connect(ctx, true); e != nil {
		t.Fatal(e)
	}
	after := m.Snapshot(ctx)
	if !m.Owned() || after.Instance.Generation <= before.Instance.Generation || after.Instance.PID == before.Instance.PID {
		t.Fatal(before, after)
	}
	if _, e := New(before.Instance.BaseURL, dir).Health(ctx); e == nil {
		t.Fatal("old child survived recovery")
	}
}

func TestSupervisorFailureDoesNotReleaseLiveChildLease(t *testing.T) {
	c := legacyFixtureConfig()
	c.Directory = t.TempDir()
	c.Runtime.Binary, _ = os.Executable()
	m := NewManager(c)
	client, e := m.Connect(context.Background(), true)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _, _ = http.Get(client.BaseURL + "/exit"); _ = m.Close() }()
	// Precisely the supervisor exec.Cmd owned by this test; never record PID kill.
	if e = m.cmd.Process.Kill(); e != nil {
		t.Fatal(e)
	}
	<-m.done
	if e = m.Close(); e == nil {
		t.Fatal("unconfirmed child exit was reported as stopped")
	}
	if _, e = client.Health(context.Background()); e != nil {
		t.Fatal("fixture child should still own inherited lease", e)
	}
	another := NewManager(c)
	defer another.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if _, e = another.Connect(ctx, true); e == nil {
		t.Fatal("started second server while old child is still alive")
	}
}

func TestPausedOwnerStopRetainsLeaseAndCanFinish(t *testing.T) {
	c := legacyFixtureConfig()
	c.Directory = t.TempDir()
	c.Runtime.Binary, _ = os.Executable()
	m := NewManager(c)
	_, e := m.Connect(context.Background(), true)
	if e != nil {
		t.Fatal(e)
	}
	supervisor := m.cmd.Process
	defer func() { _ = supervisor.Signal(syscall.SIGCONT); _ = m.Close() }()
	if e = supervisor.Signal(syscall.SIGSTOP); e != nil {
		t.Fatal(e)
	}
	if e = m.Close(); e == nil || !m.Owned() {
		t.Fatal("unconfirmed stop lost ownership", e)
	}
	other := NewManager(c)
	defer other.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if _, e = other.Connect(ctx, true); e == nil {
		t.Fatal("paused owner was preempted")
	}
	_ = supervisor.Signal(syscall.SIGCONT)
	if e = m.Close(); e != nil {
		t.Fatal("stop retry failed", e)
	}
	st, e := store.Open(filepath.Join(c.Directory, c.Storage.Database))
	if e != nil {
		t.Fatal(e)
	}
	defer st.Close()
	got, e := st.RuntimeInstance()
	if e != nil || got.Status != "STOPPED" {
		t.Fatal(got, e)
	}
}
