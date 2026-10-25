#!/usr/bin/env python3
"""Isolated hidden-role Codex check; never changes persistent routing or Codex config."""
import argparse
import json
import pathlib
import subprocess
import tempfile
import tomllib

from runtime_smoke import MCP


def quoted(value):
    return json.dumps(value)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--output", default=".smoke/skill-integration/role-exposure.json")
    args = parser.parse_args()
    root = pathlib.Path(__file__).resolve().parents[1]
    binary = (root / "bin/ai-worker-mcp").resolve()
    source = pathlib.Path.home() / "Library/Application Support/AI Worker/config.toml"
    current = tomllib.loads(source.read_text())
    isolated = pathlib.Path(tempfile.mkdtemp(prefix="llmtools-role-hidden-"))
    runtime = current["runtime"]
    agents = current["agents"]
    lines = [
        "[general]", 'log_level = "info"', "task_timeout_seconds = 180",
        "max_context_bytes = 1048576", "", "[runtime]", 'type = "opencode"',
        f"mode = {quoted(runtime['mode'])}", f"binary = {quoted(runtime.get('binary', 'opencode'))}",
        'host = "127.0.0.1"', "port = 0", "",
    ]
    for role in ["implementation", "review", "research"]:
        route = agents[role]
        lines += [
            f"[agents.{role}]", f"exposed = {'false' if role == 'research' else 'true'}",
            f"agent = {quoted(route['agent'])}", f"model = {quoted(route.get('model', ''))}", "",
        ]
    lines += ["[storage]", 'database = "llmtools.db"', ""]
    (isolated / "config.toml").write_text("\n".join(lines))

    mcp = MCP(binary, isolated)
    try:
        tools = [entry["name"] for entry in mcp.request("tools/list", {})["tools"]]
    finally:
        mcp.close()

    prompt = "让一个外部 research agent 独立调查：FIFO 队列依次 enqueue(A), enqueue(B), dequeue() 后移出什么？最终判断由你负责。不要访问文件、网络或执行命令。"
    command = [
        "codex", "exec", "--ephemeral", "--skip-git-repo-check", "--sandbox", "read-only", "--json",
        "-C", tempfile.mkdtemp(prefix="llmtools-hidden-codex-"),
        "-c", f"mcp_servers.ai-worker.command={json.dumps(str(binary))}",
        "-c", f"mcp_servers.ai-worker.args={json.dumps(['--config-dir', str(isolated)])}",
    ]
    for tool in ["delegate_implementation", "delegate_review", "ai_worker_continue", "ai_worker_cancel"]:
        command += ["-c", f'mcp_servers.ai-worker.tools.{tool}.approval_mode="approve"']
    result = subprocess.run(command + [prompt], capture_output=True, text=True, timeout=180)
    calls = []
    messages = []
    for line in result.stdout.splitlines():
        try:
            event = json.loads(line)
        except ValueError:
            continue
        item = event.get("item") or {}
        if event.get("type") == "item.completed" and item.get("type") == "mcp_tool_call":
            calls.append(item.get("tool"))
        if event.get("type") == "item.completed" and item.get("type") == "agent_message":
            messages.append(item.get("text"))
    checks = {
        "research_absent_from_tools_list": "delegate_research" not in tools,
        "fixed_controls_present": all(value in tools for value in ["ai_worker_continue", "ai_worker_cancel"]),
        "visible_roles_present": all(value in tools for value in ["delegate_implementation", "delegate_review"]),
        "hidden_research_not_called": "delegate_research" not in calls,
        "codex_session_completed": result.returncode == 0,
    }
    report = {
        "status": "PASS" if all(checks.values()) else "FAIL",
        "isolated": True,
        "persistent_config_modified": False,
        "tools": tools,
        "codex_tool_calls": calls,
        "messages": messages,
        "checks": checks,
    }
    output = pathlib.Path(args.output)
    output.parent.mkdir(parents=True, exist_ok=True)
    output.write_text(json.dumps(report, ensure_ascii=False, indent=2))
    print(json.dumps({"status": report["status"], "checks": checks}))
    raise SystemExit(0 if report["status"] == "PASS" else 1)


if __name__ == "__main__":
    main()
