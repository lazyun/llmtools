// Package agent defines the runtime-neutral communication contract.
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"
)

type CapabilityPolicy struct {
	RepoRead  bool `json:"repoRead"`
	RepoWrite bool `json:"repoWrite"`
	Shell     bool `json:"shell"`
	Web       bool `json:"web"`
	Subagent  bool `json:"subagent"`
}

// WorkspaceSnapshot freezes the user-authorized workspace a run was bound to.
// It records identity only; it never grants filesystem access and cannot be
// used to bypass a later revocation in the durable registry.
type WorkspaceSnapshot struct {
	WorkspaceID   string `json:"workspace_id,omitempty"`
	CanonicalRoot string `json:"canonical_root,omitempty"`
	GrantRevision int64  `json:"grant_revision,omitempty"`
}

func (p CapabilityPolicy) Validate() error {
	// Legacy callers and read-only roles cannot request execution privileges.
	if p.RepoWrite || p.Shell || p.Web || p.Subagent {
		return E("CONFIG_ERROR")
	}
	return nil
}

// DeveloperCapabilities is the sole server-owned write/command policy.
func DeveloperCapabilities() CapabilityPolicy {
	return CapabilityPolicy{RepoRead: true, RepoWrite: true, Shell: true}
}

// ValidateForRole keeps the legacy/default deny intact, including for old
// persisted runs. Only the new developer role accepts the execution policy.
func (p CapabilityPolicy) ValidateForRole(role string) error {
	if role == "developer" && p == DeveloperCapabilities() {
		return nil
	}
	return p.Validate()
}

type ContextItem struct {
	Type    string `json:"type"`
	Label   string `json:"label,omitempty"`
	Path    string `json:"path,omitempty"`
	Content string `json:"content"`
}

// DelegateTask carries the caller-authored message verbatim. Routing and
// workspace authorization are transport metadata, never prompt content.
type DelegateTask struct {
	Prompt      string `json:"prompt" jsonschema:"The complete message to send verbatim to the external agent. llmTools does not assemble, rewrite or wrap it. Include any context or instructions the agent needs in this text."`
	WorkspaceID string `json:"workspace_id,omitempty" jsonschema:"Id of a workspace already authorized by the user. Selects an existing grant; never a path or an additional permission."`
}
type AgentTask struct {
	Agent              string           `json:"agent" jsonschema:"Logical llmTools role, not an OpenCode agent name. Use developer for project implementation and tests, implementation for advice, review for independent review, research for investigation. llmTools resolves the configured agent/model."`
	Objective          string           `json:"objective" jsonschema:"The bounded task the external agent should complete. Keep project-level decisions with parent Codex."`
	Context            []ContextItem    `json:"context,omitempty" jsonschema:"Relevant project facts/code selected by Codex. The worker does not implicitly inherit the full conversation or repository context."`
	Constraints        []string         `json:"constraints,omitempty" jsonschema:"Frozen constraints the worker must not violate."`
	Decisions          []string         `json:"decisions,omitempty" jsonschema:"Parent-level decisions already made and not open for reinterpretation."`
	AcceptanceCriteria []string         `json:"acceptance_criteria,omitempty" jsonschema:"Criteria parent Codex uses to review the candidate result."`
	Capabilities       CapabilityPolicy `json:"capabilities" jsonschema:"Explicit capability grants; denied by default and enforced by llmTools."`
	WorkspaceID        string           `json:"workspace_id,omitempty"`

	// Workspace is the server-resolved binding for this task. It is deliberately
	// unexported from the JSON contract: no caller can supply a root path, only
	// the service can populate it from the durable workspace registry.
	Workspace *WorkspaceSnapshot `json:"-"`
}
type ContextRequest struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Target string `json:"target"`
	Reason string `json:"reason"`
}

// ContextRequestKinds are the only values a pending context request may declare.
// Runtime control tools advertise them as an enumeration so a model is never left
// to invent a plausible one - "fact" reads like an obvious kind and was refused by
// Core - that the boundary then rejects.
var ContextRequestKinds = []string{"file", "symbol", "contract", "other"}

func contextRequestKindKnown(kind string) bool {
	for _, k := range ContextRequestKinds {
		if k == kind {
			return true
		}
	}
	return false
}

// ContextRequestsValid is the runtime-neutral shape rule for a suspension
// request. Every adapter maps its own control tool payload into this contract,
// so no runtime can weaken what Core accepts as a pending context request.
func ContextRequestsValid(requests []ContextRequest) bool {
	return ContextRequestRejection(requests) == ""
}

// ContextRequestRejection names the first shape rule a pending context request
// violates, as one of a fixed set of identifiers, or "" when the requests are
// acceptable. It exists so a runtime can log which rule fired without copying
// the model's request payload - a target may name a private file and a reason may
// carry parent-only text - into an operator stream or an agent result.
func ContextRequestRejection(requests []ContextRequest) string {
	switch {
	case len(requests) == 0:
		return "EMPTY"
	case len(requests) > 20:
		return "TOO_MANY_REQUESTS"
	}
	seen := map[string]bool{}
	for _, r := range requests {
		switch {
		case r.ID == "":
			return "MISSING_ID"
		case len(r.ID) > 80:
			return "ID_TOO_LONG"
		case seen[r.ID]:
			return "DUPLICATE_ID"
		case strings.TrimSpace(r.Target) == "":
			return "MISSING_TARGET"
		case len(r.Target) > 4096:
			return "TARGET_TOO_LONG"
		case strings.TrimSpace(r.Reason) == "":
			return "MISSING_REASON"
		case len(r.Reason) > 16384:
			return "REASON_TOO_LONG"
		}
		seen[r.ID] = true
		if !contextRequestKindKnown(r.Kind) {
			return "UNKNOWN_KIND"
		}
	}
	return ""
}

type Option struct {
	ID          string `json:"id"`
	Description string `json:"description"`
}
type DecisionRequest struct {
	ID          string   `json:"id"`
	Question    string   `json:"question"`
	Options     []Option `json:"options"`
	Reason      string   `json:"reason"`
	Header      string   `json:"header,omitempty"`
	Multiple    bool     `json:"multiple,omitempty"`
	AllowCustom bool     `json:"allow_custom,omitempty"`
	CreatedAt   string   `json:"created_at,omitempty"`
}
type AdditionalContext struct {
	RequestID string `json:"request_id,omitempty" jsonschema:"Pending alias. May be omitted when exactly one context request is pending."`
	Type      string `json:"type"`
	Path      string `json:"path,omitempty"`
	Content   string `json:"content"`
}
type DecisionResponse struct {
	RequestID   string   `json:"request_id,omitempty" jsonschema:"Pending alias. May be omitted when exactly one decision request is pending."`
	Selection   string   `json:"selection,omitempty"`
	Selections  []string `json:"selections,omitempty"`
	CustomText  string   `json:"custom_text,omitempty"`
	Reject      bool     `json:"reject,omitempty"`
	Explanation string   `json:"explanation,omitempty"`
}
type Continuation struct {
	WorkerRunID       string              `json:"worker_run_id" jsonschema:"Durable llmTools run identity returned by a delegate tool. Use the same value for continuation/cancellation."`
	AdditionalContext []AdditionalContext `json:"additional_context,omitempty" jsonschema:"Exact answers to pending context requests, matched by request_id."`
	DecisionResponses []DecisionResponse  `json:"decision_responses,omitempty" jsonschema:"Parent Codex decisions answering pending decision requests by request_id."`
}
type Artifact struct {
	Type    string `json:"type"`
	Path    string `json:"path,omitempty"`
	Content string `json:"content"`
}
type AgentResult struct {
	WorkerRunID      string            `json:"worker_run_id,omitempty"`
	Status           string            `json:"status" jsonschema:"completed: review content; needs_context: gather context and continue same run; needs_decision: Codex decides and continues same run; failed: inspect stable error; cancelled: terminal."`
	Content          string            `json:"content,omitempty"`
	Summary          string            `json:"summary,omitempty"`
	Artifacts        []Artifact        `json:"artifacts,omitempty"`
	Patch            string            `json:"patch,omitempty"`
	Findings         []string          `json:"findings,omitempty"`
	ContextRequests  []ContextRequest  `json:"context_requests,omitempty"`
	DecisionRequests []DecisionRequest `json:"decision_requests,omitempty"`
	Risks            []string          `json:"risks,omitempty"`
	Runtime          string            `json:"runtime,omitempty"`
	Agent            string            `json:"agent,omitempty"`
	Model            string            `json:"model,omitempty"`
	Error            *Error            `json:"error,omitempty"`
}
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

var messages = map[string]string{
	"PENDING_DECISION_LOST":           "The pending runtime question is no longer available; execution was not replayed.",
	"PENDING_CONTEXT_LOST":            "The pending runtime context request is no longer available; execution was not replayed.",
	"QUESTION_TOOL_UNAVAILABLE":       "Native question is unavailable or not allowed; check runtime question diagnostics.",
	"CONTEXT_TOOL_UNAVAILABLE":        "Native context control is unavailable or not allowed; check runtime context diagnostics.",
	"EMPTY_AGENT_RESULT":              "Runtime completed without a usable final assistant response.",
	"CONFIG_ERROR":                    "Configuration is invalid or the legacy request exceeds server policy.",
	"ROLE_CAPABILITY_POLICY_INVALID":  "The configured role execution policy is invalid.",
	"AGENT_MAPPING_NOT_FOUND":         "No routing is configured for the requested logical role.",
	"ROLE_NOT_EXPOSED":                "The requested logical role is not exposed for new Codex runs.",
	"MODEL_NOT_AVAILABLE":             "The configured model is not currently available in the selected runtime.",
	"UPSTREAM_UNAVAILABLE":            "The selected model's provider refused or could not serve the request; no result was produced.",
	"RUNTIME_NOT_FOUND":               "The runtime executable was not found.",
	"RUNTIME_UNAVAILABLE":             "Runtime is unavailable; check runtime diagnostics.",
	"RUNTIME_START_FAILED":            "The runtime could not become ready to accept a task.",
	"RUNTIME_PROTOCOL_ERROR":          "Runtime response or event protocol was invalid.",
	"AGENT_NOT_FOUND":                 "The configured runtime agent is not currently available.",
	"SESSION_CREATE_FAILED":           "Runtime session could not be created.",
	"SESSION_LOST":                    "Stored runtime session is no longer available.",
	"SESSION_BUSY":                    "This conversation's worker session is busy. Finish, continue or cancel its active run before delegating another task.",
	"CONTINUATION_INVALID":            "Run is unknown, busy, terminal, or not awaiting these responses.",
	"WORKSPACE_INVALID":               "The workspace authorization request is invalid.",
	"WORKSPACE_NOT_FOUND":             "The selected workspace is not authorized.",
	"WORKSPACE_REQUIRED":              "This role needs one authorized workspace; add it in Desktop and retry.",
	"WORKSPACE_SELECTION_REQUIRED":    "Several authorized workspaces are active; pass the intended workspace_id.",
	"WORKSPACE_AUTHORIZATION_REVOKED": "The selected workspace authorization was revoked.",
	"WORKSPACE_UNAVAILABLE":           "The authorized workspace directory is missing or is not a directory.",
	"WORKSPACE_ACCESS_DENIED":         "The requested repository path is outside the authorized workspace or is protected.",
	"REPO_TARGET_NOT_FOUND":           "The repository target does not exist inside the authorized workspace.",
	"REPO_ACCESS_LIMIT":               "The repository target exceeds the repository size or format limits.",
	"WORKSPACE_BUSY":                  "This workspace already has an active or suspended developer run. Finish, continue or cancel it before delegating another developer task.",
	"EXECUTION_NOT_ALLOWED":           "This run has no project write or command execution capability.",
	"EXECUTION_SANDBOX_UNAVAILABLE":   "Project command execution requires the supported macOS system sandbox.",
	"REPO_WRITE_FAILED":               "The project file could not be written.",
	"REPO_EDIT_CONFLICT":              "The text to edit must occur exactly once; read the current file and retry.",
	"REPO_INPUT_INVALID":              "The repository navigation input is malformed.",
	"INVALID_CONTEXT_RESPONSE":        "Context responses must exactly answer the current pending context requests.",
	"CONTEXT_INPUT_INVALID":           "The context request was malformed: kind must be file, symbol, contract or other, with a unique id, a target and a reason.",
	"INVALID_DECISION_RESPONSE":       "Decision responses must exactly answer current requests using an offered selection.",
	"TIMEOUT":                         "Task exceeded its deadline.",
	"CANCELLED":                       "Task was cancelled.",
	"INVALID_AGENT_RESULT":            "Runtime final result does not satisfy the communication contract.",
	"INTERNAL_ERROR":                  "Local operation failed; check diagnostics.",
	"ABORT_UNCONFIRMED":               "Runtime abort could not be confirmed; task is failed, not reported as safely cancelled. Stop or inspect the runtime.",
}

func E(code string) *Error {
	m, ok := messages[code]
	if !ok {
		code = "INTERNAL_ERROR"
		m = messages[code]
	}
	return &Error{code, m}
}
func Safe(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return E(e.Code)
	}
	return E("INTERNAL_ERROR")
}

// Failure builds a terminal result with no runtime identity attached. Only a
// caller that knows which runtime served the run may stamp Runtime; Core does
// it in Service.Failure.
func Failure(id string, err error) AgentResult {
	return AgentResult{WorkerRunID: id, Status: "failed", Summary: "Task did not complete.", Error: Safe(err)}
}

type Progress struct {
	WorkerRunID string `json:"worker_run_id,omitempty"`
	Stage       string `json:"stage"`
	ElapsedMS   int64  `json:"elapsed_ms"`
	Agent       string `json:"agent"`
	Runtime     string `json:"runtime"`
}
type Emit func(Progress)

// Health reports whether the runtime's transport and control plane can accept a
// task: the executable or managed server is reachable and its protocol is
// usable. It deliberately says nothing about generation quality, and it is
// always passive — no model call, no provider request, no produced content.
// Runtime-specific instance metadata (endpoint, port, pid, generation, ownership
// for a managed server; binary, version and process model for a subprocess
// runtime) is diagnostics the owning adapter or the Desktop projection exposes
// separately; it is not part of health and Core never depends on it.
type Health struct {
	Healthy bool   `json:"healthy"`
	Version string `json:"version"`
}
type AgentInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Mode        string `json:"mode"`
}
type RuntimeModel struct {
	ID          string `json:"id"`
	ProviderID  string `json:"provider_id"`
	ModelID     string `json:"model_id"`
	DisplayName string `json:"display_name"`
}

// RuntimeHandle never appears in the MCP contract.
type RuntimeHandle struct {
	SessionID     string
	SessionFile   string
	Agent         string
	Model         string
	ModelSource   string
	DecisionState json.RawMessage
	ContextState  json.RawMessage
	ControlToken  string

	// RepoRead and WorkspaceRoot carry the frozen run binding into the adapter so
	// every follow-up request reuses the same session scope. They come from
	// durable run state, never from a caller.
	RepoRead      bool
	RepoWrite     bool
	Shell         bool
	WorkspaceRoot string
}
type RuntimeAdapter interface {
	// RuntimeKind is the durable, runtime-neutral identity Core records for a
	// run. It never selects behavior in Core.
	RuntimeKind() string
	// Health reports transport and control-plane readiness, passively. See Health.
	Health(context.Context) (Health, error)
	// AgentCatalogueSupported reports whether this runtime exposes a runtime-native
	// agent catalogue that ListAgents can enumerate. A runtime without one is not
	// unhealthy and its agents are not unavailable: it simply has no runtime agent
	// selector to validate, so Core must not require a configured agent name and
	// must not treat an empty catalogue as AGENT_NOT_FOUND. Core branches on this
	// capability, never on a runtime name.
	AgentCatalogueSupported() bool
	// ListAgents enumerates the runtime-native catalogue. For a runtime where
	// AgentCatalogueSupported is false it returns no entries; an adapter must
	// never substitute llmTools logical roles for runtime agents.
	ListAgents(context.Context) ([]AgentInfo, error)
	// ListModels is the runtime-discoverable model catalogue only. Presence here
	// does not mean the provider will serve a request: catalogue discovery must
	// stay passive, and execution availability is reported by the task itself
	// (UPSTREAM_UNAVAILABLE), never by failing health or pretending absence.
	ListModels(context.Context) ([]RuntimeModel, error)
	StartTask(context.Context, AgentTask, string, string) (RuntimeHandle, error)
	ContinueTask(context.Context, RuntimeHandle, any, Emit) (AgentResult, error)
	CancelTask(context.Context, RuntimeHandle) error
	SubscribeProgress(context.Context, RuntimeHandle, Emit) (<-chan error, error)
}

// DecisionPending is an internal adapter control signal, never an MCP payload.
// State is opaque runtime-specific communication metadata, not a transcript.
type DecisionPending struct {
	Requests []DecisionRequest
	State    json.RawMessage
}

func (*DecisionPending) Error() string { return "runtime decision pending" }

type DecisionRuntime interface {
	ResumeDecision(context.Context, RuntimeHandle, json.RawMessage, []DecisionResponse, Emit) (AgentResult, error)
}

// ContextPending is an internal suspension raised by the runtime-specific
// request_context control tool. State never enters the public MCP result.
type ContextPending struct {
	Requests []ContextRequest
	State    json.RawMessage
}

func (*ContextPending) Error() string { return "runtime context pending" }

type ContextRuntime interface {
	ResumeContext(context.Context, RuntimeHandle, json.RawMessage, []AdditionalContext, Emit) (AgentResult, error)
}
type Run struct {
	RuntimeDecision     json.RawMessage `json:"runtime_decision,omitempty"`
	RuntimeContext      json.RawMessage `json:"runtime_context,omitempty"`
	RuntimeControl      string          `json:"runtime_control,omitempty"`
	PendingDecisionIDs  []string        `json:"pending_decision_ids,omitempty"`
	PendingContextIDs   []string        `json:"pending_context_ids,omitempty"`
	PendingDecisionBase int             `json:"pending_decision_base,omitempty"`
	PendingContextBase  int             `json:"pending_context_base,omitempty"`
	NextDecisionAlias   int             `json:"next_decision_alias,omitempty"`
	NextContextAlias    int             `json:"next_context_alias,omitempty"`
	UsedContextIDs      []string        `json:"used_context_ids,omitempty"`

	ID                 string            `json:"worker_run_id"`
	ConversationKey    string            `json:"conversation_key,omitempty"`
	RuntimeSessionID   string            `json:"runtime_session_id"`
	RuntimeBinary      string            `json:"runtime_binary,omitempty"`
	RuntimeSessionDir  string            `json:"runtime_session_dir,omitempty"`
	RuntimeSessionFile string            `json:"runtime_session_file,omitempty"`
	RuntimeIdentity    string            `json:"runtime_identity"`
	RuntimeAgent       string            `json:"runtime_agent"`
	RuntimeModel       string            `json:"runtime_model,omitempty"`
	ModelSource        string            `json:"model_source,omitempty"`
	RoutingRevision    string            `json:"routing_revision,omitempty"`
	LogicalRole        string            `json:"logical_role,omitempty"`
	Agent              string            `json:"agent"`
	Runtime            string            `json:"runtime"`
	Status             string            `json:"status"`
	Objective          string            `json:"objective"`
	Created            time.Time         `json:"created"`
	Updated            time.Time         `json:"updated"`
	Capabilities       CapabilityPolicy  `json:"capabilities"`
	Workspace          WorkspaceSnapshot `json:"workspace,omitempty"`
	Result             AgentResult       `json:"result"`
}

func Strict(data []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if d.Decode(v) != nil {
		return E("INVALID_AGENT_RESULT")
	}
	if e := d.Decode(new(any)); e != io.EOF {
		return E("INVALID_AGENT_RESULT")
	}
	return nil
}
func ValidateResult(r AgentResult) error {
	bad := E("INVALID_AGENT_RESULT")
	if r.WorkerRunID != "" || r.Runtime != "" || r.Error != nil {
		return bad
	}
	ids := map[string]bool{}
	add := func(id string) bool {
		if id == "" || len(id) > 80 || ids[id] {
			return false
		}
		ids[id] = true
		return true
	}
	switch r.Status {
	case "completed":
		if strings.TrimSpace(r.Content) == "" || len(r.ContextRequests)+len(r.DecisionRequests) > 0 {
			return bad
		}
	case "needs_context":
		if strings.TrimSpace(r.Summary) == "" || r.Content != "" || len(r.ContextRequests) == 0 || len(r.DecisionRequests) > 0 || r.Patch != "" || len(r.Artifacts) > 0 {
			return bad
		}
		for _, q := range r.ContextRequests {
			if !add(q.ID) || q.Target == "" || q.Reason == "" {
				return bad
			}
			switch q.Kind {
			case "file", "symbol", "contract", "other":
			default:
				return bad
			}
		}
	case "needs_decision":
		if strings.TrimSpace(r.Summary) == "" || r.Content != "" || len(r.DecisionRequests) == 0 || len(r.ContextRequests) > 0 || r.Patch != "" || len(r.Artifacts) > 0 {
			return bad
		}
		for _, q := range r.DecisionRequests {
			if !add(q.ID) || q.Question == "" || q.Reason == "" || (len(q.Options) == 0 && !q.AllowCustom) {
				return bad
			}
			opts := map[string]bool{}
			for _, o := range q.Options {
				if o.ID == "" || o.Description == "" || opts[o.ID] {
					return bad
				}
				opts[o.ID] = true
			}
		}
	case "failed":
		if strings.TrimSpace(r.Summary) == "" || r.Content != "" || len(r.ContextRequests)+len(r.DecisionRequests) > 0 {
			return bad
		}
	default:
		return bad
	}
	return nil
}

// ValidateDecisionResponse preserves native single/multiple/custom semantics.
// Reject is an explicit refusal: the service cancels, never invents a choice.
func ValidateDecisionResponse(q DecisionRequest, a DecisionResponse) error {
	bad := E("INVALID_DECISION_RESPONSE")
	if a.Reject {
		if a.Selection != "" || len(a.Selections) > 0 || a.CustomText != "" {
			return bad
		}
		return nil
	}
	if a.Selection != "" && len(a.Selections) > 0 {
		return bad
	}
	selected := a.Selections
	if a.Selection != "" {
		selected = []string{a.Selection}
	}
	count := len(selected)
	if a.CustomText != "" {
		if !q.AllowCustom || strings.TrimSpace(a.CustomText) == "" || len(a.CustomText) > 16384 {
			return bad
		}
		count++
	}
	if count == 0 || (!q.Multiple && count != 1) {
		return bad
	}
	options := map[string]bool{}
	for _, o := range q.Options {
		options[o.ID] = true
	}
	seen := map[string]bool{}
	for _, id := range selected {
		if !options[id] || seen[id] {
			return bad
		}
		seen[id] = true
	}
	return nil
}
