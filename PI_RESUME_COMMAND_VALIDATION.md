# Run history and Pi resume commands

Validated 2026-10-26 on macOS with Pi 1.0.2.

- Runs displays Role / model, removing Agent from rows, task details and result metadata.
- Each row with verified Pi history displays a selectable command and Copy pi --resume. Clipboard operations preserve the command exactly and report both rejected promises and a false clipboard result. Copy never executes the command.
- The command includes --resume, --session-dir and --session with the exact session file. The installed Pi parser/session manager was exercised against a synthetic session and selected that exact file without a model call or a fork prompt.
- New runs persist the executable, session directory and reported session file. Commands retain those paths across later configuration changes. Older Pi records can resolve a matching file in the current session directory; Desktop does not rewrite their stored history.
- Only matching session headers qualify. Missing IDs/files, old OpenCode records, escaping symlinks, mismatched headers, special files and ambiguous duplicate IDs produce no copy button. Literal shell arguments safely preserve spaces, apostrophes, dollar signs and other shell syntax.

The real ai-worker Developer role completed frontend task `wrk_1581fd63befdbda5ee242459e534bc38`. Parent Codex implemented session-location persistence and command resolution, inspected the frontend, generated bindings, added clipboard-false handling and independently reran all checks.

Verification: 77 frontend tests pass; full Go tests pass; race checks pass for Desktop, Pi, service and integration packages. Real MCP subprocess tests verify persisted Pi locations through continuation/reconnection. A read-only check of the existing user state resolved commands for all 42 Pi records with session IDs, without exposing prompts/transcripts or invoking a model. Desktop and bundled MCP are rebuilt and signed; strict signature verification passes.

Restart AI Worker to load the new Runs view. Reconnect Codex MCP to record frozen session locations for subsequent tasks. Pi session files and task records are preserved.
