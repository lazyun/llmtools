# PI_RUNTIME_PARITY_ACCEPTANCE Validation

Round: turn the closed Pi adapter spike into a configurable, runtime-neutral,
repeatably acceptable second Runtime by repairing the seven spike gaps, without
moving the product default off OpenCode.

Spec: `PI_RUNTIME_PARITY_ACCEPTANCE` (106 sections). Date: 2026-09-19.
Supersedes nothing: `PI_RUNTIME_ADAPTER_SPIKE_VALIDATION.md` is history and was
not edited (see [Correction: the control token is durable](#correction-the-control-token-is-durable)).

Follow-up round on the same date: `PI_RUNTIME_PARITY_REAL_MODEL_RECHECK`
(52 sections), whose sole purpose was to buy the seven blocked scenario gates a
fresh provider window. Its outcome, evidence and preflight are recorded in
[Real Model Recheck](#real-model-recheck-2026-09-19-same-day) — everything below
that heading is the original acceptance round.

## Final status

**`PI_RUNTIME_PARITY_REAL_MODEL_BLOCKED`** — the implementation half of the round
is complete and every gate that does not need a servable Pi model is CLOSED with
fresh artifacts from this round; the six scenario gates are stopped by the
provider behind the selected Pi model, not by llmTools.

46 of section 101's 53 gates PASS. The 7 that do not are numbers 28-34 (real Pi
Implementation, Review, NEEDS_CONTEXT, NEEDS_DECISION, Cancellation,
process-restart, and restart-from-persisted-config continuity), all with the same
cause and the same runtime-neutral code, `UPSTREAM_UNAVAILABLE`, recorded per
scenario in `.smoke/pi-parity/*-blocked.json`.

`PI_RUNTIME_PARITY_ACCEPTANCE_CLOSED` is deliberately **not** emitted: section 102
forbids lowering the acceptance bar to buy a CLOSED verdict, and section 100
forbids automating the user's UI acceptance, so no `*_CLOSED` marker appears
anywhere in this document.

What the round did establish:

- Selection is configuration, not environment: `runtime.type` in the persisted
  `config.toml` chooses the runtime, and the production file still says
  `type = 'opencode'`.
- Nothing in Core, storage, security, MCP, the skill or the Desktop knows the
  word "pi" except the two declared boundaries (schema validation, factory).
- Both runtimes now classify the same provider boundary with the same shared
  detector, so a parent agent reads the same error regardless of which runtime
  served the run.
- Pi's real model-driven repository access works: this round's blocked
  Implementation record shows four permitted control-tool accesses
  (`llmtools_control_repo_glob` + 3 × `llmtools_control_repo_read`) before the
  provider refused the turn.
- One real model-driven live revocation on Pi completed this round with a full
  durable timeline, and OpenCode's real shared-contract acceptance was re-run
  green against the final shipped binary.

## Baseline that had to survive

| Protected fact | State at the end of the round |
| --- | --- |
| Production runtime default | `runtime.type = 'opencode'`, `mode = 'managed'`, `binary = 'opencode'`, `host = '127.0.0.1'`, `port = 0` |
| User config digests | `HASHES_UNCHANGED` for all 7 files (before and after, section 97) |
| Codex / OpenCode / Pi user configuration | no writes, no installs, no credential changes (section 48) |
| MCP contract | no tool, argument or schema change; `internal/mcp/server.go` names no runtime anywhere in its text |
| Codex skill | `integrations/codex/plugins/llmtools/skills/llmtools-delegation/SKILL.md` untouched since 2026-09-19 01:13, before this round |
| Storage schema | 6 tables (`runs`, `run_requests`, `runtime_instances`, `runtime_context_controls`, `workspaces`, `repo_access`); no new table, no migration (section 75) |
| Revoke semantics in Core / Storage / Security | unchanged; the parity round only reused them |
| Git | still NOT INITIALIZED (section 99) |
| Desktop manual acceptance | Add / Rename / Remove Access PASS, Active-run Live Remove Access DEFERRED, overall closure not claimed |

## The seven spike gaps

| # | Spike gap | Resolution | Evidence |
| --- | --- | --- | --- |
| 1 | Formal selection needed `LLMTOOLS_RUNTIME_SPIKE` | `runtime.type = "pi"` is the supported route; the override is a test/dev route and an unrecognized override fails closed | `factory.go:52-74`, `TestSpikeOverrideRemainsATestRoute`, `persisted-selection.json` (override explicitly cleared) |
| 2 | Contract wording leaked one runtime | Shared neutral contract in `internal/agent/contract.go`; per-adapter addenda; vocabulary test forbids the leaked words in every persistable message | `TestSharedContractNamesNoRuntime`, `TestErrorVocabularyIsRuntimeNeutral` |
| 3 | Provider refusal could be reported as a missing model | Four distinct buckets (section 26) with one shared text detector | `agent/upstream.go`, `TestUpstreamAndCatalogueAreDistinctCodes`, `opencode/error_mapping_test.go` |
| 4 | Agent catalogue assumed a runtime-native selector exists | `AgentCatalogueSupported()` capability bit; Core branches on it, never on a name | `types.go:317-330`, `service/runtime_capability_test.go` |
| 5 | Health conflated transport with model/provider availability | `agent.Health{Healthy, Version}` only, passive by construction | `types.go:277-288`, `TestHealthProbeNeverAsksForAGeneration` |
| 6 | Pi configuration shape was improvised | Tight Pi schema: `type`/`binary`/`session_dir` only, OpenCode transport fields rejected, absolute canonical session dir required | `config.go:77-92`, `config.go:173-200`, `TestPiConfigRejectsIncompleteOrForeignSections` |
| 7 | Parity evidence was spike evidence | New `.smoke/pi-parity/` records written by a new suite; spike directory untouched | section [Evidence](#evidence) |

## Runtime configuration

`Runtime` gained a `Type` selector and a Pi-only `SessionDir`
(`internal/config/config.go:27`), while `Default()` keeps
`Type: "opencode", Mode: "managed", Binary: "opencode", Host: "127.0.0.1"`
(`config.go:55`), so a legacy file with no `[runtime] type` key still means
OpenCode.

Validation is per-runtime (`config.go:58-93`):

- `opencode` — managed mode needs a binary and loopback-only host; external mode
  needs a loopback HTTP URL with no credentials, query or fragment.
- `pi` — `binary` and an absolute, cleaned `session_dir` are required, and
  `mode`, `host`, `base_url` and `port` must be empty or zero, because Pi is a
  per-turn process and a written-out endpoint would read like a live route that
  never starts. The decoder applies the same rule before the struct exists
  (`config.go:181-186`), so a hand-written Pi section cannot smuggle in an
  OpenCode route; values that this program's own writer emits as zeroes stay
  inert (`inertValue`).
- anything else — `CONFIG_ERROR: invalid runtime configuration`, with no
  fallback. Falling back to OpenCode would execute a task under tools nobody
  selected.

Routing accepts an omitted agent (`config.go:103-107` with `validAgent("")`),
which is what a catalogue-less runtime needs; the OpenCode JSON shape is
otherwise byte-stable (`TestOpenCodeRuntimeJSONIsUnchanged`).

Tests: `internal/config/runtime_config_test.go` —
`TestPersistedConfigExpressesPi`, `TestLegacyConfigStillSelectsOpenCode`,
`TestUnknownRuntimeFailsClosed`, `TestPiConfigRejectsIncompleteOrForeignSections`,
`TestPiRoutingMayOmitTheAgentSelector`, `TestOpenCodeRuntimeJSONIsUnchanged`.

## Runtime factory

`internal/runtime/factory/factory.go` is the only package that imports both
adapters and the only place that knows which runtimes exist.

- `New(config)` validates first, then dispatches on `c.Runtime.Type`
  (`factory.go:65-73`); the default arm returns
  `CONFIG_ERROR: unsupported runtime type`.
- `LLMTOOLS_RUNTIME_SPIKE` remains, but only as an override for a test or dev
  process: `pi` selects the Pi adapter through the same constructor with harness
  paths, any other value fails closed (`factory.go:56-64`).
- The process entry point wires the factory once
  (`cmd/ai-worker-mcp/main.go:56-63`) and Core receives an
  `agent.RuntimeAdapter`; there is no runtime name inside Core.
- Construction never launches anything: selecting OpenCode must not create a
  runtime directory, and selecting Pi resolves the binary, control DB, session
  tree and an llmTools-owned working directory under the configuration directory
  (`newPi`, `factory.go:103-130`). A missing Pi executable is
  `RUNTIME_NOT_FOUND`, never an OpenCode fallback.

Tests: `TestFactorySelectsThePersistedRuntime`,
`TestFactoryFailsClosedOnAnUnknownRuntime`,
`TestMissingPiBinaryIsNotFoundNotAnOpenCodeFallback`,
`TestSpikeOverrideRemainsATestRoute`.

## Shared agent contract and addenda

The neutral contract lives in `internal/agent/contract.go`; each adapter adds an
addendum describing only its own mechanism
(`internal/runtime/opencode/contract.go`, Pi's in `internal/runtime/pi/turn.go`).
The shared text advertises exactly the granted tools and never names a runtime,
an agent, `StructuredOutput` or a native question channel — which is what gate
9/10 pin, via a word list (`OpenCode`, `opencode`, `Pi`, `StructuredOutput`)
applied to every persistable error message and to the shared contract:
`TestContractAdvertisesOnlyTheGrantedTools`, `TestSharedContractNamesNoRuntime`,
`TestErrorVocabularyIsRuntimeNeutral`, `TestPiPromptKeepsCoreContractNeutral`,
`TestPiAddendumNamesOnlyTheBridgeTools`.

OpenCode's addendum keeps its native mechanism (its own question/context tool
plus a session permission policy). Pi's keeps llmTools' embedded bridge tools
(`llmtools_control_request_context`, `llmtools_control_request_decision`,
`llmtools_control_repo_read|glob|grep`) and its `ctx.abort()` suspension, because
Pi has neither a question tool nor a structured-output channel.

### Context-request shape: this round's runtime-neutral fix

Section 67's OpenCode real acceptance exposed a real defect that had nothing to
do with Pi: the two adapters disagreed about which `kind` values are legal, so a
correct-looking `context_request` could be rejected on one runtime and accepted
on the other.

Fixed by making one list authoritative and having both adapters enforce it
identically:

- `agent.ContextRequestKinds` = `file`, `symbol`, `contract`, `other`;
  `ContextRequestRejection` / `ContextRequestsValid` in `internal/agent`.
- OpenCode advertises the same set as an `Enum` in its MCP input schema
  (`internal/runtime/opencode/context.go`) and answers a violation with
  `CONTEXT_INPUT_INVALID` plus a payload-free `context_control_rejected
  reason=<RULE>` line on stderr; Pi's `suspension()` returns the same code for
  the same condition.
- Coverage without a model: `opencode/context_control_test.go` —
  `TestContextControlToolAdvertisesAcceptedKinds`,
  `TestContextControlToolSuspendsUntilTheParentAnswers`,
  `TestContextRequestsFollowOneShapeRule` (empty / unknown kind / missing id /
  duplicate id / missing target / missing reason), asserting validator↔diagnostic
  agreement and that no token, target or reason text reaches the log.

## Runtime-neutral errors

The vocabulary is one map (`internal/agent/types.go:205-243`) and every entry is
runtime-neutral by test. Section 26's four buckets are distinct codes:

| Boundary | Code | Message |
| --- | --- | --- |
| A · absent from the runtime's model catalogue | `MODEL_NOT_AVAILABLE` | "The configured model is not currently available in the selected runtime." |
| B · executable / managed server / transport down | `RUNTIME_UNAVAILABLE` (+ `RUNTIME_NOT_FOUND`, `RUNTIME_START_FAILED`) | "Runtime is unavailable; check runtime diagnostics." |
| C · model exists but the provider refuses (quota, auth, billing, rate limit, pre-dispatch) | `UPSTREAM_UNAVAILABLE` | "The selected model's provider refused or could not serve the request; no result was produced." |
| D · the runtime broke the protocol | `RUNTIME_PROTOCOL_ERROR` | "Runtime response or event protocol was invalid." |

`TestUpstreamAndCatalogueAreDistinctCodes` pins that A, B, C and D never collapse
into each other.

Bucket C used to be decided twice, differently. It is now one shared detector,
`agent.MentionsUpstreamBlock` (`internal/agent/upstream.go`), matching the
provider vocabulary (unauthorized, forbidden, authentication, invalid api key,
api key, provider, quota, credit, payment required, insufficient, budget, rate
limit, temporarily unavailable, pre-dispatch) and the refusal statuses
`\b(40[1-9]|429|50[0-4])\b`, with word boundaries so a model name or a byte count
cannot read as a status code. Pi's three local copies were deleted and its call
sites (`internal/runtime/pi/util.go`, `internal/runtime/pi/rpc.go`) now call the
shared function. OpenCode previously reported *every* assistant-turn error as
`RUNTIME_UNAVAILABLE`, i.e. it mislabelled bucket C as bucket B;
`messageError` in `internal/runtime/opencode/execution.go` now classifies the
message's `name` plus `data.message` through the same detector.

`TestUpstreamBlockDetectionIsShared` requires both runtimes to agree: four
provider-refusal texts match, and `connection refused`, `session lost`,
`no assistant message`, `MessageAbortedError` do not. Case coverage:
`opencode/error_mapping_test.go` (7 cases),
`pi/util_test.go:TestMapAssistantErrorUsesTheRuntimeNeutralVocabulary`,
`pi/rpc_test.go:TestRequestMapsProtocolFailureToRuntimeNeutralCode`, and
`service_test.go`'s `provider_error` scenario, which now expects
`UPSTREAM_UNAVAILABLE` for a partial run on a listed model.

Section 27 holds: raw provider text goes only to operator diagnostics and the
redacting log path (`redactSecrets`, bounded stderr tails). The Core state a
parent agent sees is the neutral message above — the tests assert the provider's
words are absent from it.

## Health semantics

Formal definition (`types.go:277-284`): health says the transport and control
plane can accept a task. It is always passive — no model call, no provider
request, no produced content — and says nothing about generation quality.
Runtime-specific instance metadata (endpoint, port, pid, generation, ownership
for a managed server; binary, version, process model for a subprocess runtime) is
diagnostics the owning adapter or the Desktop projection exposes separately; it is
not health and Core never depends on it.

- OpenCode: `GET /global/health`; a response that is unhealthy or versionless is
  `RUNTIME_PROTOCOL_ERROR` (`opencode/client.go:93-102`).
- Pi: `pi --version` plus one RPC-only handshake — a single `get_state` round
  trip against a process launched `--offline --no-session --no-tools
  --no-builtin-tools --no-extensions --no-context-files --no-skills
  --no-prompt-templates --no-themes --no-approve`
  (`pi/adapter.go:96-153`), result cached with a TTL, zero provider traffic, and
  no server metadata invented: the returned `agent.Health` carries `Healthy` and
  the observed version, nothing else.
  `TestHealthProbeNeverAsksForAGeneration` fails the test binary if the probe ever
  sends a prompt, and section 29's no-usage property follows from the flags.
- Passive diagnostics never probe a provider (section 45); a real provider probe
  exists only inside the explicit acceptance suites behind
  `LLMTOOLS_REAL_E2E` / `LLMTOOLS_PI_PARITY`.

## Agent catalogue, roles and routing

`RuntimeAdapter.AgentCatalogueSupported()` is the capability bit Core branches on
— at catalogue validation (`service.go:156`) and route validity (`service.go:361`)
— and nowhere does Core compare a runtime name.

- OpenCode: `true`, with `GET /agent` enumeration and name validation
  (`client.go:103-113`). Its routing agents (`build`, `review`, …) remain real.
- Pi: `false`, `ListAgents` returns no entries and no error
  (`pi/adapter.go:155-159`), because Pi has no runtime-native selector. llmTools'
  logical roles are routing identities in configuration; they are never reported
  as Pi agents, and Pi runs record an empty `runtime_agent`
  (`internal/service/runtime_capability_test.go`,
  `TestCataloguelessRuntimeRoutesWithoutAnAgentSelector`, which asserts zero
  catalogue calls, no `AGENT_NOT_FOUND`, a completed run, a stored empty agent and
  the `implementation` logical role preserved).
- Role routing therefore stays orthogonal to the catalogue: a Pi route may omit
  `agent`, and an explicit model still comes from the role's routing, not from an
  agent selector.

## Model catalogue and availability

Model catalogue (`ListModels`) and execution availability (a real turn) are
separate facts. Pi enumerates its own catalogue with
`pi --offline --list-models` parsed into routing identities
(`pi/adapter.go:162-…`, `TestParseModelTable*`); an empty enumeration is a
discovery failure, not "no models". Providers and credentials stay Pi's: llmTools
never installs, stores or edits them (section 48, section 80).

The catalogue/execution split is demonstrated on both sides by real evidence:
`persisted-selection.json` (model absent from Pi's catalogue →
`MODEL_NOT_AVAILABLE`, no run created) and the six `*-blocked.json` records
(model present, provider refused → `UPSTREAM_UNAVAILABLE`).

## Persistence

Runs keep runtime-neutral fields: `Runtime` (a string kind),
`RuntimeSessionID`, `RuntimeAgent`, `RuntimeModel`, `Capabilities`, plus the
continuation states. `agent.RuntimeHandle` never appears in the MCP contract, and
a handle is rebuilt from durable state on continuation (`service.go:464`). No
Pi-specific table, column or migration was added, and OpenCode's stored runs are
read unchanged (section 39's backward compatibility is asserted by the model-free
service/desktop suites that keep replaying old bodies).

### Correction: the control token is durable

The spike document states that the per-run control token "is never written to
disk, logged, or returned"
(`PI_RUNTIME_ADAPTER_SPIKE_VALIDATION.md`, *Repository Tools and Security*).
**That is inaccurate as written, and the parity round verified the real
behavior.** `service.go:206` assigns `run.RuntimeControl = h.ControlToken` and
`Store.Save` marshals the whole run into `runs.body`, so the token *is* persisted
in the local control DB — deliberately: `ContinueTask` rebuilds the handle from
that row (`service.go:464`) and the Pi bridge validates the token against the
registered session, which is what makes cross-process continuation (§58/§59)
possible at all.

What *is* true, and what the sentence should have said:

- the token never appears on stderr, in an audit event, in an MCP response, in
  Pi's session content, or in this round's evidence records
  (`TestRunTurnRecordsToolEvidenceWithoutContent`, plus the section 92 field
  audit below);
- the model cannot choose it — the adapter mints it and the bridge appends it;
- the Desktop projection blanks it before a run leaves the service
  (`internal/desktop/service.go:137`);
- the control DB itself is `0600` (`internal/store/store.go:43,53`), so the token
  is durable but confined to the owning user's file.

Recording this here rather than editing the spike file keeps the correction
forward-looking without rewriting history.

## Pi real availability (section 49)

Model: `huggingface/deepseek-ai/DeepSeek-V4-Flash`, selected because Pi already
serves it and llmTools configured nothing (`pi auth` was never run).

Gate: at most three bounded real turns, at least two completions required, cached
for one binary run, and `availability.json` rewritten on every invocation so a
round cannot quietly inherit an older verdict.

This round the gate passed **twice, independently**:

| Probe | Started (UTC) | Result |
| --- | --- | --- |
| 1 | 10:14:07 → 10:14:19 | 3/3 completed |
| 2 | 11:55:30 → 11:55:42 | 3/3 completed |

`.smoke/pi-parity/availability.json` currently holds probe 2 with sentinel
`PI_RUNTIME_PARITY_REAL_MODEL_AVAILABLE`; probe 1's record is preserved at
`/tmp/pi-parity-evidence-1012/availability.json`. Section 51 is respected: two
probe passes are reported as a repeatable *gate*, not as proof that the round's
six scenarios are repeatable — see the next section.

## Pi scenario results

All eight scenario tests ran against the final shipped binary on the persisted
`runtime.type = "pi"` route with `LLMTOOLS_RUNTIME_SPIKE`,
`LLMTOOLS_TEST_PI`-style path overrides and `LLMTOOLS_PI_SESSION_DIR` cleared in
the child environment, from an isolated temp config/data root only (section 98).

| Gate | Scenario | Record | Outcome |
| --- | --- | --- | --- |
| 28 | Implementation | `implementation-blocked.json` · `wrk_0a1c25eec89bbb37dace61572f7ced7c` | BLOCKED `UPSTREAM_UNAVAILABLE` after 4 permitted control-tool accesses (`repo_glob` + 3 × `repo_read`) |
| 29 | Review | `review-blocked.json` · `wrk_d2587f81944dbecbb782586df6e9a6e1` | BLOCKED `UPSTREAM_UNAVAILABLE` |
| 30 | NEEDS_CONTEXT | `context-blocked.json` · `wrk_ec7139ba830e8cad773760d18baaebcc` | BLOCKED `UPSTREAM_UNAVAILABLE` |
| 31 | NEEDS_DECISION | `decision-blocked.json` · `wrk_9fad3cbac2f62e15a9287769654c54fb` | BLOCKED `UPSTREAM_UNAVAILABLE` |
| 32 | Cancellation | `cancellation-blocked.json` · `wrk_55110d9e45dac5fd18b517ab156551bd` | BLOCKED `UPSTREAM_UNAVAILABLE` before a tool stage was reachable to cancel |
| 33/34 | Process restart | `process-restart-blocked.json` · `wrk_e0e0b86e8c553b39e664410d0f9c9593` | BLOCKED `UPSTREAM_UNAVAILABLE`; the restart leg never got a suspended run to restore |
| 35/36 | Live model-driven revoke | `live-revoke.json` · `wrk_b5d3a3bd392a4e2b54888fc28eec61ec` | **PASS** (see below) |
| — | Persisted selection | `persisted-selection.json` | **PASS**: `runtime.type = "pi"` alone selected Pi, unlisted model → `MODEL_NOT_AVAILABLE`, neutral wording, no run row |

The six blocks are the same provider boundary: the model completes a short probe
turn and refuses the longer, tool-using scenario turns (billing/quota wording
surfaced as `UPSTREAM_UNAVAILABLE`). Every blocked record names its runtime,
model, logical role, workspace and run identity, so the refusal is readable
without the harness. No record was upgraded, and none of the six was reported as
a pass.

Cancellation therefore remains unproven *on Pi's real model path* this round even
though the durable cancel contract itself is unchanged and model-free covered
(`service_test.go:TestCancellationAndProgress`,
`TestExplicitCancelAndRecovery`,
`TestExplicitCancellationAcrossServicesWhileExecuting`, and the spike round's real
Pi abort). Process-restart continuity likewise keeps its spike-round evidence and
this round's persisted-configuration proof, but not a this-round real
restart-with-model artifact.

### Pi live model-driven revocation — PASS

`live-revoke.json`, `wrk_b5d3a3bd392a4e2b54888fc28eec61ec`, session
`01a0b927-c8cb-70be-aa98-ab18d5d861f8`, workspace
`ws_88897dc33f9eb2b6c6b5efecaec7a6f1`, capability snapshot `repoRead: true` and
nothing else, at 2026-09-19T10:13:10Z:

| Step | Observed |
| --- | --- |
| T1 first repository operation, allowed | `repo_access_allowed … tool=repo_glob target="src/**" count=3` |
| T2 revoke invoked | `10:13:10.202391Z` |
| T3 durable commit | `10:13:10.202551Z`, `workspace_state: REVOKED`, `grant_revision: 2` |
| T4/T5 operations after the commit | `repo_access_denied 0`, `repo_access_allowed 0`, in-flight allowed `0` |
| T6 terminal state | run `FAILED`, `WORKSPACE_AUTHORIZATION_REVOKED` |

The model drove the access (a real `llmtools_control_repo_glob` execution, not a
suspension standing in for it — section 62), no payload was returned after the
revocation, and the security implementation was not modified to obtain it (section
63). One disclosure: this record predates the `T5_refusal_layer` field, so it does
not name which layer refused first; this round's OpenCode run does
(`runtime_abort`), and the Pi record's zero counters are the assertion that
matters.

## Pi tool surface and safety

Unchanged from the spike and re-asserted against final source: the Pi process sees
`llmtools_control_repo_read|glob|grep` (read-scoped roles only),
`llmtools_control_request_context` and `llmtools_control_request_decision`, plus
Pi's read-only built-ins; no write, edit, shell, web/fetch or subagent tool
reaches the model, the workspace root is never an argument, and the deny hook
re-confines every access against the run's live grant.
`TestToolAllowlistIsTheBridgeSurface`, `TestLaunchArgsPinTheControlledSurface`,
`TestLaunchArgsNeverCarryTheWorkspaceRoot`,
`TestEmbeddedBridgeIsTheControlledSurface`,
`TestPiSpikeRealToolSurfaceIsTheBridgeOnly`, and `internal/repoaccess`'s 13
confinement tests (absolute paths, traversal, sibling-prefix collision, symlinked
targets and symlink swaps, escaping glob/grep patterns, oversized and binary
reads). After the suite, `pgrep -fl "pi-coding-agent|opencode"` found no process:
no orphan Pi process (gate 42), backed by
`TestKillTerminatesTheWholeProcessGroup` and the cancellation test's own
`piProcesses` assertion in its pass path.

## OpenCode regression

Model-free (section 66): the full main-module and desktop-module suites, unchanged
mock transport, plus the new context-control and error-mapping tests. 11 packages
`ok` without `-race` and 11 `ok` with `-race`.

Real shared-contract acceptance (sections 65/67), re-run at 19:42-19:45 local
against the final source and the freshly built `bin/ai-worker-mcp`, all five
PASS in 209.5s:

| Scenario | Run | Result |
| --- | --- | --- |
| Implementation code chain | `wrk_340382b18112f58692552ea1ea508829` · `ses_f46868178ffeswpF4MZuTrqZ8P` | COMPLETED, 5545 chars, `repo_glob`/`repo_grep`/`repo_read` through the control tools |
| Review code chain | `wrk_bd05607dffe589c9c3cb5038c4140da9` · `ses_f4685052effey4JKzZimjAMhAO` | COMPLETED, 5630 chars, read-only |
| NEEDS_CONTEXT → answer → continue | `wrk_ea070c86003b19d11d075c2c205b5bbe` | COMPLETED with the injected release code |
| NEEDS_DECISION → answer → continue | `wrk_7c2396eb98725da786e42286782fe555` · `ses_f4683a41bffeqJKePTASAw2C2a` | COMPLETED after the offered selection was answered |
| Live model-driven revoke | `wrk_1415d3f29f5c7edea3b710c5e94e9f47` | FAILED `WORKSPACE_AUTHORIZATION_REVOKED`, `T5_refusal_layer: runtime_abort`, 0 accesses and 0 payloads after the durable commit |

The route used is the same managed OpenCode route as production
(`agent: build`, `omni_router/gpt-5.6-sol` for Implementation and
`omni_router/gpt-5.6-terra` for Review), from an isolated copy of the user's
config — the user's file itself was never written (section 68). OpenCode health
semantics did not regress (section 69): the only OpenCode error-path change is
bucket C classification, whose abort path stays `RUNTIME_UNAVAILABLE`.

Evidence: `.smoke/read-only-repo/real-*-e2e.jsonl` (appending files; the
`11:42Z`-`11:45Z` lines are this run), with the earlier 18:2x local records kept
at `/tmp/opencode-real-evidence-18xx/`.

## Desktop

Section 40 is honored: **no Pi selector exists** — the Settings page still edits
the managed OpenCode route and nothing offers Pi as a choice. Section 41/§73/§74
are the part that had to be proven: a Pi configuration must survive the Desktop
read-only, and every write path must refuse it.
`internal/desktop/pi_runtime_test.go:TestDesktopKeepsAnUnmanagedRuntimeConfigReadOnly`
asserts that with a Pi config the service (a) loads it faithfully, (b) reports no
OpenCode ownership, health or instance metadata
(`internal/desktop/service.go:161-166` adds the single warning
"Desktop manages the OpenCode runtime only; this configuration is read-only
here."), (c) refuses `SaveRuntime`, `SaveSettings` and `SaveRoutings`, and (d)
emits no `Runtime connection` / `Runtime health` / `Agent discovery` check that
would observe the OpenCode server for an unmanaged runtime
(`service.go:488-494`).

The formal OpenCode path is unchanged: no file under `desktop/frontend/src` was
modified this round, and `npm run build` + 57 DOM tests + `tsc --noEmit` pass,
including the Remove Access modal cases. `alert(` / `prompt(` / `confirm(` remain
at 0 occurrences in production frontend source.

Manual Desktop acceptance stays exactly where the previous round left it, with no
automated UI acceptance performed (section 100): Workspace Add PASS, Rename PASS,
Remove Access PASS, Active-run Live Remove Access **DEFERRED**, overall manual
closure not claimed.

## Frozen surfaces

MCP contract: no new tool, argument or schema change, and no runtime name in
any server-facing text (`internal/mcp/server.go` contains none), asserted model-free by
`TestRoleToolSchemaIsSharedSimpleAndHasNoAgent` and
`TestWorkspaceSelectionSchemaAndInstructions`, and by the Desktop diagnostics
`MCP Tool Surface` check that enumerates the exposed tools from configuration.
Skill: unchanged, and it still never names a runtime. Codex integration: unchanged.
Provider/credential ownership: Pi's, untouched; no install, no auth write, no
`pi auth` invocation. The Pi bridge stays an adapter implementation detail
(section 81), embedded in the MCP binary and materialized into the configuration
directory (`TestMaterializeExtension*`).
Tool-token lifecycle (sections 83/84): minted per run, registered
`ready → pending → answered → delivered`/`cancelled`, and after a revocation the
still-held token yields `WORKSPACE_AUTHORIZATION_REVOKED` rather than a payload —
observed in both runtimes' live-revoke records.

## Evidence

Section 91: this round's artifacts are in `.smoke/pi-parity/` (10 files) and the
OpenCode acceptance lines in `.smoke/read-only-repo/`. `.smoke/pi-spike/` was not
written to; its mtime and its 10 files are still the spike round's.

Section 92 minimum fields, per Pi record:

`timestamp`, `runtime_kind`, `worker_run_id`, `runtime_session_id`, `role`,
`model`, `workspace_id`, `capability_snapshot`, `tool_calls`, `status`,
`error_code` — plus `selection_source`, `repo_access_allowed`/`denied`,
`run_identity` and, where the scenario has them, `timeline` steps. Never: an API
key, a credential, a control token, or repository file content; `final_response`
is kept only for runs that produced one, and only the scenario's own answer text.

Three field-completeness points, disclosed rather than smoothed over:

- `run_identity` was added this round because a refusal that lands before the run
  row is readable used to leave `role`/`workspace_id` empty. Each record now says
  whether its identity came from the durable run, from a run row that was refused
  before it could be read back, or from the harness that addressed the call
  (`record()` in `internal/integration/pi_parity_real_test.go`).
- `capability_snapshot` is what the run actually held. For the three records whose
  run row was never readable (`cancellation`, `live-revoke-blocked`,
  `persisted-selection`) it is all-false, which is the truth about an
  unreadable row and not a claim that the role is capability-free; `run_identity`
  is the qualifier.
- `live-revoke-blocked.json` is an extra record from the second probe cycle: the
  live-revoke scenario also hit the provider boundary that time, so this round
  has one genuine Pi revoke (10:13Z) and one blocked retry (11:55Z). Both are
  reported.

## Tests

Section 94, all against the final source (last production edit 19:29 local):

| Gate | Command | Result |
| --- | --- | --- |
| Main module | `go vet ./...` | clean (19:36) |
| Main module | `go test ./... -count=1` | 11 packages `ok` (`/tmp/sweep2_main.log`, 19:32) |
| Main module | `go test -race ./... -count=1` | 11 packages `ok`, no `DATA RACE` (`/tmp/race_final_main.log`, 19:35) |
| Desktop module | `go vet ./...` / `go test ./... -count=1` / `go test -race ./... -count=1` | 0 / 0 / 0 |
| Frontend | `tsc --noEmit`, `vite build` (via `npm run build`), DOM tests | clean / clean / 57 pass, 0 fail |
| Python | `python3 -m unittest discover -s scripts -p 'test_*.py'` | `OK` (22 tests) |
| Plugin | `python3 scripts/validate_codex_plugin.py` | `Plugin validation passed` |
| Pi persisted config (real Pi, no provider turn) | `go test ./internal/integration -run TestPiParityPersistedConfigSelectsPi` | PASS |
| Pi parity suite (real Pi) | `go test ./internal/integration -run TestPiParity` | probe PASS; 6 scenarios + revoke retry SKIP under `PI_RUNTIME_PARITY_REAL_MODEL_BLOCKED` |
| OpenCode real acceptance | `LLMTOOLS_REAL_E2E=1 go test ./internal/integration -run 'TestReal…'` | 5/5 PASS, 209.5s |

`gofmt -l internal cmd` is empty. Test-only changes after the last race run were
confined to `internal/integration/pi_parity_real_test.go` (evidence fields), which
compiles under `go vet` and was exercised by the two Pi runs above; no production
file changed after the 19:35 race sweep or after the 19:36 build.

## Build, codesign, disk, config protection

- `make build` → `bin/ai-worker-mcp`, exit 0.
- `make desktop` → Wails bundle rebuilt, MCP binary copied into the app,
  ad-hoc re-signed, exit 0.
- Bundled MCP verified: the app's `Contents/MacOS/ai-worker-mcp` and a fresh
  `make build` output carry identical Go build info (same module, deps,
  `-trimpath`, `CGO_ENABLED=1`, `GOARCH=arm64`) and, with signatures stripped,
  identical size (16,564,800 bytes) with only the `__LINKEDIT` segment length
  differing — i.e. the same build product, differing only in its embedded code
  signature extent.
- `codesign --verify --deep --strict "desktop/build/bin/AI Worker.app"` → PASS.
- Section 97: `/tmp/pi_parity_hashes.sh` over the 7 user configuration files
  (`.codex/config.toml`, AI Worker `config.toml`, OpenCode config + auth, Pi
  auth + settings + models-store) diffed before/after → `HASHES_UNCHANGED`;
  the production `[runtime]` block still reads `type = 'opencode'`.
- Section 95: disk 99% full throughout — 2.6Gi free when the sweep opened, 2.1Gi
  free with the final race sweep, both real acceptance suites and both builds
  done. No command ever failed for lack of space, so section 95's condition for
  freeing it was never met and **no cleanup was performed**: the 3.0G Go build
  cache, `bin/`, `desktop/build/`, every `.smoke/` record and every user file are
  untouched. The previous round's errno 28 did not recur. The margin is thin
  enough that the next full `-race` + Wails cycle should free the Go build cache
  (`go clean -cache`) first, which is regenerable and inside what section 95
  permits. Scratch
  copies this session made (`/tmp/pi-parity-evidence-1012/`,
  `/tmp/opencode-real-evidence-18xx/`, `/tmp/mcp_resign`, `/tmp/sigcmp/`) were
  deleted after use, except the two evidence backups, which are retained because
  they preserve superseded run evidence.
- Section 99: `git` was never invoked; `.git` still does not exist in either
  module.

## Runtime abstraction verdict

The abstraction held. Persisted Pi selection was expressible without weakening
OpenCode: the runtime name exists only in the config schema and the factory, Core
branches on capabilities rather than identities, one neutral contract with two
addenda replaced one contract with an OpenCode dialect baked in, and the last
classification asymmetry between the adapters collapsed into a shared detector
that both now depend on. Every place the round initially got this wrong was caught
by a test, not by a manual read: the mock transport had been hiding a
catalogue-less route (`"agent": "fixture"` hardcoded against the client's
`info.Agent != h.Agent` check), and OpenCode's error path had been flattening
bucket C into bucket B.

Gates 21, 24, 25, 26 and 43-53 — the ones that measure whether the abstraction is
runtime-neutral *and* whether OpenCode still works — are all CLOSED with fresh
artifacts. The seven open gates measure Pi's provider, not the abstraction.

## Remaining gaps

1. Six Pi scenario passes (Implementation, Review, NEEDS_CONTEXT,
   NEEDS_DECISION, Cancellation, process-restart continuation) still date from the
   spike round: this round's runs of those scenarios produced blocked records, not
   passes, because the provider refuses the longer Pi turns. Section 90's repeatability claim therefore holds for the availability
   gate (2/2 probe passes) and for everything model-free, but not for the Pi
   scenario suite.
2. Pi's real cancellation and real cross-process restart are unproven on this
   round's configuration path; the durable contracts behind them are unchanged and
   model-free covered.
3. `live-revoke.json` (Pi) predates the `T5_refusal_layer` field, so that record
   does not name the refusing layer; the field exists in the harness and this
   round's OpenCode record carries it.
4. Blocked Pi records carry an all-false `capability_snapshot` when their run row
   was never readable, qualified by `run_identity` rather than filled in.
5. Pi remains invisible to the Desktop by design (section 40), so a Pi
   configuration is only editable by hand; whether to build that UX is a product
   decision, recorded under Recommendation B.
6. Disk sits at 99% / 2.1Gi free at the end of this round. No cleanup was
   warranted (nothing failed for space), but the next full `-race` + Wails cycle is
   close to the margin and should start with `go clean -cache`.

## Recommendation

**A. Is Pi ready as a formal second runtime?** Technically yes on every axis this
round could control: persisted configuration selection, neutral contract, neutral
errors, passive health, honest catalogue reporting, unchanged security boundary, no
orphan processes, and a real model-driven live revocation. The qualifier is
operational, not architectural — one provider behind one model refused the longer
turns twice, so Pi's real scenario suite is not yet repeatably green. Recommend
treating Pi as a *supported second backend pending a repeatability rerun*, and
re-running `TestPiParity` against a servable model (or a less throttled route)
before claiming scenario-level parity.

**B. Proceed to `PI_RUNTIME_PRODUCTIZATION` (Desktop selector, configuration UX,
user-facing Pi diagnostics)?** Not yet, and not for UI reasons: a selector is only
worth building once the second runtime has a green scenario suite and someone has
decided what a Pi run should look like in Diagnostics. The read-only guard
(`TestDesktopKeepsAnUnmanagedRuntimeConfigReadOnly`) is the right amount of Desktop
support for now.

**C. Long-term coexistence of Pi and OpenCode?** Yes — the current design is
already coexistence-shaped: one factory, capability-gated Core branches, one
evidence vocabulary per boundary, and neither runtime's fields leaking into the
other's configuration. Coexistence costs the round only what it cost here: shared
detectors and shared contracts in place of per-runtime duplication.

**D. Any evidence for changing the default?** No. `default runtime = opencode`
must hold and does hold: every OpenCode gate passed against the final binary, the
production config digest is unchanged, and Pi has seven open scenario gates.
Switching the default would be an unearned judgment on a provider-side block and
is explicitly out of scope for this round (section 103).

## Reproduction

```sh
# model-free gates (main + desktop modules)
go vet ./... && go test ./... -count=1 && go test -race ./... -count=1
cd desktop && go vet ./... && go test ./... -count=1 && go test -race ./... -count=1
cd desktop/frontend && npm run build && npm test
python3 -m unittest discover -s scripts -p 'test_*.py'
python3 scripts/validate_codex_plugin.py

# build + signature + config protection
make build && make desktop
codesign --verify --deep --strict "desktop/build/bin/AI Worker.app"
/tmp/pi_parity_hashes.sh   # diff before/after; expect HASHES_UNCHANGED

# real OpenCode acceptance (isolated config copy, real managed route)
LLMTOOLS_TEST_OPENCODE=$(command -v opencode) AI_WORKER_MCP_BINARY=$PWD/bin/ai-worker-mcp \
  LLMTOOLS_REAL_E2E=1 go test ./internal/integration -timeout 45m -v \
  -run 'TestRealImplementationCodeChainE2E|TestRealReviewCodeChainE2E|TestRealOpenCodeContextContinuationE2E|TestRealOpenCodeDecisionContinuationE2E|TestRealModelLiveRevocation'

# real Pi parity (isolated temp config/data root; rewrites .smoke/pi-parity)
LLMTOOLS_PI_PARITY=1 LLMTOOLS_TEST_PI="$HOME/.nvm/versions/node/v23.11.1/bin/pi" \
  LLMTOOLS_PI_MODEL=huggingface/deepseek-ai/DeepSeek-V4-Flash \
  AI_WORKER_MCP_BINARY=$PWD/bin/ai-worker-mcp \
  go test ./internal/integration -timeout 45m -v -run TestPiParity
```

## Real Model Recheck (2026-09-19, same day)

Round: `PI_RUNTIME_PARITY_REAL_MODEL_RECHECK` (52 sections). One job: buy the
seven blocked scenario gates a fresh provider window, touching nothing else. The
sections above stay as the original acceptance round's record, including its
46 PASS / 7 BLOCKED ledger and its blocker evidence; nothing here rewrites them.

**Outcome: `PI_RUNTIME_PARITY_REAL_MODEL_BLOCKED`, ledger unchanged at 46 PASS /
7 BLOCKED (gates 28-34).** The Pi provider boundary exhausted its monthly
included credits between the availability gate and the first scenario, and the
one other already-configured route refused at the same boundary. No product
defect surfaced, so `PI_RUNTIME_PARITY_PRODUCT_DEFECT` was not emitted, and no
harness file was edited, so no `HARNESS_DEFECT` applies.

### Starting ledger read (section 2)

Mapping was taken from the acceptance spec's own 53-gate list rather than
assumed: 27 = real Pi availability (PASS), **28 Implementation, 29 Review,
30 NEEDS_CONTEXT, 31 NEEDS_DECISION, 32 Cancellation, 33 process-restart
continuation, 34 restart-from-persisted-config** (the seven BLOCKED), and
35/36 = real model-driven live revoke plus no-payload-after-revoke (PASS with a
genuine artifact). Gates 35/36 were therefore **not** re-run: section 46 requires
this-round artifacts only for currently-blocked gates, and section 25 says a
valid PASS is not re-burned for form. Every other gate was already PASS on
this-round-or-model-free evidence.

### Disk preflight (section 8)

The APFS data volume was at 100% capacity with **1.7Gi free** — below the margin
the acceptance round itself flagged. The one sanctioned action was run:
`go clean -cache`, which reclaimed the 3.8Gi Go build cache (3.8Gi → 12Ki) and
brought the volume to **5.6Gi free** (4.7Gi at the end of this round after two
builds and three `-race` sweeps). No `.smoke/` evidence, validation document,
user file, workspace grant or unknown `/tmp` content was deleted; the prior
evidence directory was copied intact to
`/tmp/pi-recheck-backup-20260919T122504Z/` before anything was re-run.

### Configuration protection (sections 7 and 43)

Seven user configuration files (`~/.codex/config.toml`, the AI Worker production
`config.toml`, OpenCode config and auth, Pi auth, settings and models-store) and
four live registry files (`llmtools.db` plus `-shm`, `-wal`, `.init.lock`) were
hashed before the first command and verified after the last with
`shasum -a 256 -c`: every one reported `OK`. The production file still reads
`[runtime] type = 'opencode'`. No credential, provider, model or route was
modified, and no `pi auth` subcommand was run.

### Availability gate (sections 9 and 10)

Passive prechecks: Pi `0.85.1` at `~/.nvm/versions/node/v23.11.1/bin/pi`,
OpenCode `1.18.31` untouched, `deepseek-ai/DeepSeek-V4-Flash` present in the
user's existing Pi model catalogue, and every run addressed Pi through the
persisted isolated `config.toml` (`selection_source` in each artifact records
`runtime.type = "pi"` with `LLMTOOLS_RUNTIME_SPIKE` cleared).

The bounded gate then passed outright — 3 of 3 probe turns completed in
12:27:46.20–12:28:02.48Z (`wrk_54a63be888e55ce5f9bfabe318d9a611`,
`wrk_94e848773dd31f74c9512a7387cd71bf`, `wrk_568626c75b9fb49d05c634e54a8a764a`),
`PI_RUNTIME_PARITY_REAL_MODEL_AVAILABLE`. That is gate 27's third independent
pass (after 10:14Z and 11:55Z in the acceptance round). Scenarios were allowed to
start, per the rule that availability precedes long turns.

### Scenario sequence (sections 11 through 29)

Run serially in the prescribed order at 12:29:35–12:29:51Z, one process, so the
gate spent its three probe turns once. All six were refused inside 17 seconds,
with the same provider text — `402 "You have depleted your monthly included
credits"` — and the same runtime-neutral durable code:

| Gate | Scenario | worker_run_id | durable run? | code |
| --- | --- | --- | --- | --- |
| 28 | Implementation | `wrk_d40860962f51cce1c4b1aa134a9310bb` | yes | `UPSTREAM_UNAVAILABLE` |
| 29 | Review | `wrk_65ffb3fa0fa82997c47361b8d1ea33bc` | yes | `UPSTREAM_UNAVAILABLE` |
| 30 | NEEDS_CONTEXT | `wrk_7756bb5674678c528748b985c4d8cb0d` | yes | `UPSTREAM_UNAVAILABLE` |
| 31 | NEEDS_DECISION | `wrk_d5d16fd800f0d3f50db4246289f115d9` | yes | `UPSTREAM_UNAVAILABLE` |
| 32 | Cancellation | `wrk_88c2fec756b8f491ee9b4296349b2d2d` | refused before readable | `UPSTREAM_UNAVAILABLE` |
| 33/34 | Process restart | `wrk_2688a172e696144529b66f134cf6d0a5` | yes | `UPSTREAM_UNAVAILABLE` |

The Implementation row is not an empty failure: the turn really reached the
repository before the refusal, recording `llmtools_control_repo_glob` with
`repo_access_allowed=1`, `repo_access_denied=0`, `grant_revision=1` and
`capabilities={RepoRead:true RepoWrite:false Shell:false Web:false
Subagent:false}`. Pi's tool surface, capability snapshot and live confinement
therefore worked again on this round's persisted route; only generation stopped.

Blocked artifacts were written to `*-blocked.json` as required by section 27, so
the acceptance round's `live-revoke.json` (10:13:10Z) and every success record
survived this round's failures untouched. Section 29's honesty rule held too: the
cancellation row is labelled `run row refused before it was readable` rather than
having a session or capability snapshot invented for it.

### Alternate model attempt (section 10)

Because the frozen parity route refused again, the user's existing Pi model
catalogue was checked for another already-configured model. One was available
without touching any credential or config file: `openai/gpt-4.1-mini`. It was
probed under the same bounded three-attempt gate at 12:31:42–12:31:56Z and
completed 0 of 3 (`wrk_54ce4081ce389154819fde4190062b0a`,
`wrk_ebad813a281024fcf0a60418f54fdcbd`, `wrk_7ec6152bff8aac962b2bc416f8712d67`), again `UPSTREAM_UNAVAILABLE`. Section 11
then required stopping rather than continuing to spend, so no long scenario was
attempted on that route. Note what this did *not* do: two different providers
refusing were both classified as bucket C, and neither was allowed to read as
`MODEL_NOT_AVAILABLE`, which is exactly the separation gates 14 and 30 protect —
the raw 402 sentence is likewise a fixture in `TestUpstreamBlockDetectionIsShared`
and `internal/runtime/pi/util_test.go`, and it stayed out of durable Core state
(section 31) in both cases.

### Model-free regression, build and signature (sections 32, 40 and 42)

Product source had zero edits this round, so section 32 applied: the expensive
real OpenCode shared-contract E2E was deliberately **not** re-run, and the
acceptance round's 5/5 against this same source stands. Everything else ran
against the current tree:

| Surface | Result |
| --- | --- |
| `go vet ./...` (main) | clean, exit 0 (`/tmp/pi_recheck_vet.log`, 12:29Z) |
| `go test ./... -count=1` (main) | 11 packages `ok`, no `FAIL` (`/tmp/pi_recheck_gotest.log`) |
| `go test -race ./... -count=1` (main) | 11 packages `ok`, no `DATA RACE` (`/tmp/pi_recheck_race_main.log`) |
| Desktop module vet / test / -race | exit 0 / 0 / 0 (`/tmp/pi_recheck_race_desktop.log`) |
| Frontend `tsc --noEmit` / `vite build` / DOM tests | 0 / 0 / 57 pass, 0 fail (`/tmp/pi_recheck_frontend.log`) |
| Python `unittest discover -s scripts` | `OK`, 22 tests |
| `scripts/validate_codex_plugin.py` | `Plugin validation passed` |
| `gofmt -l internal cmd desktop` | empty |
| Pi model-free persisted selection | `TestPiParityPersistedConfigSelectsPi` PASS 2.38s, fresh `persisted-selection.json` at 12:36:31Z, `MODEL_NOT_AVAILABLE` for the deliberately absent model |

`make build` and `make desktop` both exit 0 (20:37 local) and
`codesign --verify --deep --strict` passes. The bundled
`Contents/MacOS/ai-worker-mcp` reports byte-identical `go version -m` build info
to `bin/ai-worker-mcp`; after detaching signatures both are 16,564,800 bytes with
three differing bytes at offset 2033 (the `LC_UUID` region), the same
re-diagnosis as the acceptance round rather than an assumed match.

### Evidence provenance

`.smoke/pi-parity/availability.json` now holds the alternate-route record,
because the harness writes that one name per probe and this round probed twice.
Both records are preserved verbatim beside it —
`availability-huggingface-route-3of3.json` (the PASS that unlocked the
scenarios) and `availability-openai-alt-route.json` (the 0/3 that ended the
round) — as disclosed manual copies, not harness output, so no reader has to
guess which probe a number came from. The directory holds 12 files on close of this round
(10 from the acceptance round, plus the two probe copies); `.smoke/pi-spike/` was not
written to.

### What this round adds to the acceptance conclusion

The gate passed at 12:28:02Z and the first scenario was refused at 12:29:35Z, 93 seconds later. That is worth
recording as a property of the acceptance design, not a defect in it: the bounded
probe measures short, single-tool turns, so it is a necessary but not sufficient
predictor for scenario-length turns when depletion is progressive. Raising the
probe to scenario length would spend the quota the gate exists to protect, so the
gate stays as it is and the ledger simply reports the window it landed in.

## Final project state

19 non-test Go files carry this round's edits — `cmd/ai-worker-mcp/main.go`,
`internal/agent/{types,contract,upstream}.go`, `internal/config/config.go`,
`internal/mcp/server.go`, `internal/service/service.go`,
`internal/desktop/service.go`, `internal/runtime/factory/factory.go`,
`internal/runtime/opencode/{adapter,client,context,contract,execution}.go`,
`internal/runtime/pi/{adapter,control,rpc,util}.go` — plus 19 test and fixture
files, including the new `opencode/context_control_test.go`,
`opencode/error_mapping_test.go`, `service/runtime_capability_test.go` and the
shared-detector cases in `agent/neutrality_test.go`. Storage schema, MCP contract, Codex skill,
provider configuration, revoke semantics and the Desktop frontend are unchanged.

`bin/ai-worker-mcp` and `desktop/build/bin/AI Worker.app` are built from the
current source and carry a valid ad-hoc signature. `.smoke/pi-spike/` and
`PI_RUNTIME_ADAPTER_SPIKE_VALIDATION.md` are frozen as history. Git is not
initialized. The runtime product default is OpenCode. Pi is selectable by
configuration and is not the default, not deprecated, and not exposed in the
Desktop. Outstanding verdict for the user: Pi's real scenario suite needs one more
provider-servable window before `PI_RUNTIME_PARITY_ACCEPTANCE_CLOSED` can be
earned without lowering a gate.
