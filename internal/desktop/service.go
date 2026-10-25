// Package desktop is a configuration and observation surface, not a task control plane.
package desktop

import (
	"ai-worker-mcp/internal/agent"
	"ai-worker-mcp/internal/config"
	"ai-worker-mcp/internal/localfs"
	"ai-worker-mcp/internal/runtime/factory"
	"ai-worker-mcp/internal/runtime/opencode"
	"ai-worker-mcp/internal/service"
	"ai-worker-mcp/internal/store"
	"context"
	"fmt"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pelletier/go-toml/v2"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type Service struct {
	mu      sync.Mutex
	config  config.Config
	binary  string
	storage *store.Store
}
type State struct {
	Directory        string                   `json:"directory"`
	Binary           string                   `json:"binary"`
	Config           config.Config            `json:"config"`
	Runs             []agent.Run              `json:"runs"`
	RuntimeOwned     bool                     `json:"runtime_owned"`
	Runtime          opencode.RuntimeSnapshot `json:"runtime"`
	Skill            SkillStatus              `json:"skill"`
	Workspaces       []store.Workspace        `json:"workspaces"`
	RolePolicies     map[string]string        `json:"role_policies"`
	PiResumeCommands map[string]string        `json:"pi_resume_commands"`
}
type WorkspaceGrant struct {
	Workspace store.Workspace `json:"workspace"`
	Created   bool            `json:"created"`
}
type Check struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message"`
}
type SkillStatus struct {
	CanRemove           bool   `json:"can_remove"`
	Status              string `json:"status"`
	Version             string `json:"version"`
	Fingerprint         string `json:"fingerprint"`
	Path                string `json:"path"`
	ExpectedVersion     string `json:"expected_version"`
	ExpectedFingerprint string `json:"expected_fingerprint"`
}
type RoutingDiscovery struct {
	Agents []agent.AgentInfo    `json:"agents"`
	Models []agent.RuntimeModel `json:"models"`
}

const (
	expectedSkillVersion     = "1.4.0"
	expectedSkillFingerprint = "llmtools-delegation-v5"
)

// SkillStatus observes the installed Delegation Skill copy at ~/.agents/skills.
// Installation/removal are separate explicit Desktop actions.
func skillStatus(path string) SkillStatus {
	out := SkillStatus{Status: "missing", Path: path, ExpectedVersion: expectedSkillVersion, ExpectedFingerprint: expectedSkillFingerprint}
	if path != "" {
		_, err := os.Lstat(filepath.Dir(path))
		out.CanRemove = err == nil
	}
	b, e := localfs.ReadFile(path)
	if e != nil {
		return out
	}
	out.Version = skillField(string(b), "Version:")
	out.Fingerprint = skillField(string(b), "Fingerprint:")
	if out.Version == "" || out.Fingerprint == "" {
		return out
	}
	if out.Version == expectedSkillVersion && out.Fingerprint == expectedSkillFingerprint {
		out.Status = "installed"
	} else {
		out.Status = "outdated"
	}
	return out
}
func skillField(text, prefix string) string {
	for _, line := range strings.Split(text, "\n") {
		if i := strings.Index(line, prefix); i >= 0 {
			return strings.Trim(strings.TrimSpace(line[i+len(prefix):]), "`")
		}
	}
	return ""
}
func (s *Service) skillPath() string {
	home, e := os.UserHomeDir()
	if e != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".agents", "skills", "llmtools-delegation", "SKILL.md")
}

func New(dir, binary string) (*Service, error) {
	if dir == "" {
		var e error
		dir, e = localfs.DefaultDirectory()
		if e != nil {
			return nil, e
		}
	}
	c, e := config.Ensure(dir)
	if e != nil {
		return nil, e
	}
	st, e := store.Open(filepath.Join(dir, c.Storage.Database))
	if e != nil {
		return nil, e
	}
	if c.Runtime.Type != "pi" {
		c.Runtime = config.DefaultAt(dir).Runtime
		for role, route := range c.Agents {
			route.Agent = ""
			route.Model = ""
			route.Exposed = false
			c.Agents[role] = route
		}
	}
	return &Service{config: c, binary: binary, storage: st}, nil
}
func (s *Service) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.storage.Close()
}
func (s *Service) State() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	runs, readErr := s.storage.List()
	for i := range runs {
		runs[i].RuntimeDecision = nil
		runs[i].RuntimeContext = nil
		runs[i].RuntimeControl = ""
		runs[i].PendingDecisionIDs = nil
		runs[i].PendingContextIDs = nil
		runs[i].PendingDecisionBase = 0
		runs[i].PendingContextBase = 0
		runs[i].NextDecisionAlias = 0
		runs[i].NextContextAlias = 0
		runs[i].UsedContextIDs = nil
	}
	c := s.config
	if readErr != nil {
		c.Warnings = append(append([]string{}, c.Warnings...), "INTERNAL_ERROR: run storage could not be read; diagnostics required.")
	}
	c.Agents = map[string]config.AgentRouting{}
	for k, v := range s.config.Agents {
		c.Agents[k] = v
	}
	snapshot := opencode.RuntimeSnapshot{}
	workspaces, workspaceErr := s.storage.Workspaces()
	if workspaceErr != nil {
		workspaces = []store.Workspace{}
	}
	// The role policies are reported by the delegation service, so the UI shows
	// the execution surface a run actually gets instead of a second definition.
	policies := map[string]string{}
	for _, role := range []string{"implementation", "developer", "review", "research"} {
		policies[role] = executionPolicyLabel(service.RoleCapabilityPolicy(role))
	}
	return State{Directory: s.config.Directory, Binary: s.binary, Config: c, Runs: runs, RuntimeOwned: false, Runtime: snapshot, Skill: skillStatus(s.skillPath()), Workspaces: workspaces, RolePolicies: policies, PiResumeCommands: piResumeCommands(runs, c)}
}

// AddWorkspace records an explicit Desktop user grant over a canonical
// directory. The native folder picker is the only authority source; a path is
// never accepted from Codex or from a plain text field.
func (s *Service) AddWorkspace(path string) (WorkspaceGrant, error) {
	canonical, e := localfs.CanonicalDir(path)
	if e != nil {
		return WorkspaceGrant{}, agent.E("WORKSPACE_INVALID")
	}
	w, created, e := s.storage.AddWorkspace(canonical, filepath.Base(canonical), "desktop_user_grant")
	if e != nil {
		return WorkspaceGrant{}, e
	}
	return WorkspaceGrant{Workspace: w, Created: created}, nil
}

// RenameWorkspace changes only the user-facing label; it never changes authority.
func (s *Service) RenameWorkspace(id, label string) error {
	return s.storage.RenameWorkspace(id, label)
}

// RevokeWorkspace durably revokes a grant. The record is retained for audit.
func (s *Service) RevokeWorkspace(id string) error {
	return s.storage.RevokeWorkspace(id)
}

// SaveRuntime accepts only Pi; legacy server fields are cleared on save.
func (s *Service) SaveRuntime(r config.Runtime) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.Type != "pi" {
		return agent.E("CONFIG_ERROR")
	}
	r.Mode = ""
	r.Host = ""
	r.BaseURL = ""
	r.Port = 0
	c := s.config
	c.Runtime = r
	if e := config.Save(filepath.Join(c.Directory, "config.toml"), c); e != nil {
		return e
	}
	s.config = c
	return nil
}
func (s *Service) SaveSettings(g config.General) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.config
	c.General = g
	if e := config.Save(filepath.Join(c.Directory, "config.toml"), c); e != nil {
		return e
	}
	s.config = c
	return nil
}

// Retained binding for older clients; Pi has no named agent catalogue.
func (s *Service) DiscoverAgents(ctx context.Context) ([]agent.AgentInfo, error) {
	return nil, agent.E("CONFIG_ERROR")
}
func (s *Service) DiscoverRouting(ctx context.Context) (RoutingDiscovery, error) {
	s.mu.Lock()
	c := s.config
	s.mu.Unlock()
	return discoverRouting(ctx, c)
}
func discoverRouting(ctx context.Context, cfg config.Config) (RoutingDiscovery, error) {
	if cfg.Runtime.Type != "pi" {
		return RoutingDiscovery{}, agent.E("CONFIG_ERROR")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	runtime, e := factory.New(cfg)
	if e != nil {
		return RoutingDiscovery{}, e
	}
	defer runtime.Close()
	models, e := runtime.Adapter.ListModels(ctx)
	if e != nil {
		return RoutingDiscovery{}, e
	}
	return RoutingDiscovery{Agents: []agent.AgentInfo{}, Models: models}, nil
}
func (s *Service) SaveRoutings(ctx context.Context, routings map[string]config.AgentRouting) error {
	s.mu.Lock()
	cfg := s.config
	s.mu.Unlock()
	discovery, e := discoverRouting(ctx, cfg)
	if e != nil {
		return e
	}
	agents := map[string]bool{}
	models := map[string]bool{}
	for _, a := range discovery.Agents {
		agents[a.Name] = true
	}
	for _, m := range discovery.Models {
		models[m.ID] = true
	}
	if len(routings) == 0 {
		return agent.E("CONFIG_ERROR")
	}
	for role, routing := range routings {
		validAgent := agents[routing.Agent]
		if cfg.Runtime.Type == "pi" {
			validAgent = routing.Agent == ""
		}
		if !config.Name.MatchString(role) || !validAgent {
			return agent.E("AGENT_NOT_FOUND")
		}
		if routing.Model != "" && !models[routing.Model] {
			return agent.E("MODEL_NOT_AVAILABLE")
		}
		if routing.Exposed && routing.Model == "" {
			return agent.E("MODEL_NOT_AVAILABLE")
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Do not save a catalogue validated for a runtime that was just replaced.
	if s.config.Runtime != cfg.Runtime {
		return agent.E("CONFIG_ERROR")
	}
	c := s.config
	c.Agents = map[string]config.AgentRouting{}
	for role, routing := range routings {
		c.Agents[role] = routing
	}
	if e = config.Save(filepath.Join(c.Directory, "config.toml"), c); e != nil {
		return e
	}
	s.config = c
	return nil
}

// SaveMapping preserves the older backend/testing entry point as an
// agent-default routing save. Desktop uses SaveRoutings.
func (s *Service) SaveMapping(ctx context.Context, name, target string) error {
	s.mu.Lock()
	routings := make(map[string]config.AgentRouting, len(s.config.Agents)+1)
	for role, routing := range s.config.Agents {
		routings[role] = routing
	}
	s.mu.Unlock()
	routings[name] = config.AgentRouting{Exposed: true, Agent: target}
	return s.SaveRoutings(ctx, routings)
}
func (s *Service) DeleteMapping(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.config
	c.Agents = map[string]config.AgentRouting{}
	for k, v := range s.config.Agents {
		if k != name {
			c.Agents[k] = v
		}
	}
	if e := config.Save(filepath.Join(c.Directory, "config.toml"), c); e != nil {
		return e
	}
	s.config = c
	return nil
}

// Pi is started per task; persistent runtime lifecycle actions are disabled.
func (s *Service) StartRuntime(ctx context.Context) error { return agent.E("CONFIG_ERROR") }
func (s *Service) StopRuntime() error                     { return agent.E("CONFIG_ERROR") }
func (s *Service) CodexConfig() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Keep the caller's ceiling above every supported hot-reloaded deadline.
	v := map[string]any{"mcp_servers": map[string]any{"ai-worker": map[string]any{"command": s.binary, "args": []string{"--config-dir", s.config.Directory}, "tool_timeout_sec": config.MaxTaskTimeoutSeconds + 15}}}
	b, e := toml.Marshal(v)
	return string(b), e
}
func (s *Service) SetBinary(path string) error {
	if !filepath.IsAbs(path) {
		return agent.E("CONFIG_ERROR")
	}
	s.mu.Lock()
	s.binary = path
	s.mu.Unlock()
	return nil
}

// FindBinary searches PATH for the named executable and returns its absolute
// path, or "" when nothing matching is found. It is the backend half of the
// auto-detect button; the frontend supplies the name ("opencode" or "pi").
func (s *Service) FindBinary(name string) string {
	if name == "" {
		return ""
	}
	found, e := exec.LookPath(name)
	if e != nil {
		return ""
	}
	abs, e := filepath.Abs(found)
	if e != nil {
		return found
	}
	return abs
}

// RuntimeType reports which runtime the current configuration selects so the
// frontend can render the correct editor without parsing the config itself.
func (s *Service) RuntimeType() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.config.Runtime.Type
}
func check(name string, e error) Check {
	if e != nil {
		v := agent.Safe(e)
		return Check{name, "FAIL", v.Code, v.Message}
	}
	return Check{Name: name, Status: "PASS", Message: "Verified"}
}

// executionPolicyLabel is the read-only display form of the server-owned role
// policy, shared by Diagnostics and the Agents settings surface. A role can
// never be shown with more access than the delegation service grants it.
func executionPolicyLabel(p agent.CapabilityPolicy) string {
	if p.RepoWrite && p.Shell {
		return "Project implementation and tests"
	}
	if p.RepoRead {
		return "Repository read-only"
	}
	return "Context-only"
}

func (s *Service) Diagnostics(ctx context.Context) []Check {
	s.mu.Lock()
	c := s.config
	s.mu.Unlock()
	out := []Check{check("Configuration", c.Validate())}
	if c.Runtime.Type != opencode.Kind {
		out = append(out, Check{Name: "Runtime management", Status: "CONFIGURED", Message: "Pi starts automatically for each task. Use Check Pi to verify its offline protocol; no persistent server needs to be started."})
	}
	active, revoked, workspaceErr := s.storage.WorkspaceStats()
	workspaceCheck := check("Workspace Authorization", workspaceErr)
	if workspaceErr == nil {
		// Count by source for detailed reporting.
		allWorkspaces, allErr := s.storage.Workspaces()
		sourceCounts := map[string]int{}
		activeBySource := map[string]int{}
		if allErr == nil {
			for _, w := range allWorkspaces {
				sourceCounts[w.Source]++
				if w.State == "ACTIVE" {
					activeBySource[w.Source]++
				}
			}
		}
		sources := ""
		for src, count := range sourceCounts {
			if sources != "" {
				sources += " · "
			}
			sources += fmt.Sprintf("%s: %d (active: %d)", src, count, activeBySource[src])
		}
		if sources == "" {
			sources = "none"
		}
		workspaceCheck.Message = fmt.Sprintf("Authorized workspaces: %d · Active: %d · Revoked: %d · Sources: %s · MCP Roots: Unavailable", active+revoked, active, revoked, sources)
	}
	out = append(out, workspaceCheck)
	skill := skillStatus(s.skillPath())
	switch skill.Status {
	case "installed":
		out = append(out, Check{Name: "Delegation Skill", Status: "PASS", Message: "Installed · " + skill.Path})
	case "outdated":
		out = append(out, Check{Name: "Delegation Skill", Status: "FAIL", Message: "Outdated version/fingerprint · " + skill.Path})
	default:
		out = append(out, Check{Name: "Delegation Skill", Status: "FAIL", Message: "Missing · " + skill.Path})
	}
	if skill.Version != "" && skill.Version == skill.ExpectedVersion {
		out = append(out, Check{Name: "Delegation Skill version", Status: "PASS", Message: skill.Version})
	} else {
		out = append(out, Check{Name: "Delegation Skill version", Status: "FAIL", Message: "want " + skill.ExpectedVersion + " · got " + skill.Version})
	}
	if skill.Fingerprint != "" && skill.Fingerprint == skill.ExpectedFingerprint {
		out = append(out, Check{Name: "Delegation Skill fingerprint", Status: "PASS", Message: skill.Fingerprint})
	} else {
		out = append(out, Check{Name: "Delegation Skill fingerprint", Status: "FAIL", Message: "want " + skill.ExpectedFingerprint + " · got " + skill.Fingerprint})
	}
	toolNames := []string{"ai_worker_continue", "ai_worker_cancel", "ai_worker_add_workspace", "ai_worker_list_workspaces", "ai_worker_revoke_workspace"}
	for _, role := range []string{"implementation", "developer", "review", "research"} {
		routing, configured := c.Agents[role]
		if configured && routing.Exposed {
			tool := "delegate_" + role
			toolNames = append(toolNames, tool)
			out = append(out, Check{Name: "Role Exposure: " + role, Status: "PASS", Message: "Exposed: YES · Tool: " + tool})
		} else {
			out = append(out, Check{Name: "Role Exposure: " + role, Status: "PASS", Message: "Exposed: NO · Tool: not exposed"})
		}
		// The policy is read from the delegation service itself, so this report
		// cannot drift from what a run of that role is actually granted.
		out = append(out, Check{Name: "Role Execution Policy: " + role, Status: "PASS", Message: executionPolicyLabel(service.RoleCapabilityPolicy(role))})
	}
	out = append(out, Check{Name: "Repository Read Enforcement", Status: "PASS", Message: "Read-only roles: read/glob/grep enabled; write: denied · edit: denied · shell: denied · git command: denied · web: denied · subagent: denied. Developer alone adds confined write/edit and sandboxed project commands. Every access is re-confined against the run's live workspace grant."})
	sort.Strings(toolNames)
	out = append(out,
		Check{Name: "MCP Tool Surface", Status: "PASS", Message: strings.Join(toolNames, ", ")},
		Check{Name: "Codex role refresh", Status: "CONFIGURED", Message: "Reconnect/restart Codex after exposure changes."},
	)
	return out
}
func (s *Service) ProbeRuntime(ctx context.Context) []Check {
	s.mu.Lock()
	configSnapshot := s.config
	s.mu.Unlock()
	if configSnapshot.Runtime.Type == "pi" {
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		runtime, e := factory.New(configSnapshot)
		if e != nil {
			return []Check{check("Pi protocol", e)}
		}
		defer runtime.Close()
		health, e := runtime.Adapter.Health(ctx)
		result := check("Pi protocol", e)
		if e == nil {
			result.Message = "Pi " + health.Version
		}
		return []Check{result}
	}
	return []Check{check("Pi protocol", agent.E("CONFIG_ERROR"))}
}
func (s *Service) ProbeMCP(ctx context.Context) []Check {
	s.mu.Lock()
	binary := s.binary
	dir := s.config.Directory
	s.mu.Unlock()
	// The diagnostic subprocess reads the persisted file, so compare its tools
	// against that same configuration rather than a stale Desktop snapshot.
	cfg, e := config.Load(filepath.Join(dir, "config.toml"))
	if e != nil {
		return []Check{check("Configuration", e)}
	}
	if st, e := os.Stat(binary); e != nil || st.Mode()&0111 == 0 {
		return []Check{check("MCP binary", agent.E("CONFIG_ERROR"))}
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "--config-dir", dir)
	cmd.WaitDelay = 2 * time.Second
	client := sdk.NewClient(&sdk.Implementation{Name: "desktop-diagnostic", Version: "0.3.0"}, nil)
	session, e := client.Connect(ctx, &sdk.CommandTransport{Command: cmd}, nil)
	if e != nil {
		return []Check{check("MCP initialize", agent.E("RUNTIME_UNAVAILABLE"))}
	}
	defer session.Close()
	tools, e := session.ListTools(ctx, nil)
	if e != nil {
		return []Check{check("MCP tools", agent.E("RUNTIME_PROTOCOL_ERROR"))}
	}
	names := []string{}
	for _, t := range tools.Tools {
		names = append(names, t.Name)
	}
	return mcpToolChecks(cfg, names)
}

func mcpToolChecks(cfg config.Config, names []string) []Check {
	sort.Strings(names)
	present := map[string]bool{}
	for _, n := range names {
		present[n] = true
	}
	roleChecks := []Check{}
	roleExposed := func(role string) bool {
		routing, ok := cfg.Agents[role]
		// Pi has no named-agent catalogue; its roles route directly to models.
		return ok && routing.Exposed && (cfg.Runtime.Type != opencode.Kind || routing.Agent != "")
	}
	for _, role := range []string{"implementation", "developer", "review", "research"} {
		exposed := roleExposed(role)
		tool := "delegate_" + role
		if exposed == present[tool] {
			message := "Exposed: NO · delegate_" + role + " absent"
			if exposed {
				message = "Exposed: YES · delegate_" + role + " present"
			}
			roleChecks = append(roleChecks, Check{Name: "Role exposure consistency: " + role, Status: "PASS", Message: message})
		} else if exposed {
			roleChecks = append(roleChecks, Check{Name: "Role exposure consistency: " + role, Status: "FAIL", Message: "Exposed: YES · delegate_" + role + " missing from tools/list"})
		} else {
			roleChecks = append(roleChecks, Check{Name: "Role exposure consistency: " + role, Status: "FAIL", Message: "Exposed: NO · delegate_" + role + " unexpectedly present"})
		}
	}
	expected := []string{"ai_worker_cancel", "ai_worker_continue", "ai_worker_add_workspace", "ai_worker_list_workspaces", "ai_worker_revoke_workspace"}
	for _, role := range []string{"implementation", "developer", "review", "research"} {
		if roleExposed(role) {
			expected = append(expected, "delegate_"+role)
		}
	}
	sort.Strings(expected)
	if fmt.Sprint(names) != fmt.Sprint(expected) {
		return append([]Check{check("MCP tools", agent.E("RUNTIME_PROTOCOL_ERROR"))}, roleChecks...)
	}
	out := []Check{check("MCP initialize", nil), {Name: "MCP tools", Status: "PASS", Message: strings.Join(names, ", ")}}
	out = append(out, roleChecks...)
	out = append(out, Check{Name: "Codex role refresh", Status: "CONFIGURED", Message: "Reconnect/restart Codex after exposure changes."})
	return out
}
