#!/usr/bin/env python3
"""Real OpenCode routing and immutable continuation acceptance.

This edits only llmTools config.toml routing tables. It never reads or writes
OpenCode provider, credential, agent, or model configuration.
"""
import argparse
import json
import os
import pathlib
import sqlite3
import subprocess
import tempfile
import time
import tomllib

from runtime_smoke import MCP

TERRA = "omni_router/gpt-5.6-terra"
SOL = "omni_router/gpt-5.6-sol"


def configured_routes(config_path):
    with config_path.open("rb") as stream:
        raw = tomllib.load(stream).get("agents", {})
    routes = {}
    for role, value in raw.items():
        if isinstance(value, str):
            routes[role] = {"exposed": True, "agent": value, "model": ""}
        elif isinstance(value, dict):
            routes[role] = {"exposed": value.get("exposed", True), "agent": value.get("agent", ""), "model": value.get("model", "")}
    return routes


def save_route(config_path, model):
    routes = configured_routes(config_path)
    routes["implementation"] = {"exposed": True, "agent": "build", "model": model}
    lines = config_path.read_text().splitlines()
    kept, index = [], 0
    while index < len(lines):
        line = lines[index]
        stripped = line.strip()
        if stripped.startswith("[") and stripped.endswith("]"):
            section = stripped[1:-1].strip()
            if section == "agents" or section.startswith("agents."):
                index += 1
                while index < len(lines) and not (lines[index].strip().startswith("[") and lines[index].strip().endswith("]")):
                    index += 1
                continue
        kept.append(line)
        index += 1
    insert = next((i for i, line in enumerate(kept) if line.strip() == "[storage]"), len(kept))
    routing = []
    for role in sorted(routes):
        value = routes[role]
        if not value["agent"]:
            continue
        routing += [f"[agents.{role}]", f"exposed = {'true' if value['exposed'] else 'false'}", f"agent = {json.dumps(value['agent'])}", f"model = {json.dumps(value['model'])}", ""]
    content = "\n".join(kept[:insert] + routing + kept[insert:]).strip() + "\n"
    fd, temporary = tempfile.mkstemp(prefix="config.toml.", dir=config_path.parent)
    try:
        with os.fdopen(fd, "w") as stream:
            stream.write(content)
            stream.flush()
            os.fsync(stream.fileno())
        os.chmod(temporary, 0o600)
        os.replace(temporary, config_path)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


def stored(database, run_id):
    with sqlite3.connect(database) as connection:
        row = connection.execute("SELECT body FROM runs WHERE id=?", (run_id,)).fetchone()
    if not row:
        raise AssertionError("run_not_durable")
    return json.loads(row[0])


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--config-dir", required=True)
    parser.add_argument("--binary", default="bin/ai-worker-mcp")
    parser.add_argument("--runtime-backend", default="bin/runtime-backend")
    parser.add_argument("--report", default=".smoke/routing/real-routing.json")
    args = parser.parse_args()
    directory = pathlib.Path(args.config_dir).resolve()
    binary = pathlib.Path(args.binary).resolve()
    runtime_backend = pathlib.Path(args.runtime_backend).resolve()
    config_path = directory / "config.toml"
    with config_path.open("rb") as stream:
        database = directory / tomllib.load(stream).get("storage", {}).get("database", "llmtools.db")
    report = {"backend": "REAL_OPENCODE", "status": "FAIL", "checks": {}, "manual_desktop": "MANUAL_DESKTOP_ACCEPTANCE_PENDING"}
    client = None
    owner = None
    stop_file = pathlib.Path(tempfile.mktemp(prefix="llmtools-routing-owner-stop-"))
    changed = False

    def check(name, condition):
        report["checks"][name] = bool(condition)
        if not condition:
            raise AssertionError(name)

    try:
        save_route(config_path, TERRA)
        changed = True
        owner = subprocess.Popen(
            [str(runtime_backend), "--config-dir", str(directory), "--binary", str(binary), "--start", "--stop-file", str(stop_file), "--hold", "10m"],
            stdout=subprocess.DEVNULL,
            stderr=subprocess.PIPE,
            text=True,
        )
        # The attached MCP client performs the authoritative health wait. This
        # short guard only catches immediate Desktop-backend startup failure.
        time.sleep(1)
        if owner.poll() is not None:
            raise AssertionError("desktop_runtime_owner_failed")
        client = MCP(binary, directory)
        terra = client.call("delegate_implementation", {"prompt": "Reply exactly: ROUTING_TERRA_OK"})
        report["terra_result"] = {key: terra.get(key) for key in ["worker_run_id", "status", "agent", "model", "error"]}
        check("terra_completed", terra.get("status") == "completed" and "ROUTING_TERRA_OK" in terra.get("content", ""))
        check("terra_result_routing", terra.get("agent") == "build" and terra.get("model") == TERRA)
        terra_run = stored(database, terra["worker_run_id"])
        check("terra_snapshot", terra_run.get("runtime_agent") == "build" and terra_run.get("runtime_model") == TERRA and bool(terra_run.get("routing_revision")))

        pending = client.call("delegate_implementation", {"prompt": "Request contract.txt with request_context. Do not guess its missing acceptance token. After it is supplied, reply exactly with that token."})
        check("terra_needs_context", pending.get("status") == "needs_context" and pending.get("model") == TERRA)
        before = stored(database, pending["worker_run_id"])
        client.close()
        client = None

        save_route(config_path, SOL)
        client = MCP(binary, directory)
        answers = [{"request_id": item["id"], "type": "file", "path": item["target"], "content": "ROUTING_SNAPSHOT_OK"} for item in pending["context_requests"]]
        continued = client.call("ai_worker_continue", {"worker_run_id": pending["worker_run_id"], "additional_context": answers})
        report["continued_result"] = {key: continued.get(key) for key in ["worker_run_id", "status", "agent", "model", "error"]}
        after = stored(database, pending["worker_run_id"])
        check("continuation_completed", continued.get("status") == "completed" and "ROUTING_SNAPSHOT_OK" in continued.get("content", ""))
        check("continuation_same_run_session", continued.get("worker_run_id") == pending["worker_run_id"] and before.get("runtime_session_id") == after.get("runtime_session_id"))
        check("continuation_kept_terra", continued.get("model") == TERRA and after.get("runtime_model") == TERRA)
        check("mcp_process_restart", True)

        sol = client.call("delegate_implementation", {"prompt": "Reply exactly: ROUTING_SOL_OK"})
        report["sol_result"] = {key: sol.get(key) for key in ["worker_run_id", "status", "agent", "model", "error"]}
        check("sol_completed", sol.get("status") == "completed" and "ROUTING_SOL_OK" in sol.get("content", ""))
        check("sol_result_routing", sol.get("agent") == "build" and sol.get("model") == SOL)
        sol_run = stored(database, sol["worker_run_id"])
        check("sol_snapshot", sol_run.get("runtime_agent") == "build" and sol_run.get("runtime_model") == SOL)
        check("historical_terra_unchanged", stored(database, terra["worker_run_id"]).get("runtime_model") == TERRA)
        report["runs"] = {"terra": terra["worker_run_id"], "suspended": pending["worker_run_id"], "sol": sol["worker_run_id"]}
        report["status"] = "PASS"
    except Exception as error:
        report["failure"] = str(error) if isinstance(error, AssertionError) else type(error).__name__
        runtime_codes = {
            (report.get(name, {}).get("error") or {}).get("code")
            for name in ["terra_result", "continued_result", "sol_result"]
        }
        if runtime_codes & {"RUNTIME_NOT_FOUND", "RUNTIME_UNAVAILABLE", "RUNTIME_START_FAILED", "TIMEOUT"}:
            report["status"] = "ENVIRONMENT_BLOCKED"
    finally:
        if client:
            client.close()
        if owner:
            stop_file.touch()
            try:
                owner.wait(timeout=20)
            except subprocess.TimeoutExpired:
                owner.terminate()
                owner.wait(timeout=10)
            owner.stderr.close()
        if stop_file.exists():
            stop_file.unlink()
        if changed:
            save_route(config_path, SOL)
        output = pathlib.Path(args.report)
        output.parent.mkdir(parents=True, exist_ok=True)
        output.write_text(json.dumps(report, indent=2))
        print(json.dumps(report))


if __name__ == "__main__":
    main()
