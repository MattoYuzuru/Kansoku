#!/usr/bin/env python3
"""Validate Session 01 contracts using only the Python standard library.

The .yaml registries intentionally use the JSON subset of YAML 1.2. This keeps
the bootstrap contract checks deterministic before a package manager exists.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import math
import re
import sqlite3
import subprocess
import sys
from pathlib import Path
from typing import Any, Iterable


ROOT = Path(__file__).resolve().parents[1]
CONTRACTS = ROOT / "contracts"
FIXTURES = ROOT / "tests" / "fixtures" / "session-01"
EXPECTED_ROUTES = {
    "/",
    "/activity",
    "/prompts",
    "/agents",
    "/agents/:id",
    "/models",
    "/components/skills",
    "/components/skills/:id",
    "/components/plugins",
    "/components/plugins/:id",
    "/components/mcp",
    "/tools",
    "/reliability",
    "/privacy",
    "/system",
    "/glossary",
    "/settings",
}
IDENTIFIER_RE = re.compile(r"^[a-z0-9][a-z0-9._/:_-]{2,127}$")
SEMVER_CORE_RE = re.compile(r"^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$")
SHA256_RE = re.compile(r"^[0-9a-f]{64}$")
FORMULA_IMPLEMENTATIONS = {
    "count": "count_distinct_records",
    "sum": "sum_record_field",
    "duration": "sum_record_field",
    "ratio": "ratio_of_record_field_sums",
    "p95": "percentile_cont_record_field",
    "mean": "mean_record_field",
    "latest": "latest_record_by_order_field",
    "max": "max_record_field",
}
EVALUATOR_PARAMETERS = {
    "count": {"distinct_field": "record_id"},
    "sum": {"value_field": "value"},
    "duration": {"value_field": "value"},
    "ratio": {
        "numerator_field": "numerator",
        "denominator_field": "denominator",
        "zero_denominator_state": "unknown",
    },
    "p95": {
        "value_field": "value",
        "probability": 0.95,
        "method": "percentile_cont",
        "null_policy": "explicit_exclusion_only",
    },
    "mean": {"value_field": "value"},
    "latest": {
        "value_field": "value",
        "order_field": "observed_order",
        "direction": "ascending",
    },
    "max": {"value_field": "value"},
}


def load_json_yaml(path: Path) -> dict[str, Any]:
    try:
        value = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise ValueError(f"cannot load {path.relative_to(ROOT)}: {exc}") from exc
    if not isinstance(value, dict):
        raise ValueError(f"{path.relative_to(ROOT)} must contain a mapping")
    return value


def registry(name: str) -> dict[str, Any]:
    return load_json_yaml(CONTRACTS / name)


def fixture(name: str) -> dict[str, Any]:
    return load_json_yaml(FIXTURES / name)


def unique_ids(items: Iterable[dict[str, Any]], field: str, scope: str) -> list[str]:
    errors: list[str] = []
    seen: set[str] = set()
    for item in items:
        value = item.get(field)
        if not isinstance(value, str) or not value:
            errors.append(f"{scope}: missing non-empty {field}")
        elif value in seen:
            errors.append(f"{scope}: duplicate {field} {value}")
        else:
            seen.add(value)
    return errors


def validate_glossary() -> list[str]:
    data = registry("glossary.yaml")
    errors = unique_ids(data.get("terms", []), "id", "glossary terms")
    term_ids = {term.get("id") for term in data.get("terms", [])}
    required = {
        "observed",
        "unsupported",
        "not_observed",
        "redacted",
        "unknown",
        "numeric_zero",
        "complete",
        "partial",
        "degraded",
    }
    missing = sorted(required - term_ids)
    if missing:
        errors.append(f"glossary: missing required terms {missing}")
    forbidden = data.get("forbidden_aliases", [])
    errors.extend(unique_ids(forbidden, "alias", "forbidden aliases"))
    for item in forbidden:
        if not item.get("use_instead"):
            errors.append(f"forbidden alias {item.get('alias')}: use_instead is required")
    states = data.get("state_registry", {})
    expected_value_states = ["observed", "unsupported", "not_observed", "redacted", "unknown", "numeric_zero"]
    expected_completeness_states = ["complete", "partial", "degraded"]
    if states.get("value_states") != expected_value_states:
        errors.append(f"glossary: canonical value states must be {expected_value_states}")
    if states.get("completeness_states") != expected_completeness_states:
        errors.append(f"glossary: canonical completeness states must be {expected_completeness_states}")
    expected_display_states = expected_completeness_states + [state for state in expected_value_states if state != "observed"]
    if states.get("display_states") != expected_display_states:
        errors.append("glossary: display states must compose completeness and user-visible value states")
    return errors


def validate_lifecycle() -> list[str]:
    data = registry("capabilities.yaml")
    cases = fixture("lifecycle-cases.yaml")
    errors: list[str] = []
    stages = data.get("lifecycle", {}).get("states", [])
    errors.extend(unique_ids(stages, "id", "lifecycle states"))
    by_id = {state.get("id"): state for state in stages}
    canonical = data.get("lifecycle", {}).get("canonical_progression", [])
    expected = ["installed", "enabled", "exposed", "invoked", "loaded", "executed", "succeeded"]
    if canonical != expected:
        errors.append(f"lifecycle: canonical progression must be {expected}")
    if data.get("lifecycle", {}).get("parallel_state") != "opportunity_detected":
        errors.append("lifecycle: opportunity_detected must remain a parallel state")

    rank = {state: index for index, state in enumerate(canonical)}
    for case in cases.get("classification_cases", []):
        prior = case.get("prior_state")
        observed = case.get("observed_state")
        tier = case.get("evidence_tier")
        actual_state: str | None = observed
        actual_error: str | None = None
        state = by_id.get(observed)
        if state is None:
            actual_state = None
            actual_error = "unknown_state"
        elif tier not in state.get("allowed_evidence_tiers", []):
            actual_state = None
            actual_error = "invalid_evidence_tier"
        elif observed != "opportunity_detected" and prior in rank and rank[observed] < rank[prior]:
            actual_state = None
            actual_error = "lifecycle_regression"
        if actual_state != case.get("expected_state") or actual_error != case.get("expected_error"):
            errors.append(
                f"lifecycle fixture {case.get('id')}: expected "
                f"({case.get('expected_state')}, {case.get('expected_error')}), got "
                f"({actual_state}, {actual_error})"
            )

    value_states = cases.get("value_state_cases", [])
    errors.extend(unique_ids(value_states, "input", "value-state cases"))
    outputs = [case.get("expected") for case in value_states]
    if len(outputs) != len(set(outputs)):
        errors.append("value-state fixtures must keep observed/unsupported/not_observed/redacted/unknown/numeric_zero distinct")
    return errors


def parse_version(scheme: str, value: Any) -> tuple[int, ...] | None:
    if scheme != "semver_core" or not isinstance(value, str):
        return None
    match = SEMVER_CORE_RE.fullmatch(value)
    return tuple(int(part) for part in match.groups()) if match else None


def validate_version_range(
    value: Any, schemes: set[str], scope: str
) -> tuple[list[str], tuple[tuple[int, ...], tuple[int, ...]] | None]:
    errors: list[str] = []
    if not isinstance(value, dict) or set(value) != {"scheme", "min_inclusive", "max_exclusive"}:
        return [f"{scope}: version range must contain scheme/min_inclusive/max_exclusive only"], None
    scheme = value.get("scheme")
    if scheme not in schemes:
        return [f"{scope}: unknown or unimplemented version scheme {scheme!r}"], None
    lower = parse_version(scheme, value.get("min_inclusive"))
    upper = parse_version(scheme, value.get("max_exclusive"))
    if lower is None:
        errors.append(f"{scope}: invalid min_inclusive for {scheme}")
    if upper is None:
        errors.append(f"{scope}: invalid max_exclusive for {scheme}")
    if lower is not None and upper is not None and lower >= upper:
        errors.append(f"{scope}: version range must be ordered min_inclusive < max_exclusive")
    return errors, (lower, upper) if not errors and lower is not None and upper is not None else None


def valid_identifier(value: Any) -> bool:
    return isinstance(value, str) and IDENTIFIER_RE.fullmatch(value) is not None


def validate_public_claim_evidence(
    claim: dict[str, Any], adapter_id: str, governance: dict[str, Any],
    artifacts_by_id: dict[str, dict[str, Any]],
) -> list[str]:
    capability_id = claim.get("capability_id")
    scope = f"{adapter_id}/{capability_id}"
    errors: list[str] = []
    schemes = set(governance.get("version_schemes", {})) - {"extension_policy"}
    version_range = claim.get("version_range")
    range_errors, _parsed = validate_version_range(version_range, schemes, scope)
    errors.extend(range_errors)
    evidence = claim.get("evidence")
    if not isinstance(evidence, dict) or set(evidence) != {
        "official_docs", "receipts", "human_classification_reviews"
    }:
        return errors + [f"{scope}: public evidence must use official_docs/receipts/human_classification_reviews only"]
    official_docs = evidence.get("official_docs")
    if not isinstance(official_docs, list) or not official_docs or any(not isinstance(url, str) or not url for url in official_docs):
        errors.append(f"{scope}: public claim requires non-empty official_docs list")
    required_kinds = set(governance.get("required_evidence_kinds", []))
    receipt_fields = set(governance.get("evidence_receipt_schema", {}).get("required_fields", []))
    receipts = evidence.get("receipts")
    if not isinstance(receipts, list):
        errors.append(f"{scope}: evidence receipts must be a list")
        receipts = []
    receipt_ids: list[str] = []
    receipt_kinds: list[str] = []
    for index, receipt in enumerate(receipts):
        receipt_scope = f"{scope}/receipt[{index}]"
        if not isinstance(receipt, dict) or set(receipt) != receipt_fields:
            errors.append(f"{receipt_scope}: typed receipt fields differ from contract")
            continue
        receipt_id = receipt.get("receipt_id")
        kind = receipt.get("kind")
        if not valid_identifier(receipt_id):
            errors.append(f"{receipt_scope}: invalid receipt_id")
        else:
            receipt_ids.append(receipt_id)
        if kind not in required_kinds:
            errors.append(f"{receipt_scope}: invalid evidence kind {kind!r}")
        else:
            receipt_kinds.append(kind)
        if receipt.get("adapter_id") != adapter_id or receipt.get("capability_id") != capability_id:
            errors.append(f"{receipt_scope}: adapter/capability binding mismatch")
        if receipt.get("version_range") != version_range:
            errors.append(f"{receipt_scope}: version-range binding mismatch")
        artifact_ids = receipt.get("artifact_ids")
        if not isinstance(artifact_ids, list) or not artifact_ids or any(not valid_identifier(item) for item in artifact_ids):
            errors.append(f"{receipt_scope}: artifact_ids must be non-empty bounded identifiers")
        else:
            for artifact_id in artifact_ids:
                artifact = artifacts_by_id.get(artifact_id)
                if artifact is None:
                    errors.append(f"{receipt_scope}: unresolved evidence artifact {artifact_id!r}")
                elif artifact.get("kind") != kind:
                    errors.append(f"{receipt_scope}: evidence artifact kind differs for {artifact_id!r}")
        if receipt.get("result") != governance.get("evidence_receipt_schema", {}).get("allowed_result"):
            errors.append(f"{receipt_scope}: receipt result must be pass")
    if set(receipt_kinds) != required_kinds or len(receipt_kinds) != len(required_kinds):
        errors.append(f"{scope}: exactly one passing receipt per required evidence kind is required")
    if len(receipt_ids) != len(set(receipt_ids)):
        errors.append(f"{scope}: duplicate evidence receipt IDs")

    review_schema = governance.get("human_review_receipt_schema", {})
    review_fields = set(review_schema.get("required_fields", []))
    reviews = evidence.get("human_classification_reviews")
    if not isinstance(reviews, list):
        errors.append(f"{scope}: human review receipts must be a list")
        reviews = []
    reviewer_ids: list[str] = []
    review_ids: list[str] = []
    for index, review in enumerate(reviews):
        review_scope = f"{scope}/review[{index}]"
        if not isinstance(review, dict) or set(review) != review_fields:
            errors.append(f"{review_scope}: typed review receipt fields differ from contract")
            continue
        review_id = review.get("review_id")
        reviewer_id = review.get("reviewer_id")
        if not valid_identifier(review_id) or not valid_identifier(reviewer_id):
            errors.append(f"{review_scope}: invalid review/reviewer identifier")
        else:
            review_ids.append(review_id)
            reviewer_ids.append(reviewer_id)
        if review.get("adapter_id") != adapter_id or review.get("capability_id") != capability_id:
            errors.append(f"{review_scope}: adapter/capability binding mismatch")
        if review.get("version_range") != version_range:
            errors.append(f"{review_scope}: version-range binding mismatch")
        fixture_ids = review.get("fixture_ids")
        if not isinstance(fixture_ids, list) or not fixture_ids or any(not valid_identifier(item) for item in fixture_ids):
            errors.append(f"{review_scope}: concrete fixture_ids are required")
        else:
            for fixture_id in fixture_ids:
                artifact = artifacts_by_id.get(fixture_id)
                if artifact is None:
                    errors.append(f"{review_scope}: unresolved classification fixture {fixture_id!r}")
                elif artifact.get("kind") != "classification_fixture":
                    errors.append(f"{review_scope}: review fixture kind differs for {fixture_id!r}")
        cited_receipts = review.get("evidence_receipt_ids")
        if not isinstance(cited_receipts, list) or set(cited_receipts) != set(receipt_ids):
            errors.append(f"{review_scope}: evidence receipt bindings must cite the exact claim receipts")
        if review.get("result") != review_schema.get("required_result"):
            errors.append(f"{review_scope}: review result must be approved")
    minimum = review_schema.get("minimum_independent_reviewers", 2)
    if len(reviewer_ids) < minimum or len(set(reviewer_ids)) < minimum:
        errors.append(f"{scope}: two distinct bound approved human review receipts required")
    if len(review_ids) != len(set(review_ids)):
        errors.append(f"{scope}: duplicate human review IDs")
    return errors


def canonical_json_v1(raw: bytes) -> tuple[bytes | None, dict[str, Any] | None]:
    def reject_non_finite(constant: str) -> None:
        raise ValueError(f"non-finite JSON constant {constant}")

    try:
        value = json.loads(raw.decode("utf-8"), parse_constant=reject_non_finite)
        if not isinstance(value, dict):
            return None, None
        canonical = (json.dumps(
            value, sort_keys=True, separators=(",", ":"), ensure_ascii=False, allow_nan=False
        ) + "\n").encode("utf-8")
    except (UnicodeDecodeError, json.JSONDecodeError, ValueError):
        return None, None
    return canonical, value


def validate_capability_data(data: dict[str, Any], artifact_root: Path = ROOT) -> list[str]:
    errors = unique_ids(data.get("capabilities", []), "id", "capabilities")
    capability_ids = {item.get("id") for item in data.get("capabilities", [])}
    labels = data.get("support_labels", {})
    if set(labels) != {"supported", "beta", "experimental", "unsupported"}:
        errors.append("support labels must be supported/beta/experimental/unsupported")
    agents = data.get("agent_evidence_baseline", [])
    errors.extend(unique_ids(agents, "adapter_id", "agent evidence baseline"))
    governance = data.get("support_governance", {})
    public_labels = set(governance.get("public_claim_labels", []))
    if public_labels != {"supported", "beta"}:
        errors.append("support governance: Supported and Beta must both be public evidence-gated claims")
    evidence_requirements = governance.get("required_evidence_kinds", [])
    expected_evidence = {
        "capability_contract",
        "privacy_test",
        "sanitized_fixture_replay",
        "passive_audit",
        "canary_or_end_to_end",
    }
    if set(evidence_requirements) != expected_evidence:
        errors.append("support governance: incomplete structured evidence requirements")
    artifact_schema = governance.get("artifact_registry_schema", {})
    artifact_fields = set(artifact_schema.get("required_fields", []))
    allowed_artifact_kinds = set(artifact_schema.get("allowed_kinds", []))
    expected_artifact_kinds = expected_evidence | {"classification_fixture"}
    expected_artifact_fields = {"artifact_id", "kind", "path", "canonicalization", "sha256"}
    allowed_path_root = artifact_schema.get("allowed_path_root")
    if (
        artifact_fields != expected_artifact_fields
        or allowed_artifact_kinds != expected_artifact_kinds
        or allowed_path_root != "tests/fixtures"
        or artifact_schema.get("max_bytes") != 1048576
    ):
        errors.append("support governance: verifiable content-addressed artifact registry required")
    artifacts = data.get("evidence_artifact_registry", [])
    if not isinstance(artifacts, list):
        errors.append("support governance: evidence artifact registry must be a list")
        artifacts = []
    typed_artifacts = [artifact for artifact in artifacts if isinstance(artifact, dict)]
    if len(typed_artifacts) != len(artifacts):
        errors.append("evidence artifact registry: every entry must be a typed mapping")
    errors.extend(unique_ids(typed_artifacts, "artifact_id", "evidence artifact registry"))
    artifacts_by_id: dict[str, dict[str, Any]] = {}
    allowed_root_path = artifact_root / "tests" / "fixtures"
    try:
        artifact_root_resolved = artifact_root.resolve(strict=True)
        allowed_root_resolved = allowed_root_path.resolve(strict=True)
        allowed_root_resolved.relative_to(artifact_root_resolved)
    except (OSError, ValueError):
        allowed_root_resolved = None
    for artifact in typed_artifacts:
        if set(artifact) != artifact_fields:
            errors.append("evidence artifact registry: typed artifact fields differ from contract")
            continue
        artifact_id = artifact.get("artifact_id")
        artifact_valid = True
        if not valid_identifier(artifact_id):
            errors.append("evidence artifact registry: invalid artifact_id")
            artifact_valid = False
        if artifact.get("kind") not in allowed_artifact_kinds:
            errors.append(f"evidence artifact registry: invalid kind {artifact.get('kind')!r}")
            artifact_valid = False
        digest = artifact.get("sha256")
        if not isinstance(digest, str) or SHA256_RE.fullmatch(digest) is None:
            errors.append(f"evidence artifact registry: invalid SHA-256 for {artifact_id!r}")
            artifact_valid = False
        if artifact.get("canonicalization") != "canonical_json_v1":
            errors.append(f"evidence artifact registry: unsupported canonicalization for {artifact_id!r}")
            artifact_valid = False
        path_value = artifact.get("path")
        candidate: Path | None = None
        if (
            not isinstance(path_value, str)
            or not path_value
            or "\\" in path_value
            or path_value.startswith("/")
            or any(part in {"", ".", ".."} for part in path_value.split("/"))
        ):
            errors.append(f"evidence artifact registry: unsafe relative path for {artifact_id!r}")
            artifact_valid = False
        elif not path_value.startswith("tests/fixtures/"):
            errors.append(f"evidence artifact registry: path outside allowed root for {artifact_id!r}")
            artifact_valid = False
        elif allowed_root_resolved is None:
            errors.append("evidence artifact registry: allowed path root is missing")
            artifact_valid = False
        else:
            try:
                candidate = (artifact_root / Path(*path_value.split("/"))).resolve(strict=True)
                candidate.relative_to(allowed_root_resolved)
            except (OSError, ValueError):
                errors.append(f"evidence artifact registry: missing file or symlink escape for {artifact_id!r}")
                artifact_valid = False
                candidate = None
        if candidate is not None:
            if not candidate.is_file():
                errors.append(f"evidence artifact registry: artifact is not a regular file for {artifact_id!r}")
                artifact_valid = False
            elif candidate.stat().st_size > 1048576:
                errors.append(f"evidence artifact registry: artifact exceeds byte limit for {artifact_id!r}")
                artifact_valid = False
            else:
                try:
                    raw = candidate.read_bytes()
                except OSError:
                    errors.append(f"evidence artifact registry: artifact bytes are unreadable for {artifact_id!r}")
                    artifact_valid = False
                else:
                    canonical, payload = canonical_json_v1(raw)
                    if canonical is None or canonical != raw:
                        errors.append(f"evidence artifact registry: non-canonical JSON bytes for {artifact_id!r}")
                        artifact_valid = False
                    recomputed = hashlib.sha256(raw).hexdigest()
                    if digest != recomputed:
                        errors.append(f"evidence artifact registry: SHA-256 mismatch for {artifact_id!r}")
                        artifact_valid = False
                    if artifact_id != f"sha256:{recomputed}":
                        errors.append(f"evidence artifact registry: artifact_id is not its content address for {artifact_id!r}")
                        artifact_valid = False
                    if payload is None or payload.get("kind") != artifact.get("kind"):
                        errors.append(f"evidence artifact registry: payload kind mismatch for {artifact_id!r}")
                        artifact_valid = False
        if artifact_valid and isinstance(artifact_id, str):
            artifacts_by_id[artifact_id] = artifact
    review_policy = governance.get("human_review_receipt_schema", {})
    if review_policy.get("minimum_independent_reviewers") != 2:
        errors.append("support governance: exactly the minimum of two independent reviews must be declared")
    schemes = set(governance.get("version_schemes", {})) - {"extension_policy"}
    if schemes != {"semver_core"}:
        errors.append("support governance: semver_core parser/comparator must be registered")
    for agent in agents:
        claims = agent.get("capabilities", [])
        omissions = agent.get("omitted_capabilities", [])
        errors.extend(unique_ids(claims, "capability_id", f"{agent.get('adapter_id')} claims"))
        errors.extend(unique_ids(omissions, "capability_id", f"{agent.get('adapter_id')} omissions"))
        claim_ids = {claim.get("capability_id") for claim in claims}
        omission_ids = {item.get("capability_id") for item in omissions}
        overlap = sorted(claim_ids & omission_ids)
        if overlap:
            errors.append(f"{agent.get('adapter_id')}: capabilities are both claimed and omitted {overlap}")
        missing_coverage = sorted(capability_ids - claim_ids - omission_ids)
        extra_coverage = sorted((claim_ids | omission_ids) - capability_ids)
        if missing_coverage or extra_coverage:
            errors.append(
                f"{agent.get('adapter_id')}: capability baseline coverage differs "
                f"missing={missing_coverage}, extra={extra_coverage}"
            )
        version_scope = agent.get("version_scope", {})
        scheme = version_scope.get("scheme")
        if scheme not in schemes:
            errors.append(f"{agent.get('adapter_id')}: unknown version scheme {scheme!r}")
        if not version_scope.get("documentation_snapshot"):
            errors.append(f"{agent.get('adapter_id')}: structured documentation snapshot is required")
        if version_scope.get("public_claim_range") is not None:
            errors.append(f"{agent.get('adapter_id')}: adapter-wide public claim range is forbidden")
        for observed_version in version_scope.get("locally_observed_versions", []):
            if parse_version(scheme, observed_version) is None:
                errors.append(f"{agent.get('adapter_id')}: invalid locally observed version {observed_version!r}")
        for omitted in omissions:
            if omitted.get("capability_id") not in capability_ids:
                errors.append(f"{agent.get('adapter_id')}: unknown omitted capability {omitted.get('capability_id')}")
            if omitted.get("applicability") not in {"not_established", "core_owned", "planned", "not_applicable"}:
                errors.append(f"{agent.get('adapter_id')}/{omitted.get('capability_id')}: invalid applicability")
            if not omitted.get("reason"):
                errors.append(f"{agent.get('adapter_id')}/{omitted.get('capability_id')}: omission reason required")
        for claim in claims:
            capability = claim.get("capability_id")
            support = claim.get("support")
            if capability not in capability_ids:
                errors.append(f"{agent.get('adapter_id')}: unknown capability {capability}")
            if support not in labels:
                errors.append(f"{agent.get('adapter_id')}/{capability}: unknown support label {support}")
            evidence = claim.get("evidence", {})
            if support in public_labels:
                if claim.get("version_range", {}).get("scheme") != scheme:
                    errors.append(f"{agent.get('adapter_id')}/{capability}: claim version scheme differs from adapter")
                errors.extend(validate_public_claim_evidence(
                    claim, agent.get("adapter_id"), governance, artifacts_by_id
                ))
            elif support == "experimental" and not evidence.get("official_docs"):
                errors.append(f"{agent.get('adapter_id')}/{capability}: experimental claim lacks source probe")
            elif support == "unsupported" and not claim.get("reason"):
                errors.append(f"{agent.get('adapter_id')}/{capability}: unsupported claim lacks reason")
    return errors


def validate_capabilities() -> list[str]:
    return validate_capability_data(registry("capabilities.yaml"))


def percentile_cont(values: list[float], probability: float) -> float | None:
    if not values:
        return None
    ordered = sorted(values)
    rank = (len(ordered) - 1) * probability
    lower = math.floor(rank)
    upper = math.ceil(rank)
    if lower == upper:
        return float(ordered[lower])
    fraction = rank - lower
    return float(ordered[lower] + (ordered[upper] - ordered[lower]) * fraction)


def validate_evaluator_contract(
    evaluator: Any, calculation: Any, expected_id: Any = None, scope: str = "evaluator"
) -> list[str]:
    if not isinstance(evaluator, dict) or set(evaluator) != {"id", "implementation", "parameters"}:
        return [f"{scope}: evaluator must contain id/implementation/parameters only"]
    errors: list[str] = []
    evaluator_id = evaluator.get("id")
    if not valid_identifier(evaluator_id):
        errors.append(f"{scope}: evaluator ID must be a bounded identifier")
    if expected_id is not None and evaluator_id != expected_id:
        errors.append(f"{scope}: evaluator ID must equal formula version")
    expected_implementation = FORMULA_IMPLEMENTATIONS.get(calculation)
    expected_parameters = EVALUATOR_PARAMETERS.get(calculation)
    if expected_implementation is None or expected_parameters is None:
        errors.append(f"{scope}: unknown calculation {calculation!r}")
        return errors
    if evaluator.get("implementation") != expected_implementation:
        errors.append(f"{scope}: evaluator implementation differs from registered calculation")
    parameters = evaluator.get("parameters")
    if not isinstance(parameters, dict) or parameters != expected_parameters:
        errors.append(f"{scope}: evaluator parameters differ from exact typed schema")
    return errors


def calculate(calculation: str, inputs: dict[str, Any]) -> float | int | None:
    status = inputs.get("completeness", "complete")
    if status == "unknown":
        return None
    values = inputs.get("values", [])
    if calculation == "count":
        return len(values)
    if calculation in {"sum", "duration"}:
        return sum(values)
    if calculation == "ratio":
        denominator = inputs.get("denominator")
        numerator = inputs.get("numerator")
        return None if denominator in (None, 0) or numerator is None else numerator / denominator
    if calculation == "p95":
        return percentile_cont(values, 0.95)
    if calculation == "mean":
        return None if not values else sum(values) / len(values)
    if calculation == "latest":
        return None if not values else values[-1]
    if calculation == "max":
        return None if not values else max(values)
    raise ValueError(f"unknown calculation {calculation}")


def evaluate_formula_case(case: dict[str, Any]) -> tuple[float | int | None, str, str | None]:
    authorized_values = case.get("authorized_exclusions", [])
    if not isinstance(authorized_values, list) or any(not isinstance(item, str) for item in authorized_values):
        return None, "unknown", "invalid_policy"
    authorized = set(authorized_values)
    records = case.get("records", [])
    if not isinstance(records, list) or any(not isinstance(record, dict) for record in records):
        return None, "unknown", "invalid_record"
    for record in records:
        exclusion = record.get("exclusion_code")
        if exclusion is not None and exclusion not in authorized:
            return None, "unknown", "unauthorized_exclusion"
    selected = [record for record in records if record.get("eligible") and record.get("in_interval")]
    included = [record for record in selected if record.get("exclusion_code") is None]
    if any(record.get("completeness_state") != "complete" for record in included):
        return None, "unknown", None
    dedupe_key = case.get("dedupe_key")
    if not isinstance(dedupe_key, str) or any(
        dedupe_key not in record or not isinstance(record[dedupe_key], (str, int))
        or isinstance(record[dedupe_key], bool)
        for record in included
    ):
        return None, "unknown", "invalid_record"
    deduplicated: list[dict[str, Any]] = []
    seen: set[Any] = set()
    for record in included:
        identity = record.get(dedupe_key)
        if identity not in seen:
            seen.add(identity)
            deduplicated.append(record)
    evaluator = case.get("evaluator", {})
    if validate_evaluator_contract(evaluator, case.get("calculation")):
        return None, "unknown", "invalid_evaluator"
    if not deduplicated:
        state = "not_observed" if case.get("source_interval_state", "complete") == "complete" else "unknown"
        return None, state, None
    implementation = evaluator.get("implementation")
    parameters = evaluator.get("parameters", {})
    numeric_fields: list[str] = []
    if implementation in {"sum_record_field", "percentile_cont_record_field", "mean_record_field", "max_record_field"}:
        numeric_fields = [parameters["value_field"]]
    elif implementation == "ratio_of_record_field_sums":
        numeric_fields = [parameters["numerator_field"], parameters["denominator_field"]]
    elif implementation == "latest_record_by_order_field":
        numeric_fields = [parameters["value_field"], parameters["order_field"]]
    if any(
        field not in record
        or not isinstance(record[field], (int, float))
        or isinstance(record[field], bool)
        for record in deduplicated
        for field in numeric_fields
    ):
        return None, "unknown", "invalid_record"
    if implementation == "ratio_of_record_field_sums" and any(
        record[parameters["numerator_field"]] < 0
        or record[parameters["denominator_field"]] < 0
        or record[parameters["numerator_field"]] > record[parameters["denominator_field"]]
        for record in deduplicated
    ):
        return None, "unknown", "invalid_ratio_record"
    if implementation == "count_distinct_records":
        value: float | int | None = len(deduplicated)
    elif implementation == "sum_record_field":
        value = sum(record[parameters["value_field"]] for record in deduplicated)
    elif implementation == "ratio_of_record_field_sums":
        numerator = sum(record[parameters["numerator_field"]] for record in deduplicated)
        denominator = sum(record[parameters["denominator_field"]] for record in deduplicated)
        value = None if denominator == 0 else numerator / denominator
    elif implementation == "percentile_cont_record_field":
        value = percentile_cont(
            [float(record[parameters["value_field"]]) for record in deduplicated],
            float(parameters["probability"]),
        )
    elif implementation == "mean_record_field":
        values = [record[parameters["value_field"]] for record in deduplicated]
        value = sum(values) / len(values)
    elif implementation == "latest_record_by_order_field":
        latest = max(deduplicated, key=lambda record: record[parameters["order_field"]])
        value = latest[parameters["value_field"]]
    elif implementation == "max_record_field":
        value = max(record[parameters["value_field"]] for record in deduplicated)
    else:
        return None, "unknown", "unknown_evaluator"
    if value is None:
        return None, "unknown", None
    return value, "numeric_zero" if value == 0 else "complete", None


def formula_semantic_payload(metric: dict[str, Any]) -> dict[str, Any]:
    formula = metric.get("formula", {})
    return {
        "metric_id": metric.get("id"),
        "formula_version": formula.get("version"),
        "population_id": formula.get("population_id"),
        "population": metric.get("population"),
        "expression": formula.get("expression"),
        "evaluator": formula.get("evaluator"),
        "fixture_policy": formula.get("fixture_policy"),
        "numerator": formula.get("numerator"),
        "denominator": formula.get("denominator"),
    }


def semantic_sha256(value: dict[str, Any]) -> str:
    encoded = json.dumps(value, sort_keys=True, separators=(",", ":")).encode()
    return hashlib.sha256(encoded).hexdigest()


def formula_lock_map(data: Any, scope: str) -> tuple[list[str], dict[str, str]]:
    if not isinstance(data, dict):
        return [f"{scope}: lock registry must be a mapping"], {}
    errors: list[str] = []
    if data.get("schema_version") != "kansoku.formula-version-locks/1":
        errors.append(f"{scope}: unexpected schema version")
    trust_model = data.get("trust_model")
    if not isinstance(trust_model, dict) or set(trust_model) != {
        "bootstrap", "history", "version_transition", "limit"
    } or any(not isinstance(value, str) or not value for value in trust_model.values()):
        errors.append(f"{scope}: explicit bootstrap/history/limit trust model required")
    locks = data.get("locks")
    if not isinstance(locks, list):
        return errors + [f"{scope}: locks must be a list"], {}
    typed_locks = [item for item in locks if isinstance(item, dict)]
    if len(typed_locks) != len(locks):
        errors.append(f"{scope}: every lock must be a typed mapping")
    errors.extend(unique_ids(typed_locks, "formula_version", scope))
    result: dict[str, str] = {}
    for lock in typed_locks:
        version = lock.get("formula_version")
        digest = lock.get("semantic_sha256")
        if set(lock) != {"formula_version", "semantic_sha256"}:
            errors.append(f"{scope}: lock fields differ from contract")
            continue
        if not valid_identifier(version):
            errors.append(f"{scope}: invalid formula version {version!r}")
        if not isinstance(digest, str) or SHA256_RE.fullmatch(digest) is None:
            errors.append(f"{scope}: invalid semantic SHA-256 for {version!r}")
        if valid_identifier(version) and isinstance(digest, str) and SHA256_RE.fullmatch(digest):
            result[version] = digest
    return errors, result


def git_formula_history(ref: str, required: bool = False) -> dict[str, Any] | None:
    if not isinstance(ref, str) or not ref or ref.startswith("-"):
        if required:
            raise ValueError("formula history: invalid trusted Git ref")
        return None
    try:
        completed = subprocess.run(
            ["git", "show", f"{ref}:contracts/formula-version-locks.yaml"],
            cwd=ROOT,
            check=False,
            capture_output=True,
            text=True,
            timeout=5,
        )
    except (OSError, subprocess.SubprocessError):
        if required:
            raise ValueError(f"formula history: cannot read trusted Git ref {ref!r}")
        return None
    if completed.returncode != 0:
        if required:
            raise ValueError(f"formula history: trusted Git ref lacks formula locks: {ref!r}")
        return None
    try:
        value = json.loads(completed.stdout)
    except json.JSONDecodeError:
        raise ValueError(f"formula history: invalid lock registry at trusted Git ref {ref!r}")
    if not isinstance(value, dict):
        raise ValueError(f"formula history: lock registry is not a mapping at trusted Git ref {ref!r}")
    return value


def validate_metric_data(
    data: dict[str, Any], formula_fixture: dict[str, Any],
    formula_locks: dict[str, Any] | None = None,
    historical_locks: dict[str, Any] | None = None,
) -> list[str]:
    product = registry("product.yaml")
    capabilities = registry("capabilities.yaml")
    case_items = formula_fixture.get("metric_cases", [])
    formula_cases = {item.get("metric_id"): item for item in case_items}
    errors: list[str] = []
    dimensions = data.get("dimensions", [])
    metrics = data.get("metrics", [])
    errors.extend(unique_ids(dimensions, "id", "metric dimensions"))
    errors.extend(unique_ids(metrics, "id", "metrics"))
    errors.extend(unique_ids(case_items, "metric_id", "metric formula cases"))
    dimension_ids = {item.get("id") for item in dimensions}
    question_ids = {item.get("id") for item in product.get("user_questions", [])}
    capability_ids = {item.get("id") for item in capabilities.get("capabilities", [])}
    formula_versions: set[str] = set()
    if formula_locks is None:
        formula_locks = registry("formula-version-locks.yaml")
    lock_errors, current_locks = formula_lock_map(formula_locks, "formula version locks")
    errors.extend(lock_errors)
    if historical_locks is not None:
        history_errors, history_map = formula_lock_map(historical_locks, "historical formula version locks")
        errors.extend(history_errors)
        for historical_version, historical_digest in history_map.items():
            if current_locks.get(historical_version) != historical_digest:
                errors.append(
                    f"formula version locks: append-only history changed for {historical_version}"
                )
    metric_ids = {item.get("id") for item in metrics}
    extra_bindings = sorted(set(formula_cases) - metric_ids)
    if extra_bindings:
        errors.append(f"metric formula bindings: unknown metrics {extra_bindings}")
    for metric in metrics:
        metric_id = metric.get("id")
        formula = metric.get("formula", {})
        version = formula.get("version")
        calculation = formula.get("calculation")
        if not version or version in formula_versions:
            errors.append(f"metric {metric_id}: formula version must be unique and non-empty")
        else:
            formula_versions.add(version)
        binding = formula_cases.get(metric_id)
        if binding is None:
            errors.append(f"metric {metric_id}: missing formula-version fixture binding")
        elif binding.get("formula_version") != version or binding.get("calculation") != calculation:
            errors.append(f"metric {metric_id}: formula fixture binding differs from registry")
        evaluator = formula.get("evaluator", {})
        errors.extend(validate_evaluator_contract(
            evaluator, calculation, version, f"metric {metric_id}"
        ))
        fixture_policy = formula.get("fixture_policy", {})
        if set(fixture_policy) != {"filter", "dedupe_key", "interval", "authorized_exclusions", "ordering"}:
            errors.append(f"metric {metric_id}: complete fixture policy required")
        version_match = re.fullmatch(re.escape(str(metric_id)) + r"/([1-9][0-9]*)", str(version))
        if version_match is None:
            errors.append(f"metric {metric_id}: formula version must be metric_id/positive-integer")
        elif formula.get("population_id") != f"{metric_id}.population/{version_match.group(1)}":
            errors.append(f"metric {metric_id}: population ID must transition with formula version")
        expected_semantic = semantic_sha256(formula_semantic_payload(metric))
        if formula.get("semantic_sha256") != expected_semantic:
            errors.append(f"metric {metric_id}: registry semantic fingerprint mismatch")
        if current_locks.get(version) != expected_semantic:
            errors.append(f"metric {metric_id}: formula version differs from independent lock")
        for dimension in metric.get("dimensions", []):
            if dimension not in dimension_ids:
                errors.append(f"metric {metric_id}: unknown dimension {dimension}")
        for question in metric.get("question_ids", []):
            if question not in question_ids:
                errors.append(f"metric {metric_id}: unknown question {question}")
        for capability in metric.get("source_capabilities", []):
            if capability not in capability_ids:
                errors.append(f"metric {metric_id}: unknown source capability {capability}")
        for required in ("unit", "population", "provenance", "completeness", "exactness"):
            if required not in metric:
                errors.append(f"metric {metric_id}: missing {required}")
        if calculation == "ratio":
            if not (formula.get("numerator") and formula.get("denominator")):
                errors.append(f"metric {metric_id}: ratio requires numerator and denominator")
            elif formula.get("numerator") == formula.get("denominator"):
                errors.append(f"metric {metric_id}: ratio numerator and denominator must be distinct")

    required_case_fields = set(formula_fixture.get("fixture_contract", {}).get("required_fields", []))
    required_record_fields = set(formula_fixture.get("fixture_contract", {}).get("record_fields", []))
    for case in case_items:
        metric_id = case.get("metric_id")
        missing_fields = sorted(required_case_fields - set(case))
        if missing_fields:
            errors.append(f"metric formula fixture {metric_id}: missing fields {missing_fields}")
        if case.get("filter") != {"eligible": True, "in_interval": True}:
            errors.append(f"metric formula fixture {metric_id}: canonical population filter required")
        if case.get("dedupe_key") != "record_id":
            errors.append(f"metric formula fixture {metric_id}: record_id dedupe required")
        if case.get("calculation") in {"p95", "latest"} and not case.get("ordering"):
            errors.append(f"metric formula fixture {metric_id}: explicit ordering required")
        metric = next((item for item in metrics if item.get("id") == metric_id), None)
        if metric is not None:
            formula = metric.get("formula", {})
            if case.get("population") != metric.get("population"):
                errors.append(f"metric formula fixture {metric_id}: population differs from registry")
            if case.get("population_id") != formula.get("population_id"):
                errors.append(f"metric formula fixture {metric_id}: population ID differs from registry")
            if case.get("expression") != formula.get("expression"):
                errors.append(f"metric formula fixture {metric_id}: expression differs from registry")
            if case.get("evaluator") != formula.get("evaluator"):
                errors.append(f"metric formula fixture {metric_id}: evaluator differs from registry")
            if case.get("fixture_policy") != formula.get("fixture_policy"):
                errors.append(f"metric formula fixture {metric_id}: fixture policy differs from registry")
            policy = formula.get("fixture_policy", {})
            for field in ("filter", "dedupe_key", "interval", "authorized_exclusions", "ordering"):
                if case.get(field) != policy.get(field):
                    errors.append(f"metric formula fixture {metric_id}: {field} differs from registry policy")
            if case.get("registry_semantic_sha256") != formula.get("semantic_sha256"):
                errors.append(f"metric formula fixture {metric_id}: semantic fingerprint differs from registry")
        for record in case.get("records", []):
            missing_record_fields = sorted(required_record_fields - set(record))
            if missing_record_fields:
                errors.append(f"metric formula fixture {metric_id}: record missing {missing_record_fields}")
        actual, state, error = evaluate_formula_case(case)
        expected = case.get("expected_value")
        if isinstance(expected, float) and isinstance(actual, (int, float)):
            values_match = math.isclose(float(actual), expected, rel_tol=1e-12, abs_tol=1e-12)
        else:
            values_match = actual == expected
        if not values_match or state != case.get("expected_state") or error is not None:
            errors.append(
                f"metric formula fixture {metric_id}: got ({actual}, {state}, {error}), expected "
                f"({expected}, {case.get('expected_state')}, None)"
            )
    for case in formula_fixture.get("adversarial_cases", []):
        actual, state, error = evaluate_formula_case(case)
        if (actual, state, error) != (
            case.get("expected_value"), case.get("expected_state"), case.get("expected_error")
        ):
            errors.append(f"adversarial formula fixture {case.get('id')}: unexpected result {(actual, state, error)}")
    glossary_states = registry("glossary.yaml").get("state_registry", {})
    metric_states = data.get("state_contract", {})
    if metric_states.get("value_states") != glossary_states.get("value_states"):
        errors.append("metrics: value states must reference the canonical glossary registry")
    if metric_states.get("completeness_states") != glossary_states.get("completeness_states"):
        errors.append("metrics: completeness states must reference the canonical glossary registry")
    boundary = data.get("formula_fixture_boundary", {})
    if boundary.get("input_stage") != "normalized_metric_records" or not boundary.get("proves") or not boundary.get("does_not_prove"):
        errors.append("metrics: formula fixture proof boundary must be explicit")
    quantile = data.get("quantile_semantics", {})
    if quantile.get("method") != "percentile_cont" or quantile.get("postgresql_equivalent") != "percentile_cont(0.95) WITHIN GROUP (ORDER BY value)":
        errors.append("metrics: p95 semantics must match PostgreSQL percentile_cont")
    return errors


def validate_metrics(
    history_ref: str | None = "HEAD", history_required: bool = False
) -> list[str]:
    historical = git_formula_history(history_ref, history_required) if history_ref is not None else None
    return validate_metric_data(
        registry("metrics.yaml"), fixture("formula-cases.yaml"),
        registry("formula-version-locks.yaml"), historical,
    )


def validate_dashboard() -> list[str]:
    data = registry("dashboard.yaml")
    metrics = registry("metrics.yaml")
    product = registry("product.yaml")
    errors: list[str] = []
    routes = data.get("routes", [])
    errors.extend(unique_ids(routes, "path", "dashboard routes"))
    actual_routes = {route.get("path") for route in routes}
    if actual_routes != EXPECTED_ROUTES:
        errors.append(f"dashboard routes differ: missing={sorted(EXPECTED_ROUTES-actual_routes)}, extra={sorted(actual_routes-EXPECTED_ROUTES)}")
    metric_ids = {item.get("id") for item in metrics.get("metrics", [])}
    question_ids = {item.get("id") for item in product.get("user_questions", [])}
    panel_ids: list[str] = []
    for route in routes:
        panels = route.get("panels", [])
        if not panels:
            errors.append(f"route {route.get('path')}: must own at least one panel")
        for panel in panels:
            panel_ids.append(panel.get("id"))
            if not panel.get("metrics"):
                errors.append(f"panel {panel.get('id')}: must reference metrics")
            for metric in panel.get("metrics", []):
                if metric not in metric_ids:
                    errors.append(f"panel {panel.get('id')}: unknown metric {metric}")
            for question in panel.get("question_ids", []):
                if question not in question_ids:
                    errors.append(f"panel {panel.get('id')}: unknown question {question}")
            required_states = registry("glossary.yaml").get("state_registry", {}).get("display_states", [])
            if panel.get("view_states", []) != required_states:
                errors.append(f"panel {panel.get('id')}: incomplete explicit view-state set")
    if len(panel_ids) != len(set(panel_ids)):
        errors.append("dashboard panels: duplicate id")
    return errors


def aggregate(values: list[float], method: str) -> float:
    if method == "p95":
        result = percentile_cont(values, 0.95)
        if result is None:
            raise ValueError("p95 requires at least one value")
        return result
    if method == "max":
        return max(values)
    if method == "sum":
        return sum(values)
    if method == "mean":
        return sum(values) / len(values)
    raise ValueError(f"unknown SLI aggregation {method}")


def compare(value: float, operator: str, target: float) -> bool:
    return {
        "<=": value <= target,
        "<": value < target,
        "==": value == target,
        ">=": value >= target,
        ">": value > target,
    }[operator]


def failing_samples(operator: str, target: float) -> list[float]:
    if operator in {"<=", "<", "=="}:
        return [target + max(abs(target), 1.0)] * 20
    return [target - max(abs(target), 1.0)] * 20


def passing_slo_records(slo_id: str, case: dict[str, Any]) -> list[dict[str, Any]]:
    if "records" in case:
        return case["records"]
    if "scope_values" in case:
        return [
            {
                "value": value,
                "eligible": True,
                "completeness_state": "complete",
                "exclusion_code": None,
                "evidence_scope": scope,
            }
            for scope, value in case["scope_values"].items()
        ]
    return [
        {
            "value": value,
            "eligible": True,
            "completeness_state": "complete",
            "exclusion_code": None,
            "evidence_scope": "primary",
        }
        for value in case.get("values", [])
    ]


def evaluate_slo_records(
    conn: sqlite3.Connection, slo: dict[str, Any], records: list[dict[str, Any]]
) -> dict[str, Any]:
    slo_id = slo.get("id")
    allowed = {item.get("code"): item for item in slo.get("allowed_exclusions", [])}
    exclusion_counts: dict[str, int] = {}
    scope_status: dict[str, str] = {}

    def result(
        value: float | None, state: str, gate: str, reason: str | None
    ) -> dict[str, Any]:
        return {
            "measured_value": value,
            "measurement_state": state,
            "gate": gate,
            "reason": reason,
            "required_scope_status": scope_status,
            "authorized_exclusion_counts": exclusion_counts,
        }

    for record in records:
        exclusion = record.get("exclusion_code")
        if exclusion is not None and exclusion not in allowed:
            return result(None, "unknown", "fail", "unauthorized_exclusion")
        if exclusion is not None:
            exclusion_counts[exclusion] = exclusion_counts.get(exclusion, 0) + 1
    requirement = slo.get("completeness_requirement", {})
    required_scopes = set(requirement.get("required_evidence_scopes", []))
    if not required_scopes:
        return result(None, "unknown", "fail", "missing_required_scope")
    for scope in sorted(required_scopes):
        scope_records = [record for record in records if record.get("evidence_scope") == scope]
        measured = any(
            record.get("eligible") is True
            and record.get("completeness_state") == "complete"
            and record.get("exclusion_code") is None
            for record in scope_records
        )
        excluded = any(
            record.get("scope_exclusion") is True
            and record.get("exclusion_code") in allowed
            and allowed[record.get("exclusion_code")].get("scope_effect") == "may_cover_as_excluded"
            for record in scope_records
        )
        scope_status[scope] = "measured" if measured else "excluded" if excluded else "missing"
    if "missing" in scope_status.values():
        return result(None, "unknown", "fail", "missing_required_scope")
    if "excluded" in scope_status.values():
        return result(None, "partial", "fail", "required_scope_excluded")
    eligible = [record for record in records if record.get("eligible") and record.get("exclusion_code") is None]
    complete = [record for record in eligible if record.get("completeness_state") == "complete"]
    policy = requirement.get("policy")
    if policy == "all_required_scopes_complete":
        if len(complete) != len(eligible):
            return result(None, "unknown", "fail", "incomplete_required_evidence")
    elif policy == "minimum_complete_ratio":
        ratio = len(complete) / len(eligible) if eligible else 0.0
        if ratio < float(requirement.get("minimum_complete_ratio", 1.0)):
            return result(None, "unknown", "fail", "incomplete_required_evidence")
    else:
        return result(None, "unknown", "fail", "unknown_completeness_policy")
    if not complete:
        return result(None, "unknown", "fail", "zero_complete_samples")
    conn.execute("DELETE FROM sli_samples")
    conn.executemany(
        "INSERT INTO sli_samples VALUES (?, ?, ?, ?, ?, ?, ?)",
        [
            (
                slo_id,
                float(record["value"]),
                int(bool(record.get("eligible"))),
                record.get("completeness_state"),
                record.get("exclusion_code"),
                record.get("evidence_scope"),
                int(bool(record.get("scope_exclusion"))),
            )
            for record in records
        ],
    )
    try:
        selected_values = [float(row[0]) for row in conn.execute(slo.get("sli", {}).get("query"), (slo_id,))]
        measured = aggregate(selected_values, slo.get("sli", {}).get("aggregation"))
    except (sqlite3.Error, TypeError, ValueError) as exc:
        return result(None, "unknown", "fail", f"query_not_runnable:{exc}")
    target = float(slo.get("target", {}).get("value"))
    operator = slo.get("target", {}).get("operator")
    return result(measured, "complete", "pass" if compare(measured, operator, target) else "fail", None)


def validate_slo_data(
    data: dict[str, Any], sample_data: dict[str, Any], selected: str | None = None
) -> list[str]:
    metric_ids = {item.get("id") for item in registry("metrics.yaml").get("metrics", [])}
    passing_cases = sample_data.get("passing_cases", {})
    errors: list[str] = []
    slos = data.get("slos", [])
    errors.extend(unique_ids(slos, "id", "SLOs"))
    profile_ids = {item.get("id") for item in data.get("load_profiles", [])}
    conn = sqlite3.connect(":memory:")
    conn.execute(
        "CREATE TABLE sli_samples (slo_id TEXT, value REAL, eligible INTEGER, "
        "completeness_state TEXT, exclusion_code TEXT, evidence_scope TEXT, scope_exclusion INTEGER)"
    )
    slo_by_id = {item.get("id"): item for item in slos}
    for slo in slos:
        slo_id = slo.get("id")
        if selected and slo_id != selected:
            continue
        if slo.get("reference_profile") not in profile_ids:
            errors.append(f"SLO {slo_id}: unknown reference profile")
        if slo.get("metric_id") not in metric_ids:
            errors.append(f"SLO {slo_id}: unknown metric {slo.get('metric_id')}")
        for key in ("rolling_window", "allowed_exclusions", "completeness_requirement", "error_budget"):
            if key not in slo:
                errors.append(f"SLO {slo_id}: missing {key}")
        exclusion_codes = [item.get("code") for item in slo.get("allowed_exclusions", [])]
        if len(exclusion_codes) != len(set(exclusion_codes)) or any(not code for code in exclusion_codes):
            errors.append(f"SLO {slo_id}: exclusion codes must be unique and non-empty")
        for exclusion in slo.get("allowed_exclusions", []):
            if exclusion.get("effect") != "remove_sample" or exclusion.get("scope_effect") not in {
                "sample_only", "may_cover_as_excluded"
            }:
                errors.append(f"SLO {slo_id}: exclusion effects must be machine-readable")
        requirement = slo.get("completeness_requirement", {})
        if requirement.get("policy") not in {"all_required_scopes_complete", "minimum_complete_ratio"}:
            errors.append(f"SLO {slo_id}: machine-readable completeness policy required")
        if not requirement.get("required_evidence_scopes"):
            errors.append(f"SLO {slo_id}: required evidence scopes must be explicit")
        aggregation = slo.get("sli", {}).get("aggregation")
        target = float(slo.get("target", {}).get("value"))
        operator = slo.get("target", {}).get("operator")
        passing = passing_cases.get(slo_id)
        if not passing:
            errors.append(f"SLO {slo_id}: no test load samples")
            continue
        outcome = evaluate_slo_records(conn, slo, passing_slo_records(slo_id, passing))
        if outcome["gate"] != "pass" or outcome["measurement_state"] != "complete" or outcome["reason"] is not None:
            errors.append(f"SLO {slo_id}: passing fixture got {outcome}")
        failed = aggregate(failing_samples(operator, target), aggregation)
        if compare(failed, operator, target):
            errors.append(f"SLO {slo_id}: comparator does not reject a violating load")
    for case in sample_data.get("adversarial_cases", []):
        if selected and case.get("slo_id") != selected:
            continue
        slo = slo_by_id.get(case.get("slo_id"))
        if slo is None:
            errors.append(f"adversarial SLO fixture {case.get('id')}: unknown SLO")
            continue
        outcome = evaluate_slo_records(conn, slo, case.get("records", []))
        if outcome["measurement_state"] != case.get("expected_measurement_state") or outcome["gate"] != case.get("expected_gate"):
            errors.append(f"adversarial SLO fixture {case.get('id')}: got {outcome}")
    conn.close()
    if selected and selected not in {item.get("id") for item in slos}:
        errors.append(f"unknown SLO {selected}")
    return errors


def validate_slos(selected: str | None = None) -> list[str]:
    return validate_slo_data(registry("slo.yaml"), fixture("slo-samples.yaml"), selected)


def validate_documentation() -> list[str]:
    errors: list[str] = []
    required_files = [
        ROOT / "README.md",
        ROOT / "ROADMAP.md",
        ROOT / "SOURCES.md",
        ROOT / "Engineering Proposal" / "01-product-contract-and-success.md",
        ROOT / "Technical Design Document" / "01-product-contract-and-success.md",
        ROOT / "Technical Design Document" / "adapter-compatibility-matrix.md",
        ROOT / "adr" / "0001-technology-baseline.md",
        ROOT / "adr" / "0002-session-exit-and-support-governance.md",
        ROOT / "adr" / "0003-formula-version-identity-and-proof-boundary.md",
        ROOT / "reports" / "session-01-reconciliation.md",
    ]
    text = "\n".join(path.read_text(encoding="utf-8") for path in required_files)
    for term in ("installed", "enabled", "exposed", "invoked", "loaded", "executed", "succeeded"):
        if term not in text:
            errors.append(f"documentation: canonical lifecycle term {term} is absent")
    for label in ("Supported", "Beta", "Experimental", "Unsupported"):
        if label not in text:
            errors.append(f"documentation: support label {label} is absent")
    if "2026-07-21" not in (ROOT / "SOURCES.md").read_text(encoding="utf-8"):
        errors.append("SOURCES.md: current retrieval date is absent")
    sources = (ROOT / "SOURCES.md").read_text(encoding="utf-8")
    for marker in ("2.1.216", "2.1.197", "not fixture-verified"):
        if marker not in sources:
            errors.append(f"SOURCES.md: Claude documentation/runtime boundary lacks {marker}")
    proposal = required_files[3].read_text(encoding="utf-8")
    tdd = required_files[4].read_text(encoding="utf-8")
    for marker in ("contracts/product.yaml", "contracts/glossary.yaml", "adr/0001-", "ADR 0002"):
        if marker not in proposal + tdd:
            errors.append(f"session documents: missing implemented-artifact marker {marker}")
    return errors


def validate_all(
    selected_slo: str | None = None, formula_history_ref: str | None = "HEAD",
    formula_history_required: bool = False,
) -> list[str]:
    validators = (
        validate_glossary,
        validate_lifecycle,
        validate_capabilities,
        lambda: validate_metrics(formula_history_ref, formula_history_required),
        validate_dashboard,
        lambda: validate_slos(selected_slo),
        validate_documentation,
    )
    errors: list[str] = []
    for validator in validators:
        try:
            errors.extend(validator())
        except ValueError as exc:
            errors.append(str(exc))
    return errors


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--slo", help="also constrain the SLO fixture execution to one SLO ID")
    parser.add_argument(
        "--formula-history-ref", default="auto",
        help="trusted Git ref for append-only formula locks; 'auto' uses optional HEAD and 'none' selects deterministic archive/bootstrap mode",
    )
    parser.add_argument("--json", action="store_true", help="emit machine-readable output")
    args = parser.parse_args()
    if args.formula_history_ref == "none":
        history_ref, history_required = None, False
    elif args.formula_history_ref == "auto":
        history_ref, history_required = "HEAD", False
    else:
        history_ref, history_required = args.formula_history_ref, True
    errors = validate_all(args.slo, history_ref, history_required)
    result = {"status": "pass" if not errors else "fail", "errors": errors}
    if args.json:
        print(json.dumps(result, indent=2, sort_keys=True))
    elif errors:
        print("Session 01 contract validation failed:", file=sys.stderr)
        for error in errors:
            print(f"- {error}", file=sys.stderr)
    else:
        print("Session 01 contract validation passed")
    return 1 if errors else 0


if __name__ == "__main__":
    raise SystemExit(main())
