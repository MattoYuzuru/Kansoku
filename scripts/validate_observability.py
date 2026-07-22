#!/usr/bin/env python3
"""Independent closed-world validator for the Session 03 architecture."""

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
CONTRACT_DIR = ROOT / "contracts" / "observability"
LOCK_PATH = ROOT / "contracts" / "observability-policy-locks.yaml"
FIXTURE_PATH = ROOT / "tests" / "fixtures" / "session-03" / "shared-scenario.json"

FILES = ("envelope.yaml", "ingress.yaml", "lifecycles.yaml", "reconciliation.yaml")
EVENT_FIELDS = ["spec_version", "event_id", "fact_key", "event_type", "emitted_at", "observed_at", "ingested_at", "timestamp_quality", "source", "scope", "subject", "measurements", "value_state", "outcome", "correlation_status", "lifecycle"]
EVIDENCE_FIELDS = ["evidence_id", "event_id", "source", "tier", "confidence", "completeness", "replay_count", "first_seen_at", "last_seen_at", "sanitizer_version", "privacy_contract_sha256", "assertion"]
EVIDENCE_ASSERTION_FIELDS = ["event_type", "outcome", "value_state"]
SOURCE_LIFECYCLE = ["discovered", "configured", "connected", "producing", "reconciled"]
EVENT_LIFECYCLE = ["received", "sanitized", "validated", "normalized", "deduplicated", "correlated", "reconciled"]
CORRELATIONS = ["exact", "candidate", "ambiguous", "unmatched"]
COMPLETENESS = ["complete", "partial", "degraded", "unknown", "unsupported"]
PROTOCOLS = {"hook_http", "otlp_http_logs", "otlp_http_metrics", "otlp_http_traces", "otlp_grpc_logs", "otlp_grpc_metrics", "otlp_grpc_traces", "transcript_jsonl"}
FORBIDDEN = {"prompt", "response", "body", "content", "source_code", "tool_input", "tool_output", "command", "path", "environment", "credential", "exception", "attributes", "payload", "error_message"}
DIRECT_MODULES = {"go.opentelemetry.io/proto/otlp": "v1.10.0", "google.golang.org/grpc": "v1.82.1", "google.golang.org/protobuf": "v1.36.11"}
LOCK_BASES = {
    "observability.envelope": "contracts/observability/envelope.yaml",
    "observability.ingress": "contracts/observability/ingress.yaml",
    "observability.lifecycles": "contracts/observability/lifecycles.yaml",
    "observability.reconciliation": "contracts/observability/reconciliation.yaml",
}


def load(path: Path) -> dict[str, Any]:
    value = json.loads(path.read_text(encoding="utf-8"))
    if not isinstance(value, dict):
        raise ValueError(f"{path}: object required")
    return value


def semantic_sha256(value: Any) -> str:
    encoded = json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=False).encode()
    return hashlib.sha256(encoded).hexdigest()


def registries() -> dict[str, dict[str, Any]]:
    return {f"contracts/observability/{name}": load(CONTRACT_DIR / name) for name in FILES}


def validate(candidate: dict[str, dict[str, Any]] | None = None, locks: dict[str, Any] | None = None, include_code: bool = True, historical: dict[str, Any] | None = None) -> list[str]:
    data = registries() if candidate is None else candidate
    lock = load(LOCK_PATH) if locks is None else locks
    errors: list[str] = []
    if set(data) != {f"contracts/observability/{name}" for name in FILES}:
        errors.append("observability registry set is not exact")
        return errors
    by_name = {Path(path).name: value for path, value in data.items()}
    envelope, ingress, lifecycles, reconciliation = (by_name[name] for name in FILES)

    expected_top = {
        "envelope.yaml": {"schema_version", "contract_version", "effective_at", "event_spec_version", "event_fields", "source_fields", "scope_fields", "subject_fields", "measurement_fields", "evidence_fields", "evidence_assertion_fields", "forbidden_fields", "identity", "time"},
        "ingress.yaml": {"schema_version", "contract_version", "effective_at", "protocols", "limits", "otlp", "source_schemas", "otlp_safe_attributes", "ignored_prohibited_otlp_surfaces", "unknown_schema", "durability"},
        "lifecycles.yaml": {"schema_version", "contract_version", "effective_at", "source_primary", "source_branches", "event_primary", "terminal_failure", "correlation_states", "completeness_states", "evidence_tiers", "confidence_ceilings", "invariants"},
        "reconciliation.yaml": {"schema_version", "contract_version", "effective_at", "capability", "logical_fixture", "expected_lanes", "one_fact_rule", "complete_when", "partial_when", "dedupe", "reorder", "late", "watermark_fields", "silence", "recovery", "contradiction", "durable_state_validation"},
    }
    for name, fields in expected_top.items():
        if set(by_name[name]) != fields:
            errors.append(f"{name}: top-level closed schema changed")
    if envelope.get("event_fields") != EVENT_FIELDS or envelope.get("evidence_fields") != EVIDENCE_FIELDS or envelope.get("evidence_assertion_fields") != EVIDENCE_ASSERTION_FIELDS:
        errors.append("canonical event/evidence fields changed")
    if set(envelope.get("forbidden_fields", [])) != FORBIDDEN:
        errors.append("forbidden durable fields changed")
    if lifecycles.get("source_primary") != SOURCE_LIFECYCLE or lifecycles.get("event_primary") != EVENT_LIFECYCLE:
        errors.append("source/event lifecycle changed")
    if lifecycles.get("correlation_states") != CORRELATIONS or lifecycles.get("completeness_states") != COMPLETENESS:
        errors.append("correlation/completeness states changed")
    if lifecycles.get("terminal_failure") != "metadata_only_quarantine":
        errors.append("unknown or poison input must use metadata-only quarantine")
    protocol_ids = [item.get("id") for item in ingress.get("protocols", []) if isinstance(item, dict)]
    if set(protocol_ids) != PROTOCOLS or len(protocol_ids) != len(PROTOCOLS):
        errors.append("HTTP/gRPC protobuf and import protocol set changed")
    for protocol in ingress.get("protocols", []):
        if not isinstance(protocol, dict) or set(protocol) != {"id", "route", "encoding", "auth", "ack", "failure"}:
            errors.append("protocol records must be closed and typed")
            continue
        if "after_durable_commit" not in protocol["ack"] and "same_transaction" not in protocol["ack"]:
            errors.append(f"{protocol['id']}: acknowledgement is not bound to durability")
    otlp = ingress.get("otlp", {})
    if otlp.get("specification") != "1.10.0" or otlp.get("proto_module") != "go.opentelemetry.io/proto/otlp@v1.10.0" or set(otlp.get("signals", [])) != {"logs", "metrics", "traces"}:
        errors.append("OTLP version/protobuf/signal contract changed")
    if ingress.get("limits", {}).get("compression") != "rejected_by_reviewed_session02_policy" or otlp.get("gzip") != "known_conformance_gap":
        errors.append("OTLP gzip gap must remain explicit until privacy review")
    unknown = ingress.get("unknown_schema", {})
    if unknown.get("raw_bytes") is not False or unknown.get("incident") != "degraded" or "schema_fingerprint" not in unknown.get("durable_fields", []):
        errors.append("unknown schema metadata-only degraded behavior changed")
    durability = ingress.get("durability", {})
    if durability.get("transaction") != "temp_write_file_fsync_atomic_rename_directory_fsync" or "PostgreSQL" not in durability.get("not_claimed", []):
        errors.append("Session03 durability boundary is overclaimed or weakened")
    if reconciliation.get("expected_lanes") != ["hook_http", "otlp", "transcript_jsonl"] or reconciliation.get("logical_fixture") != "tests/fixtures/session-03/shared-scenario.json":
        errors.append("shared three-lane reconciliation scenario changed")
    if reconciliation.get("silence", {}).get("true_inactivity") != "sets inactivity and opens no gap incident":
        errors.append("true inactivity and source loss collapsed")

    errors.extend(validate_policy_locks(lock, data, historical))

    if include_code:
        errors.extend(validate_code_and_fixture())
    return errors


def validate_policy_locks(lock: dict[str, Any], data: dict[str, dict[str, Any]], historical: dict[str, Any] | None = None) -> list[str]:
    errors: list[str] = []
    if set(lock) != {"schema_version", "effective_at", "locks"} or lock.get("schema_version") != "kansoku.observability-policy-locks/1":
        errors.append("observability policy lock registry is not exact")
    records = lock.get("locks", [])
    if not isinstance(records, list):
        return errors + ["observability policy locks must be a list"]
    if historical is not None:
        old = historical.get("locks", []) if isinstance(historical, dict) else []
        if records[:len(old)] != old:
            errors.append("observability policy lock list must retain the exact append-only trusted prefix")
    latest: dict[str, tuple[int, dict[str, Any]]] = {}
    seen: set[str] = set()
    ordinals: dict[str, list[int]] = {base: [] for base in LOCK_BASES}
    for item in records:
        if not isinstance(item, dict) or set(item) != {"policy_version", "registry", "semantic_sha256"}:
            errors.append("observability policy lock entries must be closed")
            continue
        version = item.get("policy_version", "")
        match = re.fullmatch(r"(observability\.(?:envelope|ingress|lifecycles|reconciliation))/([1-9][0-9]*)", version)
        if not match or item.get("registry") != LOCK_BASES.get(match.group(1)) or re.fullmatch(r"[0-9a-f]{64}", str(item.get("semantic_sha256"))) is None:
            errors.append("observability policy lock entry has invalid version/registry/digest binding")
            continue
        if version in seen:
            errors.append(f"duplicate observability policy version {version}")
        seen.add(version)
        ordinal = int(match.group(2))
        ordinals[match.group(1)].append(ordinal)
        if match.group(1) not in latest or ordinal > latest[match.group(1)][0]:
            latest[match.group(1)] = (ordinal, item)
    for base, path in LOCK_BASES.items():
        values = sorted(ordinals[base])
        if values != list(range(1, values[-1] + 1)) if values else True:
            errors.append(f"{base}: policy versions must start at 1 and remain contiguous")
        current = latest.get(base)
        if current is None or current[1].get("semantic_sha256") != semantic_sha256(data[path]):
            errors.append(f"{path}: semantic digest changed without reviewed policy version")
    return errors


def trusted_lock_from_head() -> dict[str, Any] | None:
    result = subprocess.run(
        ["git", "show", "HEAD:contracts/observability-policy-locks.yaml"], cwd=ROOT,
        check=False, capture_output=True, text=True,
    )
    if result.returncode != 0:
        return None
    value = json.loads(result.stdout)
    return value if isinstance(value, dict) else None


def validate_code_and_fixture() -> list[str]:
    errors: list[str] = []
    fixture = load(FIXTURE_PATH)
    if fixture.get("synthetic") is not True or fixture.get("expected") != {"facts": 1, "evidence": 3, "completeness": "complete", "correlation": "exact", "duplicate_fact_inflation": 0}:
        errors.append("shared scenario expected outcome changed")
    if {lane.get("id") for lane in fixture.get("lanes", [])} != {"hook_http", "otlp_log", "transcript_jsonl"}:
        errors.append("shared fixture must cover all three lanes")
    serialized_fixture = json.dumps(fixture, sort_keys=True)
    if "/Users/" in serialized_fixture or "@example.com" in serialized_fixture or "sk-" in serialized_fixture:
        errors.append("Session03 fixture is not sanitized/synthetic")
    source = "\n".join((ROOT / "internal" / "observability" / name).read_text(encoding="utf-8") for name in ("types.go", "store.go", "normalize.go", "ingest.go", "importer.go", "otlp.go", "routes.go"))
    for required in ("type Event struct", "type Evidence struct", "type Quarantine struct", "os.Rename", "file.Sync()", "directory.Sync()", "privacy.FixtureSourceSchema", "RegisterLogsServiceServer", "RegisterMetricsServiceServer", "RegisterTraceServiceServer"):
        if required not in source:
            errors.append(f"Go boundary missing {required}")
    event_block = re.search(r"type Event struct \{(.*?)\n\}", source, re.S)
    if not event_block:
        errors.append("Event struct not found")
    elif any(f'json:"{field}"' in event_block.group(1) for field in FORBIDDEN):
        errors.append("Event struct contains prohibited durable field")
    go_mod = (ROOT / "go.mod").read_text(encoding="utf-8")
    for module, version in DIRECT_MODULES.items():
        if not re.search(rf"^\s*{re.escape(module)}\s+{re.escape(version)}\s*$", go_mod, re.M):
            errors.append(f"direct module is not pinned: {module}@{version}")
    vendor_modules = ROOT / "vendor" / "modules.txt"
    if not vendor_modules.is_file():
        errors.append("vendored offline dependency inventory missing")
    return errors


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--json", action="store_true")
    args = parser.parse_args()
    try:
        errors = validate(historical=trusted_lock_from_head())
    except (OSError, ValueError, json.JSONDecodeError) as exc:
        errors = [str(exc)]
    if args.json:
        print(json.dumps({"status": "pass" if not errors else "fail", "errors": errors}, indent=2, sort_keys=True))
    else:
        for error in errors:
            print(error, file=sys.stderr)
    return 1 if errors else 0


if __name__ == "__main__":
    sys.exit(main())
