#!/usr/bin/env python3
"""Validate Session 12 incident/quarantine contracts and static boundary."""

from __future__ import annotations

import hashlib
import json
import re
import subprocess
import sys
from pathlib import Path
from typing import Any

ROOT = Path(__file__).resolve().parents[1]
REGISTRIES = {
    "contracts/incidents/model.yaml": ROOT / "contracts/incidents/model.yaml",
    "contracts/incidents/quarantine.yaml": ROOT / "contracts/incidents/quarantine.yaml",
}
LOCK = ROOT / "contracts/incidents-policy-locks.yaml"
FIXTURE = ROOT / "tests/fixtures/session-12/unknown-schema-canary.json"


def load(path: Path) -> dict[str, Any]:
    value = json.loads(path.read_text(encoding="utf-8"))
    if not isinstance(value, dict):
        raise ValueError(f"{path}: object required")
    return value


def digest(value: Any) -> str:
    raw = json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=False).encode()
    return hashlib.sha256(raw).hexdigest()


def committed_lock() -> dict[str, Any] | None:
    result = subprocess.run(
        ["git", "show", "HEAD:contracts/incidents-policy-locks.yaml"],
        cwd=ROOT, text=True, capture_output=True, check=False,
    )
    if result.returncode != 0:
        return None
    value = json.loads(result.stdout)
    return value if isinstance(value, dict) else None


def validate() -> list[str]:
    errors: list[str] = []
    model = load(REGISTRIES["contracts/incidents/model.yaml"])
    quarantine = load(REGISTRIES["contracts/incidents/quarantine.yaml"])
    locks = load(LOCK)
    fixture = load(FIXTURE)

    if set(model) != {
        "schema_version", "contract_version", "effective_at", "identity",
        "detector_states", "triage_states", "triage_note_categories",
        "triage_detector_separation", "occurrence", "read_model", "pagination",
        "recovery", "api",
    }:
        errors.append("incident model top-level schema changed")
    if model.get("detector_states") != ["open", "recovering", "resolved"]:
        errors.append("detector states changed")
    if model.get("triage_states") != ["new", "acknowledged", "investigating", "action_ready"]:
        errors.append("triage states changed")
    if model.get("api", {}).get("no_resolve_route") is not True:
        errors.append("incident API must not expose resolve")
    if "idempotency_key" not in model.get("occurrence", {}).get("fields", []):
        errors.append("occurrence idempotency key missing")
    if "later targeted audit" not in model.get("recovery", {}).get("rule", ""):
        errors.append("recovery must require a later targeted audit")

    if set(quarantine) != {
        "schema_version", "contract_version", "effective_at", "metadata_only",
        "manifest_fields", "value_states", "primitive_types", "field_path_policy",
        "prohibited_fields", "shape_semantics", "debug_bundle", "retention",
    }:
        errors.append("quarantine top-level schema changed")
    if quarantine.get("metadata_only") is not True:
        errors.append("quarantine must remain metadata-only")
    prohibited = set(quarantine.get("prohibited_fields", []))
    for required in {"prompt", "response", "reasoning", "tool_input", "tool_output", "environment", "credential", "resource_uri", "payload"}:
        if required not in prohibited:
            errors.append(f"quarantine prohibited field missing: {required}")
    if quarantine.get("debug_bundle", {}).get("construction") != "Generated from typed allowlisted fields, never database row serialization.":
        errors.append("debug bundle must be typed allowlist construction")

    records = locks.get("locks", [])
    expected = {
        "incidents.model/1": "contracts/incidents/model.yaml",
        "incidents.quarantine/1": "contracts/incidents/quarantine.yaml",
    }
    if locks.get("schema_version") != "kansoku.incidents-policy-locks/1" or len(records) != 2:
        errors.append("incident policy lock registry changed")
    for record in records:
        version = record.get("policy_version")
        registry = record.get("registry")
        if expected.get(version) != registry or not re.fullmatch(r"[0-9a-f]{64}", str(record.get("semantic_sha256"))):
            errors.append(f"invalid incident policy lock {version}")
            continue
        if record["semantic_sha256"] != digest(load(ROOT / registry)):
            errors.append(f"{registry}: semantic digest mismatch")
    historical = committed_lock()
    if historical is not None and records[: len(historical.get("locks", []))] != historical.get("locks", []):
        errors.append("incident policy lock history is not append-only")

    if fixture.get("synthetic") is not True:
        errors.append("Session 12 fixture must be synthetic")
    expected_fixture = fixture.get("expected", {})
    if expected_fixture != {
        "incident_count": 1, "quarantine_manifest_count": 1,
        "occurrence_count": 2, "raw_canary_matches": 0,
        "resolution_without_fresh_audit": False,
    }:
        errors.append("Session 12 fixture exit expectations changed")
    serialized = json.dumps(fixture, sort_keys=True)
    for forbidden in ("/Users/", "sk-", "AKIA", "BEGIN PRIVATE KEY"):
        if forbidden in serialized:
            errors.append(f"Session 12 fixture contains non-synthetic marker {forbidden}")

    required_code = {
        ROOT / "internal/dataplatform/incident_workbench.go": [
            "ORDER BY last_seen_at DESC, incident_id DESC",
            "IncidentListFormulaVersion",
        ],
        ROOT / "internal/runtime/api_incidents.go": [
            "subtle.ConstantTimeCompare", "invalid_cursor", "debug-bundle",
        ],
        ROOT / "internal/integrity/incident_workbench.go": [
            "fresh_targeted_audit_required", "SetIncidentTriage",
        ],
    }
    for path, snippets in required_code.items():
        source = path.read_text(encoding="utf-8")
        for snippet in snippets:
            if snippet not in source:
                errors.append(f"{path.relative_to(ROOT)} missing {snippet!r}")
    if "/api/v1/incidents/{opaque_id}/resolve" in (ROOT / "internal/runtime/api_incidents.go").read_text(encoding="utf-8"):
        errors.append("resolve route implemented")
    return errors


if __name__ == "__main__":
    found = validate()
    if found:
        for error in found:
            print(f"ERROR: {error}")
        sys.exit(1)
    print("Session 12 incident contracts: PASS")
