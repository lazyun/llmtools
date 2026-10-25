//go:build !windows

package procsup

import (
	"bytes"
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestKillReachesChildrenAfterLeaderWasReaped(t *testing.T) {
	cmd := exec.Command("/bin/sh", "-c", "sleep 30 & printf '%s' $!")
	Configure(cmd)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.WaitDelay = 50 * time.Millisecond
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer Kill(cmd)
	if err := Started(cmd); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); !errors.Is(err, exec.ErrWaitDelay) {
		t.Fatalf("expected inherited pipe holder: %v", err)
	}
	child, err := strconv.Atoi(strings.TrimSpace(out.String()))
	if err != nil {
		t.Fatal(err)
	}
	if err := Kill(cmd); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for syscall.Kill(child, 0) == nil {
		if time.Now().After(deadline) {
			t.Fatal("owned background child survived group kill")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
