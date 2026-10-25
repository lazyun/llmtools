//go:build windows

package localfs

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// lockExclusive takes a non-blocking exclusive LockFileEx range over the whole
// file. Windows range locks are mandatory and per open file description, so
// they behave like flock for cross-process mutual exclusion: another holder
// causes an immediate lock-violation error, and the OS releases the lock when
// the handle closes (including after a crash).
func lockExclusive(f *os.File) error {
	const lockRange = 1 << 30
	ol := new(windows.Overlapped)
	if e := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, lockRange, 0, ol); e != nil {
		return e
	}
	return nil
}

func unlock(f *os.File) {
	const lockRange = 1 << 30
	_ = windows.UnlockFileEx(windows.Handle(f.Fd()), 0, lockRange, 0, new(windows.Overlapped))
}

// IsLockBusy reports whether a Lock error means the lock is currently held by
// another process: ERROR_LOCK_VIOLATION is what LOCKFILE_FAIL_IMMEDIATELY
// yields under contention.
func IsLockBusy(err error) bool {
	return errors.Is(err, windows.ERROR_LOCK_VIOLATION)
}
