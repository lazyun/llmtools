#!/usr/bin/env python3
"""Dependency-free validation for the repository-owned Codex plugin package."""
import json
import pathlib
import re
import sys

root = pathlib.Path(__file__).resolve().parents[1] / "integrations/codex/plugins/llmtools"
errors = []

try:
    manifest = json.loads((root / ".codex-plugin/plugin.json").read_text())
    for field in ["name", "version", "description", "author", "interface", "skills", "mcpServers"]:
        if field not in manifest:
            errors.append(f"plugin.json missing {field}")
    if not re.fullmatch(r"\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.-]+)?", manifest.get("version", "")):
        errors.append("plugin version is not semver")
    if manifest.get("skills") != "./skills/" or manifest.get("mcpServers") != "./.mcp.json":
        errors.append("plugin component paths are not canonical")
except (OSError, json.JSONDecodeError) as exc:
    errors.append(f"invalid plugin.json: {exc}")

for relative in [
    ".mcp.json",
    "skills/llmtools-delegation/SKILL.md",
    "skills/llmtools-delegation/agents/openai.yaml",
    "scripts/launch-ai-worker-mcp",
]:
    if not (root / relative).is_file():
        errors.append(f"missing {relative}")

try:
    servers = json.loads((root / ".mcp.json").read_text()).get("mcpServers")
    if not isinstance(servers, dict) or not servers:
        errors.append(".mcp.json has no server definition")
except (OSError, json.JSONDecodeError) as exc:
    errors.append(f"invalid .mcp.json: {exc}")

if errors:
    print("\n".join(errors), file=sys.stderr)
    raise SystemExit(1)
print(f"Plugin validation passed: {root}")
