package desktop

import (
	codexintegration "ai-worker-mcp/integrations/codex"
	"os"
	"path/filepath"
	"testing"
)

func TestDelegationSkillInstallUpdateAndRemoval(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".agents", "skills", "llmtools-delegation", "SKILL.md")
	sibling := filepath.Join(home, ".agents", "skills", "another-skill", "SKILL.md")
	config := filepath.Join(home, ".codex", "config.toml")
	for _, file := range []string{sibling, config} {
		if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte("keep"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if status := skillStatus(path); status.CanRemove || status.Status != "missing" {
		t.Fatal(status)
	}
	installed, err := installDelegationSkill(path)
	if err != nil || installed.Status != "installed" || !installed.CanRemove || installed.Version != expectedSkillVersion || installed.Fingerprint != expectedSkillFingerprint {
		t.Fatalf("installed: %+v %v", installed, err)
	}
	// Update a previously installed version using the same bundled sources.
	if err := os.WriteFile(path, []byte("Version: `0.1.0`\nFingerprint: `old`"), 0600); err != nil {
		t.Fatal(err)
	}
	custom := filepath.Join(filepath.Dir(path), "custom-notes.txt")
	if err := os.WriteFile(custom, []byte("personal notes"), 0600); err != nil {
		t.Fatal(err)
	}
	installed, err = installDelegationSkill(path)
	if err != nil || installed.Status != "installed" {
		t.Fatal(installed, err)
	}
	for name, want := range codexintegration.DelegationSkillFiles() {
		got, err := os.ReadFile(filepath.Join(filepath.Dir(path), filepath.FromSlash(name)))
		if err != nil || string(got) != string(want) {
			t.Fatalf("bundled %s missing or stale: %v", name, err)
		}
	}
	if got, err := os.ReadFile(custom); err != nil || string(got) != "personal notes" {
		t.Fatal("update removed custom file", err)
	}
	removed, err := removeDelegationSkill(path)
	if err != nil || removed.Status != "missing" || removed.CanRemove {
		t.Fatal(removed, err)
	}
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Fatal("Skill folder remains", err)
	}
	for _, file := range []string{sibling, config} {
		if got, err := os.ReadFile(file); err != nil || string(got) != "keep" {
			t.Fatal("unrelated file modified", file, err)
		}
	}
	if _, err := removeDelegationSkill(path); err != nil {
		t.Fatal("removing an absent Skill is not idempotent", err)
	}
}

func TestDelegationSkillNeverWritesThroughSymlinks(t *testing.T) {
	for _, location := range []string{"root", "agents", "SKILL.md"} {
		t.Run(location, func(t *testing.T) {
			base, outside := t.TempDir(), t.TempDir()
			path := filepath.Join(base, "llmtools-delegation", "SKILL.md")
			target := filepath.Join(outside, "keep")
			if err := os.WriteFile(target, []byte("keep"), 0600); err != nil {
				t.Fatal(err)
			}
			link, destination := filepath.Dir(path), outside
			if location != "root" {
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if location == "agents" {
					link = filepath.Join(filepath.Dir(path), "agents")
				} else {
					link = path
					destination = target
				}
			}
			if err := os.Symlink(destination, link); err != nil {
				t.Fatal(err)
			}
			if _, err := installDelegationSkill(path); err == nil {
				t.Fatal("injection followed a symlink")
			}
			if _, err := removeDelegationSkill(path); err != nil {
				t.Fatal(err)
			}
			if got, err := os.ReadFile(target); err != nil || string(got) != "keep" {
				t.Fatal("external target modified", err)
			}
		})
	}
}

func TestDelegationSkillRejectsInvalidOperationPaths(t *testing.T) {
	for _, path := range []string{"", "SKILL.md", "/", "/tmp/another-skill/SKILL.md"} {
		if _, err := installDelegationSkill(path); err == nil {
			t.Fatalf("install accepted %q", path)
		}
		if _, err := removeDelegationSkill(path); err == nil {
			t.Fatalf("remove accepted %q", path)
		}
	}
}
