package repoaccess

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteEditAndBoundaries(t *testing.T) {
	tr := build(t)
	if _, err := Write(tr.root, "new/deep/file.txt", "hello world"); err != nil {
		t.Fatal(err)
	}
	if _, err := Edit(tr.root, "new/deep/file.txt", "world", "tests"); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(filepath.Join(tr.root, "new/deep/file.txt"))
	if string(body) != "hello tests" {
		t.Fatal(string(body))
	}
	if _, err := Edit(tr.root, "new/deep/file.txt", "missing", "x"); err == nil {
		t.Fatal("missing match accepted")
	}
	os.MkdirAll(filepath.Join(tr.root, ".git"), 0700)
	os.Symlink(filepath.Join(tr.root, ".git"), filepath.Join(tr.root, "git-alias"))
	for _, path := range []string{"../outside/new.txt", filepath.Join(tr.outside, "new.txt"), "link-outside/new.txt", "escape.txt", ".git/config", "git-alias/config"} {
		if _, err := Write(tr.root, path, "bad"); err == nil {
			t.Errorf("write accepted %s", path)
		}
	}
}
