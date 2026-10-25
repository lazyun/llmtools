package store

import (
	"ai-worker-mcp/internal/agent"
	"ai-worker-mcp/internal/localfs"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
)

// LockConversation protects session selection and dispatch across MCP processes.
// The key is a digest, so host-supplied identifiers never become file paths.
func (s *Store) LockConversation(key string) (*os.File, error) {
	if b, e := hex.DecodeString(key); e != nil || len(b) != 32 {
		return nil, agent.E("INTERNAL_ERROR")
	}
	f, e := localfs.Lock(filepath.Join(s.Directory, "conversation-"+key+".lock"))
	if e != nil {
		return nil, agent.E("SESSION_BUSY")
	}
	return f, nil
}

// ConversationRun finds the last task that acquired the conversation's session.
// The binding lives in the run JSON, so reconnects need no in-memory registry or
// transcript copy. Failed attempts that never acquired a session are skipped.
func (s *Store) ConversationRun(key string) (agent.Run, error) {
	var body string
	e := s.db.QueryRow(`SELECT body FROM runs
		WHERE json_extract(body, '$.conversation_key') = ?
		AND coalesce(json_extract(body, '$.runtime_session_id'), '') <> ''
		ORDER BY rowid DESC LIMIT 1`, key).Scan(&body)
	if e == sql.ErrNoRows {
		return agent.Run{}, nil
	}
	var run agent.Run
	if e != nil || json.Unmarshal([]byte(body), &run) != nil {
		return run, agent.E("INTERNAL_ERROR")
	}
	return run, nil
}
