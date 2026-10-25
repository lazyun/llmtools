package pi

import (
	"os"
	"path/filepath"
	"testing"
)

// fakePiScript stands in for the Pi executable in unit tests. It speaks the
// measured JSONL RPC protocol: every command object is answered with a response
// carrying the same correlation id, and a prompt turn streams the event records
// Pi really emits. Behaviour is selected through FAKE_PI_* environment values,
// which the adapter passes down because it launches the child with os.Environ().
const fakePiScript = `#!/bin/sh
[ -n "$FAKE_PI_ARGV" ] && printf '%s\n' "$@" >> "$FAKE_PI_ARGV"
if [ "$1" = "--version" ]; then echo "${FAKE_PI_VERSION:-0.85.1-fake}"; exit 0; fi
if [ "$1" = "--list-models" ] || [ "$2" = "--list-models" ]; then printf '%s\n' "$FAKE_PI_MODEL_TABLE"; exit 0; fi
session="${FAKE_PI_SESSION-pi-test-session}"
if [ "$FAKE_PI_UNIQUE_SESSIONS" = "1" ]; then
  session="pi-test-$$"
  restore=0
  for arg in "$@"; do
    if [ "$restore" = "1" ]; then session="$arg"; restore=0; fi
    if [ "$arg" = "--session" ]; then restore=1; fi
  done
fi
while IFS= read -r line; do
  [ -n "$FAKE_PI_LOG" ] && printf '%s\n' "$line" >> "$FAKE_PI_LOG"
  id=$(printf '%s' "$line" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')
  [ -n "$id" ] || continue
  case "$line" in
    *'"type":"get_state"'*)
      printf '{"type":"response","id":"%s","command":"get_state","success":true,"data":{"sessionId":"%s","isStreaming":false,"messageCount":1}}\n' "$id" "$session"
      ;;
    *'"type":"get_last_assistant_text"'*)
      printf '{"type":"response","id":"%s","command":"get_last_assistant_text","success":true,"data":{"text":"%s"}}\n' "$id" "$FAKE_PI_TEXT"
      ;;
    *'"type":"prompt"'*)
      printf '{"type":"response","id":"%s","command":"prompt","success":true,"data":{}}\n' "$id"
      if [ -n "$FAKE_PI_TOOL" ]; then
        printf '{"type":"tool_execution_start","toolName":"%s"}\n' "$FAKE_PI_TOOL"
        details="$FAKE_PI_DETAILS"
        [ -n "$details" ] || details='{}'
        printf '{"type":"tool_execution_end","toolName":"%s","result":{"details":%s}}\n' "$FAKE_PI_TOOL" "$details"
      fi
      extra=''
      if [ -n "$FAKE_PI_ERRMSG" ]; then extra="\"errorMessage\":\"$FAKE_PI_ERRMSG\","; fi
      printf '{"type":"message_end","message":{"role":"assistant",%s"provider":"%s","model":"%s","stopReason":"%s"}}\n' "$extra" "${FAKE_PI_PROVIDER-openai}" "${FAKE_PI_MODEL-gpt-5-mini}" "${FAKE_PI_STOP-stop}"
      printf '{"type":"agent_settled"}\n'
      ;;
    *'"type":"abort"'*)
      printf '{"type":"response","id":"%s","command":"abort","success":true,"data":{}}\n' "$id"
      ;;
    *)
      printf '{"type":"response","id":"%s","success":true,"data":{}}\n' "$id"
      ;;
  esac
done
`

// fakePi writes the stand-in runtime and returns its absolute path.
func fakePi(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pi")
	if e := os.WriteFile(path, []byte(fakePiScript), 0o755); e != nil {
		t.Fatal(e)
	}
	return path
}
