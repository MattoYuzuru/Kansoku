#!/usr/bin/env python3
"""Independent closed-world validator for the Session 07 Claude adapter.

Mirrors `scripts/validate_codex.py`'s structure exactly. Two independent
things are checked:

1. The static contract: `contracts/claude/*.yaml` registries are exact,
   closed and bound by `contracts/claude-policy-locks.yaml` versioned
   canonical semantic digests, using the identical append-only lock
   mechanism established for `contracts/adapter-sdk`/`contracts/codex`. The
   two `contracts/cross-agent/*.yaml` registries (the second fictional
   fixture-agent "Wayfinder" and the Codex+Claude cross-agent invariant
   scenario) this same session introduced are checked the same way, bound by
   `contracts/cross-agent-policy-locks.yaml` -- there is no separate
   `scripts/validate_cross_agent.py`; folding the check in here keeps one
   standalone validator for everything this session's stage actually
   produced, matching `contracts/README.md`'s check-command block, which
   lists only `scripts/validate_claude.py` for Session 07.
2. The code/contract alignment: `internal/claudeadapter` (plus the small
   `claude`-adapter-name case inside `internal/observability/routes.go`'s
   generic hook mux) actually implements the invariants the registries
   declare -- hook events/allowlist/spool, OTel event mapping/dropped
   surfaces (including the unconditional detailed-telemetry strip),
   transcript checkpoint fields, the closed skill-evidence vocabulary, the
   eight reconciliation lanes -- and that the registered adapter never
   introduces a second sanitizer, a second ingress mechanism or a second OTel
   installer target. It also checks `internal/adaptersdk/wayfinder` (the
   second fictional fixture-agent) and `internal/crossagent` (the Codex+
   Claude cross-agent invariant test) exist and reuse the same
   `adaptersdk.Adapter`/registry machinery with zero new agent-name branch
   inside `internal/adaptersdk` itself.

Session 07 adds no new external runtime and (per `git diff HEAD -- go.mod
go.sum`) no new third-party dependency, so -- as with Session 05/06 -- there
is no ephemeral-container harness here; the Go proof is `go vet`/`go test`
for the Session 07 packages inside the same pinned, network-disabled Go
image `scripts/run_go_tests.py` already uses. That full-repo sweep is
authoritative; this validator's `--with-go` flag re-runs the narrower
Session-07-package slice so `validate_claude.py` remains a standalone,
single-command proof of the Session 07 exit gate (Claude-only; Gemini/Cursor
are Session 07b and are not evaluated here).
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
CLAUDE_DIR = ROOT / "contracts" / "claude"
CLAUDE_LOCK_PATH = ROOT / "contracts" / "claude-policy-locks.yaml"
CROSS_AGENT_DIR = ROOT / "contracts" / "cross-agent"
CROSS_AGENT_LOCK_PATH = ROOT / "contracts" / "cross-agent-policy-locks.yaml"
FIXTURES_DIR = ROOT / "tests" / "fixtures" / "session-07"
GO_IMAGE = "golang@sha256:1ecb7edf62a0408027bd5729dfd6b1b8766e578e8df93995b225dfd0944eb651"

CLAUDE_FILES = ("manifest.yaml", "hooks-and-otel.yaml", "transcript-and-inventory.yaml", "skill-evidence-and-reconciliation.yaml")
CROSS_AGENT_FILES = ("second-fixture-agent.yaml", "invariant-scenario.yaml")

CAPABILITY_IDS = [
    "discovery.agent_and_surface", "inventory.components", "activity.sessions", "activity.prompt_metadata",
    "activity.token_model_cost", "components.skill_invocation", "components.plugin_and_custom_command",
    "components.mcp_lifecycle", "components.builtin_tool_calls_and_approvals", "components.subagents_and_compaction",
    "ingestion.historical_import", "ingestion.live_stream", "configuration.install", "configuration.live_canary",
]
HOOK_EVENTS = ["SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "SubagentStart", "SubagentStop", "Stop"]
SOURCE_SCOPES = ["system", "user", "repository", "admin", "marketplace", "plugin_cache"]
NODE_KINDS = [
    "agent_installation", "agent_surface", "agent_version", "plugin_package", "plugin_version",
    "skill_identity", "mcp_server_instance", "mcp_tool", "hook_definition", "custom_command_definition",
    "subagent_definition", "cache_artifact",
]
EDGE_KINDS = ["bundles", "provides", "configured_in", "enabled_for", "shadows", "collides_with", "depends_on", "observed_using"]
SKILL_EVIDENCE_KINDS = [
    "skill_tool_call_explicit", "skill_tool_call_implicit", "otel_skill_name_attribution",
    "skill_md_load_evidence", "plugin_or_mcp_declared_use", "uniquely_owned_helper_execution",
    "semantic_opportunity_classifier",
]
RECONCILIATION_LANES = [
    "hook_prompt_events_vs_otel_prompt_events_vs_transcript_user_messages",
    "hook_tool_terminal_events_vs_otel_results_vs_transcript_calls_and_outputs",
    "session_start_stop_resume_vs_transcript_lifecycle",
    "subagent_lifecycle_vs_parent_transcript_evidence",
    "mcp_call_counts_vs_configured_and_advertised_tools",
    "skill_transcript_calls_vs_skill_otel_attribution_vs_tool_hooks",
    "plugin_ownership_vs_bundled_component_inventory",
    "component_explicit_load_execute_evidence_compared_without_forcing_equality",
]

CLAUDE_LOCK_BASES = {
    "claude.manifest": "contracts/claude/manifest.yaml",
    "claude.hooks-and-otel": "contracts/claude/hooks-and-otel.yaml",
    "claude.transcript-and-inventory": "contracts/claude/transcript-and-inventory.yaml",
    "claude.skill-evidence-and-reconciliation": "contracts/claude/skill-evidence-and-reconciliation.yaml",
}
CROSS_AGENT_LOCK_BASES = {
    "cross-agent.second-fixture-agent": "contracts/cross-agent/second-fixture-agent.yaml",
    "cross-agent.invariant-scenario": "contracts/cross-agent/invariant-scenario.yaml",
}


def load(path: Path) -> dict[str, Any]:
    value = json.loads(path.read_text(encoding="utf-8"))
    if not isinstance(value, dict):
        raise ValueError(f"{path}: object required")
    return value


def semantic_sha256(value: Any) -> str:
    encoded = json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=False).encode()
    return hashlib.sha256(encoded).hexdigest()


def claude_registries() -> dict[str, dict[str, Any]]:
    return {f"contracts/claude/{name}": load(CLAUDE_DIR / name) for name in CLAUDE_FILES}


def cross_agent_registries() -> dict[str, dict[str, Any]]:
    return {f"contracts/cross-agent/{name}": load(CROSS_AGENT_DIR / name) for name in CROSS_AGENT_FILES}


def registries() -> dict[str, dict[str, Any]]:
    """Combined registry map (Claude + cross-agent) for convenience callers
    that want the whole Session 07 static contract at once."""
    combined = claude_registries()
    combined.update(cross_agent_registries())
    return combined


def validate(
    candidate: dict[str, dict[str, Any]] | None = None,
    locks: dict[str, Any] | None = None,
    cross_candidate: dict[str, dict[str, Any]] | None = None,
    cross_locks: dict[str, Any] | None = None,
    include_code: bool = True,
    historical: dict[str, Any] | None = None,
    cross_historical: dict[str, Any] | None = None,
) -> list[str]:
    errors: list[str] = []
    errors.extend(validate_claude(candidate, locks, include_code=False, historical=historical))
    errors.extend(validate_cross_agent(cross_candidate, cross_locks, historical=cross_historical))
    if include_code:
        errors.extend(validate_code_and_fixtures())
    return errors


def validate_claude(
    candidate: dict[str, dict[str, Any]] | None = None,
    locks: dict[str, Any] | None = None,
    include_code: bool = True,
    historical: dict[str, Any] | None = None,
) -> list[str]:
    data = claude_registries() if candidate is None else candidate
    lock = load(CLAUDE_LOCK_PATH) if locks is None else locks
    errors: list[str] = []
    if set(data) != {f"contracts/claude/{name}" for name in CLAUDE_FILES}:
        errors.append("claude registry set is not exact")
        return errors
    by_name = {Path(path).name: value for path, value in data.items()}
    manifest, hooks_and_otel, transcript_and_inventory, skill_evidence = (by_name[name] for name in CLAUDE_FILES)

    expected_top = {
        "manifest.yaml": {
            "schema_version", "contract_version", "effective_at", "adapter_id", "adapter_id_naming",
            "manifest_api_version", "execution_form", "network_grade", "reused_parse_limits",
            "installation_discovery", "agent_detection", "permissions",
            "compatibility_registry_fields_reused", "unknown_agent_version_policy", "documented_version_gates",
            "sources", "capability_ids", "capability_ids_source", "installer_target_reuse", "hook_ingress_reuse",
        },
        "hooks-and-otel.yaml": {
            "schema_version", "contract_version", "effective_at", "hook_source", "otel_source",
            "source_event_mapping", "independent_capability_degradation",
        },
        "transcript-and-inventory.yaml": {
            "schema_version", "contract_version", "effective_at", "transcript_source", "inventory_source",
        },
        "skill-evidence-and-reconciliation.yaml": {
            "schema_version", "contract_version", "effective_at", "skill_evidence_model",
            "source_to_canonical_mapping", "reconciliation", "required_tests", "exit_gate",
        },
    }
    for name, fields in expected_top.items():
        if set(by_name[name]) != fields:
            errors.append(f"{name}: top-level closed schema changed")

    # -- manifest.yaml --
    if manifest.get("adapter_id") != "claude":
        errors.append("manifest adapter_id must remain claude")
    if manifest.get("manifest_api_version") != "kansoku.adapter/v1":
        errors.append("manifest_api_version must reuse adapter-sdk's kansoku.adapter/v1 verbatim")
    if manifest.get("execution_form") != "builtin":
        errors.append("claude adapter execution form must remain builtin")
    if manifest.get("network_grade") != "loopback_only":
        errors.append("claude adapter network grade must remain loopback_only, never unrestricted")
    parse_limits = manifest.get("reused_parse_limits", {})
    for field in ("max_config_entries", "max_config_depth", "max_config_string"):
        if not isinstance(parse_limits.get(field), int) or parse_limits.get(field, 0) <= 0:
            errors.append(f"manifest reused_parse_limits.{field} must be a positive integer")
    if "forbidden" not in str(parse_limits.get("code_execution", "")) or "never_evaluated_or_executed" not in str(parse_limits.get("code_execution", "")):
        errors.append("manifest parsing must explicitly forbid code execution and state manifests/hooks/settings/plugin/transcript records are never evaluated or executed")
    discovery = manifest.get("installation_discovery", {})
    if "never_scans_an_entire_home_directory" not in str(discovery.get("never_speculative_home_scan", "")):
        errors.append("installation discovery must explicitly forbid scanning the entire home directory")
    if "documented_claude_config_locations_are_resolved_first" not in str(discovery.get("never_speculative_home_scan", "")):
        errors.append("installation discovery must resolve documented Claude config locations first")
    if "installation_merge_rule" not in discovery or "remain_distinct_installation_candidates" not in str(discovery.get("installation_merge_rule", "")):
        errors.append("installation discovery must keep same-state-root/different-surface candidates distinct")
    agent_detection = manifest.get("agent_detection", {})
    if agent_detection.get("executables") != ["claude"]:
        errors.append("agent_detection.executables must remain exactly ['claude']")
    if "none_documented" not in str(agent_detection.get("state_root_env_var", "")):
        errors.append("agent_detection.state_root_env_var must state no dedicated CLAUDE_HOME-shaped env var is documented")
    if set(agent_detection.get("documented_config_roots", [])) != {"claude_user_settings", "claude_project_settings", "claude_managed_settings"}:
        errors.append("agent_detection.documented_config_roots must remain the three documented settings locations")
    if set(agent_detection.get("surfaces", [])) != {"cli", "ide_extension", "app"}:
        errors.append("agent_detection.surfaces must remain cli/ide_extension/app")
    if manifest.get("capability_ids") != CAPABILITY_IDS:
        errors.append("claude manifest capability_ids must reuse adapter-sdk's closed capability id list verbatim, inventing none")
    if "no_claude_specific_capability_id_is_invented" not in str(manifest.get("capability_ids_source", "")):
        errors.append("manifest must explicitly state no claude-specific capability id is invented")
    if manifest.get("unknown_agent_version_policy", "").find("defaults_to_degraded") == -1:
        errors.append("unknown agent version outside every compatibility range must default to degraded")
    installer_reuse = manifest.get("installer_target_reuse", {})
    if "claude.user_otel_target_already_declared_in_contracts/privacy/installer.yaml" not in str(installer_reuse.get("otel", "")) or "reused_verbatim" not in str(installer_reuse.get("otel", "")):
        errors.append("manifest must reuse contracts/privacy/installer.yaml's existing claude.user_otel target verbatim, never redefine it")
    if "BuildClaudePlan" not in str(installer_reuse.get("otel", "")):
        errors.append("manifest must state internal/installer/protocol.go's existing BuildClaudePlan is reused verbatim for claude.user_otel")
    if "never_selected_by_default" not in str(installer_reuse.get("project_local_scope", "")):
        errors.append("project-local Claude config scope must never be selected by default")
    hook_reuse = manifest.get("hook_ingress_reuse", {})
    if hook_reuse.get("route_template") != "reused_verbatim_from_contracts/observability/ingress.yaml_hook_http_protocol_route_/v1/hooks/{adapter}/{event}":
        errors.append("hook ingress route must be the reused generic /v1/hooks/{adapter}/{event} template, not a new one")
    if hook_reuse.get("auth") != "session02_loopback_bearer_reused_verbatim_no_second_auth_mechanism":
        errors.append("hook ingress auth must remain session02_loopback_bearer with no second auth mechanism")
    no_parallel = str(hook_reuse.get("no_parallel_route", ""))
    if "never" not in no_parallel or "fixture-agent" not in no_parallel:
        errors.append("hook ingress must never introduce a parallel ingress mechanism and must never collide with the reserved fixture-agent literal adapter id")

    # -- hooks-and-otel.yaml --
    hook_source = hooks_and_otel.get("hook_source", {})
    if hook_source.get("supported_events") != HOOK_EVENTS:
        errors.append("claude.hook supported_events set changed")
    if hook_source.get("route") != "/v1/hooks/claude/{event}":
        errors.append("claude.hook route must remain /v1/hooks/claude/{event}")
    route_reuse = str(hook_source.get("route_reuse", ""))
    if "already_declared_in_contracts/observability/ingress.yaml" not in route_reuse or "no_parallel_ingress_route_is_declared_here" not in route_reuse:
        errors.append("claude.hook must declare route reuse of the existing generic ingress route, no parallel route")
    if "fixture-agent" not in route_reuse:
        errors.append("claude.hook route_reuse must explicitly state it never collides with the reserved fixture-agent adapter path segment")
    if hook_source.get("auth") != "session02_loopback_bearer_reused_verbatim":
        errors.append("claude.hook auth must remain session02_loopback_bearer_reused_verbatim")
    payload_surfaces = hook_source.get("documented_payload_surfaces", {})
    content_fields = set(payload_surfaces.get("content_bearing_fields_present_upstream", []))
    if not {"transcript_path", "cwd", "prompt", "tool_input", "tool_response"}.issubset(content_fields):
        errors.append("documented_payload_surfaces must keep listing every documented content-bearing hook field")
    treatment = str(payload_surfaces.get("kansoku_treatment", ""))
    if "stripped_unconditionally" not in treatment:
        errors.append("hook payload treatment must strip every content-bearing field unconditionally at the helper boundary")
    helper = hook_source.get("helper_contract", {})
    if "raw_prompt_text_is_never_written_to_disk_or_sent" not in str(helper.get("prompt_feature_computation", "")):
        errors.append("hook helper prompt feature computation must state raw prompt text is never written to disk or sent")
    path_pseudo = str(helper.get("path_pseudonymization", ""))
    if "raw_path_is_never_sent_or_stored" not in path_pseudo:
        errors.append("hook helper must pseudonymize transcript_path/cwd and state the raw path is never sent or stored")
    allowlisted = " ".join(helper.get("allowlisted_fields", [])).lower()
    if "session_id" not in helper.get("allowlisted_fields", []) or "prompt" in allowlisted or "path" in allowlisted:
        errors.append("hook helper allowlisted_fields must never include a raw prompt or raw path field")
    if "already_sanitized_events" not in str(helper.get("spool", "")):
        errors.append("hook helper spool must only ever hold already-sanitized events")
    hook_target = hook_source.get("hook_installer_target", {})
    if hook_target.get("id") != "claude.user_hook":
        errors.append("claude hook installer target id must remain claude.user_hook")
    if hook_target.get("config_locator_kind") != "claude_user_settings" or hook_target.get("format") != "json":
        errors.append("claude.user_hook installer target locator/format changed")
    forbidden_keys = set(hook_target.get("forbidden_keys", []))
    if not {"remote_command", "raw_payload_log", "credential_forwarding", "project_local_hook"}.issubset(forbidden_keys):
        errors.append("claude.user_hook forbidden_keys must keep forbidding remote command/raw payload logging/credential forwarding/project-local hooks")
    if "never_modified_unless_the_user_explicitly_selects_that_scope" not in str(hook_target.get("default_scope", "")):
        errors.append("claude.user_hook default_scope must state project-local config is never modified without explicit user selection")
    if "does_not_edit_or_redefine_claude.user_otel" not in str(hook_target.get("new_target_note", "")):
        errors.append("claude.user_hook must explicitly state it does not edit or redefine the existing claude.user_otel target")
    trust = hook_source.get("trust_and_enabled_state", {})
    if trust.get("audited") is not True:
        errors.append("claude hook trust/enabled state must be audited")
    if "never_bypasses_or_silently_repairs" not in str(trust.get("bypass_forbidden", "")):
        errors.append("claude hook trust/enabled-state bypass_forbidden text weakened")
    if "never_a_silent_repair" not in str(trust.get("remediation_only", "")):
        errors.append("claude hook trust remediation must never be a silent repair")

    otel_source = hooks_and_otel.get("otel_source", {})
    installer_target_reuse_text = str(otel_source.get("installer_target_reuse", ""))
    if "claude.user_otel_target_already_declared_in_contracts/privacy/installer.yaml" not in installer_target_reuse_text:
        errors.append("claude.otel must reuse the existing claude.user_otel installer target")
    if "never_redefined_or_renamed" not in installer_target_reuse_text:
        errors.append("claude.otel installer target reuse must state the target is never redefined or renamed")
    if "declares_no_new_otel_installer_target" not in str(otel_source.get("no_second_otel_target", "")):
        errors.append("hooks-and-otel.yaml must explicitly declare no second OTel installer target")
    if otel_source.get("log_user_prompt") is not False:
        errors.append("claude.otel log_user_prompt must remain false")
    dropped = set(otel_source.get("dropped_surfaces", []))
    if not {"log.body", "tool_payload", "output_snippet", "prompt_text", "assistant_response_text", "raw_api_body"}.issubset(dropped):
        errors.append("claude.otel dropped_surfaces must keep dropping log.body/tool_payload/output_snippet/prompt_text/assistant_response_text/raw_api_body")
    if "reuses_contracts/observability/ingress.yaml_otlp_safe_attributes_allowlist_verbatim" not in str(otel_source.get("otlp_safe_attributes_reuse", "")):
        errors.append("claude.otel must reuse the existing OTLP safe-attribute allowlist verbatim")
    unconditional = str(otel_source.get("unconditional_strip_rule", ""))
    if "regardless_of_what_those_settings_report" not in unconditional:
        errors.append("claude.otel must strip detailed-telemetry content fields unconditionally regardless of upstream settings")
    documented_attrs = otel_source.get("documented_attributes", {})
    if not {"prompt_text", "assistant_response_text", "tool_input", "tool_output", "raw_api_bodies"}.issubset(set(documented_attrs.get("detailed_gates_may_expose", []))):
        errors.append("documented_attributes.detailed_gates_may_expose must keep listing every content-bearing field detailed telemetry may expose")

    if "claude.hook_and_claude.otel_and_claude.transcript_are_independently_capability_scoped" not in str(hooks_and_otel.get("independent_capability_degradation", "")):
        errors.append("hook/OTel/transcript independent capability degradation guarantee weakened")

    mapping = hooks_and_otel.get("source_event_mapping", [])
    if len(mapping) != 17:
        errors.append("hooks-and-otel.yaml source_event_mapping row count changed")

    # -- transcript-and-inventory.yaml --
    transcript_source = transcript_and_inventory.get("transcript_source", {})
    if "never_a_CLAUDE_HOME_env_var_which_claude_code_does_not_document" not in str(transcript_source.get("state_root", "")):
        errors.append("claude.transcript state root resolution must explicitly reject a CLAUDE_HOME-shaped env var since Claude Code documents no such variable")
    if "never_a_speculative_home_directory_scan" not in str(transcript_source.get("state_root", "")):
        errors.append("claude.transcript state root resolution must never speculatively scan the home directory")
    checkpoint_fields = set(transcript_source.get("checkpoint_fields", []))
    if not {"file_identity", "byte_offset", "first_record_fingerprint", "last_record_fingerprint", "rotation_generation", "truncation_detected"}.issubset(checkpoint_fields):
        errors.append("claude.transcript checkpoint_fields must keep file identity/offset/fingerprint/rotation/truncation fields")
    if "never_writes_to_the_claude_code_session_tree" not in str(transcript_source.get("parsing", "")):
        errors.append("claude.transcript parsing must never write back into the Claude Code session tree")
    if "never_guesses_a_mode_the_transcript_does_not_label" not in str(transcript_source.get("skill_tool_call_mapping", "")):
        errors.append("claude.transcript Skill tool call mapping must never guess an explicit/implicit mode the transcript does not label")
    historical_mode = transcript_source.get("historical_content_mode", {})
    if "raw_content_is_never_written_to_a_durable_path" not in str(historical_mode.get("opt_in", "")):
        errors.append("historical content opt-in mode must state raw content is never written to a durable path")
    if "users_may_disable_historical_content_parsing_entirely" not in str(historical_mode.get("user_disable", "")):
        errors.append("historical content parsing must be user-disableable")
    quarantine = transcript_source.get("corrupt_or_unknown_schema", {})
    if quarantine.get("raw_bytes_durable") is not False:
        errors.append("corrupt/unknown-schema transcript records must never durably retain raw bytes")
    replay = transcript_source.get("replay_and_crash", {})
    if "never_a_duplicate_fact" not in str(replay.get("idempotency", "")):
        errors.append("transcript replay idempotency guarantee weakened")
    if "degraded_incident_scoped_to_claude.transcript_only" not in str(replay.get("rotation_truncation", "")):
        errors.append("transcript rotation/truncation handling must scope its degraded incident to claude.transcript only")

    inventory_source = transcript_and_inventory.get("inventory_source", {})
    if set(inventory_source.get("scopes_inventoried", [])) != set(SOURCE_SCOPES):
        errors.append("claude.inventory scopes_inventoried must reuse the closed adapter-sdk source scope set verbatim")
    if "no_new_scope_is_invented" not in str(inventory_source.get("scopes_reused_from", "")):
        errors.append("claude.inventory must explicitly state no new scope is invented")
    if set(inventory_source.get("node_kinds_used", [])) - set(NODE_KINDS):
        errors.append("claude.inventory node_kinds_used must stay within adapter-sdk's closed node kind vocabulary")
    if "no_claude_specific_node_kind_is_invented" not in str(inventory_source.get("node_kinds_source", "")):
        errors.append("claude.inventory must explicitly state no claude-specific node kind is invented")
    if set(inventory_source.get("edge_kinds_used", [])) != set(EDGE_KINDS):
        errors.append("claude.inventory edge_kinds_used must reuse the closed adapter-sdk edge kind set verbatim")
    if "never_a_speculative_recursive_filesystem_walk" not in str(inventory_source.get("repository_scan_bound", "")):
        errors.append("claude.inventory repository scan must never be a speculative recursive filesystem walk")
    cache_rule = str(inventory_source.get("cache_rule", ""))
    if "cache_packages_are_never_considered_enabled" not in cache_rule or "requires_an_explicit_enabled_for_edge" not in cache_rule:
        errors.append("claude.inventory cache separation rule weakened")
    if "never_merged" not in str(inventory_source.get("collision_rule", "")):
        errors.append("claude.inventory collision rule must never merge same-named nodes across scopes")
    if "never_reported_as_a_standalone_unowned_component_when_bundling_is_observable" not in str(inventory_source.get("plugin_bundled_component_relationship", "")):
        errors.append("claude.inventory must never report a plugin-bundled component as standalone/unowned when bundling is observable")

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
    if "never_a_zero_value_that_could_be_misread_as_observed_absence_of_use" not in str(evidence_model.get("unsupported_rendering_rule", "")):
        errors.append("unsupported rendering rule weakened; an unsupported field/capability must never render as a misleading zero")

    mapping_rows = skill_evidence.get("source_to_canonical_mapping", [])
    if len(mapping_rows) != 9:
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
    cost_rule = str(reconciliation.get("cost_and_token_attribution", ""))
    if "retained_and_surfaced_rather_than_silently_divided_or_summed" not in cost_rule:
        errors.append("cost/token attribution must retain and surface a documented double-attribution case rather than silently dividing or summing it")

    required_tests = set(skill_evidence.get("required_tests", []))
    required_test_fragments = {
        "sanitized_transcript_fixtures_for_each_declared_compatibility_version_and_event_variant",
        "hook_and_otel_golden_maps",
        "offset_rotation_truncation_replay_crash_importer_tests",
        "skill_collision_and_ambiguous_ownership_tests",
        "claude_settings_multi_surface_and_project_scope_inventory_layout_tests",
        "configuration_concurrent_change_and_rollback_tests",
        "cross_source_mismatch_and_inactive_source_logic_tests",
        "prohibited_content_canaries_with_detailed_upstream_telemetry_enabled",
    }
    if required_tests != required_test_fragments:
        errors.append("required_tests list changed")

    exit_gate = skill_evidence.get("exit_gate", {})
    if "fixtures_required_at_minimum" not in str(exit_gate.get("compatibility_matrix_backed_by", "")):
        errors.append("exit gate must require fixtures at minimum, neither a fixture-only result nor an unfixtured live claim alone sufficient for a production label")
    if "never_silently_reporting_plausible_looking_zero_usage" not in str(exit_gate.get("independent_visible_degradation", "")):
        errors.append("exit gate independent visible degradation guarantee weakened")
    if "never_represents_inferred_or_reconstructed_tier_claude_skill_use_as_a_native_exact_activation" not in str(exit_gate.get("no_inferred_promoted_to_native", "")):
        errors.append("exit gate no-inferred-promoted-to-native guarantee weakened")
    if "never_as_a_misleading_zero" not in str(exit_gate.get("unsupported_fields_render_as_unsupported", "")):
        errors.append("exit gate unsupported-fields-render-as-unsupported guarantee weakened")
    detailed_strip = str(exit_gate.get("detailed_telemetry_stripped_unconditionally", ""))
    for fragment in ("prompt_text", "assistant_response_text", "tool_input", "tool_output", "tool_parameters", "transcript_path", "raw_api_bodies", "even_when_the_upstream_agent_has_detailed_telemetry_settings_enabled"):
        if fragment not in detailed_strip:
            errors.append(f"exit gate detailed_telemetry_stripped_unconditionally must keep mentioning {fragment}")
    for flag in ("inventory_correct", "raw_prompt_absent_from_every_durable_path", "replay_idempotent"):
        if exit_gate.get(flag) is not True:
            errors.append(f"exit gate {flag} must remain true")
    if "never_asserted_ahead_of_it" not in str(exit_gate.get("support_label_governance", "")):
        errors.append("exit gate support_label_governance must state Claude's exact support label is never asserted ahead of actually produced evidence")

    errors.extend(validate_claude_policy_locks(lock, data, historical))
    if include_code:
        errors.extend(validate_code_and_fixtures())
    return errors


def validate_claude_policy_locks(lock: dict[str, Any], data: dict[str, dict[str, Any]], historical: dict[str, Any] | None = None) -> list[str]:
    return _validate_policy_locks(
        lock, data, historical,
        schema_version="kansoku.claude-policy-locks/1",
        version_pattern=r"(claude\.(?:manifest|hooks-and-otel|transcript-and-inventory|skill-evidence-and-reconciliation))/([1-9][0-9]*)",
        lock_bases=CLAUDE_LOCK_BASES,
    )


def validate_cross_agent(
    candidate: dict[str, dict[str, Any]] | None = None,
    locks: dict[str, Any] | None = None,
    historical: dict[str, Any] | None = None,
) -> list[str]:
    data = cross_agent_registries() if candidate is None else candidate
    lock = load(CROSS_AGENT_LOCK_PATH) if locks is None else locks
    errors: list[str] = []
    if set(data) != {f"contracts/cross-agent/{name}" for name in CROSS_AGENT_FILES}:
        errors.append("cross-agent registry set is not exact")
        return errors
    by_name = {Path(path).name: value for path, value in data.items()}
    second_fixture, invariant_scenario = (by_name[name] for name in CROSS_AGENT_FILES)

    if second_fixture.get("adapter_id") != "wayfinder":
        errors.append("second fixture-agent adapter_id must remain wayfinder")
    distinctness = str(second_fixture.get("adapter_id_distinctness", ""))
    for term in ("codex", "claude", "gemini", "cursor", "loomwright", "fixture-agent"):
        if term not in distinctness:
            errors.append(f"second fixture-agent adapter_id_distinctness must keep mentioning {term}")
    shape = second_fixture.get("shape_deliberately_unlike_loomwright_and_real_adapters", {})
    if "wayfinder_declares_zero_otlp_source" not in str(shape.get("no_otel", "")):
        errors.append("second fixture-agent must declare zero OTel source")
    if "recipe" not in str(shape.get("component_vocabulary", "")):
        errors.append("second fixture-agent must use a 'recipe' component vocabulary, distinct from skill/thread")
    if "non-uuid" not in str(shape.get("session_identifiers", "")).lower():
        errors.append("second fixture-agent session identifiers must be explicitly non-UUID")
    if "never_populated_with_a_placeholder_zero" not in str(shape.get("missing_token_capability", "")):
        errors.append("second fixture-agent missing token capability must never be populated with a placeholder zero")
    if "must_be_quarantined_as_an_unknown-schema_incident_never_silently_dropped_or_guessed_into_a_known_canonical_type" not in str(shape.get("one_deliberately_unknown_schema", "")):
        errors.append("second fixture-agent's one deliberately unknown schema event must be quarantined, never silently dropped or guessed")
    if second_fixture.get("event_vocabulary") != ["path.opened", "recipe.consulted", "recipe.mystery", "path.closed"]:
        errors.append("second fixture-agent event_vocabulary changed")
    if "no_wayfinder-specific_capability_id_is_invented" not in str(second_fixture.get("capability_ids_reused", "")):
        errors.append("second fixture-agent must explicitly state no wayfinder-specific capability id is invented")
    conformance = second_fixture.get("required_conformance_checks", [])
    if not any("zero_new_if_agentid_branch_inside_internal/adaptersdk" in check.lower() for check in conformance):
        errors.append("second fixture-agent required_conformance_checks must require zero new agent-name branch inside internal/adaptersdk")

    if invariant_scenario.get("logical_scenario") != "session -> prompt metadata -> skill activation -> MCP tool call -> model tokens -> success":
        errors.append("cross-agent invariant scenario logical_scenario text changed")
    stage_mapping = invariant_scenario.get("scenario_stage_to_capability_mapping", [])
    if len(stage_mapping) != 6:
        errors.append("cross-agent invariant scenario stage mapping row count changed")
    if invariant_scenario.get("participating_adapters") != ["codex", "claude"]:
        errors.append("cross-agent invariant scenario participating_adapters must remain exactly [codex, claude]")
    note = str(invariant_scenario.get("participating_adapters_note", ""))
    if "gemini_and_cursor_are_explicitly_excluded" not in note:
        errors.append("cross-agent invariant scenario must explicitly exclude gemini/cursor from this session")
    assertion_rule = str(invariant_scenario.get("assertion_rule", ""))
    if "never_to_a_string_equality_check_against_codex_or_claude_as_an_agent_id" not in assertion_rule:
        errors.append("cross-agent invariant scenario assertion_rule must forbid asserting on an agent-id string equality check")
    if "never_asserts_a_uniform_zero_or_forces_equal_evidence_tiers_across_both_agents" not in str(invariant_scenario.get("unsupported_rendering_rule", "")):
        errors.append("cross-agent invariant scenario unsupported_rendering_rule must forbid forcing equal evidence tiers or a uniform zero across both agents")
    if "it_declares_no_new_evidence_tier_or_reconciliation_lane_shape" not in str(invariant_scenario.get("reconciliation_reuse", "")):
        errors.append("cross-agent invariant scenario must declare no new evidence tier or reconciliation lane shape")

    errors.extend(_validate_policy_locks(
        lock, data, historical,
        schema_version="kansoku.cross-agent-policy-locks/1",
        version_pattern=r"(cross-agent\.(?:second-fixture-agent|invariant-scenario))/([1-9][0-9]*)",
        lock_bases=CROSS_AGENT_LOCK_BASES,
    ))
    return errors


def _validate_policy_locks(
    lock: dict[str, Any],
    data: dict[str, dict[str, Any]],
    historical: dict[str, Any] | None,
    *,
    schema_version: str,
    version_pattern: str,
    lock_bases: dict[str, str],
) -> list[str]:
    errors: list[str] = []
    if set(lock) != {"schema_version", "effective_at", "locks"} or lock.get("schema_version") != schema_version:
        errors.append(f"policy lock registry ({schema_version}) is not exact")
    records = lock.get("locks", [])
    if not isinstance(records, list):
        return errors + [f"policy locks ({schema_version}) must be a list"]
    if historical is not None:
        old = historical.get("locks", []) if isinstance(historical, dict) else []
        if records[: len(old)] != old:
            errors.append("policy lock list must retain the exact append-only trusted prefix")
    latest: dict[str, tuple[int, dict[str, Any]]] = {}
    seen: set[str] = set()
    ordinals: dict[str, list[int]] = {base: [] for base in lock_bases}
    for item in records:
        if not isinstance(item, dict) or set(item) != {"policy_version", "registry", "semantic_sha256"}:
            errors.append("policy lock entries must be closed")
            continue
        version = item.get("policy_version", "")
        match = re.fullmatch(version_pattern, version)
        if not match or item.get("registry") != lock_bases.get(match.group(1)) or re.fullmatch(r"[0-9a-f]{64}", str(item.get("semantic_sha256"))) is None:
            errors.append("policy lock entry has invalid version/registry/digest binding")
            continue
        if version in seen:
            errors.append(f"duplicate policy version {version}")
        seen.add(version)
        ordinal = int(match.group(2))
        ordinals[match.group(1)].append(ordinal)
        if match.group(1) not in latest or ordinal > latest[match.group(1)][0]:
            latest[match.group(1)] = (ordinal, item)
    for base, path in lock_bases.items():
        values = sorted(ordinals[base])
        if (values != list(range(1, values[-1] + 1))) if values else True:
            errors.append(f"{base}: policy versions must start at 1 and remain contiguous")
        current = latest.get(base)
        if current is None or current[1].get("semantic_sha256") != semantic_sha256(data[path]):
            errors.append(f"{path}: semantic digest changed without reviewed policy version")
    return errors


def trusted_lock_from_head(path: str) -> dict[str, Any] | None:
    result = subprocess.run(
        ["git", "show", f"HEAD:{path}"], cwd=ROOT,
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

    claudeadapter_dir = ROOT / "internal" / "claudeadapter"
    core_source_paths = sorted(p for p in claudeadapter_dir.glob("*.go") if not p.name.endswith("_test.go"))
    if not core_source_paths:
        errors.append("internal/claudeadapter core source is missing")
        return errors
    core_source = "\n".join(p.read_text(encoding="utf-8") for p in core_source_paths)

    required_types = [
        "type Adapter struct", "func New()", "func (a *Adapter) Manifest()", "func (a *Adapter) Discover(",
        "AdapterID = ", "type Surface string",
        "type HookEvent", "func DecodeHookInput(", "func BuildHookOutput(", "func ValidateHookOutputAllowlist(",
        "func HookRoutePath(", "func CanonicalEventForOTel(", "func DocumentedOTelEvents(",
        "func OTLPSafeAttributes(", "func DroppedOTelSurfaces(",
        "func BuildInventorySnapshot(", "func ResolveSkillEvidence(", "func SourceToCanonicalTable(",
        "func AllReconciliationLanes(", "func ReconcileLane(", "func ReconcileSession(",
    ]
    for required in required_types:
        if required not in core_source:
            errors.append(f"internal/claudeadapter core missing required declaration: {required}")

    if "kansoku.local/kansoku/internal/adaptersdk" not in core_source:
        errors.append("internal/claudeadapter must build on internal/adaptersdk.Adapter, not a parallel adapter mechanism")
    var_assert_paths = [p for p in core_source_paths if "var _ adaptersdk.Adapter" in p.read_text(encoding="utf-8")]
    if not var_assert_paths:
        errors.append("internal/claudeadapter must statically assert *Adapter implements adaptersdk.Adapter")

    if "kansoku.local/kansoku/internal/privacy" not in core_source:
        errors.append("internal/claudeadapter must reuse internal/privacy's sanitizer/feature-extraction machinery, not invent a second one")
    if re.search(r"type\s+Safe(Record|Error)\s+struct", core_source):
        errors.append("internal/claudeadapter must not declare a second SafeRecord/SafeError sanitizer type")
    if re.search(r"func\s+extractPromptFeatures\s*\(", core_source):
        errors.append("internal/claudeadapter must reuse internal/privacy.ExtractPromptFeatures, not redeclare a second prompt-feature extractor")

    if "internal/installer" not in core_source or "BuildClaudePlan" not in core_source:
        errors.append("internal/claudeadapter must reuse internal/installer.BuildClaudePlan verbatim for the claude.user_otel install target")

    # Discovery must never speculatively scan the whole home directory, and
    # must resolve documented settings locations first (never a CLAUDE_HOME
    # env var, which Claude Code does not document). Doc comments are allowed
    # to name CLAUDE_HOME when explaining that no such variable is consulted;
    # what must never appear is an actual reference to it as an environment
    # variable (a quoted string literal, e.g. passed to os.Getenv/LookupEnv).
    if '"CLAUDE_HOME"' in core_source:
        errors.append("internal/claudeadapter must never reference an undocumented CLAUDE_HOME env var as a string literal")
    if "ConfigRootUserSettings" not in core_source or "ConfigRootProjectSettings" not in core_source or "ConfigRootManagedSettings" not in core_source:
        errors.append("internal/claudeadapter must resolve the three documented Claude settings roots")

    # Hook allowlist must never include a raw prompt or path field.
    hook_path = claudeadapter_dir / "hook.go"
    hook_source = hook_path.read_text(encoding="utf-8") if hook_path.exists() else ""
    if not hook_source:
        errors.append("internal/claudeadapter/hook.go is missing")
    else:
        if "AllowlistedHookFields" not in hook_source:
            errors.append("hook.go missing AllowlistedHookFields")
        if "never" not in hook_source.lower():
            errors.append("hook.go must document that the raw prompt/path is never copied/persisted")
        if "HookRoutePath" not in hook_source or "/v1/hooks/claude/" not in hook_source:
            errors.append("hook.go must build routes through the reused generic /v1/hooks/claude/{event} template")
        if "pseudonymizePath" not in hook_source and "pseudonymize" not in hook_source.lower():
            errors.append("hook.go must pseudonymize transcript_path/cwd rather than forwarding them raw")

    # OTel mapping must document the unconditional strip of detailed
    # telemetry content regardless of upstream settings.
    otel_path = claudeadapter_dir / "otel.go"
    otel_source = otel_path.read_text(encoding="utf-8") if otel_path.exists() else ""
    if not otel_source:
        errors.append("internal/claudeadapter/otel.go is missing")
    else:
        if "DroppedOTelSurfaces" not in otel_source:
            errors.append("otel.go missing DroppedOTelSurfaces")

    # Evidence resolution must forbid ambiguous ownership promotion and
    # never let semantic_opportunity_classifier resolve to anything but
    # inferred.
    evidence_path = claudeadapter_dir / "evidence.go"
    evidence_source = evidence_path.read_text(encoding="utf-8") if evidence_path.exists() else ""
    if not evidence_source:
        errors.append("internal/claudeadapter/evidence.go is missing")
    else:
        if "ErrAmbiguousOwnershipPromotion" not in evidence_source:
            errors.append("evidence.go must expose ErrAmbiguousOwnershipPromotion so ambiguous helper/MCP ownership can never be silently promoted")
        if "SemanticOpportunity" not in evidence_source or "TierInferred" not in evidence_source:
            errors.append("evidence.go must keep semantic_opportunity_classifier bound to the inferred tier")

    # Reconciliation must degrade only the missing source, never fabricate
    # a whole-session zero.
    reconcile_path = claudeadapter_dir / "reconcile.go"
    reconcile_source = reconcile_path.read_text(encoding="utf-8") if reconcile_path.exists() else ""
    if not reconcile_source:
        errors.append("internal/claudeadapter/reconcile.go is missing")
    else:
        if "AllReconciliationLanes" not in reconcile_source or "ReconcileSession" not in reconcile_source:
            errors.append("reconcile.go must expose the closed reconciliation lane set and a per-session reconciler")

    for path in core_source_paths:
        text = path.read_text(encoding="utf-8")
        for forbidden in ('exec.Command("sh", "-c"', 'exec.Command("bash", "-c"', "eval(", 'os.ReadFile(os.Getenv("HOME")'):
            if forbidden in text:
                errors.append(f"{path.relative_to(ROOT)} contains a forbidden pattern: {forbidden}")

    # -- internal/observability/routes.go must route Claude hooks through the
    # existing generic mux, not a parallel ingress mechanism, and must never
    # collide with the reserved fixture-agent case.
    routes_path = ROOT / "internal" / "observability" / "routes.go"
    routes_source = routes_path.read_text(encoding="utf-8") if routes_path.exists() else ""
    if not routes_source:
        errors.append("internal/observability/routes.go is missing")
    else:
        if "/v1/hooks/{adapter}/{event}" not in routes_source:
            errors.append("internal/observability/routes.go must keep the single generic hook_http route template")
        if "claudeadapter.AdapterID" not in routes_source:
            errors.append("internal/observability/routes.go must dispatch Claude hook events by claudeadapter.AdapterID through the generic mux, not a second HTTP server")
        if "codexadapter.AdapterID" not in routes_source:
            errors.append("internal/observability/routes.go must keep dispatching Codex hook events by codexadapter.AdapterID (Session 06 case must not be removed)")
        if '"fixture-agent"' not in routes_source:
            errors.append('internal/observability/routes.go must keep the reserved "fixture-agent" case (Session 03 conformance identity) untouched')
        if routes_source.count("http.NewServeMux()") != 1:
            errors.append("internal/observability/routes.go must not stand up a second HTTP mux for Claude")

    # -- internal/adaptersdk core must have zero new agent-name branch. --
    adaptersdk_core_paths = sorted(
        p for p in (ROOT / "internal" / "adaptersdk").glob("*.go")
        if not p.name.endswith("_test.go")
    )
    for path in adaptersdk_core_paths:
        text = path.read_text(encoding="utf-8")
        for forbidden in ('"claude"', '"wayfinder"', "AdapterID ==", "agentID ==", "adapterID =="):
            if forbidden in text:
                errors.append(f"internal/adaptersdk core file {path.relative_to(ROOT)} appears to contain an agent-name branch: {forbidden!r}")

    # -- internal/adaptersdk/wayfinder (second fixture-agent). --
    wayfinder_dir = ROOT / "internal" / "adaptersdk" / "wayfinder"
    wayfinder_path = wayfinder_dir / "wayfinder.go"
    if not wayfinder_path.is_file():
        errors.append("internal/adaptersdk/wayfinder/wayfinder.go is missing")
    else:
        wayfinder_source = wayfinder_path.read_text(encoding="utf-8")
        for required in ("AdapterID", "func New()", "func (a *Adapter) Manifest()", "func (a *Adapter) Discover(", "func (a *Adapter) Normalize(", "ErrUnknownEventSchema"):
            if required not in wayfinder_source:
                errors.append(f"internal/adaptersdk/wayfinder missing required declaration: {required}")
        if 'AdapterID      = "wayfinder"' not in wayfinder_source and 'AdapterID = "wayfinder"' not in wayfinder_source:
            errors.append("internal/adaptersdk/wayfinder AdapterID must remain wayfinder")
        if "var _ adaptersdk.Adapter" not in wayfinder_source:
            errors.append("internal/adaptersdk/wayfinder must statically assert *Adapter implements adaptersdk.Adapter")

    # -- internal/crossagent (Codex+Claude cross-agent invariant test). --
    crossagent_dir = ROOT / "internal" / "crossagent"
    if not crossagent_dir.is_dir() or not any(crossagent_dir.glob("*_test.go")):
        errors.append("internal/crossagent test package is missing")
    else:
        crossagent_source = "\n".join(p.read_text(encoding="utf-8") for p in crossagent_dir.glob("*.go"))
        if "codexadapter" not in crossagent_source or "claudeadapter" not in crossagent_source:
            errors.append("internal/crossagent must exercise both codexadapter and claudeadapter")

    # -- fixtures: synthetic, sanitized, and no raw prohibited-content leak.
    wayfinder_fixture_path = FIXTURES_DIR / "wayfinder-eventfile.json"
    if not wayfinder_fixture_path.is_file():
        errors.append("tests/fixtures/session-07/wayfinder-eventfile.json is missing")
    else:
        wayfinder_fixture = load(wayfinder_fixture_path)
        if wayfinder_fixture.get("adapter_id") != "wayfinder":
            errors.append("wayfinder-eventfile.json adapter_id must remain wayfinder")
        if wayfinder_fixture.get("unknown_schema_event_type") != "recipe.mystery":
            errors.append("wayfinder-eventfile.json must declare recipe.mystery as its one unknown-schema event type")

    scenario_fixture_path = FIXTURES_DIR / "cross-agent-invariant-scenario.json"
    if not scenario_fixture_path.is_file():
        errors.append("tests/fixtures/session-07/cross-agent-invariant-scenario.json is missing")
    else:
        scenario_fixture = load(scenario_fixture_path)
        if set(scenario_fixture.get("codex", {})) == set() or set(scenario_fixture.get("claude", {})) == set():
            errors.append("cross-agent-invariant-scenario.json must declare both a codex and a claude fixture branch")
        stage_mapping = scenario_fixture.get("scenario_stage_to_capability_mapping", [])
        if len(stage_mapping) != 6:
            errors.append("cross-agent-invariant-scenario.json stage mapping row count changed")

    for path in sorted(FIXTURES_DIR.rglob("*.json")):
        text = path.read_text(encoding="utf-8")
        if "/Users/" in text or str(ROOT) in text:
            errors.append(f"{path.relative_to(ROOT)} references a real machine path; fixtures must stay synthetic")

    return errors


def run_go_suite() -> dict[str, Any]:
    """Run only the Session 07 Go packages (internal/claudeadapter,
    internal/adaptersdk/wayfinder, internal/crossagent, plus
    internal/observability for the routes.go wiring) inside the exact
    pinned, offline, network-disabled Go image scripts/run_go_tests.py
    already uses. This is a narrower re-run for standalone use of this
    validator; scripts/run_go_tests.py's full ./... sweep remains the
    authoritative cross-session Go proof."""
    import os

    command = [
        "docker", "run", "--rm", "--network", "none", "--read-only", "--cap-drop", "ALL",
        "--security-opt", "no-new-privileges", "--user", f"{os.getuid()}:{os.getgid()}",
        "--tmpfs", "/tmp:rw,exec,nosuid,nodev,mode=1777",
        "--mount", f"type=bind,src={ROOT},dst=/src,readonly", "--workdir", "/src",
        "--env", "GOCACHE=/tmp/go-cache", "--env", "GOTMPDIR=/tmp/go-tmp", "--env", "HOME=/tmp/home",
        GO_IMAGE, "sh", "-c",
        "mkdir -p /tmp/go-cache /tmp/go-tmp /tmp/home && "
        "/usr/local/go/bin/go build -mod=vendor ./internal/claudeadapter/... ./internal/adaptersdk/... ./internal/crossagent/... ./internal/observability/... && "
        "/usr/local/go/bin/go vet -mod=vendor ./internal/claudeadapter/... ./internal/adaptersdk/... ./internal/crossagent/... && "
        "/usr/local/go/bin/go test -mod=vendor -v -count=1 ./internal/claudeadapter/... ./internal/adaptersdk/... ./internal/crossagent/...",
    ]
    result = subprocess.run(command, cwd=ROOT, check=False, capture_output=True, text=True)
    return {
        "status": "pass" if result.returncode == 0 else "fail",
        "stdout_tail": "\n".join(result.stdout.splitlines()[-160:]),
        "stderr_tail": "\n".join(result.stderr.splitlines()[-40:]),
        "returncode": result.returncode,
    }


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--json", action="store_true")
    parser.add_argument("--with-go", action="store_true", help="also shell out to build/vet/test the Session 07 packages in the pinned offline Go image")
    args = parser.parse_args()

    errors: list[str] = []
    go_result: dict[str, Any] | None = None
    try:
        errors = validate(
            historical=trusted_lock_from_head("contracts/claude-policy-locks.yaml"),
            cross_historical=trusted_lock_from_head("contracts/cross-agent-policy-locks.yaml"),
        )
        if args.with_go:
            go_result = run_go_suite()
            if go_result["status"] != "pass":
                errors.append("Session 07 Go build/vet/test failed inside the pinned offline Go image")
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
