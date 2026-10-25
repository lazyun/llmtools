package store

import (
	"ai-worker-mcp/internal/agent"
	"ai-worker-mcp/internal/localfs"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
)

func (s *Store) LockWorkspaceWriter(workspaceID string) (*os.File, error) {
	key := sha256.Sum256([]byte(workspaceID))
	lock, err := localfs.Lock(filepath.Join(s.Directory, "writer-"+hex.EncodeToString(key[:])+".lock"))
	if err != nil {
		return nil, agent.E("WORKSPACE_BUSY")
	}
	return lock, nil
}

func (s *Store) ActiveWorkspaceWriter(workspaceID string) (bool, error) {
	var count int
	err := s.db.QueryRow(`SELECT count(*) FROM runs WHERE status IN ('RUNNING','NEEDS_CONTEXT','NEEDS_DECISION') AND json_extract(body,'$.workspace.workspace_id')=? AND json_extract(body,'$.capabilities.repoWrite')=1`, workspaceID).Scan(&count)
	if err != nil {
		return false, agent.E("INTERNAL_ERROR")
	}
	return count > 0, nil
}
