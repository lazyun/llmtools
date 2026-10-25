---
{"name":"llmtools-delegation","description":"Use llmTools external worker roles for complete project implementation and tests, implementation advice, independent review, and technical research when matching delegate_* tools are available. This skill governs delegation workflow while Codex retains parent-level decisions and final acceptance."}
---

# llmTools Delegation

Version: `1.4.0`
Fingerprint: `llmtools-delegation-v5`

## Purpose

Use configured external roles for bounded execution while retaining user intent, project architecture, scope, product semantics, parent-level decisions, and final acceptance in the parent Codex agent.

## Available roles

Before delegating, inspect the currently available llmTools `delegate_*` tools. A role is available only when its corresponding tool is present. Never assume a hidden or unconfigured role exists.

- If present, use `delegate_developer` for a complete development task: modify the project, run tests, fix failures, and return changed files, actual commands/results and remaining issues. Codex inspects the changes and owns final acceptance. Prefer this role when working changes and test execution are required.
- If present, use `delegate_implementation` for substantive bounded implementation, bug fixes, refactoring, candidate code changes, and implementation-oriented local engineering work.
- If present, use `delegate_review` for independent code or design review, defect and risk discovery, and a second opinion.
- If present, use `delegate_research` for substantive technical, source, or code investigation, fact gathering, and alternatives. Do not equate this role with web access.

## Repository read

Implementation and Review workers may inspect the one user-authorized workspace bound to their run through server-controlled read-only repository tools (`read`, `glob`, `grep`). That access is fixed by the llmTools server:

- It is scoped to the workspace selected for the run. Never ask the worker to read outside it, and never request broader filesystem access.
- It is read-only. Writing, editing, shell or Git command execution, web access and subagents remain unavailable, and reading through a shell is not a substitute.
- It never extends to another workspace, and revoking the workspace in Desktop ends the access immediately.
- Research stays context-only. It has no repository read.

## Project implementation and tests

Developer alone receives server-controlled `repo_write`, `repo_edit` and `repo_exec`, plus repository read. File operations stay inside the selected workspace. Commands run in a macOS sandbox with writes limited to that project and private run scratch; system toolchains and dependency caches are readable. Outbound network access (including localhost) and root `.git` writes are denied. Native runtime shell/write tools remain disabled. Never ask for broader access or automatic commits, pushes or deployment. Missing dependencies must be reported to Codex.

An active or suspended Developer task reserves its workspace. Continue or cancel that task before starting another Developer there. Do not edit the same files concurrently in the parent. Cancellation or workspace revocation stops running project commands; partial project edits remain for Codex to inspect.

## When to delegate

For substantive work matching an available external role, prefer the corresponding `delegate_*` tool over performing all matching execution in the parent agent. If the user explicitly requests an external role and its tool is available, use that tool.

Handle parent-level decisions and trivial local work directly. If the requested role is unavailable, do not call or invent it. Use another role only when its semantics genuinely match; otherwise proceed in the parent and mention the unavailable role when useful.

## Task Contract

Pass the complete caller-authored request in the single `prompt` field. llmTools forwards this string verbatim to the agent; it does not assemble a prompt from separate objective, context, constraints, decisions or acceptance-criteria fields, wrap it in JSON, or append task instructions. Preserve the request's wording and format. Codex supplies any parent-only information the agent needs directly in that message.

Do not request execution capabilities in delegate calls. llmTools applies and freezes the configured role capability policy on the server.

When the current session advertises authorized workspaces, you may pass `workspace_id` to select one the user already authorized in Desktop. It selects an existing user grant only; it never points at a path and never grants access by itself. For a read-capable role with more than one active grant, `workspace_id` is required: llmTools never guesses a workspace and never falls back to another one.

Stop copying large amounts of repository source into the prompt for a read-capable role; name the paths and let the worker read them. Send everything that is not in the repository: parent decisions, requirements, constraints, user reports, transcripts, designs, and content from outside the authorized workspace.

For review, include the subject, intended behavior, the relevant paths or diff, constraints, and review criteria without pre-biasing the reviewer. For research, include the question, known facts, scope, constraints, and what must be established; research has no repository read, so supply the source it needs. Do not assume web or other capabilities.

## NEEDS_CONTEXT

Read `context_requests`, obtain the currently available relevant information, and call `ai_worker_continue` with the same `worker_run_id`. Preserve the same run, runtime session, and frozen role, agent, and model routing. Do not create a replacement run.

## NEEDS_DECISION

Treat each decision request as a parent-level decision. Decide from the user's goals, architecture, and constraints, then call `ai_worker_continue` with the same `worker_run_id`. Do not delegate the decision back or create a replacement run.

## Cancellation

When the user or parent no longer needs the worker, call `ai_worker_cancel` for the existing `worker_run_id`. Do not continue a cancelled run or use cancellation to change routing.

## Final review

Completed MCP tool text is the original assistant response; `AgentResult.content` in structured metadata contains that same text. llmTools preserves whitespace, Markdown, code and JSON-looking text without parsing business fields or adding separators. Pending requests and failures still use lifecycle envelopes. For Developer, inspect the actual project changes and test evidence; rerun acceptance checks as needed. Worker completion does not mean Codex acceptance. Treat the result as advisory. Check it against parent context and acceptance criteria before accepting or continuing. The parent Codex agent owns final acceptance.

## What not to delegate

Do not delegate trivial work merely to add overhead. Do not delegate user intent, project-level architecture, scope, product semantics, parent-level decisions, or final acceptance. This workflow grants no capability: the MCP server and runtime remain authoritative and fail closed.
