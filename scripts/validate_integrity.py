#!/usr/bin/env python3
"""Authoritative Session 08 integrity/drift contract validator."""

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
CONTRACT_DIR = ROOT / "contracts" / "integrity"
LOCK_PATH = ROOT / "contracts" / "integrity-policy-locks.yaml"
FILES = (
    "audit-run-and-schedule.yaml",
    "drift-fingerprint-and-schema.yaml",
    "incident-and-health.yaml",
    "fault-injection-and-live-canary.yaml",
)
PATHS = {f"contracts/integrity/{name}" for name in FILES}
POLICY_BASE_BY_REGISTRY = {
    "contracts/integrity/audit-run-and-schedule.yaml": "integrity.audit-run-and-schedule",
    "contracts/integrity/drift-fingerprint-and-schema.yaml": "integrity.drift-fingerprint-and-schema",
    "contracts/integrity/incident-and-health.yaml": "integrity.incident-and-health",
    "contracts/integrity/fault-injection-and-live-canary.yaml": "integrity.fault-injection-and-live-canary",
}
AUTHORITATIVE_SEMANTIC_SHA256 = {
    "contracts/integrity/audit-run-and-schedule.yaml": "09fc0db07cba190f1426533be16cc75e0edee651339975df63c95b23fc51b430",
    "contracts/integrity/drift-fingerprint-and-schema.yaml": "2fd03b2dcbe841ff9ba9410a9784fcf695f681b4622aec52af192f6f7a26127c",
    "contracts/integrity/incident-and-health.yaml": "04501626b26dcc77611e5aed4263499eb7dc76e98292095d70e432d8746799d5",
    "contracts/integrity/fault-injection-and-live-canary.yaml": "e04cec8c81408be251d553572d6114b6778d9bf997bb851f7ab586f2fb2323ed",
}

STAGES = [
    ("stage_1_discovery_and_configuration", 1, 30),
    ("stage_2_endpoint_and_hook_verification", 2, 30),
    ("stage_3_watermark_vs_inactivity", 3, 15),
    ("stage_4_parser_fixture_replay", 4, 60),
    ("stage_5_synthetic_pipeline_probe", 5, 45),
    ("stage_6_cross_source_reconciliation", 6, 60),
    ("stage_7_unknown_schema_and_lag", 7, 30),
    ("stage_8_rollup_formula_and_db_integrity", 8, 60),
    ("stage_9_retention_disk_and_backup", 9, 90),
    ("stage_10_optional_live_canary", 10, 300),
    ("stage_11_persist_report_and_raise_incidents", 11, 30),
]
FINGERPRINT_KINDS = {
    "executable_version", "config_recipe_fingerprint", "adapter_version",
    "fixture_version", "formula_registry_version", "event_schema_fingerprint",
}
PRIMITIVE_TYPES = {"string", "integer", "number", "boolean", "null", "array", "object"}
FAILURE_CLASSES = {
    "endpoint_unreachable", "hook_removed_disabled_or_untrusted", "otlp_misconfigured",
    "permission_denied", "watermark_stall", "true_inactivity_flagged", "eligibility_unknown",
    "parser_incompatibility", "unknown_schema", "duplicate_evidence_anomaly", "ingest_lag",
    "rollup_stale", "formula_version_mismatch", "db_integrity_violation",
    "retention_job_failed", "disk_budget_exceeded", "backup_stale", "restore_test_failed",
    "privacy_canary_violation", "synthetic_pipeline_probe_failed", "live_canary_partial_dag",
    "live_canary_provider_timeout", "reconciliation_mismatch",
}
HEALTH_STAGES = {
    "configuration": ["stage_1_discovery_and_configuration"],
    "connectivity": ["stage_2_endpoint_and_hook_verification"],
    "event_freshness": ["stage_3_watermark_vs_inactivity"],
    "schema_compatibility": ["stage_4_parser_fixture_replay", "stage_7_unknown_schema_and_lag"],
    "parser_fixture_status": ["stage_4_parser_fixture_replay"],
    "reconciliation_coverage": ["stage_6_cross_source_reconciliation"],
    "privacy_canary": ["stage_9_retention_disk_and_backup"],
    "live_canary_age_result": ["stage_10_optional_live_canary"],
    "storage_rollup_health": ["stage_8_rollup_formula_and_db_integrity", "stage_9_retention_disk_and_backup"],
}
FAULTS = {
    "hook_removed_disabled_or_untrusted": ("hook_removed_disabled_or_untrusted", 30),
    "otlp_wrong_port_protocol_or_auth": ("otlp_misconfigured", 30),
    "transcript_truncate_rotate_schema_or_permission_change": ("permission_denied", 60),
    "active_process_with_absent_events": ("watermark_stall", 90),
    "duplicate_and_stalled_watermarks": ("duplicate_evidence_anomaly", 60),
    "parser_panic_timeout_or_unknown_field": ("parser_incompatibility", 60),
    "delayed_rollup": ("rollup_stale", 60),
    "full_disk": ("disk_budget_exceeded", 90),
    "db_restart": ("db_integrity_violation", 90),
    "corrupt_spool": ("db_integrity_violation", 90),
    "stale_backup": ("backup_stale", 90),
    "failed_restore": ("restore_test_failed", 90),
    "privacy_canary_violation": ("privacy_canary_violation", 45),
    "live_canary_partial_dag": ("live_canary_partial_dag", 300),
    "live_canary_provider_timeout": ("live_canary_provider_timeout", 300),
    "endpoint_unreachable": ("endpoint_unreachable", 30),
    "synthetic_pipeline_probe_failure": ("synthetic_pipeline_probe_failed", 45),
    "unknown_schema_quarantine": ("unknown_schema", 30),
    "cross_source_reconciliation_regression": ("reconciliation_mismatch", 60),
    "ingest_lag": ("ingest_lag", 60),
    "inventory_cache_miscount": ("permission_denied", 30),
}
FAULT_EVIDENCE = {
    "component_classifier": {
        "hook_removed_disabled_or_untrusted", "otlp_wrong_port_protocol_or_auth",
        "transcript_truncate_rotate_schema_or_permission_change", "active_process_with_absent_events",
        "duplicate_and_stalled_watermarks", "parser_panic_timeout_or_unknown_field",
        "delayed_rollup", "full_disk", "stale_backup", "privacy_canary_violation",
        "live_canary_partial_dag", "live_canary_provider_timeout", "endpoint_unreachable",
        "unknown_schema_quarantine", "cross_source_reconciliation_regression", "ingest_lag",
        "inventory_cache_miscount",
    },
    "deterministic_mutation_integration": {
        "corrupt_spool", "synthetic_pipeline_probe_failure",
    },
    "runtime_required": {"db_restart", "failed_restore"},
}

TOP_LEVEL = {
    "audit-run-and-schedule.yaml": {
        "schema_version", "contract_version", "effective_at", "workflow_name",
        "single_writer_mechanism", "triggers", "run_modes", "reduced_mode_stage_scope",
        "run_states", "state_machine", "stage_registry", "stage_ordinal_rule",
        "no_mutation_rule", "idempotency_rule", "timeout_bound_rule", "crash_recovery",
        "durable_state_validation",
    },
    "drift-fingerprint-and-schema.yaml": {
        "schema_version", "contract_version", "effective_at", "structural_only_rule",
        "prohibited_field_categorization_reuse", "fingerprint_kinds",
        "new_shape_counting_rule", "targeted_revalidation_rule", "storage_boundary",
    },
    "incident-and-health.yaml": {
        "schema_version", "contract_version", "effective_at", "one_incident_concept_rule",
        "incident_key", "incident_field_extension", "dedup_and_lifecycle_rules",
        "health_dimensions", "failure_class_health_mapping", "failure_tier_rule",
        "state_colors", "no_single_score_rule", "capability_state_relationship",
    },
    "fault-injection-and-live-canary.yaml": {
        "schema_version", "contract_version", "effective_at", "fault_injection_catalog",
        "catalog_completeness_rule", "measured_detection_time_rule",
        "evidence_classification_rule",
        "live_canary_recipe_schema", "expected_event_dag_precision_rule",
    },
}


def load(path: Path) -> dict[str, Any]:
    value = json.loads(path.read_text(encoding="utf-8"))
    if not isinstance(value, dict):
        raise ValueError(f"{path}: object required")
    return value


def semantic_sha256(value: Any) -> str:
    raw = json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=False).encode()
    return hashlib.sha256(raw).hexdigest()


def trusted_lock_from_head() -> dict[str, Any] | None:
    """Read the committed lock prefix without trusting the worktree copy.

    Session 08's first pre-commit validation legitimately has no historical
    file at HEAD; subsequent edits must retain the exact committed prefix.
    """
    completed = subprocess.run(
        ["git", "show", "HEAD:contracts/integrity-policy-locks.yaml"],
        cwd=ROOT, text=True, capture_output=True, check=False,
    )
    if completed.returncode != 0:
        return None
    value = json.loads(completed.stdout)
    if not isinstance(value, dict):
        raise ValueError("committed integrity policy lock must be an object")
    return value


def registries() -> dict[str, dict[str, Any]]:
    return {f"contracts/integrity/{name}": load(CONTRACT_DIR / name) for name in FILES}


def validate(
    candidate: dict[str, dict[str, Any]] | None = None,
    locks: dict[str, Any] | None = None,
    include_code: bool = True,
    historical: dict[str, Any] | None = None,
) -> list[str]:
    data = registries() if candidate is None else candidate
    lock = load(LOCK_PATH) if locks is None else locks
    errors: list[str] = []
    if set(data) != PATHS:
        return ["integrity registry set is not exact"]
    by_name = {Path(path).name: value for path, value in data.items()}
    for name, fields in TOP_LEVEL.items():
        if set(by_name[name]) != fields:
            errors.append(f"{name}: top-level closed schema changed")

    audit = by_name["audit-run-and-schedule.yaml"]
    actual_stages = [
        (row.get("stage_id"), row.get("ordinal"), row.get("timeout_seconds"))
        for row in audit.get("stage_registry", [])
    ]
    if actual_stages != STAGES:
        errors.append("11-stage IDs/ordinals/timeouts changed")
    if any(row.get("mutates_target") is not False for row in audit.get("stage_registry", [])):
        errors.append("every audit stage must remain target-read-only")
    if audit.get("run_modes") != ["full", "reduced"]:
        errors.append("run modes must remain full/reduced")
    if audit.get("run_states") != ["scheduled", "running", "passed", "degraded", "failed", "cancelled"]:
        errors.append("run state machine vocabulary changed")
    if [row.get("trigger") for row in audit.get("triggers", [])] != [
        "scheduled_daily", "startup", "version_change_detected", "manual_operator_request"
    ]:
        errors.append("trigger vocabulary changed")
    identity_rule = str(audit.get("idempotency_rule", ""))
    for token in ("audit_run_id", "check_id", "capability_id", "installation_id", "source_id"):
        if token not in identity_rule:
            errors.append(f"audit check idempotency key lost {token}")
    if "source_id is explicit" not in identity_rule or "never overloaded into capability_id" not in identity_rule:
        errors.append("source identity must remain separate from closed capability_id")

    drift = by_name["drift-fingerprint-and-schema.yaml"]
    if {row.get("kind") for row in drift.get("fingerprint_kinds", [])} != FINGERPRINT_KINDS:
        errors.append("fingerprint kind vocabulary changed")
    structural = str(drift.get("structural_only_rule", ""))
    if "STRUCTURAL metadata only" not in structural or "field VALUE" not in structural:
        errors.append("structural fingerprints must never sample/hash values")
    computation = " ".join(str(row.get("computation", "")) for row in drift.get("fingerprint_kinds", []))
    if not PRIMITIVE_TYPES.issubset(set(re.findall(r"\b(?:string|integer|number|boolean|null|array|object)\b", computation))):
        errors.append("event schema primitive type vocabulary changed")
    if "stage_4_parser_fixture_replay" not in str(drift.get("new_shape_counting_rule", "")) or "stage_7_unknown_schema_and_lag" not in str(drift.get("new_shape_counting_rule", "")):
        errors.append("new shape counting must remain in stages 4 and 7")

    health = by_name["incident-and-health.yaml"]
    failure_classes = health.get("incident_key", {}).get("failure_class_vocabulary", [])
    if set(failure_classes) != FAILURE_CLASSES or len(failure_classes) != len(FAILURE_CLASSES):
        errors.append("23 failure-class vocabulary changed")
    actual_health = {
        row.get("dimension"): row.get("sourced_from_checks")
        for row in health.get("health_dimensions", [])
    }
    if actual_health != HEALTH_STAGES:
        errors.append("nine health dimensions/stage mappings changed")
    if set(health.get("state_colors", {})) != {"green", "yellow", "red", "gray"}:
        errors.append("health colors must remain green/yellow/red/gray")
    if "actually passed" not in str(health.get("state_colors", {}).get("green", "")) or "still-fresh" not in str(health.get("state_colors", {}).get("green", "")):
        errors.append("green must require fresh runtime evidence")
    mapped = {
        item for values in health.get("failure_class_health_mapping", {}).values() for item in values
    }
    if mapped != FAILURE_CLASSES:
        errors.append("failure-class to health-dimension mapping must cover all 23 classes exactly")

    fault = by_name["fault-injection-and-live-canary.yaml"]
    actual_faults = {
        row.get("fault_id"): (
            row.get("expected_incident_failure_class"),
            row.get("expected_detection_slo_seconds"),
        )
        for row in fault.get("fault_injection_catalog", [])
    }
    if actual_faults != FAULTS or len(fault.get("fault_injection_catalog", [])) != len(FAULTS):
        errors.append("21 fault IDs/classes/SLOs changed")
    evidence = {
        name: set(ids) if isinstance(ids, list) else set()
        for name, ids in fault.get("evidence_classification_rule", {}).items()
    }
    if evidence != FAULT_EVIDENCE:
        errors.append("fault evidence classification changed")
    flattened = [fault_id for values in evidence.values() for fault_id in values]
    if len(flattened) != len(set(flattened)) or set(flattened) != set(FAULTS):
        errors.append("fault evidence classes must partition all 21 fault IDs exactly")
    recipe = fault.get("live_canary_recipe_schema", {})
    command = str(recipe.get("fields", {}).get("command", ""))
    if "array of non-empty strings" not in command or "never a shell string" not in command:
        errors.append("live canary command must remain argv, never shell text")
    gate = " ".join(str(value) for value in recipe.get("disabled_by_default_gate", {}).values())
    for token in ("enabled=false", "explicit_credentials_present=true", "explicit_user_consent_recorded=true", "does not spawn a real external agent process"):
        if token not in gate:
            errors.append(f"live canary disabled-by-default gate lost {token}")

    errors.extend(validate_locks(data, lock, historical))
    if include_code:
        errors.extend(validate_code_and_fixtures())
    return errors


def validate_locks(data: dict[str, dict[str, Any]], lock: dict[str, Any], historical: dict[str, Any] | None) -> list[str]:
    errors: list[str] = []
    if set(lock) != {"schema_version", "effective_at", "locks"}:
        errors.append("integrity lock top-level closed schema changed")
    if lock.get("schema_version") != "kansoku.integrity-policy-locks/1":
        errors.append("integrity lock schema_version changed")
    rows = lock.get("locks", [])
    if not isinstance(rows, list):
        return ["integrity locks must be a list"]
    if historical is not None:
        trusted = historical.get("locks", [])
        if rows[:len(trusted)] != trusted:
            errors.append("integrity lock append-only trusted prefix changed")
    latest: dict[str, tuple[int, str]] = {}
    counts: dict[str, list[int]] = {}
    for row in rows:
        if not isinstance(row, dict) or set(row) != {"policy_version", "registry", "semantic_sha256"}:
            errors.append("integrity lock entry closed schema changed")
            continue
        match = re.fullmatch(r"(integrity\.[a-z0-9-]+)/([1-9][0-9]*)", str(row.get("policy_version", "")))
        if not match:
            errors.append("integrity policy version format invalid")
            continue
        base, version = match.group(1), int(match.group(2))
        registry = row.get("registry")
        if registry not in PATHS:
            errors.append("integrity lock references unknown registry")
            continue
        if base != POLICY_BASE_BY_REGISTRY[registry]:
            errors.append(f"{registry}: policy name does not match registry identity")
            continue
        if re.fullmatch(r"[0-9a-f]{64}", str(row.get("semantic_sha256", ""))) is None:
            errors.append("integrity lock semantic digest format invalid")
        counts.setdefault(base, []).append(version)
        latest[registry] = (version, str(row.get("semantic_sha256", "")))
    for base, versions in counts.items():
        if versions != list(range(1, len(versions) + 1)):
            errors.append(f"{base} policy versions must start at 1 and remain contiguous")
    if set(latest) != PATHS:
        errors.append("every integrity registry must have a current lock")
    for path in PATHS:
        observed = semantic_sha256(data[path])
        if observed != AUTHORITATIVE_SEMANTIC_SHA256[path]:
            errors.append(f"{path}: authoritative Session08 semantics changed")
        if path in latest and latest[path][1] != observed:
            errors.append(f"{path}: semantic digest changed without a matching lock transition")
    return errors


def validate_code_and_fixtures() -> list[str]:
    errors: list[str] = []
    core_files = [
        path for path in (ROOT / "internal" / "integrity").glob("*.go")
        if not path.name.endswith("_test.go")
    ]
    core = "\n".join(path.read_text(encoding="utf-8") for path in core_files)
    imports_only = "\n".join(
        line for line in core.splitlines() if line.strip().startswith('"kansoku.local/')
    )
    if re.search(r"internal/(?:codex|claude|gemini|cursor)adapter", imports_only):
        errors.append("integrity core must have zero agent-name adapter imports/branches")
    if re.search(r"DetailRef:.*(?:err\.Error\(\)|%v)", core):
        errors.append("durable DetailRef must not persist raw error strings")
    live = (ROOT / "internal" / "integrity" / "livecanary.go").read_text(encoding="utf-8")
    live_imports = "\n".join(line for line in live.splitlines() if line.strip().startswith('"'))
    if '"os/exec"' in live_imports or '"net/http"' in live_imports:
        errors.append("live canary implementation must not spawn processes or perform network egress")
    if "Adapter.Audit" not in core and ".Audit(ctx" not in core:
        errors.append("integrity core must dispatch fixture audit through generic Adapter.Audit")
    migration = (ROOT / "internal" / "integrity" / "migrations" / "0005_source_fingerprint_report_schema.up.sql").read_text(encoding="utf-8")
    for token in ("source_id", "integrity_fingerprints", "integrity_live_canary_state", "integrity_audit_reports"):
        if token not in migration:
            errors.append(f"Session08 durable migration missing {token}")
    tests = (ROOT / "internal" / "integrity" / "fault_catalog_test.go").read_text(encoding="utf-8")
    test_ids = set(re.findall(r"func TestFaultComponent_([a-z0-9_]+)\(", tests))
    if test_ids != FAULT_EVIDENCE["component_classifier"]:
        errors.append("exact TestFaultComponent_<fault_id> detector bijection changed")
    fault_code = (ROOT / "internal" / "integrity" / "faults.go").read_text(encoding="utf-8")
    type_code = (ROOT / "internal" / "integrity" / "types.go").read_text(encoding="utf-8")
    class_symbols = dict(re.findall(r"(FailureClass[A-Za-z0-9]+)\s+FailureClass\s*=\s*\"([a-z0-9_]+)\"", type_code))
    evidence_symbols = {
        "FaultEvidenceComponentClassifier": "component_classifier",
        "FaultEvidenceDeterministicMutation": "deterministic_mutation_integration",
        "FaultEvidenceRuntimeRequired": "runtime_required",
    }
    go_faults: dict[str, tuple[str, int, str]] = {}
    for fault_id, symbol, slo_text, evidence_symbol in re.findall(
        r'\{"([a-z0-9_]+)",\s*\[\]StageID\{[^}]+\},\s*(FailureClass[A-Za-z0-9]+),\s*([0-9]+)\s*\*\s*time\.Second,\s*(FaultEvidence[A-Za-z0-9]+)\}',
        fault_code,
    ):
        go_faults[fault_id] = (
            class_symbols.get(symbol, ""), int(slo_text),
            evidence_symbols.get(evidence_symbol, ""),
        )
    for fault_id, (failure_class, slo) in FAULTS.items():
        expected_evidence = next(
            name for name, ids in FAULT_EVIDENCE.items() if fault_id in ids
        )
        if go_faults.get(fault_id) != (failure_class, slo, expected_evidence):
            errors.append(f"Go fault catalog missing authoritative mapping for {fault_id}")
    stage5_tests = (ROOT / "internal" / "integrity" / "stage5_test.go").read_text(encoding="utf-8")
    postgres_tests = (ROOT / "internal" / "integrity" / "postgres_integration_test.go").read_text(encoding="utf-8")
    if "TestFaultMutationSyntheticPipelineFailurePersistsMeasuredIncidentAndCleans" not in stage5_tests:
        errors.append("synthetic pipeline mutation integration test is missing")
    if "TestFaultMutationCorruptSpoolPersistsMeasuredIncidentLifecycle" not in postgres_tests:
        errors.append("corrupt spool mutation integration test is missing")
    fixture = ROOT / "tests" / "fixtures" / "session-08" / "fault-cases.json"
    if not fixture.exists():
        errors.append("Session08 fault fixture is missing")
    for path in (
        ROOT / "reports" / "session-08-sbom.json",
        ROOT / "reports" / "session-08-reconciliation.md",
        ROOT / "adr" / "0011-session-08-integrity-and-drift.md",
    ):
        if not path.exists():
            errors.append(f"required Session08 artifact missing: {path.relative_to(ROOT)}")
    return errors


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--no-code", action="store_true")
    args = parser.parse_args()
    try:
        errors = validate(
            include_code=not args.no_code,
            historical=trusted_lock_from_head(),
        )
    except (OSError, ValueError, json.JSONDecodeError) as exc:
        errors = [str(exc)]
    if errors:
        for error in errors:
            print(error, file=sys.stderr)
        return 1
    print("Session 08 integrity contracts: pass")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
