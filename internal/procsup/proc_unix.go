//go:build !windows

package procsup

import (
	"os/exec"
	"syscall"
	"time"
)

// Configure puts the child into its own process group so the whole tree can
// be signalled together. It must be called before Start.
func Configure(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// Started is a no-op on Unix: group membership was fixed at process creation.
func Started(*exec.Cmd) error { return nil }

// Terminate stops the whole process group: a graceful SIGTERM first, then a
// SIGKILL once the grace period expires. It waits until the leader is reaped,
// mirroring the original group-kill semantics for runtime cancellation.
func Terminate(cmd *exec.Cmd, grace time.Duration) {
	if cmd.Process == nil {
		return
	}
	group := func(sig syscall.Signal) {
		if cmd.SysProcAttr != nil && cmd.SysProcAttr.Setpgid && cmd.SysProcAttr.Pgid == 0 {
			if e := syscall.Kill(-cmd.Process.Pid, sig); e == nil {
				return
			}
		}
		_ = cmd.Process.Kill()
	}
	group(syscall.SIGTERM)
	// Signal while the leader is still unreaped; its PID cannot be reused.
	done := make(chan struct{})
	go func() { _, _ = cmd.Process.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(grace):
		select {
		case <-done:
			return
		default:
		}
		group(syscall.SIGKILL)
		<-done
	}
}

// Kill force-kills the whole process group immediately. It is safe to call
// from exec.Cmd.Cancel, which must not block.
func Kill(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	if cmd.SysProcAttr != nil && cmd.SysProcAttr.Setpgid && cmd.SysProcAttr.Pgid == 0 {
		// The group ID is fixed at launch, even if Wait already reaped its
		// leader and ErrWaitDelay reports surviving pipe-holding children.
		if e := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); e == nil {
			return nil
		}
	}
	return cmd.Process.Kill()
}
