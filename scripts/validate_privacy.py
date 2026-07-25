#!/usr/bin/env python3
"""Validate Session 02 privacy/security contracts using the standard library."""

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
PRIVACY = ROOT / "contracts" / "privacy"
EXPECTED_CLASSES = {
    "prohibited_content", "sensitive_identifier", "operational_metadata",
    "derived_metadata", "public_catalog",
}
EXPECTED_SINKS = {
    "database", "application_logs", "internal_traces", "durable_queue", "retry_queue",
    "quarantine", "error_response", "dashboard_network", "export", "backup",
}
EXPECTED_TARGETS = {
    "codex.user_otel", "claude.user_otel", "gemini.user_otel", "cursor.user_hooks",
    "codex.user_hook", "claude.user_hook",
}
EXPECTED_VALUE_STATES = ["observed", "unsupported", "not_observed", "redacted", "unknown", "numeric_zero"]
EXPECTED_RECORD_FIELDS = {
    "record_id", "idempotency_key", "adapter_id", "adapter_version", "source_schema_id",
    "schema_fingerprint", "observed_at", "received_at", "confidence", "event_type", "outcome",
    "value_state", "model", "tool", "component_kind", "component_mentions", "prompt_features", "telemetry",
    "redaction_counts", "lineage",
}
EXPECTED_ERROR_FIELDS = {
    "incident_id", "source_schema_id", "schema_fingerprint", "field_path", "category",
    "total_bytes", "record_count", "observed_at", "received_at",
}
EXPECTED_RECORD_FIELD_ORDER = [
    "record_id", "idempotency_key", "adapter_id", "adapter_version", "source_schema_id",
    "schema_fingerprint", "observed_at", "received_at", "confidence", "event_type", "outcome",
    "value_state", "model", "tool", "component_kind", "component_mentions", "prompt_features", "telemetry",
    "redaction_counts", "lineage",
]
EXPECTED_ERROR_FIELD_ORDER = [
    "incident_id", "source_schema_id", "schema_fingerprint", "field_path", "category",
    "total_bytes", "record_count", "observed_at", "received_at",
]
EXPECTED_PREVIEW_FIELDS = {
    "plan_id", "plan_version", "target_id", "agent_id", "config_locator", "config_format",
    "config_locator_kind", "ownership",
    "disclosed_fields", "exact_operations", "original_sha256", "planned_sha256", "backup_locator",
    "rollback_command", "privacy_tradeoffs",
}

REGISTRY_FILES = sorted(PRIVACY.glob("*.yaml"))
POLICY_LOCK_PATH = ROOT / "contracts" / "privacy-policy-locks.yaml"
POLICY_REGISTRIES = {
    "privacy.data-classes": "contracts/privacy/data-classes.yaml",
    "privacy.deployment": "contracts/privacy/deployment.yaml",
    "privacy.host-access": "contracts/privacy/host-access.yaml",
    "privacy.ingress": "contracts/privacy/ingress.yaml",
    "privacy.installer": "contracts/privacy/installer.yaml",
    "privacy.retention": "contracts/privacy/retention.yaml",
    "privacy.sinks": "contracts/privacy/sinks.yaml",
    "privacy.threat-model": "contracts/privacy/threat-model.yaml",
}
EXPECTED_SOURCE_SCHEMA = {
    "id": "fixture.agent-hook/1",
    "adapter_id": "fixture-agent",
    "adapter_version": "1.0.0",
    "event_types": ["session_started", "user_prompt", "tool_finished", "session_finished"],
    "models": ["catalog/model-safe"],
    "tools": ["inventory/tool-safe"],
    "components": ["inventory/skill-safe"],
    "input_fields": [
        "event_id", "session_id", "observed_at", "event_type", "outcome", "value_state",
        "model", "tool_name", "prompt", "attachments", "response", "source_code",
        "tool_input", "tool_output", "command", "path", "environment", "credentials",
        "exception",
    ],
}
EXPECTED_NESTED_TYPES = {
    "CatalogObservation": {"fields": {"state": "observation_state", "id": "nullable_catalog_id"}, "closed": True},
    "PromptFeatures": {"fields": {
        "state": "completeness_state", "byte_count": "nonnegative_integer",
        "character_count": "nonnegative_integer", "word_count": "nonnegative_integer",
        "line_count": "nonnegative_integer", "coarse_script": "coarse_script_enum",
        "code_fence_count": "nonnegative_integer", "attachment_count": "nonnegative_integer",
        "url_reference_count": "nonnegative_integer", "file_reference_count": "nonnegative_integer",
    }, "closed": True},
    "TelemetryMeasurements": {"fields": {
        "duration_ms": "nullable_nonnegative_integer",
        "prompt_character_count": "nullable_nonnegative_integer",
        "input_tokens": "nullable_nonnegative_integer",
        "output_tokens": "nullable_nonnegative_integer",
        "provider_cost_micros": "nullable_nonnegative_integer",
    }, "closed": True},
    "RedactionCounts": {"fields": {
        "prompt_fields": "nonnegative_integer", "attachment_fields": "nonnegative_integer",
        "response_fields": "nonnegative_integer", "source_fields": "nonnegative_integer",
        "tool_io_fields": "nonnegative_integer", "command_fields": "nonnegative_integer",
        "path_fields": "nonnegative_integer", "environment_fields": "nonnegative_integer",
        "credential_fields": "nonnegative_integer", "exception_fields": "nonnegative_integer",
        "sensitive_identifier_fields": "nonnegative_integer",
    }, "closed": True},
    "Lineage": {"fields": {
        "source_record_pseudonym": "hmac_sha256", "session_pseudonym": "hmac_sha256",
        "turn_pseudonym": "optional_hmac_sha256", "adapter_id": "registered_adapter_id",
        "adapter_version": "registered_adapter_version", "source_schema_id": "registered_schema_id",
        "schema_fingerprint": "sha256", "sanitizer_version": "registered_sanitizer_version",
        "contract_sha256": "sha256_hex",
    }, "closed": True},
    "SafeLogEvent": {"fields": {
        "event_name": "safe_event_enum", "category": "safe_error_category",
        "adapter_id": "registered_or_empty_adapter_id",
        "source_schema_id": "registered_or_unknown_schema_id",
        "schema_fingerprint": "sha256_or_hmac_sha256", "field_path": "safe_structural_path",
        "byte_count": "nonnegative_integer", "record_count": "bounded_nonnegative_integer",
        "outcome": "outcome_enum_or_empty", "value_state": "value_state_or_empty",
        "duration_ms": "nonnegative_integer",
    }, "closed": True},
}
EXPECTED_SAFE_LOG_FIELDS = list(EXPECTED_NESTED_TYPES["SafeLogEvent"]["fields"])
EXPECTED_INSTALLER_TARGET_POLICIES = {
    "codex.user_otel": {
        "required_settings": {"otel.environment": "local", "otel.exporter": "otlp-http", "otel.endpoint": "http://127.0.0.1:4318", "otel.log_user_prompt": False},
        "forbidden_keys": ["authorization", "headers", "project_local_otel", "remote_endpoint"],
    },
    "claude.user_otel": {
        "required_settings": {"env.CLAUDE_CODE_ENABLE_TELEMETRY": "1", "env.OTEL_EXPORTER_OTLP_ENDPOINT": "http://127.0.0.1:4318", "env.OTEL_LOG_USER_PROMPTS": "0", "env.OTEL_LOG_ASSISTANT_RESPONSES": "0", "env.OTEL_LOG_TOOL_DETAILS": "0", "env.OTEL_LOG_TOOL_CONTENT": "0", "env.OTEL_LOG_RAW_API_BODIES": "0"},
        "forbidden_keys": ["env.OTEL_EXPORTER_OTLP_HEADERS", "env.OTEL_LOGS_EXPORTER_FILE", "remote_endpoint"],
    },
    "gemini.user_otel": {
        "required_settings": {"telemetry.enabled": True, "telemetry.target": "local", "telemetry.otlpEndpoint": "http://127.0.0.1:4318", "telemetry.logPrompts": False, "telemetry.useCliAuth": False},
        "forbidden_keys": ["telemetry.outfile", "telemetry.target=gcp", "telemetry.useCliAuth=true", "remote_endpoint"],
    },
    "cursor.user_hooks": {
        "required_settings": {"hook.command": "kansoku hook --endpoint http://127.0.0.1:4318 --strict-privacy", "hook.role": "collection_only", "hook.privacy_boundary": "loopback_sanitizer", "hook.raw_persistence": False},
        "forbidden_keys": ["remote_command", "raw_payload_log", "hook_as_privacy_enforcement", "credential_forwarding"],
    },
    "codex.user_hook": {
        "required_settings": {"notify.command": "kansoku-codex-hook", "notify.role": "collection_only"},
        "forbidden_keys": ["remote_command", "raw_payload_log", "credential_forwarding", "project_local_hook"],
    },
    "claude.user_hook": {
        "required_settings": {"hooks.SessionStart": "kansoku-claude-hook", "hooks.UserPromptSubmit": "kansoku-claude-hook", "hooks.PreToolUse": "kansoku-claude-hook", "hooks.PostToolUse": "kansoku-claude-hook", "hooks.SubagentStart": "kansoku-claude-hook", "hooks.SubagentStop": "kansoku-claude-hook", "hooks.Stop": "kansoku-claude-hook"},
        "forbidden_keys": ["remote_command", "raw_payload_log", "credential_forwarding", "project_local_hook"],
    },
}
EXPECTED_HOST_ACCESS = {
    "installer_agent_config": ("read_then_explicit_write", "one exact approved agent config file", False),
    "runtime_hook_ingress": ("loopback_write_only", "bounded Kansoku ingress endpoint", False),
    "historical_import_root": ("read_only", "one exact user-selected transcript root", False),
    "identity_key_file": ("read_write_create_once", "one rootless secret file outside database and backup volumes, mode 0600, nlink 1, inside an owner-only 0700 leaf directory reached by fd-relative no-symlink directory walk", True),
    "database_password_secret": ("read_only", "one exact rootless secret file, mode 0600, mounted only at /run/secrets/database_password", True),
    "kansoku_data_volume": ("read_write", "named Kansoku application data volume", True),
    "database_volume": ("read_write", "named PostgreSQL data volume", True),
}
EXPECTED_INGRESS_CONTROLS = {
    "PRV-INGRESS-001": "Only generated/typed SafeRecord and SafeError values may cross the sanitizer boundary; generic maps remain internal.",
    "PRV-INGRESS-002": "Decoder enforces byte, depth, array, string and frame limits before any logging, tracing, retry or quarantine serialization.",
    "PRV-INGRESS-003": "Unknown source schemas and fields produce metadata-only quarantine errors and never silent drop or coercion.",
}
EXPECTED_DEPLOYMENT_CONTROLS = {
    "PRV-HTTP-001": ["loopback listeners only", "loopback peer only", "exact Host allowlist", "same-origin check", "bearer and CSRF for mutation", "same checks for SSE and WebSocket", "no wildcard CORS", "payload and rate limits", "CSP frame-ancestors none", "nosniff", "strict referrer policy"],
    "PRV-CONTAINER-001": ["non-root", "read-only root filesystem", "cap_drop ALL", "no-new-privileges", "named writable volumes only", "no Docker socket", "no broad host mount", "database port unpublished", "immutable image references", "SBOM and vulnerability evidence before release"],
    "PRV-EGRESS-001": ["runtime network internal by default", "no CDN/fonts/analytics/crash upload", "optional metadata jobs separately allowlisted", "no local telemetry in metadata requests", "live canary explicit opt-in and daily budget"],
}
EXPECTED_ROUTE_MODES = {
    "ui_stream": {"methods": ["GET", "HEAD"], "auth": "bearer", "origin": "same_origin_when_present", "csrf": False},
    "hook_otlp": {"methods": ["POST"], "auth": "bearer", "origin": "reject", "csrf": False},
    "ui_mutation": {"methods": ["POST", "PUT", "PATCH", "DELETE"], "auth": "bearer", "origin": "required_same_origin", "csrf": True},
}
EXPECTED_GO_SCHEMAS = {
    "CatalogObservation": {"state": "ObservationState", "id": "*string"},
    "PromptFeatures": {"state": "CompletenessState", "byte_count": "int", "character_count": "int", "word_count": "int", "line_count": "int", "coarse_script": "string", "code_fence_count": "int", "attachment_count": "int", "url_reference_count": "int", "file_reference_count": "int"},
    "TelemetryMeasurements": {"duration_ms": "*int64", "prompt_character_count": "*int64", "input_tokens": "*int64", "output_tokens": "*int64", "provider_cost_micros": "*int64"},
    "RedactionCounts": {"prompt_fields": "int", "attachment_fields": "int", "response_fields": "int", "source_fields": "int", "tool_io_fields": "int", "command_fields": "int", "path_fields": "int", "environment_fields": "int", "credential_fields": "int", "exception_fields": "int", "sensitive_identifier_fields": "int"},
    "Lineage": {"source_record_pseudonym": "string", "session_pseudonym": "string", "turn_pseudonym": "string", "adapter_id": "string", "adapter_version": "string", "source_schema_id": "string", "schema_fingerprint": "string", "sanitizer_version": "string", "contract_sha256": "string"},
    "SafeRecord": {"record_id": "string", "idempotency_key": "string", "adapter_id": "string", "adapter_version": "string", "source_schema_id": "string", "schema_fingerprint": "string", "observed_at": "time.Time", "received_at": "time.Time", "confidence": "float64", "event_type": "string", "outcome": "string", "value_state": "ValueState", "model": "CatalogObservation", "tool": "CatalogObservation", "component_kind": "string", "component_mentions": "[]string", "prompt_features": "PromptFeatures", "telemetry": "TelemetryMeasurements", "redaction_counts": "RedactionCounts", "lineage": "Lineage"},
    "SafeError": {"incident_id": "string", "source_schema_id": "string", "schema_fingerprint": "string", "field_path": "string", "category": "string", "total_bytes": "int64", "record_count": "int", "observed_at": "time.Time", "received_at": "time.Time"},
    "SafeLogEvent": {"event_name": "string", "category": "string", "adapter_id": "string", "source_schema_id": "string", "schema_fingerprint": "string", "field_path": "string", "byte_count": "int64", "record_count": "int", "outcome": "string", "value_state": "ValueState", "duration_ms": "int64"},
}


def reject_duplicate_pairs(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
    result: dict[str, Any] = {}
    for key, value in pairs:
        if key in result:
            raise ValueError(f"duplicate JSON name: {key}")
        result[key] = value
    return result


def load(path: Path) -> dict[str, Any]:
    try:
        value = json.loads(path.read_text(encoding="utf-8"), object_pairs_hook=reject_duplicate_pairs)
    except (OSError, json.JSONDecodeError) as exc:
        raise ValueError(f"cannot load {path.relative_to(ROOT)}: {exc}") from exc
    if not isinstance(value, dict):
        raise ValueError(f"{path.relative_to(ROOT)} must contain a mapping")
    return value


def registry(name: str) -> dict[str, Any]:
    return load(PRIVACY / name)


def canonical_semantic_sha256(value: Any) -> str:
    canonical = json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(",", ":")).encode("utf-8")
    return hashlib.sha256(canonical).hexdigest()


def registry_set(overrides: dict[str, dict[str, Any]] | None = None) -> dict[str, dict[str, Any]]:
    values: dict[str, dict[str, Any]] = {}
    for path in REGISTRY_FILES:
        relative = path.relative_to(ROOT).as_posix()
        values[relative] = load(path) if overrides is None or relative not in overrides else overrides[relative]
    return values


def privacy_registry_sha256(overrides: dict[str, dict[str, Any]] | None = None) -> str:
    digest = hashlib.sha256()
    for relative, value in registry_set(overrides).items():
        canonical = json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(",", ":")).encode("utf-8")
        digest.update(relative.encode("utf-8") + b"\0" + canonical + b"\0")
    return digest.hexdigest()


def policy_lock_entries(data: dict[str, Any], scope: str = "privacy policy locks") -> tuple[list[str], dict[str, dict[str, Any]]]:
    errors: list[str] = []
    expected_trust_model = {
        "bootstrap": "In an archive or before the first reviewed commit containing this file, this review-controlled lock registry is the deterministic source of truth.",
        "trusted_head": "After bootstrap, validation compares existing policy-version locks with an explicit trusted merge-base or HEAD revision when available.",
        "history": "Existing policy-version-to-digest entries are append-only. Reusing a version for changed registry semantics is forbidden.",
        "version_transition": "A reviewed semantic policy change requires a new policy version and lock entry; every prior entry remains unchanged.",
        "external_root": "Repository-local validation cannot resist a simultaneous malicious rewrite of validator, policy registries, runtime, locks, tests and Git history. Protected review or CI with a trusted revision is the external root of trust.",
    }
    if set(data) != {"schema_version", "trust_model", "locks"} or data.get("schema_version") != "kansoku.privacy-policy-locks/1":
        errors.append(f"{scope}: exact closed schema/version required")
    if data.get("trust_model") != expected_trust_model:
        errors.append(f"{scope}: exact bootstrap, history and external-root trust disclosure required")
    locks = data.get("locks")
    if not isinstance(locks, list):
        return errors + [f"{scope}: locks must be a list"], {}
    by_version: dict[str, dict[str, Any]] = {}
    for index, entry in enumerate(locks):
        entry_scope = f"{scope}[{index}]"
        if not isinstance(entry, dict) or set(entry) != {"policy_version", "registry", "semantic_sha256"}:
            errors.append(f"{entry_scope}: exact policy_version/registry/semantic_sha256 fields required")
            continue
        version = entry.get("policy_version")
        relative = entry.get("registry")
        digest = entry.get("semantic_sha256")
        match = re.fullmatch(r"(privacy\.[a-z-]+)/([1-9][0-9]*)", str(version))
        if match is None or match.group(1) not in POLICY_REGISTRIES:
            errors.append(f"{entry_scope}: unknown or invalid policy version {version!r}")
        elif POLICY_REGISTRIES[match.group(1)] != relative:
            errors.append(f"{entry_scope}: policy version is bound to the wrong registry")
        if re.fullmatch(r"[0-9a-f]{64}", str(digest)) is None:
            errors.append(f"{entry_scope}: lowercase semantic SHA-256 required")
        if isinstance(version, str):
            if version in by_version:
                errors.append(f"{scope}: duplicate policy version {version}")
            else:
                by_version[version] = entry
    return errors, by_version


def git_privacy_policy_history(ref: str, required: bool = False) -> dict[str, Any] | None:
    if re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9._/@{}^~-]{0,255}", ref) is None:
        raise ValueError("privacy policy locks: invalid trusted history reference")
    result = subprocess.run(
        ["git", "show", f"{ref}:contracts/privacy-policy-locks.yaml"],
        cwd=ROOT, text=True, capture_output=True, check=False,
    )
    if result.returncode != 0:
        if required:
            raise ValueError(f"privacy policy locks: required trusted history unavailable at {ref}")
        return None
    try:
        value = json.loads(result.stdout, object_pairs_hook=reject_duplicate_pairs)
    except (json.JSONDecodeError, ValueError) as exc:
        raise ValueError(f"privacy policy locks: invalid trusted history at {ref}: {exc}") from exc
    if not isinstance(value, dict):
        raise ValueError(f"privacy policy locks: trusted history at {ref} must be a mapping")
    return value


def validate_privacy_policy_locks(
    data: dict[str, Any] | None = None,
    historical: dict[str, Any] | None = None,
    registry_overrides: dict[str, dict[str, Any]] | None = None,
) -> list[str]:
    data = load(POLICY_LOCK_PATH) if data is None else data
    errors, current = policy_lock_entries(data)
    historical_entries: dict[str, dict[str, Any]] = {}
    if historical is not None:
        history_errors, historical_entries = policy_lock_entries(historical, "trusted privacy policy history")
        errors.extend(history_errors)
        prior_locks = historical.get("locks", [])
        current_locks = data.get("locks", [])
        if not isinstance(prior_locks, list) or not isinstance(current_locks, list) or current_locks[:len(prior_locks)] != prior_locks:
            errors.append("privacy policy locks: trusted lock list must remain an exact append-only prefix")
        for version, prior in historical_entries.items():
            if current.get(version) != prior:
                errors.append(f"privacy policy locks: historical entry {version} was removed or changed")

    values = registry_set(registry_overrides)
    latest_by_base: dict[str, tuple[int, dict[str, Any]]] = {}
    for version, entry in current.items():
        match = re.fullmatch(r"(privacy\.[a-z-]+)/([1-9][0-9]*)", version)
        if match is None:
            continue
        base, ordinal_text = match.groups()
        ordinal = int(ordinal_text)
        previous = latest_by_base.get(base)
        if previous is None or ordinal > previous[0]:
            latest_by_base[base] = (ordinal, entry)
    for base in POLICY_REGISTRIES:
        ordinals = sorted(
            int(version.rsplit("/", 1)[1])
            for version in current
            if version.startswith(f"{base}/") and re.fullmatch(r"[1-9][0-9]*", version.rsplit("/", 1)[1])
        )
        if ordinals and ordinals != list(range(1, ordinals[-1] + 1)):
            errors.append(f"privacy policy locks: {base} versions must start at 1 and remain contiguous")
    if set(latest_by_base) != set(POLICY_REGISTRIES):
        errors.append("privacy policy locks: every security-significant privacy registry requires a versioned lock")
    for base, relative in POLICY_REGISTRIES.items():
        latest = latest_by_base.get(base)
        if latest is None or relative not in values:
            continue
        actual = canonical_semantic_sha256(values[relative])
        if latest[1].get("semantic_sha256") != actual:
            errors.append(f"privacy policy locks: {relative} semantics changed without a new reviewed policy-version lock")
    return errors


def validate_independent_security_invariants(
    overrides: dict[str, dict[str, Any]] | None = None,
) -> list[str]:
    """Review-controlled invariants deliberately not derived from mutable registries."""
    values = registry_set(overrides)
    ingress = values["contracts/privacy/ingress.yaml"]
    installer = values["contracts/privacy/installer.yaml"]
    host_access = values["contracts/privacy/host-access.yaml"]
    deployment = values["contracts/privacy/deployment.yaml"]
    errors: list[str] = []

    if ingress.get("source_schemas") != [EXPECTED_SOURCE_SCHEMA]:
        errors.append("independent policy: exact source input and catalog allowlists required")
    if ingress.get("durable_record_fields") != EXPECTED_RECORD_FIELD_ORDER:
        errors.append("independent policy: exact durable Go schema fields required")
    if ingress.get("safe_error_fields") != EXPECTED_ERROR_FIELD_ORDER:
        errors.append("independent policy: exact safe error Go schema fields required")
    if ingress.get("nested_types") != EXPECTED_NESTED_TYPES:
        errors.append("independent policy: full nested Go schemas and types must remain exact and closed")
    if ingress.get("privacy_safe_log_fields") != EXPECTED_SAFE_LOG_FIELDS:
        errors.append("independent policy: exact privacy-safe log field schema required")
    if any(ingress.get(key) is not False for key in ("stable_prompt_hashes", "embeddings", "optional_prompt_hmac")):
        errors.append("independent policy: raw/content-derived prompt persistence features are prohibited")
    safe_names = set(ingress.get("durable_record_fields", [])) | set(ingress.get("safe_error_fields", [])) | set(ingress.get("privacy_safe_log_fields", []))
    for nested in ingress.get("nested_types", {}).values():
        if isinstance(nested, dict) and isinstance(nested.get("fields"), dict):
            safe_names.update(nested["fields"])
    prohibited_safe_names = {"raw", "content", "text", "free_text", "prompt_text", "response_text", "source_code", "tool_input", "tool_output", "payload", "arguments", "command", "environment", "credentials", "headers"}
    if safe_names & prohibited_safe_names:
        errors.append("independent policy: raw, content, free-text or sensitive value fields are prohibited at the safe boundary")
    ingress_controls = {item.get("id"): item.get("requirement") for item in ingress.get("controls", []) if isinstance(item, dict)}
    if ingress_controls != EXPECTED_INGRESS_CONTROLS or len(ingress.get("controls", [])) != len(EXPECTED_INGRESS_CONTROLS):
        errors.append("independent policy: exact nonempty ingress controls required")

    targets = {item.get("id"): item for item in installer.get("targets", []) if isinstance(item, dict)}
    if set(targets) != set(EXPECTED_INSTALLER_TARGET_POLICIES):
        errors.append("independent policy: exact installer target set required")
    for target_id, expected in EXPECTED_INSTALLER_TARGET_POLICIES.items():
        target = targets.get(target_id, {})
        if target.get("required_settings") != expected["required_settings"] or target.get("forbidden_keys") != expected["forbidden_keys"]:
            errors.append(f"independent policy: exact required/forbidden installer values required for {target_id}")

    accesses = {item.get("id"): item for item in host_access.get("accesses", []) if isinstance(item, dict)}
    if set(accesses) != set(EXPECTED_HOST_ACCESS):
        errors.append("independent policy: exact host access set required")
    for access_id, expected in EXPECTED_HOST_ACCESS.items():
        actual = accesses.get(access_id, {})
        if (actual.get("mode"), actual.get("scope"), actual.get("default_enabled")) != expected:
            errors.append(f"independent policy: exact host mode/scope/default required for {access_id}")

    http = deployment.get("http", {})
    if http.get("allowed_hosts") != ["127.0.0.1", "::1", "localhost"] or http.get("allowed_origins") != ["http://127.0.0.1:3000", "http://[::1]:3000", "http://localhost:3000"] or http.get("listener_addresses") != ["127.0.0.1:3000", "[::1]:3000", "127.0.0.1:4318", "[::1]:4318"]:
        errors.append("independent policy: exact loopback-only hosts, origins and listeners required")
    if http.get("route_modes") != EXPECTED_ROUTE_MODES or "GET" in http.get("route_modes", {}).get("hook_otlp", {}).get("methods", []):
        errors.append("independent policy: exact route methods required and hook_otlp must prohibit GET")
    deployment_controls = {item.get("id"): item.get("requirements") for item in deployment.get("controls", []) if isinstance(item, dict)}
    if deployment_controls != EXPECTED_DEPLOYMENT_CONTROLS or len(deployment.get("controls", [])) != len(EXPECTED_DEPLOYMENT_CONTROLS):
        errors.append("independent policy: exact nonempty deployment controls required")
    return errors


def validate_security_policy_candidate(
    overrides: dict[str, dict[str, Any]],
    embedded_runtime_sha256: str,
    lock_data: dict[str, Any] | None = None,
    historical: dict[str, Any] | None = None,
) -> list[str]:
    """Validate a candidate even when its mutable registry/runtime aggregate is coherent."""
    errors: list[str] = []
    aggregate = privacy_registry_sha256(overrides)
    if embedded_runtime_sha256 != aggregate:
        errors.append("security policy candidate: runtime aggregate binding differs from candidate registries")
    errors.extend(validate_independent_security_invariants(overrides))
    errors.extend(validate_privacy_policy_locks(lock_data, historical, overrides))
    return errors


def validate_registry_runtime_binding() -> list[str]:
    expected = privacy_registry_sha256()
    errors: list[str] = []
    bindings = {
        ROOT / "internal/privacy/types.go": "PrivacyContractSemanticSHA256",
        ROOT / "internal/installer/protocol.go": "InstallerContractSemanticSHA256",
        ROOT / "internal/localhttp/security.go": "DeploymentContractSemanticSHA256",
    }
    for path, name in bindings.items():
        source = path.read_text(encoding="utf-8")
        match = re.search(rf'{name}\s*=\s*"([0-9a-f]{{64}})"', source)
        if match is None or match.group(1) != expected:
            errors.append(f"registry binding: {name} must equal authoritative privacy registry SHA-256 {expected}")
    return errors


def unique_ids(items: Any, scope: str) -> tuple[list[str], set[str]]:
    errors: list[str] = []
    if not isinstance(items, list):
        return [f"{scope}: entries must be a list"], set()
    ids: list[str] = []
    for item in items:
        if not isinstance(item, dict) or not isinstance(item.get("id"), str) or not item["id"]:
            errors.append(f"{scope}: every entry requires a non-empty id")
        else:
            ids.append(item["id"])
    if len(ids) != len(set(ids)):
        errors.append(f"{scope}: duplicate IDs")
    return errors, set(ids)


def exact_registry_semantics(name: str, data: dict[str, Any]) -> list[str]:
    return [] if data == registry(name) else [f"{name}: authoritative recursive registry semantics changed"]


def validate_data_classes(data: dict[str, Any] | None = None) -> list[str]:
    data = registry("data-classes.yaml") if data is None else data
    errors = exact_registry_semantics("data-classes.yaml", data)
    id_errors, ids = unique_ids(data.get("classes"), "data classes")
    errors.extend(id_errors)
    if ids != EXPECTED_CLASSES:
        errors.append(f"data classes: expected closed set {sorted(EXPECTED_CLASSES)}")
    by_id = {item.get("id"): item for item in data.get("classes", []) if isinstance(item, dict)}
    prohibited = by_id.get("prohibited_content", {})
    for field in ("durable", "logging", "export", "backup"):
        if prohibited.get(field) is not False:
            errors.append(f"prohibited_content: {field} must be false")
    sensitive = by_id.get("sensitive_identifier", {})
    if sensitive.get("durable") != "keyed_hmac_or_explicit_alias_only":
        errors.append("sensitive_identifier: durable treatment must be keyed HMAC or explicit alias only")
    aliases = data.get("prohibited_durable_aliases")
    if not isinstance(aliases, list) or len(aliases) < 30 or len(aliases) != len(set(aliases)):
        errors.append("data classes: comprehensive unique prohibited durable aliases required")
    states = data.get("state_invariants", {}).get("canonical_value_states")
    glossary_states = load(ROOT / "contracts" / "glossary.yaml").get("state_registry", {}).get("value_states")
    if states != EXPECTED_VALUE_STATES or states != glossary_states:
        errors.append("data classes: canonical states must match the glossary exactly")
    return errors


def validate_threat_model(data: dict[str, Any] | None = None) -> list[str]:
    data = registry("threat-model.yaml") if data is None else data
    errors = exact_registry_semantics("threat-model.yaml", data)
    id_errors, threat_ids = unique_ids(data.get("threats"), "threat model")
    errors.extend(id_errors)
    if len(threat_ids) < 10:
        errors.append("threat model: all ten accepted abuse cases require stable IDs")
    if not data.get("protected_assets") or not data.get("trust_boundaries"):
        errors.append("threat model: protected assets and trust boundaries required")
    controls = set()
    for name in ("ingress.yaml", "deployment.yaml"):
        controls.update(item.get("id") for item in registry(name).get("controls", []) if isinstance(item, dict))
    controls.update({registry("sinks.yaml").get("control_id"), registry("installer.yaml").get("control_id"),
                     registry("host-access.yaml").get("control_id"), registry("retention.yaml").get("control_id"),
                     "product_non_goal", "privacy_manifest"})
    for threat in data.get("threats", []):
        if not isinstance(threat, dict):
            continue
        unknown = set(threat.get("controls", [])) - controls
        if unknown:
            errors.append(f"{threat.get('id')}: unknown controls {sorted(unknown)}")
        if not threat.get("tests"):
            errors.append(f"{threat.get('id')}: abuse-case tests required")
    if "an administrator or root user already controlling the host" not in data.get("out_of_scope", []):
        errors.append("threat model: root/admin boundary must remain explicit")
    return errors


def validate_ingress(data: dict[str, Any] | None = None) -> list[str]:
    data = registry("ingress.yaml") if data is None else data
    errors: list[str] = exact_registry_semantics("ingress.yaml", data)
    limits = data.get("limits", {})
    expected_limits = {
        "max_total_bytes": 1048576, "max_depth": 16, "max_array_items": 1024,
        "max_object_fields": 1024, "max_string_bytes": 65536, "max_number_bytes": 128,
        "max_records": 128, "max_protobuf_frame_bytes": 1048576,
    }
    for key, expected in expected_limits.items():
        if limits.get(key) != expected:
            errors.append(f"ingress limits: {key} must be {expected}")
    if limits.get("compressed_input") != "reject_until_streaming_bomb_limits_are_implemented":
        errors.append("ingress: compressed input must fail closed in Session 02")
    if set(data.get("durable_record_fields", [])) != EXPECTED_RECORD_FIELDS:
        errors.append("ingress: durable SafeRecord allowlist differs from contract")
    if set(data.get("safe_error_fields", [])) != EXPECTED_ERROR_FIELDS:
        errors.append("ingress: SafeError allowlist differs from contract")
    aliases = set(registry("data-classes.yaml").get("prohibited_durable_aliases", []))
    if aliases & set(data.get("durable_record_fields", [])):
        errors.append("ingress: prohibited aliases appear in durable record fields")
    if aliases & set(data.get("privacy_safe_log_fields", [])):
        errors.append("ingress: prohibited aliases appear in safe log fields")
    schemas = data.get("source_schemas", [])
    schema_errors, schema_ids = unique_ids(schemas, "source schemas")
    errors.extend(schema_errors)
    if schema_ids != {"fixture.agent-hook/1"}:
        errors.append("ingress: Session 02 may expose only the bounded synthetic fixture schema")
    if data.get("stable_prompt_hashes") is not False or data.get("embeddings") is not False or data.get("optional_prompt_hmac") is not False:
        errors.append("ingress: prompt hashes, embeddings and optional prompt HMAC remain disabled")
    nested = data.get("nested_types", {})
    expected_nested = {"CatalogObservation", "PromptFeatures", "TelemetryMeasurements", "RedactionCounts", "Lineage", "SafeLogEvent"}
    if set(nested) != expected_nested or any(set(value) != {"fields", "closed"} or value.get("closed") is not True for value in nested.values() if isinstance(value, dict)):
        errors.append("ingress: nested boundary types must be an exact closed schema")
    policy = data.get("decoder_policy", {})
    if policy.get("duplicate_names") != "reject_after_json_unescape_before_map_materialization" or policy.get("unicode") != "valid_utf8_and_paired_utf16_escapes_only" or policy.get("numbers") != "finite_ieee754_range_only":
        errors.append("ingress: strict duplicate/Unicode/numeric decoder policy required")
    return errors


def validate_sinks(data: dict[str, Any] | None = None) -> list[str]:
    data = registry("sinks.yaml") if data is None else data
    errors = exact_registry_semantics("sinks.yaml", data)
    id_errors, ids = unique_ids(data.get("required_sinks"), "privacy sinks")
    errors.extend(id_errors)
    if ids != EXPECTED_SINKS:
        errors.append(f"privacy sinks: expected exact closed set {sorted(EXPECTED_SINKS)}")
    scopes: list[str] = []
    for item in data.get("required_sinks", []):
        if not isinstance(item, dict) or set(item) != {"id", "slo_scope", "durable"} or not isinstance(item.get("durable"), bool) or not isinstance(item.get("slo_scope"), str):
            errors.append("privacy sinks: each sink requires only typed id/slo_scope/durable fields")
        elif item["slo_scope"] in scopes:
            errors.append("privacy sinks: SLO scopes must be one-to-one")
        else:
            scopes.append(item["slo_scope"])
    slo = load(ROOT / "contracts" / "slo.yaml")
    raw_slo = next((item for item in slo.get("slos", []) if item.get("id") == "raw-content-persisted-count"), {})
    required_scopes = raw_slo.get("completeness_requirement", {}).get("required_evidence_scopes", [])
    if scopes != required_scopes:
        errors.append("privacy sinks: ordered one-to-one sink IDs to raw-content SLO scopes required")
    proof = data.get("proof", {})
    if proof.get("raw_canary_target") != 0 or proof.get("unknown_schema_sink") != "metadata-only quarantine":
        errors.append("privacy sinks: zero raw canary and metadata-only quarantine proof required")
    if not (ROOT / str(proof.get("fixture", "missing"))).is_file():
        errors.append("privacy sinks: raw canary fixture is missing")
    return errors


def validate_installer(data: dict[str, Any] | None = None) -> list[str]:
    data = registry("installer.yaml") if data is None else data
    errors = exact_registry_semantics("installer.yaml", data)
    id_errors, target_ids = unique_ids(data.get("targets"), "installer targets")
    errors.extend(id_errors)
    if target_ids != EXPECTED_TARGETS:
        errors.append(f"installer: expected exact target set {sorted(EXPECTED_TARGETS)}")
    if set(data.get("preview_required_fields", [])) != EXPECTED_PREVIEW_FIELDS:
        errors.append("installer: exact preview fields differ from protocol")
    if data.get("approval_binding") != ["plan_sha256", "target_id", "original_sha256", "planned_sha256", "approval_nonce"]:
        errors.append("installer: consent must bind plan, target, both revisions and nonce")
    protocol = data.get("protocol", [])
    for stage in ("exact_preview", "per_target_explicit_consent", "prewrite_revision_check", "revision_checked_rollback_or_remove"):
        if stage not in protocol:
            errors.append(f"installer: missing {stage}")
    if data.get("implementation_scope_session_02") != "protocol and virtual apply/rollback model only; no real agent configuration mutation":
        errors.append("installer: Session 02 must not claim or perform real agent configuration mutation")
    target_keys = {"id", "agent_id", "config_locator_kind", "format", "ownership", "required_settings", "forbidden_keys", "precedence_checks", "disable_remove"}
    for target in data.get("targets", []):
        if set(target) != target_keys or not target.get("required_settings") or not target.get("forbidden_keys") or not target.get("precedence_checks") or not target.get("disable_remove"):
            errors.append(f"installer target {target.get('id')}: exact settings, precedence, ownership and removal required")
    gate = data.get("effective_settings_gate", {})
    if gate != {"required": True, "managed_or_environment_override": "fail_closed", "runtime_canary": "required_before_real_write", "session_02_real_write": False}:
        errors.append("installer: effective-setting and runtime-canary gate must fail closed with no Session 02 writes")
    return errors


def validate_host_access(data: dict[str, Any] | None = None) -> list[str]:
    data = registry("host-access.yaml") if data is None else data
    errors = exact_registry_semantics("host-access.yaml", data)
    id_errors, ids = unique_ids(data.get("accesses"), "host access")
    errors.extend(id_errors)
    expected = {"installer_agent_config", "runtime_hook_ingress", "historical_import_root", "identity_key_file", "database_password_secret", "kansoku_data_volume", "database_volume"}
    if data.get("closed_world") is not True or ids != expected:
        errors.append(f"host access: expected exact closed world {sorted(expected)}")
    for access in data.get("accesses", []):
        required = {"id", "actor", "mode", "scope", "default_enabled", "justification", "disable_remove"}
        if not isinstance(access, dict) or set(access) != required:
            errors.append("host access: every entry requires exact typed ownership/disable fields")
    identity = next((item for item in data.get("accesses", []) if isinstance(item, dict) and item.get("id") == "identity_key_file"), {})
    if not all(fragment in str(identity.get("scope", "")) for fragment in ("0600", "nlink 1", "owner-only 0700", "fd-relative")):
        errors.append("host access: identity key requires 0600/nlink1 and an fd-relative owner-only directory")
    forbidden = set(data.get("forbidden_mounts", []))
    if not {"home root", "Docker socket", "SSH directory", "OS keychain", "agent authentication store"} <= forbidden:
        errors.append("host access: forbidden mount set is incomplete")
    if "never writes agent configuration" not in str(data.get("runtime_agent_access")):
        errors.append("host access: runtime must remain read-only toward agents")
    return errors


def validate_retention(data: dict[str, Any] | None = None) -> list[str]:
    data = registry("retention.yaml") if data is None else data
    errors: list[str] = exact_registry_semantics("retention.yaml", data)
    required_surfaces = {"normalized metadata", "sanitized ingest envelopes", "metadata-only quarantine", "hourly/daily rollups", "operational SLO samples", "audit/installer records", "exports", "daily backups", "weekly backups"}
    if set(data.get("surfaces", [])) != required_surfaces:
        errors.append("retention: every live/derived/export/backup surface must be enumerated")
    protocol = data.get("deletion_protocol", [])
    if len(protocol) < 6 or not any("explicit consent" in item for item in protocol) or not any("backups" in item for item in protocol):
        errors.append("retention: preview, consent, verification and backup coverage required")
    if not data.get("postgresql_disclosure") or not data.get("backup_disclosure"):
        errors.append("retention: PostgreSQL and backup erasure limitations must be disclosed")
    identity = data.get("identity_key", {})
    if any(identity.get(field) is not False for field in ("retained_in_database", "included_in_export", "included_in_backup")):
        errors.append("retention: identity key must be excluded from database/export/backup")
    product = load(ROOT / "contracts" / "product.yaml")
    defaults = product.get("decisions", {}).get("retention_defaults", {})
    expected_defaults = {
        "normalized_metadata_days": 365, "sanitized_ingest_envelopes_days": 7,
        "metadata_only_quarantine_days": 30, "hourly_daily_rollups_days": 1095,
        "operational_slo_samples_days": 90, "audit_and_installer_records_days": 365,
    }
    for key, expected in expected_defaults.items():
        if defaults.get(key) != expected:
            errors.append(f"retention: product default {key} changed from {expected}")
    if defaults.get("backups") != {"daily_count": 7, "weekly_count": 4}:
        errors.append("retention: backup defaults must remain 7 daily and 4 weekly")
    return errors


def validate_go_boundary() -> list[str]:
    errors: list[str] = []
    source = (ROOT / "internal" / "privacy" / "types.go").read_text(encoding="utf-8")
    record_fields = struct_json_fields(source, "SafeRecord")
    error_fields = struct_json_fields(source, "SafeError")
    if record_fields != EXPECTED_RECORD_FIELDS:
        errors.append("Go boundary: SafeRecord JSON fields differ from ingress allowlist")
    if error_fields != EXPECTED_ERROR_FIELDS:
        errors.append("Go boundary: SafeError JSON fields differ from ingress allowlist")
    for struct_name in ("SafeRecord", "SafeError"):
        body = struct_body(source, struct_name)
        if "map[string]any" in body or "json.RawMessage" in body or "interface{}" in body:
            errors.append(f"Go boundary: {struct_name} cannot contain a generic payload")
    for struct_name in ("CatalogObservation", "PromptFeatures", "TelemetryMeasurements", "RedactionCounts", "Lineage", "SafeRecord", "SafeError"):
        if struct_json_schema(source, struct_name) != EXPECTED_GO_SCHEMAS[struct_name]:
            errors.append(f"Go boundary: {struct_name} differs from independent exact field/type schema")
    sink_source = (ROOT / "internal" / "privacy" / "sinks.go").read_text(encoding="utf-8")
    if struct_json_schema(sink_source, "SafeLogEvent") != EXPECTED_GO_SCHEMAS["SafeLogEvent"]:
        errors.append("Go boundary: SafeLogEvent differs from independent exact field/type schema")
    production_go = "\n".join(
        path.read_text(encoding="utf-8")
        for path in sorted((ROOT / "internal").rglob("*.go"))
        if not path.name.endswith("_test.go")
    )
    for forbidden in ('%+v', 'log.Printf', 'log.Println', 'slog.Any'):
        if forbidden in production_go:
            errors.append(f"Go boundary: forbidden unsafe logging operation {forbidden}")
    if "func SerializeAllSinks(records []SafeRecord, safeErr *SafeError)" not in sink_source:
        errors.append("Go boundary: sink serialization must accept typed safe values only")
    installer_source = (ROOT / "internal" / "installer" / "protocol.go").read_text(encoding="utf-8")
    for forbidden in ("os.WriteFile", "os.Rename", "os.OpenFile", "syscall.Open"):
        if forbidden in installer_source:
            errors.append(f"installer: real filesystem mutation forbidden in Session 02 ({forbidden})")
    return errors


def struct_body(source: str, name: str) -> str:
    match = re.search(rf"type {re.escape(name)} struct \{{(.*?)\n\}}", source, re.DOTALL)
    return match.group(1) if match else ""


def struct_json_fields(source: str, name: str) -> set[str]:
    return set(re.findall(r'json:"([^",]+)', struct_body(source, name)))


def struct_json_schema(source: str, name: str) -> dict[str, str]:
    schema: dict[str, str] = {}
    pattern = re.compile(r'^\s*[A-Za-z][A-Za-z0-9]*\s+([^\s]+)\s+`json:"([^",]+)', re.MULTILINE)
    for go_type, json_name in pattern.findall(struct_body(source, name)):
        schema[json_name] = go_type
    return schema


def validate_deployment(data: dict[str, Any] | None = None, compose: dict[str, Any] | None = None) -> list[str]:
    data = registry("deployment.yaml") if data is None else data
    compose = compose or load(ROOT / "deploy" / "compose.security-baseline.yaml")
    errors: list[str] = exact_registry_semantics("deployment.yaml", data)
    control_ids = {item.get("id") for item in data.get("controls", []) if isinstance(item, dict)}
    if control_ids != {"PRV-HTTP-001", "PRV-CONTAINER-001", "PRV-EGRESS-001"}:
        errors.append("deployment: HTTP, container and egress controls are all required")
    http = data.get("http", {})
    if http.get("cors") != "same_origin_only" or http.get("mutation_auth") != "bearer_plus_csrf":
        errors.append("deployment: same-origin and bearer+CSRF mutation controls required")
    if http.get("bearer_secret_min_bytes") != 32 or http.get("csrf_secret_min_bytes") != 32:
        errors.append("deployment: bearer and CSRF secrets must each be at least 32 bytes")
    if http.get("allowed_hosts") != ["127.0.0.1", "::1", "localhost"] or http.get("forwarded_headers") != "reject" or http.get("secrets_must_differ") is not True or set(http.get("route_modes", {})) != {"ui_stream", "hook_otlp", "ui_mutation"}:
        errors.append("deployment: exact canonical host, route mode, forwarded-header and separate-secret policy required")
    frontend = data.get("frontend", {})
    if any(frontend.get(field) is not False for field in ("remote_assets", "cdn", "remote_fonts", "analytics", "automatic_crash_upload")):
        errors.append("deployment: remote frontend assets/analytics/crash upload must be disabled")
    services = compose.get("services", {})
    if set(services) != {"app", "database"}:
        errors.append("compose: exact app/database service baseline required")
        return errors
    for service_id, service in services.items():
        if service.get("user", "").split(":", 1)[0] in {"", "0", "root"}:
            errors.append(f"compose {service_id}: non-root user required")
        if service.get("read_only") is not True:
            errors.append(f"compose {service_id}: read-only root filesystem required")
        if service.get("cap_drop") != ["ALL"]:
            errors.append(f"compose {service_id}: cap_drop ALL required")
        if "no-new-privileges:true" not in service.get("security_opt", []):
            errors.append(f"compose {service_id}: no-new-privileges required")
        image = service.get("image", "")
        if service_id == "app":
            expected_image = "${KANSOKU_IMAGE_REPOSITORY:?set immutable image repository}@sha256:${KANSOKU_IMAGE_SHA256:?set exact 64-character digest}"
            if image != expected_image:
                errors.append("compose app: caller must supply an immutable digest image")
        elif "@sha256:" not in image or not re.fullmatch(r"[^@]+@sha256:[0-9a-f]{64}", image):
            errors.append("compose database: immutable digest image required")
        for volume in service.get("volumes", []):
            source = volume.split(":", 1)[0]
            if source.startswith(("/", "~", ".")):
                errors.append(f"compose {service_id}: host bind mount forbidden by default")
    app = services["app"]
    if app.get("ports"):
        errors.append("compose app: Session 02 static placeholder must publish no unusable ports")
    if data.get("compose_reachability") != {"session_02": "unreachable_static_placeholder", "published_ports": False, "reason": "a process bound to container loopback is not reachable through Docker port publishing; Session 09 owns a tested secure topology"}:
        errors.append("compose: static placeholder reachability limitation must be exact")
    if services["database"].get("ports"):
        errors.append("compose database: database port must not be published")
    networks = compose.get("networks", {})
    if networks != {"kansoku-internal": {"internal": True}}:
        errors.append("compose: a single internal default-deny network is required")
    rendered = json.dumps(compose, sort_keys=True)
    for forbidden in ("docker.sock", "/Users/", "/home/", "/root/", ".ssh", ".gnupg"):
        if forbidden in rendered:
            errors.append(f"compose: forbidden host access {forbidden}")
    secret = compose.get("secrets", {}).get("database_password", {})
    if secret.get("file") != "${KANSOKU_DB_PASSWORD_FILE:?set exact rootless secret path}":
        errors.append("compose: database password must use one explicit rootless secret path")
    return errors


def validate_documentation() -> list[str]:
    errors: list[str] = []
    required_refs = [
        "contracts/privacy/threat-model.yaml", "contracts/privacy/data-classes.yaml",
        "contracts/privacy/ingress.yaml", "contracts/privacy/sinks.yaml",
        "contracts/privacy/installer.yaml", "contracts/privacy/host-access.yaml",
        "contracts/privacy/deployment.yaml", "contracts/privacy/retention.yaml",
        "contracts/privacy-policy-locks.yaml", "reports/session-02-reconciliation.md",
        "adr/0004-session-02-privacy-boundary.md",
        "adr/0005-privacy-policy-lock-and-trust-root.md",
    ]
    documents = "\n".join(
        path.read_text(encoding="utf-8")
        for path in (ROOT / "README.md", ROOT / "ROADMAP.md",
                     ROOT / "Engineering Proposal" / "02-privacy-security-and-trust.md",
                     ROOT / "Technical Design Document" / "02-privacy-security-and-trust.md")
    )
    for reference in required_refs:
        if reference not in documents:
            errors.append(f"documentation: missing Session 02 reference {reference}")
    sources = (ROOT / "SOURCES.md").read_text(encoding="utf-8")
    if sources.count("Retrieved: 2026-07-21") < 5:
        errors.append("sources: current retrieval date required for four agents and OpenTelemetry")
    return errors


def validate_evidence() -> list[str]:
    errors: list[str] = []
    canary = load(ROOT / "reports" / "session-02-canary-results.json")
    if canary.get("status") != "pass" or canary.get("canary_match_count") != 0 or canary.get("secret_format_match_count") != 0:
        errors.append("evidence: committed canary result must pass with zero raw/secret matches")
    expected_top = {"schema_version", "status", "record_count", "sink_count", "canary_match_count", "secret_format_match_count", "preserved", "sinks", "rejection_sinks", "generator", "fixture", "fixture_sha256", "source_revision", "toolchain_image", "toolchain_digest", "generator_sha256", "independent_external_scan"}
    if set(canary) != expected_top:
        errors.append("evidence: canary report must use the exact closed schema")
    sinks = canary.get("sinks", [])
    sink_ids = {item.get("id") for item in sinks if isinstance(item, dict)}
    if sink_ids != EXPECTED_SINKS or canary.get("sink_count") != len(EXPECTED_SINKS):
        errors.append("evidence: committed canary result must cover the exact sink set")
    for item in sinks:
        if not isinstance(item, dict) or set(item) != {"id", "bytes", "sha256"} or not isinstance(item.get("bytes"), int) or item["bytes"] <= 0 or re.fullmatch(r"[0-9a-f]{64}", str(item.get("sha256"))) is None:
            errors.append("evidence: every sink requires positive bytes and a lowercase SHA-256")
    rejection_sinks = canary.get("rejection_sinks", [])
    if {item.get("id") for item in rejection_sinks if isinstance(item, dict)} != EXPECTED_SINKS:
        errors.append("evidence: rejection paths must materialize and scan every sink")
    for item in rejection_sinks:
        if not isinstance(item, dict) or set(item) != {"id", "bytes", "sha256"} or not isinstance(item.get("bytes"), int) or item["bytes"] <= 0 or re.fullmatch(r"[0-9a-f]{64}", str(item.get("sha256"))) is None:
            errors.append("evidence: every rejection sink requires positive bytes and SHA-256")
    external = canary.get("independent_external_scan", {})
    expected_external = {"canary_matches": 0, "secret_format_matches": 0, "accepted_artifacts_scanned": 10, "rejection_artifacts_scanned": 10, "backup_exact_bytes_match": True, "backup_checksum_match": True, "safe_record_exact_fields": True}
    if external != expected_external:
        errors.append("evidence: independent scan and backup restore/checksum proof required")
    sbom = load(ROOT / "reports" / "session-02-sbom.json")
    if sbom.get("verification", {}).get("session02_packages_standard_library_only") is not True:
        errors.append("evidence: Session 02 packages must remain standard-library only")
    if sbom.get("verification", {}).get("release_vulnerability_scan_status") != "deferred_and_release_blocking":
        errors.append("evidence: unavailable production vulnerability scan must remain release-blocking")
    return errors


def validate_all(
    include_documentation: bool = True,
    policy_history_ref: str | None = "HEAD",
    policy_history_required: bool = False,
) -> list[str]:
    errors: list[str] = []
    historical: dict[str, Any] | None = None
    if policy_history_ref is not None:
        try:
            historical = git_privacy_policy_history(policy_history_ref, required=policy_history_required)
        except ValueError as exc:
            errors.append(str(exc))
    validators = [
        validate_data_classes, validate_threat_model, validate_ingress, validate_sinks,
        validate_installer, validate_host_access, validate_retention, validate_go_boundary,
        validate_deployment, validate_evidence, validate_registry_runtime_binding,
        validate_independent_security_invariants,
    ]
    for validator in validators:
        try:
            errors.extend(validator())
        except (OSError, ValueError, TypeError, AttributeError) as exc:
            errors.append(f"{validator.__name__}: {exc}")
    try:
        errors.extend(validate_privacy_policy_locks(historical=historical))
    except (OSError, ValueError, TypeError, AttributeError) as exc:
        errors.append(f"validate_privacy_policy_locks: {exc}")
    if include_documentation:
        errors.extend(validate_documentation())
    return errors


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--json", action="store_true")
    parser.add_argument("--skip-documentation", action="store_true")
    parser.add_argument("--policy-history-ref", default="HEAD", help="trusted Git ref, or 'none' for archive/bootstrap validation")
    parser.add_argument("--require-policy-history", action="store_true", help="fail if the trusted policy lock history is unavailable")
    args = parser.parse_args()
    history_ref = None if args.policy_history_ref.lower() == "none" else args.policy_history_ref
    errors = validate_all(
        include_documentation=not args.skip_documentation,
        policy_history_ref=history_ref,
        policy_history_required=args.require_policy_history,
    )
    if args.json:
        print(json.dumps({"status": "pass" if not errors else "fail", "errors": errors}, indent=2, sort_keys=True))
    elif errors:
        for error in errors:
            print(f"ERROR: {error}")
    else:
        print("Session 02 privacy/security contract validation: pass")
    return 1 if errors else 0


if __name__ == "__main__":
    sys.exit(main())
