//go:build !windows

package pi

import (
	"ai-worker-mcp/internal/procsup"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// TestKillTerminatesTheWholeProcessGroup pins the Unix supervision contract:
// cancellation must reach the whole process group, not just the leader.
func TestKillTerminatesTheWholeProcessGroup(t *testing.T) {
	cmd := exec.Command("sleep", "30")
	procsup.Configure(cmd)
	if e := cmd.Start(); e != nil {
		t.Fatal(e)
	}
	if e := procsup.Started(cmd); e != nil {
		procsup.Terminate(cmd, 0)
		t.Fatalf("supervision setup failed: %v", e)
	}
	p := &process{cmd: cmd, stdin: newCaptureStdin(), events: make(chan Event, 1), closed: make(chan struct{}), pending: map[string]chan response{}}
	pid := cmd.Process.Pid
	if e := syscall.Kill(pid, 0); e != nil {
		t.Fatalf("child never lived: %v", e)
	}
	p.kill()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if e := syscall.Kill(pid, 0); e != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("killed process is still alive: an orphaned runtime would outlive the run")
		}
		time.Sleep(20 * time.Millisecond)
	}
	// A second kill is the shutdown path for a run that already ended.
	p.kill()
	select {
	case <-p.closed:
	default:
		t.Fatal("process was not marked closed")
	}
}
