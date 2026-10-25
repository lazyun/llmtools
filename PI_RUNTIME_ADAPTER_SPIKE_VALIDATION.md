# PI_RUNTIME_ADAPTER_SPIKE Validation

Date: 2026-09-19 (Asia/Shanghai)

## Final Status

`PI_RUNTIME_ADAPTER_SPIKE_CLOSED`

Pi runs llmTools tasks through the existing Runtime Abstraction: `internal/agent`
owns one runtime-neutral contract, `internal/service` owns one routing and
persistence path, and `internal/runtime/pi` is the second `RuntimeAdapter`
implementation. The spike reached the same Core outcomes as the production
runtime — `NEEDS_CONTEXT`, `NEEDS_DECISION`, continuation on the same durable
run and the same runtime session, real cancellation, and repository read confined
by the closed `READ_ONLY_REPOSITORY_CAPABILITY_CLOSED` authority — with a real Pi
binary, a real Pi session, and a real Pi model serving a complete Implementation
run.

§65 lists 31 success criteria; every one is satisfied, and §60's stronger
condition (at least one real Pi runtime + real model complete Implementation run,
not a fake process) is satisfied by
`wrk_5a14d974b2f7cbc29f76b945151496fc` (`--- PASS: TestPiSpikeRealImplementationReadOnly (17.96s)`).

Pi is **experimental**. OpenCode remains the only production runtime, the only
runtime in persisted configuration, and the only runtime the Desktop shows. The
spike is reachable through a process-local environment variable alone, and no
Pi-specific tool, Skill, config key, database table or UI surface was added.

Two real re-runs remain provider-limited at the moment of the final sweep: Review
and process-restart continuation each hit a `402 …depleted your monthly included
credits` response from the configured HuggingFace provider. Both had already
completed for real earlier in this round (`wrk_c90c09fa0e24e5f0a1eeccded5672ef6`
reported `status=completed`; `wrk_677467568a0da19de7a8c739ffe4d7b1` left
`.smoke/pi-spike/process-restart.json`), and the blocked re-runs are recorded as
their own artifacts rather than reported as passes. Details:
[Real Model Acceptance](#real-model-acceptance),
[Known Limitations](#known-limitations).

## Pi Environment

| Item | Evidence |
| --- | --- |
| binary | `~/.nvm/versions/node/v23.11.1/bin/pi` → `../lib/node_modules/@earendil-works/pi-coding-agent/dist/bundle/cli.js` |
| version | `pi --version` → `0.85.1`; `package.json` → `@earendil-works/pi-coding-agent` `0.85.1` |
| installation | pre-existing user install (symlink dated 2026-09-19 12:56). Nothing was installed this round: no `brew`, no `npm -g`, no curl installer, no shell profile change |
| PATH | not on the active Node v24.18.0 PATH; the harness passes an absolute path (`LLMTOOLS_TEST_PI`), and `spikeRuntime` accepts `LLMTOOLS_PI_BINARY` |
| provider/model availability | `pi --offline --list-models` returns a real catalogue; one already-configured model served runs: `huggingface/deepseek-ai/DeepSeek-V4-Flash` |
| Pi user config | read-only. `~/.pi/agent/auth.json` is `{}`; `~/.pi/agent/settings.json` is unchanged from 12:46 (`lastChangelogVersion: 0.85.1`, `theme: light`); the only `~/.pi/agent/sessions` entry predates the spike (the user's own 12:57 run) |

Measured catalogue shape, which the parser is pinned against
(`TestParseModelTableMatchesRealPiOutput`): header `provider model context
max-out thinking images`, and ids that already contain a `/`
(`huggingface` + `deepseek-ai/DeepSeek-V4-Flash`), so the provider prefix is
joined exactly once (`TestRunTurnDoesNotDoubleJoinTheProviderPrefix`).

## Integration Mode Decision

**JSON-RPC over the `pi --mode rpc` stdio protocol**, driven by a subprocess the
adapter owns. No SDK was adopted, and no HTTP helper was introduced.

Evidence from the live protocol before any code was written (§3, §6):

- One JSON record per line, framed on LF only; U+2028/U+2029 are legal inside
  JSON strings, so the reader must not split on them
  (`TestReadStdoutFramesOnLFOnly`).
- Every command carries its own `id` inside the command object and Pi echoes it
  on `{"type":"response","command":…,"success":true,"data":{…}}`
  (`TestRequestCarriesItsOwnCorrelationID`).
- A response with `success:false` is a protocol failure, mapped to a runtime
  neutral code, never surfaced raw
  (`TestRequestMapsProtocolFailureToRuntimeNeutralCode` → `RUNTIME_PROTOCOL_ERROR`).
- Turn liveness is settled by `agent_end` with `will_retry:false` plus an
  `isStreaming:false` `get_state` poll; a stream that ends unsettled is refused
  rather than reported as success (`TestObserveRefusesAnUnsettledStream`).

Rationale (§6): the RPC mode is the only Pi surface with a machine-readable
command/response correlation, an explicit abort, and a stable way to read the
final assistant text; Pi's SDK is a Node library and llmTools is Go, so an SDK
route would mean a second language runtime in the dependency graph.

The Pi-specific parts are an implementation detail of `internal/runtime/pi`
(§6): `process`, `launchArgs`, `observe`, `parseControl`, `runTurn`, the embedded
bridge. Nothing above the package gained a Pi concept (see
[Runtime Abstraction Audit](#runtime-abstraction-audit)).

## Runtime Abstraction Audit

Existing neutral parts, confirmed by reading Core rather than by assumption:
`agent.RuntimeAdapter`, `agent.AgentTask`, `agent.AgentResult`, `agent.Run`
(including the `Runtime` field), `agent.CapabilitySnapshot`,
`agent.WorkspaceSnapshot`, `agent.ContextRequest`/`ContextPending`,
`agent.DecisionRequest`/`DecisionPending`, the error-code vocabulary in
`internal/agent/types.go`, `service.RoleCapabilities`, and every durable run
record. `internal/service` stamps `run.Runtime = s.RuntimeKind()` on create,
continue, and cancel, so a Pi run is attributed without a Pi branch (§9–§11).

OpenCode-specific leaks found (all pre-existing, none created by this spike):

1. `config.Validate` (`internal/config/config.go:53`) rejects
   `runtime.type != "opencode"`, so persisted configuration cannot select a
   second runtime at all. This is why the spike is environment-selected.
2. The runtime-neutral message for `MODEL_NOT_AVAILABLE` reads *"The configured
   OpenCode model is not currently available."* (`internal/agent/types.go:177`)
   and is what a Pi run reports when its Pi model is blocked — the code is
   correct, the wording is not runtime neutral.
3. `agent.Contract` is OpenCode-flavored: it names `StructuredOutput` and tells
   the worker to *"Use the native question tool"* for parent-owned decisions.
   Pi has neither, which the adapter compensates for with `piAddendum`
   (`internal/runtime/pi/turn.go:74`) instead of editing the shared contract.
4. `Health` semantics differ per runtime (OpenCode: a managed HTTP server; Pi: a
   per-turn subprocess) and `ListAgents` has no Pi analogue, so the Pi adapter
   reports the logical llmTools roles it was given.

Genericizations actually made (§10–§11), all minimal and behavior-preserving:

- `RuntimeAdapter.RuntimeKind() string`, implemented by both adapters;
  `service.Service.RuntimeKind()` derives the stamped value instead of a
  constant, and the run/result/progress records carry it
  (`internal/service/service.go:40,51,180,515,608,735`).
- `internal/repocontrol` extracted the single authoritative repository tool
  implementation (`Call`, `Tools = repo_read|repo_glob|repo_grep`, per-access
  validation, audit lines) that the OpenCode control MCP server used to own
  inline; OpenCode now calls it, so no second copy of the security model exists.
- Host-side entry points were widened by a discriminator only:
  `pi.ControlMain(os.Args)` sits beside `opencode.HostMain(os.Args)` in
  `cmd/ai-worker-mcp/main.go:24-29`.

No forbidden type was introduced (§12–§15): there is no `PiAgentTask`, `PiRun`,
`PiDecision`, `PiContextRequest` or `PiWorkspaceSnapshot`, and no
`piSessionId`/`piThreadId`/`piRpcId`/`piEvent`/`piTool`/`piProvider` in Core's
public or domain surface. Pi's session id travels in the existing
`agent.RuntimeHandle.SessionID` and the existing `runState` JSON inside the
already-durable pending-request blob.

**Verdict: the abstraction is real, not a rename of one runtime.** A second,
structurally different runtime (subprocess + JSONL + on-disk session tree versus
managed HTTP server) was added without touching the task, result, capability,
workspace, persistence, context or decision contracts. The four leaks above are
cosmetic or config-level, and none of them blocked the spike.

## Adapter Mapping

`internal/runtime/pi/adapter.go` implements the whole interface:

| Adapter method | Pi behavior |
| --- | --- |
| `Health` | `pi --version` (cached); no model call, so runtime availability is never confused with credential availability |
| `ListAgents` | the logical roles the spec was given; Pi has no catalogue |
| `ListModels` | `pi --offline --list-models`, parsed into routing identities, 60s cache |
| `StartTask` | validate model → mint per-run control token → build launch args → spawn → `attach` (read back the session Pi actually created) → register live |
| `ContinueTask` | `json.Marshal(agent.AgentTask)` sent verbatim as the first prompt, so Pi receives the identical task contract OpenCode receives |
| `ResumeContext` / `ResumeDecision` | re-validate the durable `runState` and the pending requests, then send only the answers as the next turn |
| `CancelTask` | `{"type":"abort"}` then kill; absence of a live process is a confirmed cancellation |
| `SubscribeProgress` | reports a live Pi process dying |
| `Close` | reaps every live process unconditionally |

Selection is process-local (§14–§15): `LLMTOOLS_RUNTIME_SPIKE=pi` with
`LLMTOOLS_PI_BINARY` and `LLMTOOLS_PI_SESSION_DIR`
(`cmd/ai-worker-mcp/main.go:33-82`). An unrecognized value fails closed instead of
running the formal route under a spike label. Nothing is written to
`config.toml`, and the Desktop, MCP layer and Skill cannot select Pi at all.

One process per run is deliberate (§39): Pi's own security boundary is its
extension surface, which is fixed at launch by environment variables
(`LLMTOOLS_CONTROL_TOKEN`, `LLMTOOLS_CONTROL_DB`, `LLMTOOLS_REPO_READ`). Sharing
one process between runs would share one token and one capability snapshot. The
durable on-disk session tree — not the process — carries continuation, which is
what makes the restart spike real rather than simulated.

## Session Lifecycle and Identity

`launchArgs` pins a controlled surface on every start
(`TestLaunchArgsPinTheControlledSurface`, `TestLaunchArgsNeverCarryTheWorkspaceRoot`):

```
--mode rpc -e <materialised bridge> --tools <5 bridge tools>
--no-builtin-tools --no-context-files --no-skills --no-approve --no-extensions
--no-themes --no-prompt-templates
--session-dir <private> [--session <restored id>]
--provider <p> --model <m>
--append-system-prompt <agent.Contract + piAddendum>
```

The Pi process never runs inside a workspace and is never given the canonical
root — as an argument, in the prompt, or in its environment (§24, §28).
`TestLaunchArgsNeverCarryTheWorkspaceRoot` asserts that, and the real
Implementation run's workspace was reached only through the bridge tools.

The bridge file itself is embedded in the llmTools binary and materialised to a
private 0400 path inside a 0700 directory at start-up
(`internal/runtime/pi/embed.go`): byte-identical to the source, idempotent, no
stray copies, and an unusable directory is refused with `CONFIG_ERROR`
(`embed_test.go`). Nothing is installed into `~/.pi`.

## AgentTask and AgentResult

§16–§18: the first turn carries the runtime-neutral `agent.AgentTask`
serialization — the same bytes OpenCode is given — and it contains no
`canonical_root` (`TestContinueTaskSendsTheRuntimeNeutralTask`,
`TestStartTaskRefusesAnUnavailableModel` covers the model gate).

The final payload is Pi's last assistant text
(`get_last_assistant_text`) placed in `AgentResult.content`: Markdown and code
fences are preserved and reasoning is not leaked. A result is refused rather than
reported when it is empty, when the model identity cannot be confirmed, when the
session id or the control token appears in the answer, or when the stream ended
unsettled (`TestRunTurnRefusesUnusableResults`, ten subtests;
`assertPiResponseClean` in the real harness). The real Implementation run
returned the requested Markdown table with both workspace tokens and the literal
`WORKSPACE_ACCESS_DENIED` report of its own denied attempt.

## Events

§19: Pi events are normalized inside the adapter and never forwarded by name.
`observe` maps `agent_start`/turn boundaries to `RUNNING`,
`tool_execution_start` to `TOOL_RUNNING` and `tool_execution_end` to
`TOOL_COMPLETED`, and ignores user-message echoes
(`TestObserveNormalizesPiEvents` asserts the exact stage sequence). A tool
observation records the bridge tool's name and outcome only — never its
arguments, never file content, never the token or session id
(`TestRunTurnRecordsToolEvidenceWithoutContent`).

The real runs confirm the same sequence end to end through MCP progress
notifications, including `WAITING_FOR_CONTEXT` as the last stage of a suspending
turn.

## Control Plane

§20–§22: Pi has no question tool and no structured-output channel, so llmTools
supplies the suspension itself. `llmtools_control_request_context` and
`llmtools_control_request_decision` are registered by the embedded bridge and
return `{content, details:{llmtools_control, requests|questions}, terminate:true}`;
a `tool_result` hook additionally calls `ctx.abort()` so a batched control call
still ends the loop. The adapter reads the details off
`tool_execution_end` and produces the ordinary Core outcomes:
`*agent.ContextPending` / `*agent.DecisionPending` with a strict, versioned
`runState` (session id, control token, model) that Core already persists.

Mapping is validated without a model in `control_test.go`: only the two control
tools can suspend, a foreign `llmtools_control` marker is ignored, request bounds
(`id` 80, `target` 4096, `reason` 16384) and kinds are enforced, decision
normalization drops blank questions, duplicate ids and option-less questions
without a free-text escape, and `pendingState` refuses foreign sessions, foreign
versions, missing or wrong tokens, and unknown fields.

The system prompt is never the control mechanism (§7): the tools themselves do
not exist unless the bridge registers them, and a `tool_call` hook denies any
name outside the active set — including Pi's own native tools — independently of
the launch flags. The spike did not need to fall back to
*"please don't use those tools"*, so
`PI_RUNTIME_ADAPTER_SPIKE_CONTROL_TOOL_BLOCKED` does not apply.

## Repository Tools and Security

§23–§29: the Pi path has no read implementation of its own. The bridge shells
back into this same llmTools binary
(`pi --llmtools-pi-control <db> <tool>`, `internal/runtime/pi/host.go`), which
opens the durable store and calls the identical `repocontrol.Call` the OpenCode
control MCP server uses. Path confinement, symlink-escape refusal, per-access
live-grant validation and audit lines are all reused; there is no second
validator, registry, revoke path or reader.

`TestPiControlGatewayConfinesEveryTool` crosses a real process boundary with no
model in the loop, so the enforcement evidence does not depend on a provider:

- allowed: relative read, absolute in-workspace read, glob, grep, and the result
  stays root-relative;
- denied with `WORKSPACE_ACCESS_DENIED`: parent traversal, absolute outside path,
  unknown token, escaping glob, escaping grep — with no file content in stdout;
- `REPO_INPUT_INVALID` for an invalid regex, a missing `path`, a non-absolutely
  shaped db path, and for a `repo_write` tool name, which is refused before the
  store is consulted;
- **live revocation**: after `RevokeWorkspace`, a fresh gateway process refuses
  the same still-held token with `WORKSPACE_AUTHORIZATION_REVOKED`.

The seeded grant uses a Pi-shaped session identity, which demonstrates that the
durable validation is runtime-neutral rather than tied to one runtime's id
prefix.

Surface control, measured on a real model
(`.smoke/pi-spike/tool-surface.json`, `wrk_5255fce0cb8e6a7ddad8b1cc6448dfc6`):

```
executed_tools = { llmtools_control_repo_glob: 1, llmtools_control_repo_read: 1 }
bridge_load_line = LLMTOOLS_PI loaded repo_read=true
  tools=["llmtools_control_repo_read","llmtools_control_repo_glob","llmtools_control_repo_grep",
         "llmtools_control_request_context","llmtools_control_request_decision"]
```

The model's own inventory for that run reports no shell, no native reader, no
writer/editor, no web and no subagent. `TestToolAllowlistIsTheBridgeSurface` and
`TestEmbeddedBridgeIsTheControlledSurface` pin the same facts statically: exactly
five tool registrations, the gateway invocation by name, `if (REPO_READ)
repoTools(pi)`, the deny hook, and the absence of `exec(`, `fetch(`, `writeFile`,
`edit(`, `shell:` and `canonical_root` in the bridge. Research gets
`LLMTOOLS_REPO_READ=0` and only the two control tools
(`TestLaunchArgsPinTheControlledSurface`).

The control token is minted per run by the adapter, injected into the bridge
through the process environment, and appended by the bridge itself — the model
cannot choose it (`withToken`), and it is never written to disk, logged, or
returned (`TestRunTurnRecordsToolEvidenceWithoutContent` asserts its absence on
stderr).

## Continuation

§30–§33: continuation reuses the same `worker_run_id`, the same runtime kind, the
same Pi session id, the same model, and the frozen capability and workspace
snapshots. No parallel run row is created and no `pi.db`/`pi_runs` table exists
(§57) — Pi state lives in the existing durable pending-request blob plus Pi's own
session tree.

Real evidence, same session across turns
(`context-suspension.json` → `context-continuation.json`,
`wrk_28d5ad9e00a38750ec9e09f5ab64f4e8`, session
`01a0b89f-1a97-7569-8395-f22eb19d5228`): the turn ended `needs_context` with one
request for the release code, and after `ai_worker_continue` the **same** session
completed and reported both the parent-supplied `RELEASE-4d2f9a7c` and the
workspace token `ALPHA-7f3c9d2e`. `decision-*.json`
(`wrk_49549db83272ee233ef85c5ecbf84b8f`, session
`01a0b89f-4bb6-73ef-8419-a2eb38c1975a`) repeats the pattern for a parent-owned
API-shape decision, and a rejecting answer cancels the run through the same
cancellation contract rather than choosing for the parent.

Process restart is genuine, not simulated:
`TestPiSpikeRealProcessRestartContinuation` tears the MCP process down and dials
a fresh one, then completes on the same durable Pi session tree —
`.smoke/pi-spike/process-restart.json`, `wrk_677467568a0da19de7a8c739ffe4d7b1`,
session `01a0b87d-cfe8-7053-a7d1-91af964dfe7c`, `suspended_at=NEEDS_CONTEXT`,
`status=completed`. Continuation is therefore not restricted to a process
lifetime, and `PI_RUNTIME_ADAPTER_SPIKE_CONTINUATION_BLOCKED` does not apply. The
re-run of this scenario in the final sweep was provider-blocked and is recorded
in `process-restart-blocked.json`; the earlier completed artifact stands as the
evidence.

## Cancellation

`.smoke/pi-spike/cancellation.json`, `wrk_86c7415c900c0ec55a595bc2aabaa835`,
session `01a0b89f-6e79-703d-9641-e890d702b713`: a real turn was interrupted
mid-flight through MCP, `{"type":"abort"}` was acknowledged by that Pi process,
the process was then killed, and the run was left durably `CANCELLED` with the
runtime-neutral summary. `assertNoPiLeft` (pgrep over a 5s deadline) runs after
every fake-runtime test and after the real sweeps, so no orphan Pi process
survives an adapter, a cancelled run or a closed server (§38). A cancel against
an idle session is a confirmed no-op, not a failure.

## Real Model Acceptance

Artifacts under `.smoke/pi-spike/`, final sweep 2026-09-19 15:43–15:44
(Asia/Shanghai), route: `LLMTOOLS_RUNTIME_SPIKE=pi` with
`LLMTOOLS_TEST_PI` and `LLMTOOLS_PI_MODEL=huggingface/deepseek-ai/DeepSeek-V4-Flash`.

| Scenario | worker_run_id | Pi session | Outcome |
| --- | --- | --- | --- |
| Implementation, repo-read only | `wrk_5a14d974b2f7cbc29f76b945151496fc` | `01a0b89e-d127-7363-9aa9-827a76b23cb5` | **PASS 17.96s**, `completed`, `workspace_id=ws_adec9db67d20bfbf75d308ea1753e804`, `capabilities={repoRead:true,repoWrite:false,shell:false,web:false,subagent:false}`, audit 3 allowed / 1 denied |
| NEEDS_CONTEXT + continuation | `wrk_28d5ad9e00a38750ec9e09f5ab64f4e8` | `01a0b89f-1a97-7569-8395-f22eb19d5228` | **PASS**, `needs_context` → `completed` on the same session |
| NEEDS_DECISION + continuation | `wrk_49549db83272ee233ef85c5ecbf84b8f` | `01a0b89f-4bb6-73ef-8419-a2eb38c1975a` | **PASS**, `needs_decision` → `completed`, chosen `report(data, format)` |
| Real cancellation | `wrk_86c7415c900c0ec55a595bc2aabaa835` | `01a0b89f-6e79-703d-9641-e890d702b713` | **PASS**, durable `CANCELLED`, no orphan process |
| Tool surface (bridge only) | `wrk_5255fce0cb8e6a7ddad8b1cc6448dfc6` | `01a0b89f-87b8-7404-a1e3-8539c5940efa` | **PASS**, executed `{glob:1, read:1}` |
| Process-restart continuation | `wrk_677467568a0da19de7a8c739ffe4d7b1` | `01a0b87d-cfe8-7053-a7d1-91af964dfe7c` | **completed for real** at 15:07; the 15:44 re-run was provider-blocked (`wrk_0face6aeaa90236b48d620e81d5d0637`, `process-restart-blocked.json`) |
| Review, repo-read only | `wrk_c90c09fa0e24e5f0a1eeccded5672ef6` | `01a0b89d-7b37-70a2-85cb-37559532d8e0` | **completed for real** (`status=completed`, `agent=review`, `workspace_id=ws_13fd7c5984a03651bf369dc17deac7bc`); its JSON artifact was lost to a harness nil-pointer (fixed, see below); re-run provider-blocked (`wrk_ddf247e707d35ec1bdf7866f33e834bb`, `review-blocked.json`) |

The blocked records carry `error_code=MODEL_NOT_AVAILABLE` and the runtime-neutral
message; the provider's raw text (`402 "You have depleted your monthly included
credits…"`) stays in the operator's `LLMTOOLS_PI assistant_error` line, and Pi's
error object never becomes Core state (§40).

Harness defect found and fixed the same round: `requirePiModelUsable` dereferenced
`result.Error` for a *successful* run, so the completed Review run panicked before
writing its artifact. The helper is now nil-safe, blocked records go to a
`<name>-blocked.json` so they can never overwrite a good artifact, and the
cancellation case drains its in-flight channel before deciding whether anything
was abortable. That is why the Review row above reports a real completed run with
no JSON — stated plainly rather than dropped or back-filled.

## OpenCode Regression

- `go build ./...`, `go vet ./...`, `go test ./... -count=1`,
  `go test -race ./... -count=1` — all green, including
  `internal/runtime/opencode`, `internal/service`, `internal/store`,
  `internal/repoaccess` and the whole `internal/integration` package. The OpenCode
  suites are the same ones that closed
  `READ_ONLY_REPOSITORY_CAPABILITY_CLOSED`; the shared `repocontrol` extraction
  kept them passing without weakening an assertion, and the real-model OpenCode
  scenarios still skip by default and pass when their own gate is set.
- `make desktop-check` → exit 0 (57 frontend tests, `ok ai-worker-mcp/desktop`).
- Python: `Ran 22 tests … OK`; `Plugin validation passed`.
- `make build` → `bin/ai-worker-mcp` (16,659,266 bytes); `make desktop` → exit 0;
  `codesign --verify --deep --strict "desktop/build/bin/AI Worker.app"` → OK.
- The bundled Desktop binary still launches the MCP server with no spike
  environment, so it resolves to the OpenCode route by construction
  (`spikeRuntime` returns `nil, nil` when `LLMTOOLS_RUNTIME_SPIKE` is unset).

## Frozen Surfaces

| Surface | State |
| --- | --- |
| Desktop | Pi not exposed; zero Pi references under `desktop/`; the Runtime page still says OpenCode Managed Runtime; no Pi dropdown, selector, provider or model UI, no Install/Configure/Auth/Provider-Manager affordance |
| MCP contract | unchanged tool names and schemas; no `delegate_pi`, `pi_worker` or `pi_continue`; no new tool of any kind |
| Skill | `1.2.0` / `llmtools-delegation-v3`, byte-identical |
| Persistence | existing run storage only; no `pi.db`, no `pi_runs`, no new table; no API key, credential, large source body or opaque tool secret persisted |
| `~/.codex/config.toml` | `sha256 ee9f46b521350d1a1e83c3cbc649cc21caf89a35adef0ec65ef3761fae3657cc`, unchanged |
| AI Worker production config | `sha256 00957263484e78d9cd78a7db450746fcfca353fff819e863a82b41f2ba4f1cdb`, identical before and after the spike |
| OpenCode config/auth | untouched |
| Pi config | read-only, untouched (see [Pi Environment](#pi-environment)) |
| Scratch | confined to `/tmp/llmtools-pi-spike` and `t.TempDir()` |
| Git | `NOT INITIALIZED` — no `.git`, no init/commit/worktree/push |

## Tests

New in this spike, all model-free and part of the default suite
(60 PASS lines, package `ai-worker-mcp/internal/runtime/pi`):

- `rpc_test.go` — LF-only framing, per-command correlation ids, pending-map
  hygiene, protocol-failure mapping.
- `util_test.go` — `splitModel` (split once; rejects `""`, `deepseek`,
  `/DeepSeek`, `huggingface/`, `huggingface /x`), `parseModelTable` (routing
  identities, dedupe, header/border/bullet/count rejection, pinned against real
  0.85.1 output), control-token shape and uniqueness, `errorText`,
  `redactSecrets`, `mapAssistantError`, `mapProcessError`, `workspaceRoot`.
- `control_test.go` — suspension detection, bounds, decision normalization,
  `runState` round-trip, `pendingState` refusals.
- `turn_test.go` — allowlist exactness, launch args, workspace-root absence,
  `piAddendum`, event normalization, unsettled-stream refusal.
- `run_test.go` — full turn lifecycle against a POSIX-sh fake Pi
  (`fake_pi_test.go`) written by the operator, not installed: completion from
  final text, provider-prefix single join, ten unusable-result cases, both
  suspensions, tool evidence without content, resume routing, task serialization,
  adapter surface without a turn. Each test ends with `assertNoPiLeft`.
- `embed_test.go` — materialization is byte-identical, private, idempotent, and
  refuses bad directories; the embedded bridge's controlled surface is asserted
  statically.
- `internal/integration/pi_spike_gateway_test.go` — real child-process gateway:
  confinement, denials, invalid input, write-name refusal, live revoke.
- `internal/integration/pi_spike_real_test.go` — the seven real-runtime
  scenarios, opt-in via `LLMTOOLS_PI_SPIKE=1`; skipped by default so no CI run can
  silently claim a real Pi pass.

This satisfies §60's requirement that fake processes serve unit tests only: the
closed verdict rests on the real runs above, not on the fake.

## Known Gaps

1. `config.Validate` cannot express a second runtime, so Pi selection is
   environment-only. Formalizing Pi requires a config schema decision (and a
   migration), not an adapter change.
2. `MODEL_NOT_AVAILABLE`'s message names OpenCode, so a Pi run's user-facing text
   is wrong even though its code and routing are right.
3. `agent.Contract` is OpenCode-flavored (`StructuredOutput`, native question
   tool). `piAddendum` compensates at the adapter edge; the shared contract still
   reads as one runtime's instructions.
4. Pi exposes no agent catalogue, so `ListAgents` on the Pi adapter reports the
   logical roles it was handed rather than runtime truth.
5. `Health` means different things per runtime (managed server vs per-turn
   subprocess); a parity phase needs a runtime-neutral health definition.
6. Pi's model list is only as fresh as its own provider configuration; llmTools
   caches for 60s and cannot distinguish "no such model" from "provider not
   configured" beyond the shared code.
7. Repository access has not been observed revoking *inside* a live model-driven
   Pi turn — the model's turn is too short to straddle a revoke. Enforcement is
   proven at the shared authority across real processes
   (`TestPiControlGatewayConfinesEveryTool`), which is the layer that actually
   decides.

## Recommendation

**A. Is Pi suitable as llmTools' formal second runtime?** Functionally yes: it
reached every Core outcome through the existing abstraction with no Pi concept
above the adapter. Operationally not yet: no config-level selection, no managed
provider/credential story, no agent catalogue, and per-turn process ownership
where OpenCode has one managed server. It is a good second *adapter*, and
formalizing it is a product decision about those four items, not about the code
written here.

**B. Is the Pi adapter simpler than the OpenCode adapter?** No, and the
difference is structural rather than accidental. Pi needs an RPC client, an
embedded Node bridge, a one-shot control gateway, a model-table parser, an event
normalizer and a process reaper where OpenCode talks to a managed HTTP server it
does not own. OpenCode's simplicity comes from someone else owning the process,
the tool surface and the auth; Pi's control surface being *smaller* is what let
the spike prove the security model at all.

**C. Does Pi fit the llmTools Runtime Abstraction better?** Equally, on a
contract that is now demonstrably not a renamed OpenCode client — a genuinely
different runtime plugged in with no domain-type change, no persistence change and
no public schema change. Where it fits *worse*: suspension reporting (Pi has no
native question tool, so llmTools had to supply custom tools) and health/agents
(OpenCode's richer managed-server semantics have no Pi counterpart).

**D. Pi-specific gaps.** The seven items in [Known Gaps](#known-gaps), plus one
environment limit: the configured HuggingFace provider's monthly credits were
exhausted during the sweep, so Pi real-model acceptance is not repeatably
executable today. Pi also has no notion of llmTools' role policy, workspace
registry or control token; all of that has to be, and is, carried by llmTools.

**E. Worth proceeding to `PI_RUNTIME_PARITY_ACCEPTANCE`?** Yes, scoped as
abstraction work rather than Pi work: a config-representable runtime selection
with a backward-compatible migration, runtime-neutral wording for the
user-facing messages and contract text, a runtime-neutral `Health`/`ListAgents`
definition, and a repeatably-available model for parity runs. That ordering keeps
OpenCode production-safe throughout. Do not proceed on the current quota-limited
provider alone.

**F. Any basis for making Pi the default runtime later?** No. OpenCode's managed
server gives llmTools shared process ownership, real health semantics, an agent
catalogue and a provider/credential layer Pi does not have, and every product
surface, Skill and configuration path is built for it. This round produced no
evidence that would change the default, and per §68 F is recorded as evidence, not
as a switch.

## Reproduction

Unit, protocol, adapter, security (no model, no Pi needed):

```
go test ./internal/runtime/pi/ -count=1 -v
go test ./internal/integration -run TestPiControlGateway -count=1 -v
```

Real Pi spike (requires a pre-installed Pi and one already-configured model;
never installs or configures anything):

```
LLMTOOLS_PI_SPIKE=1 \
LLMTOOLS_TEST_PI=$HOME/.nvm/versions/node/v23.11.1/bin/pi \
LLMTOOLS_PI_MODEL=huggingface/deepseek-ai/DeepSeek-V4-Flash \
  go test ./internal/integration -run PiSpike -timeout 30m -v
```

Artifacts land in `.smoke/pi-spike/`. Provider-blocked runs skip and write
`<name>-blocked.json`.

Full regression as run this round:

```
go vet ./... && go test ./... -count=1 && go test -race ./... -count=1
make desktop-check && make build && make desktop && make plugin-check
codesign --verify --deep --strict "desktop/build/bin/AI Worker.app"
make python-test    # 22 tests
```

## Final Project State

- Runtime product default: **OpenCode**, unchanged, and the only runtime
  reachable from persisted configuration.
- `internal/runtime/pi`: experimental spike, environment-selected, fully tested
  without a model, proven with a real model on the Implementation, context,
  decision, cancellation, tool-surface, restart-continuation and (once) Review
  paths.
- Core, storage, security, MCP, Skill, Desktop and every user configuration file:
  unchanged. Git: **NOT INITIALIZED**.
- Manual Desktop acceptance: Add/Rename/Remove PASS as previously recorded by the
  user; Active-run Live Remove Access stays **DEFERRED**; no manual closure is
  claimed by this document.
