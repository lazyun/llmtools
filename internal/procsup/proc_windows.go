//go:build windows

package procsup

import (
	"errors"
	"os/exec"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	jobMu  sync.Mutex
	jobMap = map[int]windows.Handle{}
)

// Configure isolates the child from console control events. The kill-on-close
// Job Object that actually owns the tree is assigned in Started.
func Configure(cmd *exec.Cmd) {
	cmd.SysProcAttr = &windows.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP}
}

// Started assigns the freshly created process to a Job Object with
// JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE. Every process the tree spawns (including
// the node interpreter behind an npm .cmd/.shim launcher) inherits job
// membership, so cancelling the run can never leave an owned child behind and
// never touches processes outside this launch. Call once, right after a
// successful Start; on failure the caller must kill the started process.
func Started(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return errors.New("procsup: process not started")
	}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		_ = windows.CloseHandle(job)
		return err
	}
	proc, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		_ = windows.CloseHandle(job)
		return err
	}
	defer windows.CloseHandle(proc)
	if err = windows.AssignProcessToJobObject(job, proc); err != nil {
		_ = windows.CloseHandle(job)
		return err
	}
	jobMu.Lock()
	jobMap[cmd.Process.Pid] = job
	jobMu.Unlock()
	return nil
}

func takeJob(pid int) windows.Handle {
	jobMu.Lock()
	defer jobMu.Unlock()
	job := jobMap[pid]
	delete(jobMap, pid)
	return job
}

// Terminate ends the run's whole process tree. Windows has no per-group
// graceful signal, so the job — which contains only processes this launch
// created — is terminated and the leader waited on for at most the grace
// period.
func Terminate(cmd *exec.Cmd, grace time.Duration) {
	if cmd.Process == nil {
		return
	}
	if job := takeJob(cmd.Process.Pid); job != 0 {
		_ = windows.TerminateJobObject(job, 1)
		_ = windows.CloseHandle(job)
	}
	_ = cmd.Process.Kill()
	done := make(chan struct{})
	go func() { _, _ = cmd.Process.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(grace):
	}
}

// Kill force-kills the job immediately. It is safe to call from
// exec.Cmd.Cancel, which must not block.
func Kill(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	if job := takeJob(cmd.Process.Pid); job != 0 {
		_ = windows.TerminateJobObject(job, 1)
		_ = windows.CloseHandle(job)
	}
	return cmd.Process.Kill()
}
