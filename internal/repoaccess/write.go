package repoaccess

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"

	"ai-worker-mcp/internal/agent"
	"ai-worker-mcp/internal/localfs"
)

const MaxWriteBytes = 1024 * 1024

// writeName accepts new targets as well as existing files. os.Root performs
// every filesystem operation, including symlink resolution and atomic rename.
func writeName(root, raw string) (string, error) {
	if raw == "" || len(raw) > MaxTargetLen || strings.ContainsRune(raw, 0) || strings.HasPrefix(raw, "~") {
		return "", denied()
	}
	name := raw
	if filepath.IsAbs(name) {
		if !localfs.Contains(root, name) {
			return "", denied()
		}
		var err error
		name, err = filepath.Rel(root, name)
		if err != nil {
			return "", denied()
		}
	}
	name = filepath.Clean(name)
	if !filepath.IsLocal(name) || name == "." || name == ".git" || strings.HasPrefix(name, ".git"+string(filepath.Separator)) {
		return "", denied()
	}
	return name, nil
}

func Write(root, path, content string) (map[string]any, error) {
	if len(content) > MaxWriteBytes {
		return nil, agent.E("REPO_ACCESS_LIMIT")
	}
	real, err := canonicalRoot(root)
	if err != nil {
		return nil, err
	}
	name, err := writeName(real, path)
	if err != nil {
		return nil, err
	}
	tree, err := os.OpenRoot(real)
	if err != nil {
		return nil, denied()
	}
	defer tree.Close()
	// Reject symlinked parent directories as well as an existing symlink file.
	// This also prevents aliases into protected .git metadata.
	parent := ""
	for _, component := range strings.Split(filepath.Dir(name), string(filepath.Separator)) {
		parent = filepath.Join(parent, component)
		if info, e := tree.Lstat(parent); e == nil && info.Mode()&os.ModeSymlink != 0 {
			return nil, denied()
		}
	}
	if err = tree.MkdirAll(filepath.Dir(name), 0755); err != nil {
		return nil, denied()
	}
	mode := os.FileMode(0644)
	if stat, e := tree.Lstat(name); e == nil {
		if !stat.Mode().IsRegular() {
			return nil, denied()
		}
		mode = stat.Mode().Perm()
	}
	nonce := make([]byte, 12)
	if _, err = rand.Read(nonce); err != nil {
		return nil, agent.E("INTERNAL_ERROR")
	}
	temp := filepath.Join(filepath.Dir(name), ".llmtools-write-"+hex.EncodeToString(nonce))
	file, err := tree.OpenFile(temp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return nil, denied()
	}
	defer tree.Remove(temp)
	_, err = io.WriteString(file, content)
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		return nil, agent.E("REPO_WRITE_FAILED")
	}
	if err = tree.Rename(temp, name); err != nil {
		return nil, denied()
	}
	digest := sha256.Sum256([]byte(content))
	return map[string]any{"path": filepath.ToSlash(name), "bytes_written": len(content), "sha256": hex.EncodeToString(digest[:])}, nil
}

func Edit(root, path, oldText, newText string) (map[string]any, error) {
	if oldText == "" || len(oldText) > MaxWriteBytes || len(newText) > MaxWriteBytes {
		return nil, agent.E("REPO_INPUT_INVALID")
	}
	real, err := canonicalRoot(root)
	if err != nil {
		return nil, err
	}
	name, err := writeName(real, path)
	if err != nil {
		return nil, err
	}
	tree, err := os.OpenRoot(real)
	if err != nil {
		return nil, denied()
	}
	defer tree.Close()
	file, err := tree.Open(name)
	if err != nil {
		return nil, denied()
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil || !stat.Mode().IsRegular() || stat.Size() > MaxWriteBytes {
		return nil, agent.E("REPO_ACCESS_LIMIT")
	}
	body, err := io.ReadAll(io.LimitReader(file, MaxWriteBytes+1))
	if err != nil || len(body) > MaxWriteBytes {
		return nil, agent.E("REPO_ACCESS_LIMIT")
	}
	if strings.Count(string(body), oldText) != 1 {
		return nil, agent.E("REPO_EDIT_CONFLICT")
	}
	return Write(real, name, strings.Replace(string(body), oldText, newText, 1))
}
