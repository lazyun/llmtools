package desktop

import (
	codexintegration "ai-worker-mcp/integrations/codex"
	"ai-worker-mcp/internal/localfs"
	"fmt"
	"os"
	"path/filepath"
)

// InstallSkill and RemoveSkill expose no caller-supplied paths: Desktop manages
// only the user-scoped llmTools folder, independently of MCP/runtime settings.
func (s *Service) InstallSkill() (SkillStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return installDelegationSkill(s.skillPath())
}

func (s *Service) RemoveSkill() (SkillStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return removeDelegationSkill(s.skillPath())
}

func delegationSkillDirectory(path string) (string, error) {
	if !filepath.IsAbs(path) || filepath.Base(path) != "SKILL.md" || filepath.Base(filepath.Dir(path)) != "llmtools-delegation" {
		return "", fmt.Errorf("Cannot determine the user llmTools Skill directory")
	}
	return filepath.Dir(path), nil
}

func installDelegationSkill(path string) (SkillStatus, error) {
	dir, err := delegationSkillDirectory(path)
	if err != nil {
		return SkillStatus{}, err
	}
	// Never follow an existing Skill-directory or metadata-directory symlink
	// when installing: it may point to a repository or another installation.
	if err = localfs.EnsureDirectory(dir); err != nil {
		return skillStatus(path), fmt.Errorf("Cannot inject llmTools Skill: %w", err)
	}
	if err = localfs.EnsureDirectory(filepath.Join(dir, "agents")); err != nil {
		return skillStatus(path), fmt.Errorf("Cannot inject llmTools Skill metadata: %w", err)
	}
	files := codexintegration.DelegationSkillFiles()
	// Publish SKILL.md last so an initial install is discoverable only after its
	// accompanying UI/invocation metadata has been written successfully.
	for _, name := range []string{"agents/openai.yaml", "SKILL.md"} {
		if err = localfs.AtomicWrite(filepath.Join(dir, filepath.FromSlash(name)), files[name]); err != nil {
			return skillStatus(path), fmt.Errorf("Cannot inject llmTools Skill: %w", err)
		}
	}
	return skillStatus(path), nil
}

func removeDelegationSkill(path string) (SkillStatus, error) {
	dir, err := delegationSkillDirectory(path)
	if err != nil {
		return SkillStatus{}, err
	}
	// RemoveAll unlinks symlinks rather than following their targets. The fixed
	// user-scoped Skill folder is the entire uninstall boundary.
	if err = os.RemoveAll(dir); err != nil {
		return skillStatus(path), fmt.Errorf("Cannot remove llmTools Skill: %w", err)
	}
	return skillStatus(path), nil
}
