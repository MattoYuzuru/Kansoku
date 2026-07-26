#!/usr/bin/env python3
"""Independent closed-world validator for the Session 06 Codex adapter.

Two independent things are checked, mirroring `scripts/validate_adapter_sdk.py`
(and, further back, `scripts/validate_observability.py`/`scripts/validate_data_platform.py`):

1. The static contract: `contracts/codex/*.yaml` registries are exact, closed
   and bound by `contracts/codex-policy-locks.yaml` versioned canonical
   semantic digests, following the identical append-only lock mechanism
   established for `contracts/adapter-sdk`.
2. The code/contract alignment: `internal/codexadapter` (plus the small
   `codex`-adapter-name switch inside `internal/observability/routes.go`'s
   generic hook mux) actually implements the invariants the registries
   declare -- hook events/allowlist/spool, OTel event mapping/dropped
   surfaces, rollout checkpoint/quarantine/idempotency fields, the closed
   skill-evidence-tier/ambiguous-ownership/native-exact-activation-prohibition
   vocabulary, and the six reconciliation lanes -- and that the registered
   adapter never introduces a second sanitizer, a second ingress mechanism or
   a second OTel installer target.

Session 06 adds no new external runtime and (per `git diff HEAD -- go.mod
go.sum`) no new third-party dependency, so -- as with Session 05 -- there is
no ephemeral-container harness here; the Go proof is `go vet`/`go test` for
`internal/codexadapter/...` inside the same pinned, network-disabled Go image
`scripts/run_go_tests.py` already uses. That full-repo sweep is authoritative;
this validator's `--with-go` flag re-runs the narrower codexadapter-only
slice so `validate_codex.py` remains a standalone, single-command proof of
the Session 06 exit gate.
"""

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
CONTRACT_DIR = ROOT / "contracts" / "codex"
LOCK_PATH = ROOT / "contracts" / "codex-policy-locks.yaml"
FIXTURES_DIR = ROOT / "tests" / "fixtures" / "session-06"
CANARY_SCENARIO_PATH = FIXTURES_DIR / "canary" / "kansoku-canary-scenario.json"
GO_IMAGE = "golang@sha256:1ecb7edf62a0408027bd5729dfd6b1b8766e578e8df93995b225dfd0944eb651"

FILES = ("manifest.yaml", "hooks-and-otel.yaml", "rollout-and-inventory.yaml", "skill-evidence-and-reconciliation.yaml", "app-server-bridge.yaml")

REAL_AGENT_TERMS = {"codex", "claude", "gemini", "cursor"}

CAPABILITY_IDS = [
    "discovery.agent_and_surface", "inventory.components", "activity.sessions", "activity.prompt_metadata",
    "activity.token_model_cost", "components.skill_invocation", "components.plugin_and_custom_command",
    "components.mcp_lifecycle", "components.builtin_tool_calls_and_approvals", "components.subagents_and_compaction",
    "ingestion.historical_import", "ingestion.live_stream", "ingestion.evidence_bridge", "configuration.install", "configuration.live_canary",
]
EVIDENCE_TIERS = ["corroborated", "native", "reconstructed", "inferred"]
HOOK_EVENTS = ["SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "SubagentStart", "SubagentStop", "Stop"]
NODE_KINDS = [
    "agent_installation", "agent_surface", "agent_version", "plugin_package", "plugin_version",
    "skill_identity", "mcp_server_instance", "mcp_tool", "hook_definition", "custom_command_definition",
    "cache_artifact",
]
EDGE_KINDS = ["bundles", "provides", "configured_in", "enabled_for", "shadows", "collides_with", "depends_on", "observed_using"]
SOURCE_SCOPES = ["system", "user", "repository", "admin", "marketplace", "plugin_cache"]
SKILL_EVIDENCE_KINDS = [
    "explicit_user_invocation", "skill_md_load_evidence", "agent_declared_use",
    "uniquely_owned_helper_execution", "semantic_opportunity_classifier",
]
RECONCILIATION_LANES = [
    "hook_prompt_events_vs_otel_prompt_events_vs_rollout_user_messages",
    "hook_tool_terminal_events_vs_otel_results_vs_rollout_calls_and_outputs",
    "session_start_stop_resume_vs_rollout_lifecycle",
    "subagent_lifecycle_vs_parent_transcript_evidence",
    "mcp_call_counts_vs_configured_and_advertised_tools",
    "component_explicit_load_execute_evidence_compared_without_forcing_equality",
]

LOCK_BASES = {
    "codex.manifest": "contracts/codex/manifest.yaml",
    "codex.hooks-and-otel": "contracts/codex/hooks-and-otel.yaml",
    "codex.rollout-and-inventory": "contracts/codex/rollout-and-inventory.yaml",
    "codex.skill-evidence-and-reconciliation": "contracts/codex/skill-evidence-and-reconciliation.yaml",
    "codex.app-server-bridge": "contracts/codex/app-server-bridge.yaml",
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
    return {f"contracts/codex/{name}": load(CONTRACT_DIR / name) for name in FILES}


def validate(candidate: dict[str, dict[str, Any]] | None = None, locks: dict[str, Any] | None = None, include_code: bool = True, historical: dict[str, Any] | None = None) -> list[str]:
    data = registries() if candidate is None else candidate
    lock = load(LOCK_PATH) if locks is None else locks
    errors: list[str] = []
    if set(data) != {f"contracts/codex/{name}" for name in FILES}:
        errors.append("codex registry set is not exact")
        return errors
    by_name = {Path(path).name: value for path, value in data.items()}
    manifest, hooks_and_otel, rollout_and_inventory, skill_evidence, app_server_bridge = (by_name[name] for name in FILES)

    expected_top = {
        "manifest.yaml": {
            "schema_version", "contract_version", "effective_at", "adapter_id", "adapter_id_naming",
            "manifest_api_version", "execution_form", "network_grade", "reused_parse_limits",
            "installation_discovery", "agent_detection", "permissions",
            "compatibility_registry_fields_reused", "unknown_agent_version_policy", "sources",
            "capability_ids", "capability_ids_source", "installer_target_reuse", "hook_ingress_reuse",
        },
        "hooks-and-otel.yaml": {
            "schema_version", "contract_version", "effective_at", "hook_source", "otel_source",
            "source_event_mapping", "independent_capability_degradation",
        },
        "rollout-and-inventory.yaml": {
            "schema_version", "contract_version", "effective_at", "rollout_source", "inventory_source",
            "discoverability_pressure",
        },
        "skill-evidence-and-reconciliation.yaml": {
            "schema_version", "contract_version", "effective_at", "skill_evidence_model",
            "source_to_canonical_mapping", "reconciliation", "canary", "required_tests", "exit_gate",
        },
        "app-server-bridge.yaml": {
            "schema_version", "contract_version", "effective_at", "bridge_api_version",
            "bridge_id", "bridge_version", "agent_version_exact", "protocol",
            "schema_version_exact", "schema_generation_command", "target_scope",
            "network_grade", "accepted_methods", "emitting_projections", "safe_fields",
            "prohibited_surfaces", "limits", "checkpoint", "unknown_schema",
            "source_evidence", "support_grade",
        },
    }
    for name, fields in expected_top.items():
        if set(by_name[name]) != fields:
            errors.append(f"{name}: top-level closed schema changed")

    # -- manifest.yaml --
    if manifest.get("adapter_id") != "codex":
        errors.append("manifest adapter_id must remain codex")
    if manifest.get("manifest_api_version") != "kansoku.adapter/v1":
        errors.append("manifest_api_version must reuse adapter-sdk's kansoku.adapter/v1 verbatim")
    if manifest.get("execution_form") != "builtin":
        errors.append("codex adapter execution form must remain builtin")
    if manifest.get("network_grade") != "loopback_only":
        errors.append("codex adapter network grade must remain loopback_only, never unrestricted")
    parse_limits = manifest.get("reused_parse_limits", {})
    for field in ("max_config_entries", "max_config_depth", "max_config_string"):
        if not isinstance(parse_limits.get(field), int) or parse_limits.get(field, 0) <= 0:
            errors.append(f"manifest reused_parse_limits.{field} must be a positive integer")
    if "forbidden" not in str(parse_limits.get("code_execution", "")) or "never_evaluated_or_executed" not in str(parse_limits.get("code_execution", "")):
        errors.append("manifest parsing must explicitly forbid code execution and state manifests/hooks/rollout records are never evaluated or executed")
    discovery = manifest.get("installation_discovery", {})
    if "CODEX_HOME_and_documented_config_locations_are_resolved_first" not in str(discovery.get("never_speculative_home_scan", "")):
        errors.append("installation discovery must resolve CODEX_HOME/documented config before ever considering a scan, and must never scan the whole home directory")
    if "never_scans_an_entire_home_directory" not in str(discovery.get("never_speculative_home_scan", "")):
        errors.append("installation discovery must explicitly forbid scanning the entire home directory")
    if "installation_merge_rule" not in discovery or "remain_distinct_installation_candidates" not in str(discovery.get("installation_merge_rule", "")):
        errors.append("installation discovery must keep same-state-root/different-surface candidates distinct")
    agent_detection = manifest.get("agent_detection", {})
    if agent_detection.get("state_root_env_var") != "CODEX_HOME":
        errors.append("agent_detection.state_root_env_var must remain CODEX_HOME")
    if agent_detection.get("executables") != ["codex"]:
        errors.append("agent_detection.executables must remain exactly ['codex']")
    if set(agent_detection.get("surfaces", [])) != {"cli", "ide_extension", "app"}:
        errors.append("agent_detection.surfaces must remain cli/ide_extension/app")
    if manifest.get("capability_ids") != CAPABILITY_IDS:
        errors.append("codex manifest capability_ids must reuse adapter-sdk's closed capability id list verbatim, inventing none")
    if "no_codex_specific_capability_id_is_invented" not in str(manifest.get("capability_ids_source", "")):
        errors.append("manifest must explicitly state no codex-specific capability id is invented")
    if manifest.get("unknown_agent_version_policy", "").find("defaults_to_degraded") == -1:
        errors.append("unknown agent version outside every compatibility range must default to degraded")
    installer_reuse = manifest.get("installer_target_reuse", {})
    if "codex.user_otel_target_already_declared_in_contracts/privacy/installer.yaml" not in str(installer_reuse.get("otel", "")) or "reused_verbatim_never_redefined" not in str(installer_reuse.get("otel", "")):
        errors.append("manifest must reuse contracts/privacy/installer.yaml's existing codex.user_otel target verbatim, never redefine it")
    if "never_selected_by_default" not in str(installer_reuse.get("project_local_scope", "")):
        errors.append("project-local Codex config scope must never be selected by default")
    hook_reuse = manifest.get("hook_ingress_reuse", {})
    if hook_reuse.get("route_template") != "reused_verbatim_from_contracts/observability/ingress.yaml_hook_http_protocol_route_/v1/hooks/{adapter}/{event}":
        errors.append("hook ingress route must be the reused generic /v1/hooks/{adapter}/{event} template, not a new one")
    if hook_reuse.get("auth") != "session02_loopback_bearer_reused_verbatim_no_second_auth_mechanism":
        errors.append("hook ingress auth must remain session02_loopback_bearer with no second auth mechanism")
    if "never_a_second_ingress_mechanism" not in str(hook_reuse.get("no_parallel_route", "")):
        errors.append("hook ingress must never introduce a parallel ingress mechanism")

    if app_server_bridge.get("bridge_api_version") != "kansoku.evidence-bridge/v1" or \
            app_server_bridge.get("agent_version_exact") != "0.145.0" or \
            app_server_bridge.get("schema_version_exact") != "0.145.0":
        errors.append("Codex App Server bridge must remain pinned to reviewed schema 0.145.0")
    if app_server_bridge.get("target_scope") != "explicit_local" or app_server_bridge.get("network_grade") != "loopback_only":
        errors.append("Codex App Server bridge target must remain explicit local loopback")
    accepted_methods = set(app_server_bridge.get("accepted_methods", []))
    if not {"skills/list", "skills/list response", "turn/started"}.issubset(accepted_methods):
        errors.append("Codex App Server bridge must retain the reviewed skill list request/response and turn-start surfaces")
    projections = app_server_bridge.get("emitting_projections", {})
    if projections.get("skills/list_enabled_response") != "component.exposed":
        errors.append("enabled skills/list response must project component.exposed")
    if "codex-app-server-skill-input-load/1" not in str(projections.get("item/started_userMessage_skill_input", "")):
        errors.append("typed skill input invoked/load projection must retain its explicit versioned rule")
    prohibited = set(app_server_bridge.get("prohibited_surfaces", []))
    if not {"messages", "reasoning", "arguments", "results", "errors", "paths", "environment", "uris"}.issubset(prohibited):
        errors.append("Codex App Server prohibited content surface set weakened")
    if "metadata_only_rejection" not in app_server_bridge.get("unknown_schema", ""):
        errors.append("Codex App Server unknown schema must remain metadata-only")

    # -- hooks-and-otel.yaml --
    hook_source = hooks_and_otel.get("hook_source", {})
    if hook_source.get("supported_events") != HOOK_EVENTS:
        errors.append("codex.hook supported_events set changed")
    if hook_source.get("route") != "/v1/hooks/codex/{event}":
        errors.append("codex.hook route must remain /v1/hooks/codex/{event}")
    if "already_declared_in_contracts/observability/ingress.yaml" not in str(hook_source.get("route_reuse", "")) or "no_parallel_ingress_route_is_declared_here" not in str(hook_source.get("route_reuse", "")):
        errors.append("codex.hook must declare route reuse of the existing generic ingress route, no parallel route")
    if hook_source.get("auth") != "session02_loopback_bearer_reused_verbatim":
        errors.append("codex.hook auth must remain session02_loopback_bearer_reused_verbatim")
    helper = hook_source.get("helper_contract", {})
    if "raw_prompt_text_is_never_written_to_disk_or_sent" not in str(helper.get("prompt_feature_computation", "")):
        errors.append("hook helper prompt feature computation must state raw prompt text is never written to disk or sent")
    if "session_id" not in helper.get("allowlisted_fields", []) or "prompt" in " ".join(helper.get("allowlisted_fields", [])).lower():
        errors.append("hook helper allowlisted_fields must never include a raw prompt field")
    if "already_sanitized_events" not in str(helper.get("spool", "")):
        errors.append("hook helper spool must only ever hold already-sanitized events")
    hook_target = hook_source.get("hook_installer_target", {})
    if hook_target.get("id") != "codex.user_hook":
        errors.append("codex hook installer target id must remain codex.user_hook")
    if hook_target.get("config_locator_kind") != "codex_user_config" or hook_target.get("format") != "toml":
        errors.append("codex.user_hook installer target locator/format changed")
    forbidden_keys = set(hook_target.get("forbidden_keys", []))
    if not {"remote_command", "raw_payload_log", "credential_forwarding", "project_local_hook"}.issubset(forbidden_keys):
        errors.append("codex.user_hook forbidden_keys must keep forbidding remote command/raw payload logging/credential forwarding/project-local hooks")
    if "never_modified_unless_the_user_explicitly_selects_that_scope" not in str(hook_target.get("default_scope", "")):
        errors.append("codex.user_hook default_scope must state project-local config is never modified without explicit user selection")
    trust = hook_source.get("trust_and_enabled_state", {})
    if trust.get("audited") is not True:
        errors.append("codex hook trust/enabled state must be audited")
    if "never_bypasses_or_silently_repairs" not in str(trust.get("bypass_forbidden", "")):
        errors.append("codex hook trust/enabled-state bypass_forbidden text weakened")
    if "never_a_silent_repair" not in str(trust.get("remediation_only", "")):
        errors.append("codex hook trust remediation must never be a silent repair")

    otel_source = hooks_and_otel.get("otel_source", {})
    if "codex.user_otel_target_already_declared_in_contracts/privacy/installer.yaml" not in str(otel_source.get("installer_target_reuse", "")):
        errors.append("codex.otel must reuse the existing codex.user_otel installer target")
    if "declares_no_new_otel_installer_target" not in str(otel_source.get("no_second_otel_target", "")):
        errors.append("hooks-and-otel.yaml must explicitly declare no second OTel installer target")
    if otel_source.get("log_user_prompt") is not False:
        errors.append("codex.otel log_user_prompt must remain false")
    dropped = set(otel_source.get("dropped_surfaces", []))
    if not {"log.body", "tool_payload", "output_snippet"}.issubset(dropped):
        errors.append("codex.otel dropped_surfaces must keep dropping log.body/tool_payload/output_snippet")
    if "reuses_contracts/observability/ingress.yaml_otlp_safe_attributes_allowlist_verbatim" not in str(otel_source.get("otlp_safe_attributes_reuse", "")):
        errors.append("codex.otel must reuse the existing OTLP safe-attribute allowlist verbatim")

    if "codex.hook_and_codex.otel_are_independently_capability_scoped" not in str(hooks_and_otel.get("independent_capability_degradation", "")):
        errors.append("hook/OTel independent capability degradation guarantee weakened")

    # -- rollout-and-inventory.yaml --
    rollout_source = rollout_and_inventory.get("rollout_source", {})
    if "never_a_speculative_home_directory_scan" not in str(rollout_source.get("state_root", "")):
        errors.append("codex.rollout state root resolution must never speculatively scan the home directory")
    checkpoint_fields = set(rollout_source.get("checkpoint_fields", []))
    if not {"file_identity", "byte_offset", "first_record_fingerprint", "last_record_fingerprint", "rotation_generation", "truncation_detected"}.issubset(checkpoint_fields):
        errors.append("codex.rollout checkpoint_fields must keep file identity/offset/fingerprint/rotation/truncation fields")
    if "never_writes_to_the_codex_session_tree" not in str(rollout_source.get("parsing", "")):
        errors.append("codex.rollout parsing must never write back into the Codex session tree")
    historical_mode = rollout_source.get("historical_content_mode", {})
    if "raw_content_is_never_written_to_a_durable_path" not in str(historical_mode.get("opt_in", "")):
        errors.append("historical content opt-in mode must state raw content is never written to a durable path")
    if "users_may_disable_historical_content_parsing_entirely" not in str(historical_mode.get("user_disable", "")):
        errors.append("historical content parsing must be user-disableable")
    quarantine = rollout_source.get("corrupt_or_unknown_schema", {})
    if quarantine.get("raw_bytes_durable") is not False:
        errors.append("corrupt/unknown-schema rollout records must never durably retain raw bytes")
    replay = rollout_source.get("replay_and_crash", {})
    if "never_a_duplicate_fact" not in str(replay.get("idempotency", "")):
        errors.append("rollout replay idempotency guarantee weakened")
    if "degraded_incident_scoped_to_codex.rollout_only" not in str(replay.get("rotation_truncation", "")):
        errors.append("rollout rotation/truncation handling must scope its degraded incident to codex.rollout only")

    inventory_source = rollout_and_inventory.get("inventory_source", {})
    if set(inventory_source.get("scopes_inventoried", [])) != set(SOURCE_SCOPES):
        errors.append("codex.inventory scopes_inventoried must reuse the closed adapter-sdk source scope set verbatim")
    if "no_new_scope_is_invented" not in str(inventory_source.get("scopes_reused_from", "")):
        errors.append("codex.inventory must explicitly state no new scope is invented")
    if set(inventory_source.get("node_kinds_used", [])) - set(NODE_KINDS + ["device"]):
        errors.append("codex.inventory node_kinds_used must stay within adapter-sdk's closed node kind vocabulary")
    if set(inventory_source.get("edge_kinds_used", [])) != set(EDGE_KINDS):
        errors.append("codex.inventory edge_kinds_used must reuse the closed adapter-sdk edge kind set verbatim")
    if "never_a_speculative_recursive_filesystem_walk" not in str(inventory_source.get("repository_scan_bound", "")):
        errors.append("codex.inventory repository scan must never be a speculative recursive filesystem walk")
    cache_rule = str(inventory_source.get("cache_rule", ""))
    if "cache_packages_are_never_considered_enabled" not in cache_rule or "requires_an_explicit_enabled_for_edge" not in cache_rule:
        errors.append("codex.inventory cache separation rule weakened")
    if "never_merged" not in str(inventory_source.get("collision_rule", "")):
        errors.append("codex.inventory collision rule must never merge same-named nodes across scopes")

    pressure = rollout_and_inventory.get("discoverability_pressure", {})
    exposed_vs_inferred = pressure.get("exposed_vs_inferred", {})
    if "actual_session_or_source_evidence_shows_it_reached_model_context" not in str(exposed_vs_inferred.get("exposed", "")):
        errors.append("a skill must only be labeled exposed with actual evidence it reached model context")
    if "never_promoted_to_exposed" not in str(exposed_vs_inferred.get("inferred_risk", "")):
        errors.append("catalog pressure/inclusion risk estimates must never be promoted to exposed without direct evidence")
    if "it_never_asserts_this_as_a_certainty" not in str(pressure.get("catalog_budget_note", "")):
        errors.append("catalog budget pressure must never be asserted as a certainty")

    # -- skill-evidence-and-reconciliation.yaml --
    evidence_model = skill_evidence.get("skill_evidence_model", {})
    evidence_kinds = evidence_model.get("evidence_kinds", [])
    if [kind.get("kind") for kind in evidence_kinds] != SKILL_EVIDENCE_KINDS:
        errors.append("skill evidence kind vocabulary changed")
    if "no_new_tier_is_invented" not in str(evidence_model.get("evidence_tiers_reused_from", "")):
        errors.append("skill evidence tiers must state no new tier is invented")
    if "never_combines_these_evidence_kinds_into_a_single_false_exact_activation_count" not in str(evidence_model.get("no_false_exact_count", "")):
        errors.append("no-false-exact-count dashboard rule weakened")
    if "no_rule_converts_a_helper_or_mcp_call_into_component.invoked_evidence_when_ownership" not in str(evidence_model.get("ambiguous_ownership_rule", "")):
        errors.append("ambiguous ownership rule weakened; a helper/MCP call must never be converted to component.invoked under ambiguous ownership")
    prohibition = str(evidence_model.get("native_exact_activation_prohibition", ""))
    if "never_represents_component.opportunity_or_reconstructed_tier_evidence_as_a_component.invoked_native_exact_activation" not in prohibition:
        errors.append("native exact activation prohibition text weakened; this is the session's central exit-gate invariant")

    mapping_rows = skill_evidence.get("source_to_canonical_mapping", [])
    if len(mapping_rows) != 8:
        errors.append("source-to-canonical mapping row count changed")
    semantic_opportunity_rows = [row for row in mapping_rows if row.get("source_evidence") == "semantic_opportunity_classifier"]
    if not semantic_opportunity_rows or semantic_opportunity_rows[0].get("tier") != "inferred":
        errors.append("semantic_opportunity_classifier mapping must remain tier inferred")

    reconciliation = skill_evidence.get("reconciliation", {})
    if reconciliation.get("per_session_comparisons") != RECONCILIATION_LANES:
        errors.append("reconciliation per_session_comparisons lane set changed")
    if "not_hardcoded" not in str(reconciliation.get("tolerance", "")):
        errors.append("reconciliation tolerance must remain versioned, never hardcoded")
    missing_rule = str(reconciliation.get("missing_source_rule", ""))
    if "degraded" not in missing_rule or "never_silently_reports_zero_usage_for_the_whole_session" not in missing_rule:
        errors.append("missing-source reconciliation rule must degrade only that source, never silently report zero for the whole session")

    canary = skill_evidence.get("canary", {})
    constraints = set(canary.get("execution_constraints", []))
    if not {"non_interactive_only", "requires_explicit_consent_and_a_bounded_budget", "never_uses_a_real_user_repository"}.issubset(constraints):
        errors.append("canary execution constraints weakened")

    required_tests = set(skill_evidence.get("required_tests", []))
    required_test_fragments = {
        "sanitized_rollout_fixtures_for_each_declared_compatibility_version_and_event_variant",
        "hook_and_otel_golden_maps",
        "offset_rotation_truncation_replay_crash_importer_tests",
        "skill_collision_and_ambiguous_ownership_tests",
        "codex_home_multi_surface_and_project_scope_inventory_layout_tests",
        "configuration_concurrent_change_and_rollback_tests",
        "cross_source_mismatch_and_inactive_source_logic_tests",
        "prohibited_content_canaries",
    }
    if required_tests != required_test_fragments:
        errors.append("required_tests list changed")

    exit_gate = skill_evidence.get("exit_gate", {})
    if "fixtures_and_live_evidence_both_required_neither_alone_is_sufficient" not in str(exit_gate.get("compatibility_matrix_backed_by", "")):
        errors.append("exit gate must require both fixtures and live evidence, neither alone sufficient")
    if "never_silently_reporting_plausible_looking_zero_usage" not in str(exit_gate.get("independent_visible_degradation", "")):
        errors.append("exit gate independent visible degradation guarantee weakened")
    if "never_represents_inferred_or_reconstructed_tier_codex_skill_use_as_a_native_exact_activation" not in str(exit_gate.get("no_inferred_promoted_to_native", "")):
        errors.append("exit gate no-inferred-promoted-to-native guarantee weakened")
    for flag in ("inventory_correct", "raw_prompt_absent_from_every_durable_path", "replay_idempotent"):
        if exit_gate.get(flag) is not True:
            errors.append(f"exit gate {flag} must remain true")

    errors.extend(validate_policy_locks(lock, data, historical))

    if include_code:
        errors.extend(validate_code_and_fixtures())
    return errors


def validate_policy_locks(lock: dict[str, Any], data: dict[str, dict[str, Any]], historical: dict[str, Any] | None = None) -> list[str]:
    errors: list[str] = []
    if set(lock) != {"schema_version", "effective_at", "locks"} or lock.get("schema_version") != "kansoku.codex-policy-locks/1":
        errors.append("codex policy lock registry is not exact")
    records = lock.get("locks", [])
    if not isinstance(records, list):
        return errors + ["codex policy locks must be a list"]
    if historical is not None:
        old = historical.get("locks", []) if isinstance(historical, dict) else []
        if records[: len(old)] != old:
            errors.append("codex policy lock list must retain the exact append-only trusted prefix")
    latest: dict[str, tuple[int, dict[str, Any]]] = {}
    seen: set[str] = set()
    ordinals: dict[str, list[int]] = {base: [] for base in LOCK_BASES}
    for item in records:
        if not isinstance(item, dict) or set(item) != {"policy_version", "registry", "semantic_sha256"}:
            errors.append("codex policy lock entries must be closed")
            continue
        version = item.get("policy_version", "")
        match = re.fullmatch(r"(codex\.(?:manifest|hooks-and-otel|rollout-and-inventory|skill-evidence-and-reconciliation|app-server-bridge))/([1-9][0-9]*)", version)
        if not match or item.get("registry") != LOCK_BASES.get(match.group(1)) or re.fullmatch(r"[0-9a-f]{64}", str(item.get("semantic_sha256"))) is None:
            errors.append("codex policy lock entry has invalid version/registry/digest binding")
            continue
        if version in seen:
            errors.append(f"duplicate codex policy version {version}")
        seen.add(version)
        ordinal = int(match.group(2))
        ordinals[match.group(1)].append(ordinal)
        if match.group(1) not in latest or ordinal > latest[match.group(1)][0]:
            latest[match.group(1)] = (ordinal, item)
    for base, path in LOCK_BASES.items():
        values = sorted(ordinals[base])
        if (values != list(range(1, values[-1] + 1))) if values else True:
            errors.append(f"{base}: policy versions must start at 1 and remain contiguous")
        current = latest.get(base)
        if current is None or current[1].get("semantic_sha256") != semantic_sha256(data[path]):
            errors.append(f"{path}: semantic digest changed without reviewed policy version")
    return errors


def trusted_lock_from_head() -> dict[str, Any] | None:
    result = subprocess.run(
        ["git", "show", "HEAD:contracts/codex-policy-locks.yaml"], cwd=ROOT,
        check=False, capture_output=True, text=True,
    )
    if result.returncode != 0:
        return None
    try:
        value = json.loads(result.stdout)
    except json.JSONDecodeError:
        return None
    return value if isinstance(value, dict) else None


def validate_code_and_fixtures() -> list[str]:
    errors: list[str] = []

    codexadapter_dir = ROOT / "internal" / "codexadapter"
    core_source_paths = sorted(p for p in codexadapter_dir.glob("*.go") if not p.name.endswith("_test.go"))
    if not core_source_paths:
        errors.append("internal/codexadapter core source is missing")
        return errors
    core_source = "\n".join(p.read_text(encoding="utf-8") for p in core_source_paths)

    required_types = [
        "type Adapter struct", "func New()", "func (a *Adapter) Manifest()", "func (a *Adapter) Discover(",
        "AdapterID = ", "StateRootEnv = ", "type Surface string",
        "type HookEvent", "func DecodeHookInput(", "func BuildHookOutput(", "func ValidateHookOutputAllowlist(",
        "func CanonicalEventForOTel(", "func DocumentedOTelEvents(", "func OTLPSafeAttributes(", "func DroppedOTelSurfaces(",
        "func ImportRolloutFile(", "func BuildInventorySnapshot(", "func ResolveSkillEvidence(", "func SourceToCanonicalTable(",
        "func AllReconciliationLanes(", "func ReconcileLane(", "func ReconcileSession(", "func ComputeDiscoverabilityPressure(",
    ]
    for required in required_types:
        if required not in core_source:
            errors.append(f"internal/codexadapter core missing required declaration: {required}")

    if "kansoku.local/kansoku/internal/adaptersdk" not in core_source:
        errors.append("internal/codexadapter must build on internal/adaptersdk.Adapter, not a parallel adapter mechanism")
    var_assert_paths = [p for p in core_source_paths if "var _ adaptersdk.Adapter" in p.read_text(encoding="utf-8")]
    if not var_assert_paths:
        errors.append("internal/codexadapter must statically assert *Adapter implements adaptersdk.Adapter")

    if "kansoku.local/kansoku/internal/privacy" not in core_source:
        errors.append("internal/codexadapter must reuse internal/privacy's sanitizer/feature-extraction machinery, not invent a second one")
    if re.search(r"type\s+Safe(Record|Error)\s+struct", core_source):
        errors.append("internal/codexadapter must not declare a second SafeRecord/SafeError sanitizer type")
    if re.search(r"func\s+extractPromptFeatures\s*\(", core_source) or re.search(r"func\s+ExtractPromptFeatures\s*\(", core_source):
        errors.append("internal/codexadapter must reuse internal/privacy.ExtractPromptFeatures, not redeclare a second prompt-feature extractor")

    # Discovery must never speculatively scan the whole home directory, and
    # must resolve CODEX_HOME first.
    if "StateRootEnv" not in core_source or '"CODEX_HOME"' not in core_source:
        errors.append("internal/codexadapter must resolve the documented CODEX_HOME env var")

    # Hook allowlist must never include a raw prompt field.
    hook_path = codexadapter_dir / "hook.go"
    hook_source = hook_path.read_text(encoding="utf-8") if hook_path.exists() else ""
    if not hook_source:
        errors.append("internal/codexadapter/hook.go is missing")
    else:
        if "AllowlistedHookFields" not in hook_source:
            errors.append("hook.go missing AllowlistedHookFields")
        if "raw prompt" not in hook_source.lower() and "never copies raw prompt" not in hook_source.lower() and "never" not in hook_source.lower():
            errors.append("hook.go must document that the raw prompt is never copied/persisted")
        if 'HookRoutePath' not in hook_source or '/v1/hooks/codex/' not in hook_source:
            errors.append("hook.go must build routes through the reused generic /v1/hooks/codex/{event} template")

    # Rollout importer must never write back to the source file and must be
    # idempotent on replay -- checked at the Go test level; here we only
    # check the guarantee text/API surface exists.
    rollout_path = codexadapter_dir / "rollout.go"
    rollout_source = rollout_path.read_text(encoding="utf-8") if rollout_path.exists() else ""
    if not rollout_source:
        errors.append("internal/codexadapter/rollout.go is missing")
    else:
        if "never writes anything back" not in rollout_source and "never_writes" not in rollout_source and "never writes" not in rollout_source:
            errors.append("rollout.go must document that the importer never writes back into the Codex session tree")
        if "RolloutCheckpoint" not in rollout_source:
            errors.append("rollout.go must expose a RolloutCheckpoint type for offset/rotation/truncation resume")

    # Evidence resolution must forbid ambiguous ownership promotion and
    # never let semantic_opportunity_classifier resolve to anything but
    # inferred.
    evidence_path = codexadapter_dir / "evidence.go"
    evidence_source = evidence_path.read_text(encoding="utf-8") if evidence_path.exists() else ""
    if not evidence_source:
        errors.append("internal/codexadapter/evidence.go is missing")
    else:
        if "ErrAmbiguousOwnershipPromotion" not in evidence_source:
            errors.append("evidence.go must expose ErrAmbiguousOwnershipPromotion so ambiguous helper/MCP ownership can never be silently promoted")
        if "EvidenceSemanticOpportunity" not in evidence_source or "EvidenceTierInferred" not in evidence_source:
            errors.append("evidence.go must keep semantic_opportunity_classifier bound to the inferred tier")

    # Reconciliation must degrade only the missing source, never fabricate
    # a whole-session zero.
    reconcile_path = codexadapter_dir / "reconcile.go"
    reconcile_source = reconcile_path.read_text(encoding="utf-8") if reconcile_path.exists() else ""
    if not reconcile_source:
        errors.append("internal/codexadapter/reconcile.go is missing")
    else:
        if "AllReconciliationLanes" not in reconcile_source or "ReconcileSession" not in reconcile_source:
            errors.append("reconcile.go must expose the closed reconciliation lane set and a per-session reconciler")

    for path in core_source_paths:
        text = path.read_text(encoding="utf-8")
        for forbidden in ('exec.Command("sh", "-c"', 'exec.Command("bash", "-c"', "eval(", 'os.ReadFile(os.Getenv("HOME")'):
            if forbidden in text:
                errors.append(f"{path.relative_to(ROOT)} contains a forbidden pattern: {forbidden}")

    # -- internal/observability/routes.go must route Codex hooks through the
    # existing generic mux, not a parallel ingress mechanism.
    routes_path = ROOT / "internal" / "observability" / "routes.go"
    routes_source = routes_path.read_text(encoding="utf-8") if routes_path.exists() else ""
    if not routes_source:
        errors.append("internal/observability/routes.go is missing")
    else:
        if "/v1/hooks/{adapter}/{event}" not in routes_source:
            errors.append("internal/observability/routes.go must keep the single generic hook_http route template")
        if "codexadapter.AdapterID" not in routes_source:
            errors.append("internal/observability/routes.go must dispatch Codex hook events by codexadapter.AdapterID through the generic mux, not a second HTTP server")
        if routes_source.count("http.NewServeMux()") != 1:
            errors.append("internal/observability/routes.go must not stand up a second HTTP mux for Codex")

    # -- fixtures: synthetic, sanitized, and no raw prohibited-content leak.
    for name in ("hook-otel-golden-map.json", "inventory-layouts.json", "rollout-fixtures.json", "skill-collision-and-ambiguous-ownership.json", "prohibited-content-canaries.json"):
        path = FIXTURES_DIR / name
        if not path.is_file():
            errors.append(f"tests/fixtures/session-06/{name} is missing")
            continue
        fixture = load(path)
        if fixture.get("synthetic") is not True:
            errors.append(f"{name} must be marked synthetic")

    if not CANARY_SCENARIO_PATH.is_file():
        errors.append("tests/fixtures/session-06/canary/kansoku-canary-scenario.json is missing")
    else:
        canary_fixture = load(CANARY_SCENARIO_PATH)
        if canary_fixture.get("synthetic") is not True:
            errors.append("canary scenario fixture must be marked synthetic")
        constraints = canary_fixture.get("execution_constraints", {})
        if not isinstance(constraints, dict) or not constraints.get("non_interactive_only") or not constraints.get("never_uses_a_real_user_repository"):
            errors.append("canary scenario fixture must keep non_interactive_only and never_uses_a_real_user_repository true")
        serialized_canary = json.dumps(canary_fixture, sort_keys=True)
        if "/Users/" in serialized_canary or "@example.com" in serialized_canary or "sk-" in serialized_canary:
            errors.append("canary scenario fixture is not sanitized/synthetic")

    prohibited_path = FIXTURES_DIR / "prohibited-content-canaries.json"
    if prohibited_path.is_file():
        prohibited = load(prohibited_path)
        canaries = prohibited.get("canaries", [])
        if not canaries:
            errors.append("prohibited-content-canaries.json must declare at least one canary")

    return errors


def run_go_suite() -> dict[str, Any]:
    """Run only the internal/codexadapter Go suite inside the exact pinned,
    offline, network-disabled Go image scripts/run_go_tests.py already uses.
    This is a narrower re-run for standalone use of this validator;
    scripts/run_go_tests.py's full ./... sweep remains the authoritative
    cross-session Go proof."""
    import os

    command = [
        "docker", "run", "--rm", "--network", "none", "--read-only", "--cap-drop", "ALL",
        "--security-opt", "no-new-privileges", "--user", f"{os.getuid()}:{os.getgid()}",
        "--tmpfs", "/tmp:rw,exec,nosuid,nodev,mode=1777",
        "--mount", f"type=bind,src={ROOT},dst=/src,readonly", "--workdir", "/src",
        "--env", "GOCACHE=/tmp/go-cache", "--env", "GOTMPDIR=/tmp/go-tmp", "--env", "HOME=/tmp/home",
        GO_IMAGE, "sh", "-c",
        "mkdir -p /tmp/go-cache /tmp/go-tmp /tmp/home && "
        "/usr/local/go/bin/go build -mod=vendor ./internal/codexadapter/... ./internal/observability/... && "
        "/usr/local/go/bin/go vet -mod=vendor ./internal/codexadapter/... && "
        "/usr/local/go/bin/go test -mod=vendor -v -count=1 ./internal/codexadapter/...",
    ]
    result = subprocess.run(command, cwd=ROOT, check=False, capture_output=True, text=True)
    return {
        "status": "pass" if result.returncode == 0 else "fail",
        "stdout_tail": "\n".join(result.stdout.splitlines()[-120:]),
        "stderr_tail": "\n".join(result.stderr.splitlines()[-40:]),
        "returncode": result.returncode,
    }


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--json", action="store_true")
    parser.add_argument("--with-go", action="store_true", help="also shell out to build/vet/test internal/codexadapter in the pinned offline Go image")
    args = parser.parse_args()

    errors: list[str] = []
    go_result: dict[str, Any] | None = None
    try:
        errors = validate(historical=trusted_lock_from_head())
        if args.with_go:
            go_result = run_go_suite()
            if go_result["status"] != "pass":
                errors.append("internal/codexadapter Go build/vet/test failed inside the pinned offline Go image")
    except (OSError, ValueError, json.JSONDecodeError, subprocess.SubprocessError) as exc:
        errors.append(str(exc))

    if args.json:
        print(json.dumps({"status": "pass" if not errors else "fail", "errors": errors, "go": go_result}, indent=2, sort_keys=True))
    else:
        if go_result is not None:
            print(go_result.get("stdout_tail", ""))
            if go_result.get("stderr_tail"):
                print(go_result["stderr_tail"], file=sys.stderr)
        for error in errors:
            print(error, file=sys.stderr)
    return 1 if errors else 0


if __name__ == "__main__":
    sys.exit(main())
