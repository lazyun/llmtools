package localfs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAtomicWriteAndUnsafeDestination(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.toml")
	if e := AtomicWrite(p, []byte("complete")); e != nil {
		t.Fatal(e)
	}
	st, _ := os.Stat(p)
	if st.Mode().Perm() != 0600 {
		t.Fatal("unsafe config mode")
	}
	if e := AtomicWrite(p, []byte("replacement")); e != nil {
		t.Fatal(e)
	}
	b, _ := ReadFile(p)
	if string(b) != "replacement" {
		t.Fatal("non-atomic replacement")
	}
	link := filepath.Join(dir, "symlink")
	if e := os.Symlink(p, link); e != nil {
		t.Fatal(e)
	}
	if AtomicWrite(link, []byte("bad")) == nil {
		t.Fatal("symlink accepted")
	}
	if _, e := ReadFile(link); e == nil {
		t.Fatal("symlink read")
	}
	b, _ = ReadFile(p)
	if string(b) != "replacement" {
		t.Fatal("target corrupted")
	}
}

func TestCanonicalDirResolvesSymlinksAndRejectsNonDirectories(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real")
	if e := os.Mkdir(real, 0o755); e != nil {
		t.Fatal(e)
	}
	link := filepath.Join(root, "link")
	if e := os.Symlink(real, link); e != nil {
		t.Fatal(e)
	}
	canonical, e := CanonicalDir(link)
	if e != nil {
		t.Fatal(e)
	}
	want, _ := filepath.EvalSymlinks(real)
	if canonical != want {
		t.Fatalf("canonical %q want %q", canonical, want)
	}
	file := filepath.Join(root, "file.txt")
	if e := os.WriteFile(file, []byte("x"), 0o600); e != nil {
		t.Fatal(e)
	}
	if _, e := CanonicalDir(file); e == nil {
		t.Fatal("regular file accepted as workspace root")
	}
	if _, e := CanonicalDir(filepath.Join(root, "missing")); e == nil {
		t.Fatal("missing directory accepted")
	}
	if _, e := CanonicalDir(""); e == nil {
		t.Fatal("empty path accepted")
	}
}

func TestContainsUsesComponentBoundaries(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "src")
	if e := os.Mkdir(nested, 0o755); e != nil {
		t.Fatal(e)
	}
	if !Contains(root, root) || !Contains(root, nested) {
		t.Fatal("root or descendant rejected")
	}
	if Contains(root, filepath.Dir(root)) || Contains(root, root+"-sibling") {
		t.Fatal("parent or prefix-collision accepted")
	}
	if contains := Contains("/repo/foo", "/repo/foo/sub"); !contains {
		t.Fatal("component descendant rejected")
	}
	if contains := Contains("/repo/foo", "/repo/foobar"); contains {
		t.Fatal("prefix collision accepted")
	}
	if contains := Contains("/repo/foo", "/repo/foo/../bar"); contains {
		t.Fatal("traversal accepted")
	}
}
