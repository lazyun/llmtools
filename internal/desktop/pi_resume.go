package desktop

import (
	"ai-worker-mcp/internal/agent"
	"ai-worker-mcp/internal/config"
	"bufio"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Quotes literal arguments for POSIX shells, including apostrophes and $().
func shellQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }

type piSessionRoot struct {
	tree     *os.Root
	names    []string
	resolved map[string]string
}

// Session directories and executable paths are frozen on new runs. Old runs
// may use the current directory only when an exact matching session exists.
// Only bounded session headers are inspected; transcripts and provider state
// are never returned or persisted by the command resolver.
func piResumeCommands(runs []agent.Run, cfg config.Config) map[string]string {
	commands := map[string]string{}
	roots := map[string]*piSessionRoot{}
	defer func() {
		for _, root := range roots {
			if root != nil {
				_ = root.tree.Close()
			}
		}
	}()
	for _, run := range runs {
		if run.Runtime != "pi" || !validPiSessionID(run.RuntimeSessionID) {
			continue
		}
		binary, dir := run.RuntimeBinary, run.RuntimeSessionDir
		if dir == "" {
			if cfg.Runtime.Type != "pi" {
				continue
			}
			dir = cfg.Runtime.SessionDir
		}
		if binary == "" {
			binary = cfg.Runtime.Binary
		}
		if binary == "" || strings.ContainsAny(binary, "\x00\r\n") || !filepath.IsAbs(dir) || strings.ContainsAny(dir, "\x00\r\n") {
			continue
		}
		dir = filepath.Clean(dir)
		root, known := roots[dir]
		if !known {
			tree, err := os.OpenRoot(dir)
			if err != nil {
				roots[dir] = nil
				continue
			}
			entries, err := fs.ReadDir(tree.FS(), ".")
			if err != nil {
				_ = tree.Close()
				roots[dir] = nil
				continue
			}
			root = &piSessionRoot{tree: tree, resolved: map[string]string{}}
			for _, entry := range entries {
				if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".jsonl") {
					root.names = append(root.names, entry.Name())
				}
			}
			roots[dir] = root
		}
		if root == nil {
			continue
		}
		key := run.RuntimeSessionID + "\x00" + run.RuntimeSessionFile
		name, known := root.resolved[key]
		if !known {
			if run.RuntimeSessionFile != "" {
				relative := run.RuntimeSessionFile
				if filepath.IsAbs(relative) {
					relative, _ = filepath.Rel(dir, relative)
				}
				if filepath.IsLocal(relative) && matchingPiSession(root.tree, relative, run.RuntimeSessionID) {
					name = relative
				}
			}
			if name == "" {
				suffix := "_" + run.RuntimeSessionID + ".jsonl"
				for _, candidate := range root.names {
					if strings.HasSuffix(candidate, suffix) && matchingPiSession(root.tree, candidate, run.RuntimeSessionID) {
						if name != "" {
							name = ""
							break
						}
						name = candidate
					}
				}
			}
			root.resolved[key] = name
		}
		if name == "" {
			continue
		}
		commands[run.ID] = shellQuote(binary) + " --resume --session-dir " + shellQuote(dir) + " --session " + shellQuote(filepath.Join(dir, name))
	}
	return commands
}

func validPiSessionID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

// openSessionHeader opens a session file without ever blocking: a runtime
// writing its transcript must not hang the desktop state snapshot. Windows
// has no O_NONBLOCK, but pipes and other special files there are rejected by
// the Stat regular-file check below before any read can block.
func openSessionHeader(root *os.Root, name string) (*os.File, error) {
	return root.OpenFile(name, os.O_RDONLY|nonBlockFlag(), 0)
}

func matchingPiSession(root *os.Root, name, id string) bool {
	info, err := root.Lstat(name)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	file, err := openSessionHeader(root, name)
	if err != nil {
		return false
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil || !stat.Mode().IsRegular() {
		return false
	}
	line, err := bufio.NewReader(io.LimitReader(file, 16385)).ReadBytes('\n')
	if err != nil && err != io.EOF || len(line) > 16384 {
		return false
	}
	var header struct {
		Type string `json:"type"`
		ID   string `json:"id"`
	}
	return json.Unmarshal(line, &header) == nil && header.Type == "session" && header.ID == id
}
