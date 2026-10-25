package integration_test

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"
)

type fixtureTree struct {
	root   string
	tokens map[string]string // unique token -> fixture-relative path
}

// token returns the unique marker that only one fixture file carries, so an
// assertion can name a file without hard-coding its secret value.
func (f fixtureTree) token(t *testing.T, path string) string {
	t.Helper()
	found := ""
	for token, candidate := range f.tokens {
		if candidate != path {
			continue
		}
		if found != "" {
			t.Fatalf("fixture has two tokens for %s", path)
		}
		found = token
	}
	if found == "" {
		t.Fatalf("fixture has no token for %s", path)
	}
	return found
}

func buildFixtureTree(t *testing.T) fixtureTree {
	t.Helper()
	repo := filepath.Join("..", "..")
	out, e := exec.Command("python3", filepath.Join(repo, "scripts", "read_only_repo_fixture.py"), "--root", filepath.Join(repo, ".smoke", "read-only-repo")).CombinedOutput()
	if e != nil {
		t.Fatalf("fixture build: %v %s", e, out)
	}
	var fixture struct {
		Root   string            `json:"root"`
		Tokens map[string]string `json:"tokens"`
	}
	if e = json.Unmarshal(out, &fixture); e != nil {
		t.Fatal(e)
	}
	return fixtureTree{root: fixture.Root, tokens: fixture.Tokens}
}
