import hashlib
import json
import pathlib
import re
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[1]
PLUGIN = ROOT / "integrations/codex/plugins/llmtools"
SKILL = PLUGIN / "skills/llmtools-delegation/SKILL.md"


class SkillIntegration(unittest.TestCase):
    def test_plugin_packages_skill_and_mcp(self):
        manifest = json.loads((PLUGIN / ".codex-plugin/plugin.json").read_text())
        self.assertEqual("llmtools", manifest["name"])
        self.assertEqual("1.4.0", manifest["version"])
        self.assertEqual("./skills/", manifest["skills"])
        self.assertEqual("./.mcp.json", manifest["mcpServers"])
        servers = json.loads((PLUGIN / ".mcp.json").read_text())["mcpServers"]
        self.assertIn("llmtools", servers)
        self.assertFalse(servers["llmtools"]["enabled"])

    def test_skill_contract_and_metadata(self):
        text = SKILL.read_text()
        frontmatter = json.loads(text.split("---", 2)[1])
        description = frontmatter["description"].lower()
        for required in ["implementation", "review", "research", "delegate_*", "parent-level decisions", "final acceptance"]:
            self.assertIn(required, description)
        for required in [
            "inspect the currently available", "substantive work", "Task Contract",
            "NEEDS_CONTEXT", "NEEDS_DECISION", "same `worker_run_id`",
            "ai_worker_cancel", "final acceptance", "hidden or unconfigured",
            "llmtools-delegation-v5", "Version: `1.4.0`",
        ]:
            self.assertIn(required, text)
        self.assertIn("Do not request execution capabilities", text)

    def test_skill_states_repository_read_scope(self):
        text = SKILL.read_text()
        for required in [
            "## Repository read",
            "server-controlled read-only repository tools (`read`, `glob`, `grep`)",
            "scoped to the workspace selected for the run",
            "never request broader filesystem access",
            "Writing, editing, shell or Git command execution, web access and subagents remain unavailable",
            "reading through a shell is not a substitute",
            "revoking the workspace in Desktop ends the access immediately",
            "Research stays context-only",
        ]:
            self.assertIn(required, text)
        # The Task Contract must survive repository read: a bare objective is
        # never enough, and only repository-absent context has to be copied.
        for required in [
            "single `prompt` field",
            "forwards this string verbatim",
            "Stop copying large amounts of repository source",
            "Send everything that is not in the repository",
        ]:
            self.assertIn(required, text)
        self.assertIn("never guesses a workspace and never falls back to another one", text)
        # Stale claims from before repository read existed must be gone.
        for stale in ["repository read remains disabled", "no repository capability"]:
            self.assertNotIn(stale, text)

    def test_installed_skill_matches_repository_canonical(self):
        installed = pathlib.Path.home() / ".agents/skills/llmtools-delegation"
        if not (installed / "SKILL.md").is_file():
            self.skipTest("no user-scoped Skill installation to compare")
        canonical = PLUGIN / "skills/llmtools-delegation"
        for relative in ["SKILL.md", "agents/openai.yaml"]:
            with self.subTest(relative=relative):
                want = hashlib.sha256((canonical / relative).read_bytes()).hexdigest()
                got = hashlib.sha256((installed / relative).read_bytes()).hexdigest()
                self.assertEqual(want, got, f"installed {relative} differs from the canonical copy")

    def test_skill_has_no_runtime_or_routing_specifics(self):
        text = SKILL.read_text().lower()
        for forbidden in [
            "gpt-5.6-sol", "gpt-5.6-terra", "union-alpha", "omniroute",
            "http://", "https://", "api key", "provider secret", " pid ",
            "runtime url", "opencode build",
        ]:
            self.assertNotIn(forbidden, text)
        self.assertIsNone(re.search(r"omni_router/|127\.0\.0\.1|localhost:\\d+", text))

    def test_implicit_invocation_and_owned_launcher(self):
        metadata = json.loads((PLUGIN / "skills/llmtools-delegation/agents/openai.yaml").read_text())
        self.assertTrue(metadata["policy"]["allow_implicit_invocation"])
        launcher = (PLUGIN / "scripts/launch-ai-worker-mcp").read_text()
        self.assertIn("LLMTOOLS_MCP_BINARY", launcher)
        self.assertIn("LLMTOOLS_CONFIG_DIR", launcher)

    def test_marketplace_points_only_at_owned_plugin(self):
        market = json.loads((ROOT / "integrations/codex/.agents/plugins/marketplace.json").read_text())
        self.assertEqual("llmtools-local", market["name"])
        self.assertEqual(["llmtools"], [p["name"] for p in market["plugins"]])
        self.assertEqual("./plugins/llmtools", market["plugins"][0]["source"]["path"])


if __name__ == "__main__":
    unittest.main()
