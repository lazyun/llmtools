// Package codex packages the canonical Codex integration for offline Desktop use.
package codex

import _ "embed"

//go:embed plugins/llmtools/skills/llmtools-delegation/SKILL.md
var delegationSkill string

//go:embed plugins/llmtools/skills/llmtools-delegation/agents/openai.yaml
var delegationMetadata string

// DelegationSkillFiles returns fresh copies of the files installed in the user
// profile. Embedding the canonical sources keeps packaged apps independent of
// a source checkout and avoids maintaining a second version of the Skill.
func DelegationSkillFiles() map[string][]byte {
	return map[string][]byte{
		"SKILL.md":           []byte(delegationSkill),
		"agents/openai.yaml": []byte(delegationMetadata),
	}
}
