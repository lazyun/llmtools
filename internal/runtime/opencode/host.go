package opencode

import (
	"ai-worker-mcp/internal/store"
	"encoding/json"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

// HostMain is an internal subprocess entry, before CLI/Wails initialization.
// The inherited lock FD keeps ownership until the owned OpenCode child exits.
// Closing the parent pipe (including parent SIGKILL) triggers child shutdown.
// No PID from a descriptor is ever used to send a signal.
func HostMain(args []string) (bool, int) {
	if len(args) == 3 && args[1] == "--llmtools-context-control" {
		return true, runContextControlMCP(args[2])
	}
	if len(args) < 2 || args[1] != "--llmtools-runtime-host" {
		return false, 0
	}
	if len(args) != 6 {
		return true, 2
	}
	lock := os.NewFile(3, "runtime-lock")
	if lock == nil {
		return true, 2
	}
	defer lock.Close()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	cmd := exec.Command(args[2], "serve", "--hostname", "127.0.0.1", "--port", args[3])
	// Keep the kernel lease in the actual server too: an abruptly killed
	// supervisor must not make a still-live child eligible for replacement.
	cmd.ExtraFiles = []*os.File{lock}
	// Native parent interaction is a control-plane tool, not an execution grant.
	for _, v := range os.Environ() {
		if !strings.HasPrefix(v, "OPENCODE_ENABLE_QUESTION_TOOL=") {
			cmd.Env = append(cmd.Env, v)
		}
	}
	cmd.Env = append(cmd.Env, "OPENCODE_ENABLE_QUESTION_TOOL=true")
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if cmd.Start() != nil {
		return true, 1
	}
	var identity store.RuntimeInstance
	if json.Unmarshal([]byte(args[5]), &identity) != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return true, 2
	}
	identity.PID = cmd.Process.Pid
	listener, e := net.Listen("unix", args[4])
	if e != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return true, 1
	}
	_ = os.Chmod(args[4], 0600)
	defer listener.Close()
	go func() {
		for {
			c, e := listener.Accept()
			if e != nil {
				return
			}
			_ = c.SetWriteDeadline(time.Now().Add(time.Second))
			_ = json.NewEncoder(c).Encode(identity)
			_ = c.Close()
		}
	}()
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	disconnected := make(chan struct{})
	go func() { _, _ = io.Copy(io.Discard, os.Stdin); close(disconnected) }()
	select {
	case <-done:
		return true, 1
	case <-disconnected:
	case <-signals:
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		_ = cmd.Process.Kill()
		<-done
	}
	return true, 0
}
