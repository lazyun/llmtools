package repoaccess

import (
	"ai-worker-mcp/internal/agent"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// tree is the confined repository a run is bound to, plus the neighbours it
// must never reach: a sibling with a colliding name prefix, an outside
// directory, and a second authorized workspace.
type tree struct {
	root, outside, sibling, other string
}

func build(t *testing.T) tree {
	t.Helper()
	base := t.TempDir()
	var tr tree
	tr.root = filepath.Join(base, "workspace-a")
	tr.outside = filepath.Join(base, "outside")
	tr.sibling = filepath.Join(base, "workspace-abetter")
	tr.other = filepath.Join(base, "workspace-b")
	for _, d := range []string{tr.root, filepath.Join(tr.root, "src"), filepath.Join(tr.root, "lib"), filepath.Join(tr.root, "big"), tr.outside, tr.sibling, tr.other} {
		if e := os.MkdirAll(d, 0700); e != nil {
			t.Fatal(e)
		}
	}
	files := map[string]string{
		filepath.Join(tr.root, "src", "alpha.txt"):       "ALPHA-7f3c9d2e\n",
		filepath.Join(tr.root, "lib", "beta.txt"):        "BETA-4a1b8c6d\nALPHA-7f3c9d2e tail\n",
		filepath.Join(tr.root, "notes.md"):               "# notes\n",
		filepath.Join(tr.root, "big", "blob.bin"):        "header\x00footer\n",
		filepath.Join(tr.outside, "secret.txt"):          "OUTSIDE-9e8d7c6b\n",
		filepath.Join(tr.sibling, "sibling.txt"):         "SIBLING-5c4b3a2d\n",
		filepath.Join(tr.other, "secret-b.txt"):          "OTHERWORKSPACE-1a2b3c4d\n",
		filepath.Join(tr.root, "src", "foo_probe.go"):    "package src\n",
		filepath.Join(tr.root, "src", "foobar_probe.go"): "package src\n",
	}
	for p, content := range files {
		if e := os.WriteFile(p, []byte(content), 0600); e != nil {
			t.Fatal(e)
		}
	}
	links := map[string]string{
		filepath.Join(tr.root, "link-inside"):  filepath.Join(tr.root, "src"),
		filepath.Join(tr.root, "link-outside"): tr.outside,
		filepath.Join(tr.root, "escape.txt"):   filepath.Join(tr.outside, "secret.txt"),
		filepath.Join(tr.root, "up-down"):      filepath.Join(tr.root, "src", "..", "..", "outside"),
		filepath.Join(tr.root, "cycle-a"):      filepath.Join(tr.root, "cycle-b"),
		filepath.Join(tr.root, "cycle-b"):      filepath.Join(tr.root, "cycle-a"),
	}
	for p, target := range links {
		if e := os.Symlink(target, p); e != nil {
			t.Fatal(e)
		}
	}
	return tr
}

func code(t *testing.T, e error) string {
	t.Helper()
	if e == nil {
		return ""
	}
	return agent.Safe(e).Code
}

func TestReadInsideWorkspaceSucceeds(t *testing.T) {
	tr := build(t)
	f, e := Read(tr.root, "src/alpha.txt", 0)
	if e != nil {
		t.Fatal(e)
	}
	if f.Content != "ALPHA-7f3c9d2e\n" || f.Path != "src/alpha.txt" || f.Lines != 1 || f.Truncated {
		t.Fatalf("read: %+v", f)
	}
	if f.SizeBytes != int64(len(f.Content)) || f.ModifiedAt == "" {
		t.Fatalf("metadata: %+v", f)
	}
}

func TestReadDeniesAbsoluteOutsideAndTraversal(t *testing.T) {
	tr := build(t)
	for _, target := range []string{
		filepath.Join(tr.outside, "secret.txt"),
		filepath.Join(tr.sibling, "sibling.txt"),
		"../outside/secret.txt",
		"src/../../outside/secret.txt",
		"/etc/passwd",
		"~/Documents/secret.txt",
		"~",
		filepath.Join(tr.other, "secret-b.txt"),
		"",
		".",
		tr.root,
		"src/alpha.txt/x",
		strings.Repeat("a", MaxTargetLen+1),
		"src/../src/alpha.txt/../nope.txt",
	} {
		f, e := Read(tr.root, target, 0)
		if e == nil {
			t.Fatalf("escaped read succeeded: %q -> %+v", target, f)
		}
		if c := code(t, e); c != "WORKSPACE_ACCESS_DENIED" && c != "REPO_TARGET_NOT_FOUND" {
			t.Fatalf("target %q code %s", target, c)
		}
		if strings.Contains(f.Content, "OUTSIDE") || strings.Contains(f.Content, "SIBLING") || strings.Contains(f.Content, "OTHERWORKSPACE") {
			t.Fatalf("outside content leaked for %q: %q", target, f.Content)
		}
	}
}

// A sibling directory whose name starts with the root's name is not inside it:
// component-boundary containment, not a string prefix.
func TestSiblingPrefixCollisionDenied(t *testing.T) {
	tr := build(t)
	if _, e := Read(tr.root, filepath.Join(tr.sibling, "sibling.txt"), 0); code(t, e) != "WORKSPACE_ACCESS_DENIED" {
		t.Fatalf("prefix collision allowed: %v", e)
	}
	// The fixture's sibling is named so that its absolute path starts with the
	// root's absolute path, which is exactly what a string-prefix check would
	// wrongly accept.
	if _, e := Read(tr.root, "../workspace-abetter/sibling.txt", 0); code(t, e) != "WORKSPACE_ACCESS_DENIED" {
		t.Fatal("lexical prefix escape allowed")
	}
}

func TestSymlinkInsideRootReadsAndOutsideDenied(t *testing.T) {
	tr := build(t)
	f, e := Read(tr.root, "link-inside/alpha.txt", 0)
	if e != nil {
		t.Fatalf("in-root symlink denied: %v", e)
	}
	if f.Content != "ALPHA-7f3c9d2e\n" {
		t.Fatalf("symlink content: %+v", f)
	}
	// The reported path is the canonical in-root view, so one file is named one
	// way however many links reach it.
	if f.Path != "src/alpha.txt" {
		t.Fatalf("path not canonical: %q", f.Path)
	}
	for _, target := range []string{"link-outside/secret.txt", "escape.txt", "up-down/secret.txt"} {
		got, e := Read(tr.root, target, 0)
		if code(t, e) != "WORKSPACE_ACCESS_DENIED" {
			t.Fatalf("symlink escape %q: %v %+v", target, e, got)
		}
		if strings.Contains(got.Content, "OUTSIDE") {
			t.Fatalf("symlink escape leaked %q", target)
		}
	}
	if _, e := Read(tr.root, "cycle-a/alpha.txt", 0); e == nil {
		t.Fatal("broken symlink cycle read")
	}
}

// A symlink swapped in after resolution must not be followed by the open.
func TestSymlinkSwapBetweenResolveAndReadDenied(t *testing.T) {
	tr := build(t)
	swapped := filepath.Join(tr.root, "swapped.txt")
	if e := os.WriteFile(swapped, []byte("INSIDE-1\n"), 0600); e != nil {
		t.Fatal(e)
	}
	resolved, e := Resolve(tr.root, "swapped.txt")
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Remove(swapped); e != nil {
		t.Fatal(e)
	}
	if e = os.Symlink(filepath.Join(tr.outside, "secret.txt"), swapped); e != nil {
		t.Fatal(e)
	}
	// checkRegular re-resolves the frozen path, so the stale target is gone and
	// the swapped name is rejected rather than read through the old resolution.
	if _, e = Read(tr.root, "swapped.txt", 0); e == nil {
		t.Fatal("swapped symlink read")
	}
	if _, e := Read(resolved, resolved, 0); code(t, e) != "WORKSPACE_ACCESS_DENIED" {
		t.Fatalf("non-root resolve accepted: %v", e)
	}
}

// The granted root and the requested path may be spelled with either the
// recorded path or the real directory behind it (macOS /var vs /private/var).
// Confinement is decided on real paths. A root that is not itself a directory
// authorizes nothing.
func TestRootSymlinkSpellingAndNonDirectoryRoot(t *testing.T) {
	tr := build(t)
	base := filepath.Dir(tr.root)
	linkRoot := filepath.Join(base, "workspace-alias")
	if e := os.Symlink(tr.root, linkRoot); e != nil {
		t.Fatal(e)
	}
	for _, root := range []string{tr.root, linkRoot} {
		for _, target := range []string{"src/alpha.txt", filepath.Join(root, "src", "alpha.txt")} {
			f, e := Read(root, target, 0)
			if e != nil || f.Content != "ALPHA-7f3c9d2e\n" {
				t.Fatalf("root %q target %q: %v %+v", root, target, e, f)
			}
		}
	}
	// Only the granted spelling of the workspace is accepted as an absolute
	// path; an unrelated alias inside the tree is not a substitute.
	if _, e := Read(tr.root, filepath.Join(linkRoot, "src", "alpha.txt"), 0); code(t, e) != "WORKSPACE_ACCESS_DENIED" {
		t.Fatalf("unrelated alias spelling accepted: %v", e)
	}
	if _, e := Read(filepath.Join(tr.root, "notes.md"), "src/alpha.txt", 0); code(t, e) != "WORKSPACE_ACCESS_DENIED" {
		t.Fatalf("regular file accepted as root: %v", e)
	}
	if _, e := Glob(filepath.Join(tr.root, "escape.txt"), "**", ""); e == nil || code(t, e) != "WORKSPACE_ACCESS_DENIED" {
		t.Fatalf("escaping symlink accepted as glob root: %v", e)
	}
	if _, e := Read(filepath.Join(base, "missing"), "src/alpha.txt", 0); code(t, e) != "WORKSPACE_ACCESS_DENIED" {
		t.Fatal("missing root read allowed")
	}
}

func TestReadTruncatesAndRefusesBinaryAndOversized(t *testing.T) {
	tr := build(t)
	f, e := Read(tr.root, "src/alpha.txt", 5)
	if e != nil {
		t.Fatal(e)
	}
	if f.Content != "ALPHA" || !f.Truncated {
		t.Fatalf("truncation: %+v", f)
	}
	if _, e := Read(tr.root, "big/blob.bin", 0); code(t, e) != "REPO_ACCESS_LIMIT" {
		t.Fatalf("binary read: %v", e)
	}
	huge := filepath.Join(tr.root, "huge.txt")
	if e := os.WriteFile(huge, make([]byte, SkipFileBytes+1), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := Read(tr.root, "huge.txt", 0); code(t, e) != "REPO_ACCESS_LIMIT" {
		t.Fatalf("oversized read: %v", e)
	}
	if _, e := Read(tr.root, "missing.txt", 0); code(t, e) != "REPO_TARGET_NOT_FOUND" {
		t.Fatalf("missing target: %v", e)
	}
}

func TestGlobConfinesResultsToWorkspace(t *testing.T) {
	tr := build(t)
	all, e := Glob(tr.root, "**/*.txt", "")
	if e != nil {
		t.Fatal(e)
	}
	joined := strings.Join(all, "\n")
	for _, want := range []string{"lib/beta.txt", "src/alpha.txt"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %s in %v", want, all)
		}
	}
	if strings.Count(joined, "src/alpha.txt") != 1 {
		t.Fatalf("in-root symlink listed the same file twice: %v", all)
	}
	for _, never := range []string{"secret", "sibling", "OTHERWORKSPACE", "/outside/"} {
		if strings.Contains(joined, never) {
			t.Fatalf("glob leaked %q: %v", never, all)
		}
	}
	scoped, e := Glob(tr.root, "*.txt", "src")
	if e != nil {
		t.Fatal(e)
	}
	if len(scoped) != 1 || scoped[0] != "src/alpha.txt" {
		t.Fatalf("scoped glob: %v", scoped)
	}
	prefixed, e := Glob(tr.root, "foo*_probe.go", "src")
	if e != nil {
		t.Fatal(e)
	}
	if len(prefixed) != 2 {
		t.Fatalf("segment glob: %v", prefixed)
	}
	exact, e := Glob(tr.root, "foo_probe.go", "src")
	if e != nil || len(exact) != 1 || exact[0] != "src/foo_probe.go" {
		t.Fatalf("prefix-collision glob: %v %v", exact, e)
	}
	nested, e := Glob(tr.root, "./**/*.go", "")
	_ = nested
	if e != nil {
		t.Fatalf("relative-dot pattern: %v", e)
	}
	dirPattern, e := Glob(tr.root, "src", "")
	if e != nil || len(dirPattern) != 3 {
		t.Fatalf("literal directory pattern: %v %v", dirPattern, e)
	}
}

func TestGlobAndGrepDenyEscapesInPatterns(t *testing.T) {
	tr := build(t)
	for _, p := range []string{"../**", "src/../**", "/etc/*", "~/Documents/*", "..", "src/../../outside/*.txt"} {
		if _, e := Glob(tr.root, p, ""); code(t, e) != "WORKSPACE_ACCESS_DENIED" {
			t.Fatalf("glob pattern %q accepted: %v", p, e)
		}
		if _, e := Grep(tr.root, "OUTSIDE", p, ""); code(t, e) != "WORKSPACE_ACCESS_DENIED" {
			t.Fatalf("grep glob %q accepted: %v", p, e)
		}
	}
	for _, p := range []string{"../outside", "/etc", filepath.Join(tr.outside, "secret.txt"), "nope-dir"} {
		if _, e := Glob(tr.root, "*.txt", p); code(t, e) == "" {
			t.Fatalf("glob path %q accepted", p)
		}
	}
}

func TestGrepSearchesConfinedFilesOnly(t *testing.T) {
	tr := build(t)
	matches, e := Grep(tr.root, "ALPHA", "**", "")
	if e != nil {
		t.Fatal(e)
	}
	if len(matches) != 2 {
		t.Fatalf("matches: %+v", matches)
	}
	seen := map[string]bool{}
	for _, m := range matches {
		seen[m.Path] = true
		if strings.Contains(m.Text, "OUTSIDE") || strings.Contains(m.Text, "OTHERWORKSPACE") {
			t.Fatalf("outside content matched from %s", m.Path)
		}
	}
	if !seen["src/alpha.txt"] || !seen["lib/beta.txt"] {
		t.Fatalf("paths: %+v", matches)
	}
	scoped, e := Grep(tr.root, "ALPHA", "*.txt", "src")
	if e != nil || len(scoped) != 1 || scoped[0].Path != "src/alpha.txt" || scoped[0].Line != 1 {
		t.Fatalf("scoped grep: %+v %v", scoped, e)
	}
	byGlob, e := Grep(tr.root, "BETA", "lib/*", "")
	if e != nil || len(byGlob) != 1 {
		t.Fatalf("glob-limited grep: %+v %v", byGlob, e)
	}
	none, e := Grep(tr.root, "OUTSIDE-9e8d7c6b", "**", "")
	if e != nil || len(none) != 0 {
		t.Fatalf("outside marker found: %+v %v", none, e)
	}
	single, e := Grep(tr.root, "tail", "**", "lib/beta.txt")
	if e != nil || len(single) != 1 {
		t.Fatalf("file target grep: %+v %v", single, e)
	}
	if _, e := Grep(tr.root, "(", "**", ""); code(t, e) != "REPO_INPUT_INVALID" {
		t.Fatalf("invalid regexp: %v", e)
	}
	if _, e := Grep(tr.root, "", "**", ""); code(t, e) != "REPO_INPUT_INVALID" {
		t.Fatalf("empty regexp: %v", e)
	}
	if _, e := Grep(tr.root, strings.Repeat("a", 513), "**", ""); code(t, e) != "REPO_INPUT_INVALID" {
		t.Fatalf("oversized regexp: %v", e)
	}
	// Binary and oversized candidates are skipped, never aborted or leaked.
	bin, e := Grep(tr.root, "footer", "**", "")
	if e != nil || len(bin) != 0 {
		t.Fatalf("binary file matched: %+v %v", bin, e)
	}
}

func TestGrepStopsAtMatchLimit(t *testing.T) {
	tr := build(t)
	var b strings.Builder
	for i := 0; i < MaxGrepMatched+50; i++ {
		b.WriteString("line\n")
	}
	if e := os.WriteFile(filepath.Join(tr.root, "many.txt"), []byte(b.String()), 0600); e != nil {
		t.Fatal(e)
	}
	matches, e := Grep(tr.root, "^line$", "**", "")
	if e != nil {
		t.Fatal(e)
	}
	if len(matches) != MaxGrepMatched {
		t.Fatalf("limit: %d", len(matches))
	}
}

func TestGlobStopsAtResultLimit(t *testing.T) {
	tr := build(t)
	dir := filepath.Join(tr.root, "many")
	if e := os.MkdirAll(dir, 0700); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < MaxGlobResults+10; i++ {
		if e := os.WriteFile(filepath.Join(dir, "f"+strconv.Itoa(i)+".txt"), nil, 0600); e != nil {
			t.Fatal(e)
		}
	}
	found, e := Glob(tr.root, "many/*.txt", "")
	if e != nil {
		t.Fatal(e)
	}
	if len(found) > MaxGlobResults {
		t.Fatalf("glob limit: %d", len(found))
	}
}

func TestResolveRejectsEmptyAndRelativeRoots(t *testing.T) {
	tr := build(t)
	if _, e := Resolve("", "src"); code(t, e) != "WORKSPACE_ACCESS_DENIED" {
		t.Fatal("relative root accepted")
	}
	if _, e := Resolve("src", "src"); code(t, e) != "WORKSPACE_ACCESS_DENIED" {
		t.Fatal("non-absolute root accepted")
	}
	abs, e := Resolve(tr.root, filepath.Join(tr.root, "src", "alpha.txt"))
	want, e2 := filepath.EvalSymlinks(filepath.Join(tr.root, "src", "alpha.txt"))
	if e != nil || e2 != nil || abs != filepath.Clean(want) {
		t.Fatalf("in-root absolute path: %v %v", abs, e)
	}
	if _, e := Resolve(tr.root, "src\x00.txt"); code(t, e) != "WORKSPACE_ACCESS_DENIED" {
		t.Fatal("NUL target accepted")
	}
}
