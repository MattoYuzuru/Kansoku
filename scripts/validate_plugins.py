#!/usr/bin/env python3
import hashlib
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
CONTRACTS = ROOT / "contracts" / "plugins"
FILES = (
    "inventory-and-identity.yaml",
    "evidence-and-attribution.yaml",
    "metrics-and-privacy.yaml",
    "canary.yaml",
)

def load(path):
    return json.loads(path.read_text(encoding="utf-8"))

def digest(value):
    encoded = json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=False).encode()
    return hashlib.sha256(encoded).hexdigest()

def main():
    values = {name: load(CONTRACTS / name) for name in FILES}
    inventory = values["inventory-and-identity.yaml"]
    evidence = values["evidence-and-attribution.yaml"]
    metrics = values["metrics-and-privacy.yaml"]
    canary = values["canary.yaml"]
    assert all(value["version"] == "1.0.0" for value in values.values())
    assert set(inventory["relations"]) == {"bundles", "provides", "collides_with", "shadows"}
    assert set(evidence["independent_planes"]) == {"installed", "enabled", "loaded"}
    assert "exactly one current plugin owner" in evidence["child_fact_rule"]
    assert evidence["success_rule"].startswith("plugin outcome is unsupported")
    assert set(metrics["formulas"]) == {
        "plugin.loaded_sessions/1", "plugin.active_share/1", "plugin.cold_count/1",
    }
    assert metrics["content_endpoint"] is False
    assert {"plugin_content", "skill_content", "arguments", "results", "error_messages",
            "commands", "environment", "credentials", "unredacted_paths"} <= set(metrics["never_persist"])
    assert canary["expected"] == {
        "plugin_count": 1,
        "direct_child_count": 2,
        "loaded_is_not_installed": True,
        "one_tool_call_child_activity_count": 1,
        "plugin_success_state": "unsupported",
    }
    locks = load(ROOT / "contracts" / "plugins-policy-locks.yaml")
    assert locks["append_only"] is True
    assert len(locks["resources"]) == len(FILES)
    locked = {row["registry"]: row["semantic_sha256"] for row in locks["resources"]}
    for name, value in values.items():
        path = f"contracts/plugins/{name}"
        assert locked[path] == digest(value)
    required = [
        "component_relation_observations", "PluginObservatory(",
        "PluginProfile(", "persistPluginChildActivity(",
        'OutcomeState = "unsupported"',
    ]
    sources = "\n".join(
        (ROOT / path).read_text(encoding="utf-8")
        for path in (
            "internal/dataplatform/inventory.go",
            "internal/dataplatform/plugin_observatory.go",
            "internal/dataplatform/plugin_attribution.go",
        )
    )
    assert all(item in sources for item in required)
    print("Session 16 plugin contract validation passed.")

if __name__ == "__main__":
    main()
