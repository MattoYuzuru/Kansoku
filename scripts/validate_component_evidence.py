#!/usr/bin/env python3
"""Validate the Session 14 component-evidence planes and their code bindings."""

from __future__ import annotations

import argparse
import hashlib
import json
import re
import subprocess
import sys
from pathlib import Path
from typing import Any


ROOT = Path(__file__).resolve().parents[1]
CONTRACT_PATH = ROOT / "contracts" / "component-evidence.yaml"
LOCK_PATH = ROOT / "contracts" / "component-evidence-policy-locks.yaml"


def load(path: Path) -> dict[str, Any]:
    value = json.loads(path.read_text(encoding="utf-8"))
    if not isinstance(value, dict):
        raise ValueError(f"{path}: object required")
    return value


def semantic_sha256(value: Any) -> str:
    encoded = json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=False).encode()
    return hashlib.sha256(encoded).hexdigest()


def trusted_lock_from_head() -> dict[str, Any] | None:
    result = subprocess.run(
        ["git", "show", "HEAD:contracts/component-evidence-policy-locks.yaml"],
        cwd=ROOT, check=False, capture_output=True, text=True,
    )
    if result.returncode != 0:
        return None
    try:
        value = json.loads(result.stdout)
    except json.JSONDecodeError:
        return None
    return value if isinstance(value, dict) else None


def validate(
    contract: dict[str, Any] | None = None,
    locks: dict[str, Any] | None = None,
    include_code: bool = True,
    historical: dict[str, Any] | None = None,
) -> list[str]:
    data = load(CONTRACT_PATH) if contract is None else contract
    lock = load(LOCK_PATH) if locks is None else locks
    errors: list[str] = []
    expected_top = {
        "schema_version", "contract_version", "effective_at", "planes",
        "assertion", "identity", "cold", "file_tree_metadata",
        "historical_compatibility",
    }
    if set(data) != expected_top or data.get("schema_version") != "kansoku.component-evidence/2":
        errors.append("component evidence top-level closed schema changed")
        return errors

    planes = data.get("planes", {})
    if planes.get("availability") != ["installed", "enabled", "exposed"]:
        errors.append("availability plane changed")
    if planes.get("runtime") != ["invoked", "loaded", "child_activity", "outcome"]:
        errors.append("runtime plane changed")
    if planes.get("optimization", {}).get("support") != "unsupported_until_session_20":
        errors.append("optimization plane must remain unsupported until Session 20")

    assertion = data.get("assertion", {})
    kinds = ["installed", "enabled", "exposed", "invoked", "loaded", "child_activity", "outcome"]
    if assertion.get("kinds") != kinds:
        errors.append("component assertion kind vocabulary changed")
    if assertion.get("modes") != ["explicit", "proactive", "nested", "not_observed"]:
        errors.append("component invocation mode vocabulary changed")
    if assertion.get("identity_resolution") != ["exact", "unresolved", "ambiguous"]:
        errors.append("component identity resolution vocabulary changed")
    if "terminal_contract_id" not in str(assertion.get("outcome_rule", "")):
        errors.append("outcome must require a registered terminal contract")

    identity = data.get("identity", {})
    if identity.get("promotion") != "exactly one inventory match":
        errors.append("identity promotion must require exactly one inventory match")
    if "incident" not in str(identity.get("zero_matches", "")):
        errors.append("zero identity matches must remain durable and incident-backed")
    if "no winner" not in str(identity.get("multiple_matches", "")):
        errors.append("ambiguous identity must never select a winner")

    cold = data.get("cold", {})
    if cold.get("formula_version") != "skill.cold_count/1":
        errors.append("cold formula version changed")
    if "complete exposure observation window" not in str(cold.get("eligible", "")):
        errors.append("cold population requires a complete exposure window")
    if "not_observed" not in str(cold.get("zero_rule", "")):
        errors.append("unexposed installed skills must remain not_observed, never cold")

    tree = data.get("file_tree_metadata", {})
    if tree.get("allowed_fields") != [
        "node_pseudonym", "parent_pseudonym", "entry_kind", "depth", "byte_count"
    ]:
        errors.append("file-tree metadata allowlist changed")
    if not {"name", "path", "content", "hash_of_content"}.issubset(set(tree.get("prohibited_fields", []))):
        errors.append("file-tree prohibited content surface weakened")
    if tree.get("content_endpoint") is not False:
        errors.append("component content endpoint must remain absent")

    compatibility = data.get("historical_compatibility", {})
    if compatibility.get("rewrite") is not False:
        errors.append("historical component telemetry must never be rewritten")
    if "never converted" not in str(compatibility.get("executed", "")):
        errors.append("legacy executed evidence must not be converted")

    if set(lock) != {"schema_version", "effective_at", "locks"} or \
            lock.get("schema_version") != "kansoku.component-evidence-policy-locks/1":
        errors.append("component evidence policy lock registry is not exact")
    records = lock.get("locks", [])
    if historical is not None:
        old = historical.get("locks", []) if isinstance(historical, dict) else []
        if records[:len(old)] != old:
            errors.append("component evidence policy locks must retain the append-only trusted prefix")
    ordinals: list[int] = []
    latest: dict[str, Any] | None = None
    for item in records if isinstance(records, list) else []:
        match = re.fullmatch(r"component-evidence/([1-9][0-9]*)", str(item.get("policy_version", "")))
        if set(item) != {"policy_version", "registry", "semantic_sha256"} or not match or \
                item.get("registry") != "contracts/component-evidence.yaml":
            errors.append("invalid component evidence policy lock entry")
            continue
        ordinals.append(int(match.group(1)))
        latest = item
    if sorted(ordinals) != list(range(1, len(ordinals) + 1)):
        errors.append("component evidence policy versions must start at 1 and remain contiguous")
    if latest is None or latest.get("semantic_sha256") != semantic_sha256(data):
        errors.append("component evidence semantic digest changed without reviewed policy version")

    if include_code:
        migration = (ROOT / "internal/dataplatform/migrations/0009_component_evidence_planes.up.sql").read_text()
        handoff = (ROOT / "internal/dataplatform/observability_handoff.go").read_text()
        query = (ROOT / "internal/dataplatform/skill_observatory.go").read_text()
        for snippet in (
            "component_assertions", "identity_resolution = 'ambiguous'",
            "assertion_kind = 'outcome'", "component_terminal_contracts",
        ):
            if snippet not in migration:
                errors.append(f"component evidence migration missing {snippet}")
        for snippet in (
            "ComponentResolution", "ComponentCandidateCount",
            "persistComponentIdentityIncident", 'scope.ComponentResolution = "ambiguous"',
        ):
            if snippet not in handoff:
                errors.append(f"component identity handoff missing {snippet}")
        for snippet in ("skill.cold_count/1", "partial_or_missing_exposure_window", "OutcomeState"):
            if snippet not in query:
                errors.append(f"skill observatory query missing {snippet}")
    return errors


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--json", action="store_true")
    args = parser.parse_args()
    errors = validate(historical=trusted_lock_from_head())
    result = {"status": "pass" if not errors else "fail", "errors": errors}
    if args.json:
        print(json.dumps(result, indent=2, sort_keys=True))
    elif errors:
        print("Session 14 component evidence validation failed:", file=sys.stderr)
        for error in errors:
            print(f"- {error}", file=sys.stderr)
    else:
        print("Session 14 component evidence validation passed.")
    return 1 if errors else 0


if __name__ == "__main__":
    raise SystemExit(main())
