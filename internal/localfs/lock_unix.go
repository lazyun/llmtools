//go:build !windows

package localfs

import (
	"errors"
	"os"
	"syscall"
)

func lockExclusive(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}

func unlock(f *os.File) {
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}

// IsLockBusy reports whether a Lock error means the lock is currently held by
// another process, so callers can distinguish contention from breakage.
func IsLockBusy(err error) bool {
	return errors.Is(err, syscall.EWOULDBLOCK)
}
