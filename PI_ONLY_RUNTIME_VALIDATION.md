# Pi-only runtime validation

Validated 2026-10-26 on macOS.

## Product behavior

- New application configurations default to Pi, with a durable `pi-sessions` directory under the application data directory.
- The production runtime factory constructs only Pi. OpenCode configurations and retired supervisor/control command-line entries fail without starting OpenCode.
- Desktop has no runtime selector, OpenCode agent selector, server start/stop, port, URL or OpenCode migration controls. Its runtime editor and model catalogue use Pi exclusively.
- Valid legacy OpenCode configuration remains readable for migration. Desktop prepares a disabled Pi routing draft and retains a warning without rewriting the old file merely by opening it. Explicit Pi saves remove active transport values; model selection and role exposure must be configured for Pi.
- Existing Pi settings are preserved. Historical runtime/agent/model identities remain unchanged, including the display of a historical OpenCode agent-default model.

## Worker contribution and acceptance

The actual ai-worker Developer role completed `wrk_2ed6d4ed99a26f5175fa9e5b6888ae2c` through Pi with model `magpie-openai/group/gpt6.1`. It implemented the Pi-only frontend and updated the frontend tests. Parent Codex implemented the production configuration/factory/backend changes, inspected the worker edits, corrected the historical model fallback, removed redundant named-agent status, and independently reran validation.

## Verification

- All 73 frontend tests pass after rebuilding the actual bundle.
- `go test ./...` passes. The config, Desktop, factory, Pi, service and integration packages also pass race checks.
- Integration checks execute the real production MCP binary against a provider-free Pi JSONL fixture: context/decision continuation after process restart; no replay of the original prompt; frozen model routing after configuration changes; task limit reload; live workspace revocation; cancellation from another MCP process; and explicit rejection of OpenCode configurations/retired launch arguments.
- Shared repository confinement is checked through the production Pi gateway. Historical adapter unit tests remain internal compatibility checks; the production executables have no OpenCode fallback or supervisor entry.
- The existing configured Pi MCP passes the public tool/schema probe (`.smoke/pi-only/schema.json`). Skill integration checks pass.
- Desktop and its bundled MCP are rebuilt and ad-hoc signed. Strict signature verification passes.

No provider credentials, existing Pi configuration, session history or project changes are removed. No running user runtime is terminated. Restart the desktop application and reconnect Codex MCP to load the new executables. Old OpenCode task records remain history and cannot resume through Pi.
