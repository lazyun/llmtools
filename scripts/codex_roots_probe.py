#!/usr/bin/env python3
"""Record a Codex MCP client's advertised roots and roots/list response."""

import argparse
import json
import pathlib
import sys


def send(message):
    sys.stdout.write(json.dumps(message, separators=(",", ":")) + "\n")
    sys.stdout.flush()


def save(path, observation):
    path.write_text(json.dumps(observation, indent=2, sort_keys=True) + "\n")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--output", required=True)
    args = parser.parse_args()
    output = pathlib.Path(args.output)
    output.parent.mkdir(parents=True, exist_ok=True)
    observation = {"initialize": None, "roots_list": None, "requests": []}
    roots_request_id = "llmtools-roots-probe"

    for raw_line in sys.stdin:
        try:
            message = json.loads(raw_line)
        except json.JSONDecodeError:
            continue
        method = message.get("method")
        request_id = message.get("id")
        if method == "initialize" and request_id is not None:
            observation["initialize"] = message.get("params")
            save(output, observation)
            send(
                {
                    "jsonrpc": "2.0",
                    "id": request_id,
                    "result": {
                        "protocolVersion": (message.get("params") or {}).get(
                            "protocolVersion", "2025-06-18"
                        ),
                        "capabilities": {"tools": {}},
                        "serverInfo": {"name": "codex-roots-probe", "version": "1"},
                    },
                }
            )
        elif method == "notifications/initialized":
            send(
                {
                    "jsonrpc": "2.0",
                    "id": roots_request_id,
                    "method": "roots/list",
                    "params": {},
                }
            )
        elif request_id == roots_request_id and method is None:
            observation["roots_list"] = {
                key: message[key] for key in ("result", "error") if key in message
            }
            save(output, observation)
        elif method in {
            "resources/list",
            "resources/templates/list",
            "prompts/list",
            "completion/complete",
        } and request_id is not None:
            observation["requests"].append(method)
            if method == "resources/list":
                result = {"resources": []}
            elif method == "resources/templates/list":
                result = {"resourceTemplates": []}
            elif method == "prompts/list":
                result = {"prompts": []}
            else:
                result = {"completion": {"values": []}}
            send({"jsonrpc": "2.0", "id": request_id, "result": result})
        elif method == "tools/list" and request_id is not None:
            observation["requests"].append(method)
            send({"jsonrpc": "2.0", "id": request_id, "result": {"tools": []}})
        elif method == "ping" and request_id is not None:
            send({"jsonrpc": "2.0", "id": request_id, "result": {}})
        elif method and request_id is not None:
            observation["requests"].append(method)
            send({"jsonrpc": "2.0", "id": request_id, "result": {}})

    save(output, observation)


if __name__ == "__main__":
    main()
