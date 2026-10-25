# Developer role validation

Validated on macOS with Pi 1.0.2 on 2026-10-25–26. Developer is the new `delegate_developer` role; the existing Implementation and Review roles retain repository read-only access, and Research remains context-only.

## Actual worker execution

The ai-worker Developer role modified `desktop/frontend/tests/ui.test.mjs`, added two tests for routing/integration and the frozen run policy, rebuilt the frontend and ran `npm --prefix desktop/frontend test`. It also updated the existing assertions affected by the added role and revised workspace copy. Run `wrk_e279e7a0cfaf92f0c9ea4e8ed9d06953` completed through the real Pi runtime with model `magpie-openai/group/gpt6.1`. No mock substituted for this run. Parent Codex inspected the edits and independently reran all 73 frontend tests successfully. Raw MCP evidence is in `.smoke/developer/live-worker.json`.

## Automated checks

- Full `go test ./...` passes.
- Race checks pass for service, shared repository control, project execution, repository access, Pi, OpenCode and desktop packages.
- Actual sandbox commands verify project/scratch writes, provider/control environment removal, denial of outside reads/writes and root `.git` writes, cancellation, grant revocation, output bounds and command deadlines.
- A real Go module compiles and runs tests inside the command sandbox, including a loopback listener.
- Lifecycle tests verify that suspended Developer tasks reserve their workspace, continuation preserves the write/command policy, and old read-only roles cannot resume with a Developer capability snapshot.
- Actual configured MCP schema probe passes, including exposed Developer routing and absence of caller-selectable capability/path fields. Evidence: `.smoke/developer/schema.json`.
- Skill integration tests pass; Skill Creator's validator accepts version 1.4.0 / `llmtools-delegation-v5`.
- The macOS desktop application and bundled MCP are rebuilt and ad-hoc signed; strict signature verification passes.

## Independent ai-worker review

The real ai-worker Review role completed run `wrk_a86648cae17ed9cd654ea25db9bc6a7d`. Parent Codex checked its findings and applied these changes: outbound loopback access is denied to prevent reaching unconfined local services; signals are restricted to the same sandbox; normal reaped command IDs are no longer unconditionally signalled; all mutating tools for one run serialize through a cross-process operation lock, preventing `repo_edit` from racing a formatter/test command. New tests cover a reachable local service being denied and an edit waiting for an executing command. Exact ordered permission comparison is retained because OpenCode uses last-match-wins rules; sorting those rules could widen permissions. Serialization drift was speculative, and the current protocol continuation tests pass.

The operation lock coordinates llmTools tools, not external editors. Codex should avoid editing the same project files while a Developer run works. Commands should run in the foreground and clean up any servers they start.

## Scope and limitations

Project commands require macOS `sandbox-exec`. File operations remain scoped to one authorized workspace. Command writes additionally allow private run scratch; toolchains and installed dependency caches are readable. Outbound network access, including localhost services, is denied. Tests may bind loopback listeners but cannot connect to them from sandboxed commands. Commands cannot silently fall back to unrestricted execution. Missing dependencies must be reported. Cancellation terminates the command process group; partial project edits remain for Codex to inspect. Worker completion does not imply final acceptance, Git commit, push or deployment. Real end-to-end execution was checked through Pi; OpenCode execution policy and continuation were checked through its protocol integration tests.
