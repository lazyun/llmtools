# llmTools / AI Worker

[English](README.md) | [简体中文](README.zh-CN.md)

llmTools connects Codex to **Pi** through MCP. Codex owns requirements, project decisions and final acceptance. Pi executes delegated work; llmTools manages roles, authorized workspaces, sessions, limits and lifecycle state. **Pi is the only supported runtime.**

Codex sends a single `prompt`, which llmTools forwards verbatim. Completed assistant text is returned unchanged, including Markdown, whitespace and JSON-looking text. Routing, workspace grants and control state remain separate protocol metadata. Pi manages provider credentials, model configuration and conversation history; llmTools has no provider editor or credential store.

## Desktop setup

1. Install and configure Pi, including provider authentication and the models you want to use.
2. Open AI Worker. On **Runtimes**, choose the Pi executable and a durable Pi session directory, then save.
3. Click **Check Pi** to verify the executable and offline RPC protocol. This check does not invoke a model. Pi starts automatically for each task; there is no server start/stop control, endpoint or port setting.
4. On **Agents**, refresh the Pi model catalogue, choose a model for each role, enable the roles Codex may use, and save routing. Pi has no named-agent selector.
5. In **Settings → Authorized Workspaces**, authorize the project directory.
6. In **Codex Integration**, inject the delegation Skill, copy the MCP configuration into Codex, and run **Probe MCP**. Reconnect the Codex MCP connection after changing role exposure or replacing its executable.

**Check Pi** validates transport availability. **Probe MCP** validates the MCP protocol and exposed tools. Neither proves that provider credentials or a selected model can complete a real task; delegate a small request from Codex to check that separately.

## Roles

| Role | MCP tool | Access | Responsibility |
| --- | --- | --- | --- |
| Developer / 完整实现 | `delegate_developer` | Project read/write, edit and sandboxed commands | Implement, test and fix failures; Codex accepts the result |
| Implementation / 实现 | `delegate_implementation` | Repository read-only | Implementation advice and candidate changes |
| Review / 审查 | `delegate_review` | Repository read-only | Independent review |
| Research / 研究 | `delegate_research` | Context-only | Investigate supplied context |

Only exposed roles appear in MCP `tools/list`. Exposure is configuration, independent of runtime health. Missing roles remain hidden. Continuation and cancellation tools stay available for historical runs, subject to their original runtime/session being supported.

Each run freezes the logical role, Pi model, workspace and capability policy. Changing routing affects new tasks; a supported suspended Pi task continues with its original settings. Callers cannot supply capability flags or paths to enlarge a role's authority.

### Complete implementation and tests

Use Developer when the worker must actually modify the project and run tests. Ask it to return changed files, actual test commands/results and remaining issues. Its edits are already in the authorized project; Codex inspects those edits and test evidence before accepting the task.

Developer receives llmTools-owned `repo_read`, `repo_glob`, `repo_grep`, `repo_write`, `repo_edit` and `repo_exec`. Implementation and Review receive only the read tools. Research receives no repository access. Native Pi shell/write tools, user extensions, context-file discovery, web access and subagents are disabled on delegated turns.

File operations are confined to the selected workspace. File writes/edits are limited to 1 MiB each; exact edits require one matching occurrence. Outside paths, escaping symlinks and root `.git` writes are denied.

Project commands currently require macOS `sandbox-exec`; unsupported environments fail with `EXECUTION_SANDBOX_UNAVAILABLE`. Writes are confined to the project and private run scratch. System tools and installed dependency caches are readable. Outbound networking, including localhost services, is denied; tests may bind a loopback listener but cannot connect to it from sandboxed commands. Missing dependencies must be reported instead of silently falling back to unrestricted execution. Provider credentials and control tokens are not inherited by project commands.

Commands default to 120 seconds and accept 1–3600 seconds, bounded by the task deadline. Each output stream is capped at 256 KiB, with truncation reported. Nonzero exits return to the worker so it can fix failures. Commands should run in the foreground and clean up any servers they start.

One active or suspended Developer task reserves its workspace (`WORKSPACE_BUSY`). Its write, edit and command tools serialize across gateway processes. Continue or cancel a suspended task before starting another writer; avoid editing the same files concurrently in Codex or an external editor. Cancellation stops the command process group. Partial project edits remain for inspection; llmTools does not automatically roll back, commit, push or deploy.

## Configuration

See [config.example.toml](config.example.toml). A minimal Pi configuration is:

```toml
[general]
log_level = "info"
task_timeout_seconds = 1800
max_context_bytes = 1048576

[runtime]
type = "pi"
binary = "/absolute/path/to/pi"
session_dir = "/absolute/path/to/pi-sessions"

[agents.developer]
exposed = true
agent = ""
model = "provider/model"

[agents.review]
exposed = true
agent = ""
model = "provider/model"

[storage]
database = "llmtools.db"
```

Use IDs reported by **Refresh Models**, not guessed model names. Pi requires an explicit provider/model route. Its session directory must be absolute and canonical. Pi configuration has no active `mode`, `host`, `base_url` or `port` values; Desktop clears legacy transport fields when saving Pi.

New application directories default to Pi with session history under `pi-sessions/` in that data directory. The macOS default data directory is `~/Library/Application Support/AI Worker`. Existing custom Pi session locations are preserved.

### Limits

- **Task timeout seconds:** 1–3600 seconds for one delegation or continuation call, including model and tool execution. A continuation gets a fresh deadline; already executing calls retain their original deadline.
- **Maximum task / continuation bytes:** 1024–4194304 bytes for the serialized incoming request. This limits the prompt and supplied continuation data, not the model's total context window or project size.
- **Codex MCP tool timeout:** generated configuration uses 3615 seconds, covering the largest supported task deadline plus cleanup.

An existing MCP process reloads general limits at the next task/continuation when storage and runtime identity are unchanged. Runtime selection, binary updates and role exposure require a fresh MCP connection.

## Workspace authorization

Workspaces are explicit durable grants. Codex MCP Roots are not required.

| Method | Action | Audit source |
| --- | --- | --- |
| Desktop | **Settings → Authorized Workspaces → Add Workspace**, then native folder picker | `desktop_user_grant` |
| MCP | `ai_worker_add_workspace` with an absolute path and optional label | `mcp_client_grant` |

```json
{"path":"/absolute/path/to/project","label":"My project"}
```

Both methods create equivalent grants, visible and revocable in Desktop. Re-adding an active canonical directory returns its existing grant. Use `ai_worker_list_workspaces` to find IDs and `ai_worker_revoke_workspace` with `{"workspace_id":"ws_…"}` to revoke access.

Delegation selects an existing grant using `workspace_id`; it accepts no `path`, `cwd` or `workspace_root`. Developer, Implementation and Review require a grant. With one active grant it can be selected automatically; with multiple grants, specify `workspace_id`. llmTools never guesses among grants or silently substitutes a workspace. Research remains context-only even when associated with a grant.

Each repository operation checks the live grant and run state again. Revocation cancels bound work and prevents new reads, writes or commands. A missing workspace fails with `WORKSPACE_UNAVAILABLE`. Labels are display names and do not change authority. Run records store the workspace identity and revision, not repository file contents or search results.

## MCP contract and sessions

Example `delegate_developer` arguments:

```json
{
  "prompt": "Implement the requested change in this project, run the relevant tests, fix failures, and report changed files, actual commands/results and remaining issues. Codex will perform final acceptance.",
  "workspace_id": "ws_…"
}
```

The worker receives exactly that prompt and its existing Pi session history. It does not inherit the full parent Codex chat. Supply decisions and context that do not exist in the repository; let read-capable roles locate project files themselves.

Completed MCP text is the original assistant response. `structuredContent.content` holds the same text, with lifecycle metadata such as `worker_run_id`, role/model and status kept separately. Pending requests and failures use lifecycle envelopes. Runtime session IDs, control tokens and raw runtime events are not exposed as public MCP task parameters or completion text.

Within one Codex conversation, delegations share a durable Pi session using Codex's `_meta.threadId`. Each delegation still creates a separate worker run with its own routing and permissions. Earlier delegated history, including other roles/workspaces from the same conversation, may be present. Different conversations use separate Pi sessions; clients without `threadId` get isolated task sessions.

Submit delegations sequentially. An executing task or one awaiting context/decisions reserves the conversation session (`SESSION_BUSY`). Continue or cancel it before starting another task. The mapping survives MCP reconnects; the original Pi session files must remain available.

### Pending requests

- `NEEDS_CONTEXT`: obtain the requested data and call `ai_worker_continue` with the same `worker_run_id` and `additional_context`.
- `NEEDS_DECISION`: Codex decides, then calls `ai_worker_continue` with the same ID and `decision_responses`.
- A single pending response may omit `request_id`; multiple responses must use their returned `context_N` / `decision_N` IDs.
- `ai_worker_cancel` cancels an active or suspended run. It does not retry or change routing.

The continuation preserves the supported original Pi session, model, role and workspace policy. Lost history or control state fails with stable errors such as `SESSION_LOST`, `PENDING_CONTEXT_LOST` or `PENDING_DECISION_LOST`; llmTools does not silently create a substitute session.

### Exceptions and recovery

Compile/test failures can be corrected while the worker remains active. A task-level deadline becomes `FAILED` with `TIMEOUT`. A confirmed cancellation becomes `CANCELLED`; an unconfirmed abort becomes `FAILED` with `ABORT_UNCONFIRMED`.

A process restart detects orphaned running tasks, attempts to stop their old execution, and records failure. It does not automatically replay a prompt. Only `NEEDS_CONTEXT` and `NEEDS_DECISION` tasks can use `ai_worker_continue`; failed or cancelled runs cannot be reopened in place. Re-delegate after inspecting the retained project changes. A new task in the same Codex conversation can reuse surviving Pi history.

### Opening Pi history

See [resume command validation](PI_RESUME_COMMAND_VALIDATION.md) for implementation checks. Runs displays **Role / model** without the redundant Agent entry. Each task with verified Pi history shows a command and **Copy pi --resume** in its **Run / objective** cell. The copied command includes `--session` with the exact session file, so it opens that task's conversation directly; copying never executes it. Paths are quoted for the shell. New tasks retain their original executable, session directory and session file metadata. Older Pi tasks can resolve against the current session directory when a matching history file exists. Missing history and old OpenCode tasks have no copyable Pi command.

Sessions are stored in the configured `runtime.session_dir`. To browse the session picker manually, use:

```sh
pi --session-dir "/absolute/path/to/pi-sessions" --resume
```

Or open a specific JSONL file using `pi --session "/absolute/path/to/session.jsonl"`. A worker run ID is not a Pi session ID. Multiple tasks from one Codex conversation can share one picker entry. Task history and logs in Desktop are separate from Pi's own session picker.

## Codex integration and Skill management

Desktop's **Codex Integration** tab reports runtime protocol, MCP tools, role exposure, workspace policy and Skill status. It can inject, update/reinject and remove the user-scoped Skill at `~/.agents/skills/llmtools-delegation`. Removal requires the app's confirmation dialog and affects only that managed Skill folder; MCP configuration, provider credentials and session history remain intact.

The Skill is version **1.4.0**, fingerprint **llmtools-delegation-v5**. It supports implicit selection and explicit `$llmtools-delegation`. The canonical source is [SKILL.md](integrations/codex/plugins/llmtools/skills/llmtools-delegation/SKILL.md). The Skill guides role selection and same-run continuation; it grants no capabilities and leaves final acceptance with Codex. It does not guarantee that every task is delegated.

The packaged local plugin also includes the Skill and an optional disabled MCP definition:

```sh
codex plugin marketplace add /absolute/path/to/llmTools/integrations/codex
codex plugin add llmtools@llmtools-local
```

Choose one MCP registration for the running binary. The plugin launcher accepts `LLMTOOLS_MCP_BINARY` and `LLMTOOLS_CONFIG_DIR`; it does not install a Pi provider or select a model.

## Continuous integration

[GitHub Actions CI](https://github.com/lazyun/llmtools/actions/workflows/ci.yml) runs on pushes to `main`, every pull request, and manual dispatch. It uses Go 1.26.x, Node.js 22.x and Python 3.13.

- **Quality (macOS 15):** Go vet and race tests for the MCP module, Python script tests, Codex plugin validation, frontend build/tests, and vet/tests for the desktop module. The macOS runner covers the Developer command sandbox. Tests use local fixtures; no Pi installation, provider credentials or real model calls are needed.
- **Native builds:** macOS 15, Ubuntu 24.04 and Windows Server 2022 invoke the existing platform scripts with CGO enabled. Linux installs GTK 3 / WebKitGTK 4.1; Windows uses native Go and MinGW GCC in Git Bash. Linux/Windows currently validate compilation and packaging; the complete test suite runs on macOS.
- **Downloadable artifacts:** after a build succeeds, download `macos-desktop-build`, `linux-desktop-build` or `windows-desktop-build` from the run's **Artifacts** section. They contain a `.tar.gz` (macOS/Linux) or `.zip` (Windows) archive with the application, MCP and MIT license. Tar archives preserve executable permissions and the macOS bundle signature. Artifacts are retained for 7 days; CI does not publish releases.

The workflow has read-only repository permissions, pins actions to verified commit SHAs, cancels superseded runs on the same ref, and caches Go/npm dependencies using both Go module checksums and the frontend lockfile. Workflow configuration: [ci.yml](.github/workflows/ci.yml).

## Build and validation

Use the script for the operating system you are building on. Each full build regenerates Wails bindings, builds the frontend, compiles the MCP with **CGO enabled** for SQLite, builds the desktop application, and packages both together. Scripts locate the repository themselves and work when invoked from another directory.

| Build host | Command | Packaged output |
| --- | --- | --- |
| macOS | `bash scripts/build-macos.sh` | `dist/macos-<arch>/AI Worker.app` (MCP inside Contents/MacOS) |
| Linux | `bash scripts/build-linux.sh` | `dist/linux-<arch>/AI Worker` and `ai-worker-mcp` |
| Windows, Git Bash / MSYS2 | `bash scripts/build-windows.sh` | `dist/windows-<arch>/AI Worker.exe` and `ai-worker-mcp.exe` |

The scripts use the native Go host architecture (`amd64` or `arm64`). These are native builds, not CGO cross-compilation wrappers. On Windows, use native Windows Go from Git Bash/MSYS2; WSL Go builds Linux programs and is rejected by the Windows script. Keep the Linux/Windows desktop executable and its MCP sidecar together when distributing them. macOS bundles are ad-hoc signed and verified for local use; notarization/Developer ID distribution is separate.

Prerequisites for a full desktop build:

- Go **1.26+**, Node.js **22.19+** with npm, and a working native C compiler. SQLite requires CGO; a CGO-disabled source check does not produce a usable release MCP.
- Wails CLI matching `desktop/go.mod` (currently **v2.10.2**): `go install github.com/wailsapp/wails/v2/cmd/wails@v2.10.2`. The scripts check PATH and then the first GOPATH's `bin` directory; alternatively set `WAILS=/absolute/path/to/wails`.
- macOS: Xcode Command Line Tools (`xcode-select --install`), macOS signing tools and a current SDK containing UniformTypeIdentifiers.
- Linux: a C/C++ build toolchain, `pkg-config`, GTK 3 and WebKitGTK development files. On Debian/Ubuntu with WebKitGTK 4.1 these are typically `build-essential pkg-config libgtk-3-dev libwebkit2gtk-4.1-dev`. The Linux script selects the `webkit2_41` build tag when 4.1 is available and otherwise checks for 4.0.
- Windows: native Windows Go and a compatible MinGW-w64 GCC toolchain on PATH, plus Git Bash or MSYS2 Bash. Wails uses the WebView2 download strategy; end-user Windows machines need WebView2 or Internet access for its bootstrap installer.

```sh
# Reuse an already installed frontend node_modules directory:
bash scripts/build-macos.sh --skip-install

# Build only the CGO-enabled MCP; Node.js, Wails and GUI SDKs are not required:
bash scripts/build-linux.sh --mcp-only

# Show options without compiling:
bash scripts/build-windows.sh --help
```

Default builds use `npm ci`; dependencies and any needed Go modules must already be cached or reachable. The scripts do not install global toolchains, change runtime settings or launch Pi. `make desktop` selects the native script; `make desktop-macos`, `make desktop-linux` and `make desktop-windows` select one explicitly.

Cross-platform packaging does not broaden runtime permissions: Developer's sandboxed command execution currently requires macOS. On Linux/Windows it fails with `EXECUTION_SANDBOX_UNAVAILABLE`; the scripts do not bypass the sandbox.

Validation commands:

```sh
npm --prefix desktop/frontend test
go test ./...
python3 -m unittest discover -s scripts -p 'test_*.py'
```

Restart AI Worker and reconnect Codex MCP after replacing their executables.

Default tests use provider-free Pi RPC fixtures and local projects. Integration tests launch the actual production MCP to check Pi routing, process restart continuation, raw prompt preservation, frozen models, cancellation, live limit reload and workspace revocation. Legacy adapter unit tests remain internal compatibility evidence; they do not enable OpenCode in the production factory or executables. Real-provider Pi checks are opt-in and must be reported separately from mock success.

See [Pi-only runtime validation](PI_ONLY_RUNTIME_VALIDATION.md), [Developer role validation](DEVELOPER_ROLE_VALIDATION.md) and [Pi parity validation](PI_RUNTIME_PARITY_ACCEPTANCE_VALIDATION.md) for dated evidence.

## License

This project is licensed under the [MIT License](LICENSE). Third-party dependencies retain their respective licenses.
