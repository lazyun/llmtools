// Package repoaccess performs server-owned read-only repository navigation.
// Every access is confined to one canonical authorized root: the target is
// resolved lexically and then through its full symlink chain, and the final
// real path must remain inside that root. Nothing here writes to disk.
package repoaccess

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"ai-worker-mcp/internal/agent"
	"ai-worker-mcp/internal/localfs"
)

const (
	MaxTargetLen   = 4096
	MaxFileBytes   = 256 * 1024
	SkipFileBytes  = 4 * 1024 * 1024
	MaxGlobResults = 500
	MaxGrepMatched = 200
	MaxLineBytes   = 400
	walkBudget     = 200000
)

func denied() error { return agent.E("WORKSPACE_ACCESS_DENIED") }

// canonicalRoot resolves the authorized root itself. A grant recorded under a
// symlinked spelling (macOS /var vs /private/var) must still confine its own
// contents, and every containment decision below is made against this one form.
// The root must itself be a directory: a file or a symlink standing in for one
// authorizes nothing, even when its own target is readable.
func canonicalRoot(root string) (string, error) {
	if root == "" || !filepath.IsAbs(root) {
		return "", denied()
	}
	r, e := filepath.EvalSymlinks(filepath.Clean(root))
	if e != nil {
		return "", denied() // a missing or unreadable grant authorizes nothing
	}
	r = filepath.Clean(r)
	if st, e := os.Stat(r); e != nil || !st.IsDir() {
		return "", denied()
	}
	return r, nil
}

// Resolve returns the real path of raw inside root, or a stable denial.
// raw may be relative to root or absolute. Symlink chains are resolved
// completely, so a link (or chain of links) that escapes the root is denied,
// while a link whose final target is inside the root is accepted.
func Resolve(root, raw string) (string, error) {
	realRoot, e := canonicalRoot(root)
	if e != nil {
		return "", e
	}
	target := strings.TrimSpace(raw)
	if target == "" || len(target) > MaxTargetLen || strings.ContainsRune(target, 0) {
		return "", denied()
	}
	if strings.HasPrefix(target, "~") {
		return "", denied()
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(realRoot, target)
	}
	lexical := filepath.Clean(target)
	// Component-boundary containment before touching the filesystem, so
	// traversal and sibling-prefix collisions never reach an open(2). An
	// absolute target may legitimately carry the symlinked spelling of root.
	if !localfs.Contains(realRoot, lexical) && !localfs.Contains(filepath.Clean(root), lexical) {
		return "", denied()
	}
	resolved, e := filepath.EvalSymlinks(lexical)
	if e != nil {
		if os.IsNotExist(e) {
			return "", agent.E("REPO_TARGET_NOT_FOUND")
		}
		return "", denied()
	}
	resolved = filepath.Clean(resolved)
	if !localfs.Contains(realRoot, resolved) {
		return "", denied()
	}
	return resolved, nil
}

// checkRegular re-verifies the resolved path immediately before and after
// opening, so a symlink swap between resolution and read is rejected.
func checkRegular(root, resolved string) (*os.File, error) {
	again, e := filepath.EvalSymlinks(resolved)
	if e != nil || filepath.Clean(again) != resolved || !localfs.Contains(root, resolved) {
		return nil, denied()
	}
	f, e := os.Open(resolved)
	if e != nil {
		if os.IsNotExist(e) {
			return nil, agent.E("REPO_TARGET_NOT_FOUND")
		}
		return nil, denied()
	}
	st, e := f.Stat()
	if e != nil || !st.Mode().IsRegular() {
		f.Close()
		return nil, agent.E("REPO_TARGET_NOT_FOUND")
	}
	if st.Size() > SkipFileBytes {
		f.Close()
		return nil, agent.E("REPO_ACCESS_LIMIT")
	}
	return f, nil
}

type File struct {
	Path       string `json:"path"`
	SizeBytes  int64  `json:"size_bytes"`
	Lines      int    `json:"lines"`
	Truncated  bool   `json:"truncated"`
	ModifiedAt string `json:"modified_at,omitempty"`
	Content    string `json:"content"`
}

// Read returns at most maxBytes of one confined regular file.
func Read(root, raw string, maxBytes int64) (File, error) {
	var out File
	if maxBytes <= 0 || maxBytes > MaxFileBytes {
		maxBytes = MaxFileBytes
	}
	realRoot, e := canonicalRoot(root)
	if e != nil {
		return out, e
	}
	resolved, e := Resolve(root, raw)
	if e != nil {
		return out, e
	}
	f, e := checkRegular(realRoot, resolved)
	if e != nil {
		return out, e
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if e != nil && e != io.ErrUnexpectedEOF {
		return out, denied()
	}
	if int64(len(b)) > maxBytes {
		b = b[:maxBytes]
		out.Truncated = true
	}
	if looksBinary(b) {
		return out, agent.E("REPO_ACCESS_LIMIT")
	}
	out.Path = relative(realRoot, resolved)
	out.SizeBytes = int64(len(b))
	if st, serr := f.Stat(); serr == nil {
		out.SizeBytes = st.Size()
		out.ModifiedAt = st.ModTime().UTC().Format(time.RFC3339)
	}
	out.Lines = lines(b)
	out.Content = string(b)
	return out, nil
}

func looksBinary(b []byte) bool {
	limit := len(b)
	if limit > 8192 {
		limit = 8192
	}
	for _, c := range b[:limit] {
		if c == 0 {
			return true
		}
	}
	return false
}
func lines(b []byte) int {
	if len(b) == 0 {
		return 0
	}
	n := 1
	for _, c := range b {
		if c == '\n' {
			n++
		}
	}
	if b[len(b)-1] == '\n' {
		n--
	}
	return n
}
func relative(root, resolved string) string {
	rel, e := filepath.Rel(root, resolved)
	if e != nil {
		return filepath.Base(resolved)
	}
	return filepath.ToSlash(rel)
}

// Glob returns in-root paths matching pattern. The pattern is matched relative
// to path (or the workspace root). Directory symlinks are only descended when
// their final target stays inside root, and cycles are cut with a visited set
// of real paths.
func Glob(root, pattern string, path string) ([]string, error) {
	realRoot, e := canonicalRoot(root)
	if e != nil {
		return nil, e
	}
	base, matcher, file, e := splitPattern(realRoot, pattern, path)
	if e != nil {
		return nil, e
	}
	if file != "" {
		rel, ok := patternRel(base, file)
		if ok && matcher(rel) {
			return []string{relative(realRoot, file)}, nil
		}
		return []string{}, nil
	}
	return walk(realRoot, base, matcher, MaxGlobResults)
}

// patternRel expresses abs in the base-relative pattern space.
func patternRel(base, abs string) (string, bool) {
	if !localfs.Contains(base, abs) {
		return "", false
	}
	rel, e := filepath.Rel(base, abs)
	if e != nil {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// splitPattern turns a pattern and an optional scope into the canonical
// directory the walk starts from, a matcher over paths relative to it, and the
// absolute file when the scope names a single file. root must already be
// canonical. A traversal pattern is a denial, not an empty result.
func splitPattern(root, pattern, path string) (string, func(string) bool, string, error) {
	p := filepath.ToSlash(strings.TrimSpace(pattern))
	if p == "" || len(p) > MaxTargetLen || strings.ContainsRune(p, 0) {
		return "", nil, "", denied()
	}
	if filepath.IsAbs(p) || strings.HasPrefix(p, "~") {
		return "", nil, "", denied()
	}
	start := root
	if strings.TrimSpace(path) != "" {
		resolved, e := Resolve(root, path)
		if e != nil {
			return "", nil, "", e
		}
		start = resolved
	}
	file := ""
	if st, e := os.Stat(start); e == nil && !st.IsDir() {
		file = start
		start = filepath.Dir(start)
	}
	literal := make([]string, 0, 4)
	segments := strings.Split(p, "/")
	rest := len(segments)
	for i, s := range segments {
		if s == "" || s == "." {
			rest = i + 1
			continue
		}
		// Traversal in a navigation pattern is a denial, not an empty result.
		if s == ".." {
			return "", nil, "", denied()
		}
		// The static leading directories bound the walk; everything after the
		// first metacharacter is matched, not walked.
		if strings.ContainsAny(s, "*?[") {
			rest = i
			break
		}
		literal = append(literal, s)
		rest = i + 1
	}
	base := filepath.Join(append([]string{start}, literal...)...)
	base = filepath.Clean(base)
	// The walk and the matcher share one space: the canonical in-root view a
	// read would resolve to. Reporting lexical symlink paths instead would let
	// one file appear twice under two different names.
	resolved, e := filepath.EvalSymlinks(base)
	if e != nil {
		if os.IsNotExist(e) {
			return "", nil, "", agent.E("REPO_TARGET_NOT_FOUND")
		}
		return "", nil, "", denied()
	}
	resolved = filepath.Clean(resolved)
	if !localfs.Contains(root, resolved) {
		return "", nil, "", denied()
	}
	// A pattern with no metacharacter can name a file directly; it is then a
	// single result rather than a directory to walk.
	if st, serr := os.Stat(resolved); serr == nil && !st.IsDir() {
		return filepath.Dir(resolved), matchSegments(strings.Join(segments[rest:], "/")), resolved, nil
	}
	return resolved, matchSegments(strings.Join(segments[rest:], "/")), file, nil
}

// matchSegments compiles a slash-separated pattern supporting *, ?, and ** .
func matchSegments(pattern string) func(string) bool {
	if pattern == "" || pattern == "**" {
		return func(string) bool { return true }
	}
	parts := strings.Split(pattern, "/")
	return func(candidate string) bool {
		return matchParts(parts, strings.Split(candidate, "/"))
	}
}

func matchParts(pattern, name []string) bool {
	if len(pattern) == 0 {
		return len(name) == 0
	}
	if pattern[0] == "**" {
		for i := 0; i <= len(name); i++ {
			if matchParts(pattern[1:], name[i:]) {
				return true
			}
		}
		return false
	}
	if len(name) == 0 {
		return false
	}
	if matchPart(pattern[0], name[0]) {
		return matchParts(pattern[1:], name[1:])
	}
	return false
}

func matchPart(pattern, name string) bool {
	if pattern == "*" {
		return true
	}
	return globMatch(pattern, name)
}

// globMatch matches one path segment against *, ?, and character classes.
func globMatch(pattern, name string) bool {
	for len(pattern) > 0 {
		switch pattern[0] {
		case '*':
			for i := 0; i <= len(name); i++ {
				if globMatch(pattern[1:], name[i:]) {
					return true
				}
			}
			return false
		case '?':
			if name == "" {
				return false
			}
			pattern, name = pattern[1:], name[1:]
		case '[':
			end := strings.Index(pattern[1:], "]")
			if end < 0 || name == "" {
				return false
			}
			set, rest := pattern[1:end+1], pattern[end+2:]
			if !inSet(set, name[0]) {
				return false
			}
			pattern, name = rest, name[1:]
		default:
			if len(name) == 0 || pattern[0] != name[0] {
				return false
			}
			pattern, name = pattern[1:], name[1:]
		}
	}
	return name == ""
}

func inSet(set string, c byte) bool {
	negate := strings.HasPrefix(set, "^") || strings.HasPrefix(set, "!")
	if negate {
		set = set[1:]
	}
	found := false
	for i := 0; i < len(set); i++ {
		if i+2 < len(set) && set[i+1] == '-' {
			if c >= set[i] && c <= set[i+2] {
				found = true
			}
			i += 2
			continue
		}
		if set[i] == c {
			found = true
		}
	}
	return found != negate
}

type Match struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Text string `json:"text"`
}

// Grep scans confined files and never returns content from outside root.
func Grep(root, pattern, glob, path string) ([]Match, error) {
	realRoot, e := canonicalRoot(root)
	if e != nil {
		return nil, e
	}
	if len(pattern) == 0 || len(pattern) > 512 {
		return nil, agent.E("REPO_INPUT_INVALID")
	}
	re, e := regexp.Compile(pattern)
	if e != nil {
		return nil, agent.E("REPO_INPUT_INVALID")
	}
	if strings.TrimSpace(glob) == "" {
		glob = "**"
	}
	base, matcher, file, e := splitPattern(realRoot, glob, path)
	if e != nil {
		return nil, e
	}
	if file != "" {
		return grepFile(realRoot, file, re)
	}
	files, e := walk(realRoot, base, matcher, MaxGlobResults)
	if e != nil {
		return nil, e
	}
	out := []Match{}
	for _, f := range files {
		found, e := grepFile(realRoot, filepath.Join(realRoot, filepath.FromSlash(f)), re)
		if e != nil {
			continue // unreadable/too large/binary candidates never abort the search
		}
		for _, m := range found {
			out = append(out, m)
			if len(out) >= MaxGrepMatched {
				return out, nil
			}
		}
	}
	return out, nil
}

func grepFile(root, abs string, re *regexp.Regexp) ([]Match, error) {
	resolved, e := Resolve(root, abs)
	if e != nil {
		return nil, e
	}
	f, e := checkRegular(root, resolved)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	if st, e := f.Stat(); e == nil && st.Size() > SkipFileBytes {
		return nil, agent.E("REPO_ACCESS_LIMIT")
	}
	// Binary candidates are skipped, exactly as Read refuses them, so no NUL
	// run ever reaches the caller and no compiled pattern is matched against
	// unrelated bytes.
	head := make([]byte, 8192)
	n, _ := f.Read(head)
	if looksBinary(head[:n]) {
		return nil, agent.E("REPO_ACCESS_LIMIT")
	}
	if _, e = f.Seek(0, io.SeekStart); e != nil {
		return nil, denied()
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), MaxLineBytes*4)
	out := []Match{}
	line := 0
	for sc.Scan() {
		line++
		text := sc.Text()
		if !re.MatchString(text) {
			continue
		}
		if len(text) > MaxLineBytes {
			text = text[:MaxLineBytes] + "…"
		}
		out = append(out, Match{Path: relative(root, resolved), Line: line, Text: text})
		if len(out) >= MaxGrepMatched {
			return out, nil
		}
	}
	if err := sc.Err(); err != nil && strings.Contains(err.Error(), "illegal UTF-8") {
		return nil, agent.E("REPO_ACCESS_LIMIT")
	}
	return out, nil
}

// walk collects matching in-root files without following any escape. Returned
// paths are root-relative in the canonical view Read resolves to, so one file
// is reported once even when several in-root links reach it.
func walk(root, base string, matcher func(string) bool, limit int) ([]string, error) {
	st, e := os.Stat(base)
	if e != nil {
		if os.IsNotExist(e) {
			return nil, agent.E("REPO_TARGET_NOT_FOUND")
		}
		return nil, denied()
	}
	if !st.IsDir() {
		return nil, denied()
	}
	visited := map[string]bool{}
	out := []string{}
	budget := walkBudget
	var descend func(abs string)
	descend = func(abs string) {
		if budget <= 0 || len(out) >= limit {
			return
		}
		real, e := filepath.EvalSymlinks(abs)
		if e != nil || !localfs.Contains(root, real) || visited[real] {
			return // an unresolvable or escaping directory yields no results at all
		}
		real = filepath.Clean(real)
		visited[real] = true
		entries, e := os.ReadDir(abs)
		if e != nil {
			return
		}
		for _, entry := range entries {
			if budget <= 0 || len(out) >= limit {
				return
			}
			budget--
			child := filepath.Join(real, entry.Name())
			if entry.Type()&os.ModeSymlink != 0 {
				target, e := filepath.EvalSymlinks(child)
				if e != nil {
					continue
				}
				target = filepath.Clean(target)
				if !localfs.Contains(root, target) {
					continue // never report or scan an escape
				}
				isDir, e := isDirectory(target)
				if e != nil {
					continue
				}
				if isDir {
					descend(target)
					continue
				}
				child = target
			} else if entry.IsDir() {
				descend(child)
				continue
			}
			rel, ok := patternRel(base, child)
			if !ok {
				continue
			}
			if matcher(rel) {
				out = append(out, relative(root, child))
			}
		}
	}
	descend(base)
	sort.Strings(out)
	return out, nil
}

func isDirectory(p string) (bool, error) {
	st, e := os.Stat(p)
	if e != nil {
		return false, e
	}
	return st.IsDir(), nil
}
