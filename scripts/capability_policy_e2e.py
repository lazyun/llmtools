#!/usr/bin/env python3
"""Probe the real configured MCP schema without invoking a model."""
import argparse
import json
import pathlib
import tomllib

from runtime_smoke import MCP


def properties(tool):
    schema = tool.get("inputSchema") or tool.get("input_schema") or {}
    return schema.get("properties") or {}


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--output", default=".smoke/capability-policy/schema.json")
    args = parser.parse_args()
    codex = tomllib.loads((pathlib.Path.home() / ".codex/config.toml").read_text())
    server = codex["mcp_servers"]["ai-worker"]
    server_args = server["args"]
    directory = pathlib.Path(server_args[server_args.index("--config-dir") + 1])
    worker_config = tomllib.loads((directory / "config.toml").read_text())
    expected_delegates = sorted(
        "delegate_" + role
        for role, routing in worker_config.get("agents", {}).items()
        if routing.get("exposed", True)
    )
    client = MCP(pathlib.Path(server["command"]), directory)
    try:
        listed = client.request("tools/list", {})["tools"]
        instructions = client.initialize_result.get("instructions", "")
    finally:
        client.close()
    delegates = [tool for tool in listed if tool["name"].startswith("delegate_")]
    checks = {
        "exposed_role_tools_match": sorted(tool["name"] for tool in delegates) == expected_delegates,
        "capabilities_absent": all("capabilities" not in properties(tool) for tool in delegates),
        "workspace_id_present": all("workspace_id" in properties(tool) for tool in delegates),
        "workspace_root_absent": all("workspace_root" not in properties(tool) for tool in delegates),
        "workspace_instructions_safe": (
            "Authorized workspaces" not in instructions
            or ("never points at a path" in instructions
                and "never guesses and never falls back" in instructions)
        ),
        "read_policy_described": (
            "Role execution policy, fixed by the llmTools server and not requestable by a caller" in instructions
            and all(
                f"- {tool['name'][len('delegate_'):]}: "
                + ("repository read-only (read, glob, grep)"
                   if tool["name"] in ("delegate_implementation", "delegate_review")
                   else ("project read/write and sandboxed" if tool["name"] == "delegate_developer" else "context-only; no repository access")) in instructions
                for tool in delegates
            )
        ),
        "denials_described": (
            "Native runtime shell/write tools, web and subagents are unavailable" in instructions
            and "Root .git writes and outbound network access (including localhost) are denied" in instructions
        ),
        "no_overclaim": not any(
            phrase in instructions for phrase in ["full repository access", "write access", "coding permission"]
        ),
        "no_path_fields": all(
            not ({field for field in ["path", "directory", "cwd"]} & set(properties(tool)))
            for tool in delegates
        ),
        "role_descriptions_accurate": all(
            (("server-controlled read-only" in tool.get("description", "")
              and "required when more than one grant is active" in tool.get("description", ""))
             if tool["name"] in ("delegate_implementation", "delegate_review")
             else (("read and modify project files" in tool.get("description", "") and "macOS sandbox" in tool.get("description", "")) if tool["name"] == "delegate_developer" else ("context-only" in tool.get("description", "")
                   and "no repository read" in tool.get("description", ""))))
            for tool in delegates
        ),
        "task_contract_present": all(
            all(field in properties(tool) for field in ["prompt", "workspace_id"])
            for tool in delegates
        ),
        "server_policy_described": all(
            "configured llmTools role policy" in tool.get("description", "") for tool in delegates
        ),
        "instructions_server_owned": "fixed by the llmTools server and not requestable by a caller" in instructions,
        "fixed_controls_present": all(
            name in [tool["name"] for tool in listed] for name in ["ai_worker_continue", "ai_worker_cancel"]
        ),
    }
    report = {
        "status": "PASS" if all(checks.values()) else "FAIL",
        "checks": checks,
        "delegate_schemas": {tool["name"]: tool.get("inputSchema") for tool in delegates},
        "instructions": instructions,
    }
    output = pathlib.Path(args.output)
    output.parent.mkdir(parents=True, exist_ok=True)
    output.write_text(json.dumps(report, ensure_ascii=False, indent=2))
    print(json.dumps({"status": report["status"], "checks": checks}))
    raise SystemExit(0 if report["status"] == "PASS" else 1)


if __name__ == "__main__":
    main()
