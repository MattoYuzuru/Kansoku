#!/usr/bin/env python3
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
CONTRACTS = ROOT / "contracts" / "mcp"

def load(name):
    return json.loads((CONTRACTS / name).read_text())

def main():
    inventory = load("inventory-and-capabilities.yaml")
    connections = load("connection-lifecycle.yaml")
    calls = load("call-lifecycle.yaml")
    metrics = load("metrics-and-privacy.yaml")
    locks = json.loads((ROOT / "contracts" / "mcp-policy-locks.yaml").read_text())
    assert inventory["version"] == connections["version"] == calls["version"] == metrics["version"] == "1.0.0"
    assert set(connections["states"]) == {"configured","connecting","connected","failed","disconnected","timed_out","unknown"}
    assert "completed" in calls["states"] and "execution_error" in calls["states"] and "protocol_error" in calls["states"]
    assert calls["user_task_success_inference"] is False
    forbidden = set(metrics["never_persist"])
    assert {"arguments","results","error_messages","urls","commands","environment","resource_uris"} <= forbidden
    assert len(locks["resources"]) == 4 and locks["append_only"] is True
    print("Session 15 MCP contract validation passed.")

if __name__ == "__main__":
    main()
