package mcp

import (
	"fmt"
	"strings"
)

// roleMetadata carries only what a caller may see. RepoRead is filled in from
// the service's server-owned policy, so published metadata can never describe
// a capability the runtime did not grant.
type roleMetadata struct {
	Name, Title, Summary, Description string
	RepoRead                          bool
	RepoWrite                         bool
	Shell                             bool
}

// executionPolicy describes the effective repository access of one role.
func (r roleMetadata) executionPolicy() string {
	if r.RepoWrite && r.Shell {
		return "project read/write and sandboxed implementation/test commands in one authorized workspace; Codex owns final acceptance"
	}
	if r.RepoRead {
		return "repository read-only (read, glob, grep) inside the one authorized workspace bound to this run"
	}
	return "context-only; no repository access"
}

var roles = []roleMetadata{
	{Name: "developer", Title: "Delegate Complete Development", Summary: "complete implementation and testing, followed by Codex acceptance", Description: `Delegate a complete bounded development task to the configured external developer role. Pass the complete caller-authored message in prompt; llmTools forwards it verbatim. This role can read and modify project files and run implementation/test commands in one user-authorized workspace through server-controlled tools. Pass workspace_id to choose an existing grant; it cannot expand authority or select a path. Commands use the macOS sandbox: writes are confined to the project and private per-run scratch, root .git writes and outbound network access (including localhost) are denied, and standard toolchains/dependency caches are readable. Runtime-native shell/write tools remain unavailable. Web and subagent access are denied. Execution permissions come from the configured llmTools role policy and are not requested by the caller. One active or suspended developer run reserves its workspace. Ask the worker to implement, test, fix failures and report changed files, actual test commands/results and remaining issues. The worker's completion is a candidate for parent Codex to inspect and accept; it does not claim final acceptance. Continue needs_context/needs_decision using the same worker_run_id.`},
	{Name: "implementation", Title: "Delegate Implementation", Summary: "bounded implementation, fixes, and refactoring", Description: `Run bounded implementation work with the configured external implementation role. Pass the complete caller-authored message in prompt. llmTools forwards it verbatim without assembling fields or adding task instructions. The role inspects the repository through server-controlled read-only tools inside one workspace the user authorized in Desktop; pass workspace_id to select it, which is required when more than one grant is active. workspace_id never points at a path, and no path, directory or cwd field is accepted, so the caller cannot expand or move that filesystem authority. Write, edit, shell and Git command execution, web and subagents are unavailable; execution permissions come from the configured llmTools role policy and are not requested by the caller.

If the run suspends, continue the same worker_run_id with ai_worker_continue.`},
	{Name: "review", Title: "Delegate Independent Review", Summary: "independent code/design review and defect discovery", Description: `Run an independent code or design review with the configured external review role. Pass the complete review request in prompt; llmTools forwards it verbatim. The role inspects the repository through server-controlled read-only tools inside one workspace the user authorized in Desktop; pass workspace_id to select it, which is required when more than one grant is active. workspace_id never points at a path, and no path, directory or cwd field is accepted, so the caller cannot expand or move that filesystem authority. Write, edit, shell and Git command execution, web and subagents are unavailable; execution permissions come from the configured llmTools role policy and are not requested by the caller.

If the run suspends, continue the same worker_run_id with ai_worker_continue.`},
	{Name: "research", Title: "Delegate Technical Research", Summary: "technical investigation, fact gathering, and alternatives", Description: `Run a technical investigation with the configured external research role. Pass the complete research request in prompt; llmTools forwards it verbatim. This role is context-only: it gets no repository read and no other filesystem access, and write, edit, shell and Git command execution, web and subagents are unavailable. Optionally pass workspace_id to select one workspace already authorized by the user; it never grants filesystem access. Execution permissions come from the configured llmTools role policy and are not requested by the caller.

If the run suspends, continue the same worker_run_id with ai_worker_continue.`},
}

func serverInstructions(exposed []roleMetadata, workspaces []string) string {
	var b strings.Builder
	if len(exposed) == 0 {
		b.WriteString("No new external worker roles are currently exposed. Existing runs may still be continued or cancelled.\n\n")
	} else {
		b.WriteString("llmTools currently exposes these external worker roles:\n\n")
		for _, role := range exposed {
			fmt.Fprintf(&b, "- %s: %s\n", role.Name, role.Summary)
		}
		b.WriteString("\nUse the llmTools Delegation Skill for delegation policy. Only the roles listed here are currently available; do not assume hidden roles exist.\n\n")
		b.WriteString("Role execution policy, fixed by the llmTools server and not requestable by a caller:\n\n")
		for _, role := range exposed {
			fmt.Fprintf(&b, "- %s: %s\n", role.Name, role.executionPolicy())
		}
		b.WriteString("\nOnly a role whose policy above grants write and commands may use those tools. Native runtime shell/write tools, web and subagents are unavailable. Repository file operations stay inside the bound workspace; command writes additionally allow private run scratch, while standard toolchains/dependency caches are readable. Root .git writes and outbound network access (including localhost) are denied.\n\n")
	}
	if len(workspaces) > 0 {
		b.WriteString("Authorized workspaces (user grants; select one by workspace_id):\n\n")
		for _, workspace := range workspaces {
			fmt.Fprintf(&b, "- %s\n", workspace)
		}
		b.WriteString("\nworkspace_id selects a workspace the user already authorized in Desktop or via ai_worker_add_workspace. It never points at a path, never grants filesystem access by itself, and cannot widen or move the access a role already has. When a read-capable role has more than one active grant, workspace_id is required; llmTools never guesses and never falls back to another workspace.\n\n")
	} else {
		b.WriteString("No workspaces are currently authorized. Use ai_worker_add_workspace to authorize a project directory, or add one through the Desktop UI. Read-capable roles require at least one authorized workspace.\n\n")
	}
	b.WriteString("Workspace authorization tools: ai_worker_add_workspace (authorize a directory), ai_worker_list_workspaces (list all grants), ai_worker_revoke_workspace (revoke a grant). These complement the Desktop UI authorization method.\n\n")
	b.WriteString("Workers receive the prompt text exactly as supplied. llmTools does not assemble objectives, context or constraints, and completed tool text is the original assistant response. Routing, workspace grants and lifecycle state remain separate protocol metadata. Workers have the repository access described above. Because a read-capable worker can locate and read files inside its bound workspace itself, supply the requirement, scope, frozen decisions and acceptance criteria instead of copying large amounts of source into context; still pass any context that does not exist in the repository. When a run returns NEEDS_CONTEXT or NEEDS_DECISION, continue the same worker_run_id with ai_worker_continue. Use ai_worker_cancel to stop an active or suspended run.")
	return b.String()
}

const continueDescription = `Continue one existing suspended external-agent run, only when worker_run_id is waiting in NEEDS_CONTEXT or NEEDS_DECISION. Continue the SAME run using the same worker_run_id, same runtime session, and same frozen model routing. When exactly one request is pending, request_id may be omitted; otherwise use the short context_N or decision_N aliases. Parent Codex supplies missing context and makes parent-level decisions. Do not start a replacement task. Use ai_worker_cancel if the task should stop.`

const cancelDescription = `Cancel an existing active or suspended external-agent run by worker_run_id. Cancellation terminates the existing run and is not a retry. A confirmed abort returns cancelled; an unconfirmed abort returns failed with a stable error.`

const addWorkspaceDescription = `Authorize a project directory as a workspace for llmTools. The path must be absolute and point to an existing directory. Developer can modify and test the project; Implementation and Review can inspect it read-only. Permissions remain fixed by role. The workspace appears in Desktop for management and revocation. If the same directory is already authorized, the existing grant is returned without creating a duplicate.`

const listWorkspacesDescription = `List all authorized workspaces, including both active and revoked grants. Each workspace has an ID, label, canonical root path, state (ACTIVE or REVOKED), and the authorization source (desktop_user_grant or mcp_client_grant). Use workspace_id from this list to select a workspace for delegation or to revoke access.`

const revokeWorkspaceDescription = `Revoke authorization for a workspace. After revocation, no new runs can use this workspace, and existing runs bound to it are cancelled. The workspace record is retained for audit. Use ai_worker_list_workspaces to find workspace IDs.`

func workspaceGrantMessage(created bool) string {
	if created {
		return "Workspace authorized successfully. The directory is now available for read-capable roles."
	}
	return "This directory was already authorized. The existing grant is returned."
}
