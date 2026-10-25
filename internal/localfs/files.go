// Package localfs contains bounded, atomic application storage primitives.
package localfs

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func DefaultDirectory() (string, error) {
	d, e := os.UserConfigDir()
	if e != nil {
		return "", e
	}
	return filepath.Join(d, "AI Worker"), nil
}
func EnsureDirectory(d string) error {
	if e := os.MkdirAll(d, 0700); e != nil {
		return e
	}
	s, e := os.Lstat(d)
	if e != nil || !s.IsDir() || s.Mode()&os.ModeSymlink != 0 {
		return errors.New("CONFIG_ERROR: unsafe data directory")
	}
	return nil
}

// CanonicalDir resolves a user-selected directory to a canonical absolute path.
// It rejects relative or non-directory inputs and resolves symlinks so the
// durable registry never stores a path whose meaning can later change.
func CanonicalDir(p string) (string, error) {
	if strings.TrimSpace(p) == "" {
		return "", errors.New("WORKSPACE_INVALID: empty path")
	}
	abs, e := filepath.Abs(p)
	if e != nil {
		return "", errors.New("WORKSPACE_INVALID: not resolvable")
	}
	resolved, e := filepath.EvalSymlinks(abs)
	if e != nil {
		return "", errors.New("WORKSPACE_INVALID: not resolvable")
	}
	resolved, e = filepath.Abs(resolved)
	if e != nil {
		return "", errors.New("WORKSPACE_INVALID: not resolvable")
	}
	resolved = filepath.Clean(resolved)
	s, e := os.Stat(resolved)
	if e != nil || !s.IsDir() {
		return "", errors.New("WORKSPACE_INVALID: not a directory")
	}
	return resolved, nil
}

// Contains reports whether target is root itself or a descendant of root,
// comparing path components rather than string prefixes.
func Contains(root, target string) bool {
	if root == "" || target == "" {
		return false
	}
	rel, e := filepath.Rel(root, target)
	if e != nil || filepath.IsAbs(rel) {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
func ReadFile(p string) ([]byte, error) {
	s, e := os.Lstat(p)
	if e != nil {
		return nil, e
	}
	if !s.Mode().IsRegular() || s.Size() > 1048576 {
		return nil, errors.New("CONFIG_ERROR: unsafe storage file")
	}
	f, e := os.Open(p)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, 1048577))
}
func AtomicWrite(p string, b []byte) error {
	if e := EnsureDirectory(filepath.Dir(p)); e != nil {
		return e
	}
	if s, e := os.Lstat(p); e == nil && !s.Mode().IsRegular() {
		return errors.New("CONFIG_ERROR: unsafe destination")
	}
	f, e := os.CreateTemp(filepath.Dir(p), ".llmtools-*")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, e = f.Write(b); e != nil {
		return e
	}
	if e = f.Sync(); e != nil {
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	if e = os.Rename(f.Name(), p); e != nil {
		return e
	}
	d, e := os.Open(filepath.Dir(p))
	if e == nil {
		defer d.Close()
		_ = d.Sync()
	}
	return nil
}

// Lock is a process-scoped exclusive lock, released by the OS even after a
// crash. IsLockBusy reports whether a Lock failure means another holder is
// active right now (as opposed to an unusable lock file).
func Lock(p string) (*os.File, error) {
	f, e := os.OpenFile(p, os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	if e = lockExclusive(f); e != nil {
		f.Close()
		return nil, e
	}
	return f, nil
}
func Unlock(f *os.File) {
	if f != nil {
		unlock(f)
		_ = f.Close()
	}
}
