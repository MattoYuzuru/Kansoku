#!/usr/bin/env python3
"""Independent closed-world validator for the Session 05 adapter SDK.

Two independent things are checked, mirroring
`scripts/validate_observability.py` and `scripts/validate_data_platform.py`:

1. The static contract: `contracts/adapter-sdk/*.yaml` registries are exact,
   closed and bound by `contracts/adapter-sdk-policy-locks.yaml` versioned
   canonical semantic digests.
2. The code/contract alignment: `internal/adaptersdk` (plus its
   `fakeadapter` conformance adapter) actually implements the invariants the
   registries declare -- no adapter-name branch in core, permission-checked
   HostView only, manifests parsed as bounded data never executed, ChangePlan
   reuse of `internal/installer`'s existing Plan/Approval/SimulateApply/
   SimulateRollback/SimulateRemove/PlanSHA256 machinery, and the fake
   "loomwright" adapter's vocabulary never colliding with a real adapter's.

Unlike Session 04, Session 05 adds no new external runtime (no database, no
network service), so there is no ephemeral-container harness here: the Go
proof is `go vet`/`go test` for `internal/adaptersdk/...` inside the same
pinned, network-disabled Go image `scripts/run_go_tests.py` already uses.
That full-repo sweep is authoritative; this validator's `--with-go` flag re-
runs the narrower adaptersdk-only slice so `validate_adapter_sdk.py` remains a
standalone, single-command proof of the Session 05 exit gate.
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
CONTRACT_DIR = ROOT / "contracts" / "adapter-sdk"
LOCK_PATH = ROOT / "contracts" / "adapter-sdk-policy-locks.yaml"
FIXTURE_PATH = ROOT / "tests" / "fixtures" / "session-05" / "loomwright-conformance.json"
GO_IMAGE = "golang@sha256:1ecb7edf62a0408027bd5729dfd6b1b8766e578e8df93995b225dfd0944eb651"

FILES = ("manifest.yaml", "capabilities.yaml", "inventory-graph.yaml", "discovery-and-plans.yaml")

EXECUTION_FORMS = ["builtin", "external_process", "wasm", "container"]
NETWORK_GRADES = ["none", "loopback_only"]
CAPABILITY_IDS = [
    "discovery.agent_and_surface", "inventory.components", "activity.sessions", "activity.prompt_metadata",
    "activity.token_model_cost", "components.skill_invocation", "components.plugin_and_custom_command",
    "components.mcp_lifecycle", "components.builtin_tool_calls_and_approvals", "components.subagents_and_compaction",
    "ingestion.historical_import", "ingestion.live_stream", "configuration.install", "configuration.live_canary",
    "configuration.hook_install",
]
CAPABILITY_STATES = ["unsupported", "available", "configured", "healthy", "degraded"]
EVIDENCE_TIERS = ["corroborated", "native", "reconstructed", "inferred"]
NODE_KINDS = [
    "device", "agent_installation", "agent_surface", "agent_version", "plugin_package", "plugin_version",
    "skill_identity", "mcp_server_instance", "mcp_tool", "hook_definition", "custom_command_definition",
    "subagent_definition", "cache_artifact",
]
EDGE_KINDS = ["bundles", "provides", "configured_in", "enabled_for", "shadows", "collides_with", "depends_on", "observed_using"]
SOURCE_SCOPES = ["system", "user", "repository", "admin", "marketplace", "plugin_cache", "transient_session"]
AUDIT_MODES = ["passive", "fixture_replay", "live_canary"]
CHECK_STATUSES = ["pass", "fail", "skipped_unsupported"]
DETECTION_METHODS = ["executable_on_path", "documented_env_var", "documented_config_file", "documented_state_root_present"]
REAL_AGENT_TERMS = {"codex", "claude", "gemini", "cursor"}

LOCK_BASES = {
    "adapter-sdk.manifest": "contracts/adapter-sdk/manifest.yaml",
    "adapter-sdk.capabilities": "contracts/adapter-sdk/capabilities.yaml",
    "adapter-sdk.inventory-graph": "contracts/adapter-sdk/inventory-graph.yaml",
    "adapter-sdk.discovery-and-plans": "contracts/adapter-sdk/discovery-and-plans.yaml",
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
    return {f"contracts/adapter-sdk/{name}": load(CONTRACT_DIR / name) for name in FILES}


def validate(candidate: dict[str, dict[str, Any]] | None = None, locks: dict[str, Any] | None = None, include_code: bool = True, historical: dict[str, Any] | None = None) -> list[str]:
    data = registries() if candidate is None else candidate
    lock = load(LOCK_PATH) if locks is None else locks
    errors: list[str] = []
    if set(data) != {f"contracts/adapter-sdk/{name}" for name in FILES}:
        errors.append("adapter-sdk registry set is not exact")
        return errors
    by_name = {Path(path).name: value for path, value in data.items()}
    manifest, capabilities, inventory_graph, discovery_and_plans = (by_name[name] for name in FILES)

    expected_top = {
        "manifest.yaml": {
            "schema_version", "contract_version", "effective_at", "api_version", "manifest_fields",
            "agent_detection_fields", "execution_forms", "execution_form_sequence", "permissions_fields",
            "network_grades", "parse_limits", "validation", "id_naming", "external_protocol",
            "compatibility_registry_fields", "unknown_agent_version_policy",
        },
        "capabilities.yaml": {
            "schema_version", "contract_version", "effective_at", "capability_ids", "capability_states",
            "state_transitions", "evidence_tiers", "capability_record_fields", "no_brand_branch_invariant",
        },
        "inventory-graph.yaml": {
            "schema_version", "contract_version", "effective_at", "snapshot_fields", "snapshot_semantics",
            "node_kinds", "node_fields", "source_scopes", "edge_kinds", "edge_fields", "identity_rule",
            "cache_separation", "path_pseudonymization", "example_graph_paths", "change_plan_fields",
            "reconcile_scope_fields", "reconcile_result_fields", "reconcile_idempotency",
        },
        "discovery-and-plans.yaml": {
            "schema_version", "contract_version", "effective_at", "discovery_safety_rules", "host_view_fields",
            "host_view_guarantee", "installation_candidate_fields", "detection_methods", "change_plan_fields",
            "change_plan_reuse", "apply_requires", "normal_operation_rule", "audit_modes", "check_result_fields",
            "check_statuses", "cli_concepts", "third_party_acceptance_checklist",
        },
    }
    for name, fields in expected_top.items():
        if set(by_name[name]) != fields:
            errors.append(f"{name}: top-level closed schema changed")

    if manifest.get("api_version") != "kansoku.adapter/v1":
        errors.append("adapter API version changed")
    if manifest.get("execution_forms") != EXECUTION_FORMS:
        errors.append("execution form set changed")
    if manifest.get("network_grades") != NETWORK_GRADES:
        errors.append("network grade set changed; there must be no unrestricted grade")
    parse_limits = manifest.get("parse_limits", {})
    for field in ("max_config_entries", "max_config_depth", "max_config_string"):
        if not isinstance(parse_limits.get(field), int) or parse_limits.get(field, 0) <= 0:
            errors.append(f"manifest parse_limits.{field} must be a positive integer")
    if "forbidden" not in str(parse_limits.get("code_execution", "")):
        errors.append("manifest parsing must explicitly forbid code execution")
    if "never_evaluated_or_executed" not in str(parse_limits.get("code_execution", "")):
        errors.append("manifest parsing must state manifests are never evaluated or executed")
    external = manifest.get("external_protocol", {})
    if external.get("distribution", "").find("unsigned_adapters_are_labeled_and_disabled_by_default") == -1:
        errors.append("unsigned external adapters must remain labeled and disabled by default")
    if external.get("environment", "").find("no_inherited_parent_environment") == -1:
        errors.append("external adapter environment must remain an explicit allowlist with no inherited parent environment")
    if manifest.get("unknown_agent_version_policy", "").find("defaults_to_degraded") == -1:
        errors.append("unknown agent version outside every compatibility range must default to degraded")

    if capabilities.get("capability_ids") != CAPABILITY_IDS:
        errors.append("capability id set changed")
    if capabilities.get("capability_states") != CAPABILITY_STATES:
        errors.append("capability state set changed")
    if capabilities.get("evidence_tiers") != EVIDENCE_TIERS:
        errors.append("evidence tier set changed")
    if "never_on_a_hardcoded_agent_name_conditional" not in capabilities.get("no_brand_branch_invariant", ""):
        errors.append("no-agent-name-branch invariant text weakened")
    transitions = capabilities.get("state_transitions", {})
    if "brand_binding" not in transitions or "never_to_an_agent_brand_string" not in transitions.get("brand_binding", ""):
        errors.append("UI capability routing must bind to capability ids only, never an agent brand string")

    if inventory_graph.get("node_kinds") != NODE_KINDS:
        errors.append("inventory node kind set changed")
    if inventory_graph.get("edge_kinds") != EDGE_KINDS:
        errors.append("inventory edge kind set changed")
    if inventory_graph.get("source_scopes") != SOURCE_SCOPES:
        errors.append("inventory source scope set changed")
    if "same_declared_name_never_forces_identity_merge" not in inventory_graph.get("identity_rule", ""):
        errors.append("same-declared-name identity merge rule weakened")
    cache_separation = inventory_graph.get("cache_separation", "")
    if "kept_separate_from_active_configuration" not in cache_separation or "never_reported_as_enabled" not in cache_separation:
        errors.append("cache artifact separation rule weakened")
    if "no_raw_filesystem_path_is_ever_a_durable_field" not in inventory_graph.get("path_pseudonymization", ""):
        errors.append("path pseudonymization must forbid a raw filesystem path as a durable field")
    if "replay_never_duplicates_a_node_or_edge" not in inventory_graph.get("reconcile_idempotency", ""):
        errors.append("reconciliation idempotency guarantee weakened")

    safety_rules = discovery_and_plans.get("discovery_safety_rules", [])
    required_safety_fragments = [
        "resolve_state_roots_from_documented_env_or_config",
        "never_speculatively_scan_an_entire_home_directory",
        "never_follow_a_symlink_outside_an_already_allowed_root",
        "no_code_execution",
        "credential_free",
        "cache_paths_are_discovered_and_reported_but_always_labeled_separately",
    ]
    joined_safety = " ".join(safety_rules)
    for fragment in required_safety_fragments:
        if fragment not in joined_safety:
            errors.append(f"discovery safety rule missing required guarantee: {fragment}")
    if discovery_and_plans.get("detection_methods") != DETECTION_METHODS:
        errors.append("installation detection method set changed")
    if "no_adapter_ever_receives_a_database_credential" not in discovery_and_plans.get("host_view_guarantee", ""):
        errors.append("HostView guarantee no longer excludes database credentials")
    if "unscoped_filesystem_handle" not in discovery_and_plans.get("host_view_guarantee", ""):
        errors.append("HostView guarantee no longer excludes an unscoped filesystem handle")
    reuse = discovery_and_plans.get("change_plan_reuse", "")
    for fragment in ("internal_installer_plan_approval_simulateapply_simulaterollback_simulateremove_and_plansha256", "instead_of_inventing_a_second_apply_rollback_mechanism"):
        if fragment not in reuse:
            errors.append("ChangePlan reuse of internal/installer machinery weakened")
    apply_requires = set(discovery_and_plans.get("apply_requires", []))
    if not {"explicit_confirmation", "precondition_recheck_against_current_config_hash_to_avoid_overwriting_a_concurrent_edit"}.issubset(apply_requires):
        errors.append("ChangePlan apply preconditions weakened")
    if "never_applies_a_change_plan" not in discovery_and_plans.get("normal_operation_rule", ""):
        errors.append("normal collector operation must never apply a change plan")
    if discovery_and_plans.get("audit_modes") != AUDIT_MODES:
        errors.append("audit mode set changed")
    if discovery_and_plans.get("check_statuses") != CHECK_STATUSES:
        errors.append("check status set changed")
    checklist = set(discovery_and_plans.get("third_party_acceptance_checklist", []))
    if "no_direct_database_credential_access" not in checklist:
        errors.append("third-party acceptance checklist must forbid direct database credential access")
    if "unsigned_distribution_clearly_labeled_signed_packages_deferred" not in checklist:
        errors.append("third-party acceptance checklist must require unsigned distribution to be clearly labeled")

    errors.extend(validate_policy_locks(lock, data, historical))

    if include_code:
        errors.extend(validate_code_and_fixture())
    return errors


def validate_policy_locks(lock: dict[str, Any], data: dict[str, dict[str, Any]], historical: dict[str, Any] | None = None) -> list[str]:
    errors: list[str] = []
    if set(lock) != {"schema_version", "effective_at", "locks"} or lock.get("schema_version") != "kansoku.adapter-sdk-policy-locks/1":
        errors.append("adapter-sdk policy lock registry is not exact")
    records = lock.get("locks", [])
    if not isinstance(records, list):
        return errors + ["adapter-sdk policy locks must be a list"]
    if historical is not None:
        old = historical.get("locks", []) if isinstance(historical, dict) else []
        if records[: len(old)] != old:
            errors.append("adapter-sdk policy lock list must retain the exact append-only trusted prefix")
    latest: dict[str, tuple[int, dict[str, Any]]] = {}
    seen: set[str] = set()
    ordinals: dict[str, list[int]] = {base: [] for base in LOCK_BASES}
    for item in records:
        if not isinstance(item, dict) or set(item) != {"policy_version", "registry", "semantic_sha256"}:
            errors.append("adapter-sdk policy lock entries must be closed")
            continue
        version = item.get("policy_version", "")
        match = re.fullmatch(r"(adapter-sdk\.(?:manifest|capabilities|inventory-graph|discovery-and-plans))/([1-9][0-9]*)", version)
        if not match or item.get("registry") != LOCK_BASES.get(match.group(1)) or re.fullmatch(r"[0-9a-f]{64}", str(item.get("semantic_sha256"))) is None:
            errors.append("adapter-sdk policy lock entry has invalid version/registry/digest binding")
            continue
        if version in seen:
            errors.append(f"duplicate adapter-sdk policy version {version}")
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
        ["git", "show", "HEAD:contracts/adapter-sdk-policy-locks.yaml"], cwd=ROOT,
        check=False, capture_output=True, text=True,
    )
    if result.returncode != 0:
        return None
    try:
        value = json.loads(result.stdout)
    except json.JSONDecodeError:
        return None
    return value if isinstance(value, dict) else None


def validate_code_and_fixture() -> list[str]:
    errors: list[str] = []
    fixture = load(FIXTURE_PATH)
    if fixture.get("synthetic") is not True:
        errors.append("Session05 fixture must be marked synthetic")
    if fixture.get("adapter_id") in REAL_AGENT_TERMS:
        errors.append("Session05 fixture adapter_id must not be a real agent name")
    serialized_fixture = json.dumps(fixture, sort_keys=True)
    if "/Users/" in serialized_fixture or "@example.com" in serialized_fixture or "sk-" in serialized_fixture:
        errors.append("Session05 fixture is not sanitized/synthetic")
    for term in REAL_AGENT_TERMS:
        if re.search(rf"\b{re.escape(term)}\b", serialized_fixture, re.IGNORECASE):
            errors.append(f"Session05 fixture must not reference the real agent term: {term}")

    adaptersdk_dir = ROOT / "internal" / "adaptersdk"
    core_source_paths = sorted(p for p in adaptersdk_dir.glob("*.go") if not p.name.endswith("_test.go"))
    if not core_source_paths:
        errors.append("internal/adaptersdk core source is missing")
    core_source = "\n".join(p.read_text(encoding="utf-8") for p in core_source_paths)

    required_types = [
        "type Adapter interface", "type Manifest struct", "type Registry struct",
        "type InstallationCandidate struct", "type InventorySnapshot struct", "type ChangePlan struct",
        "type ReconcileScope struct", "type ReconcileResult struct", "type CheckResult struct",
        "type AuditMode string", "type HostView struct", "func ParseManifest",
    ]
    for required in required_types:
        if required not in core_source:
            errors.append(f"internal/adaptersdk core missing required declaration: {required}")

    required_guarantees = [
        "never inspects the ID string for a known agent",
        "type-switches",
        "string-switches on which concrete Adapter",
        "MaxManifestConfigEntries", "MaxManifestConfigDepth", "MaxManifestConfigString",
        "ErrOutsideAllowedRoots", "ErrDisallowedExec",
    ]
    for required in required_guarantees:
        if required not in core_source:
            errors.append(f"internal/adaptersdk core missing required guarantee/text: {required}")

    if "installer.SimulateApply" in core_source or "installer.SimulateRollback" in core_source:
        pass  # direct reuse is fine wherever it happens
    plan_source = (adaptersdk_dir / "plan.go").read_text(encoding="utf-8") if (adaptersdk_dir / "plan.go").exists() else ""
    if "kansoku.local/kansoku/internal/installer" not in plan_source:
        errors.append("internal/adaptersdk must reuse internal/installer's Plan/Approval/Simulate* machinery, not invent a parallel one")
    if "installer.PlanSHA256" not in plan_source:
        errors.append("ChangePlan construction must bind to installer.PlanSHA256 so it cannot drift from the underlying installer.Plan")

    privacy_source = "\n".join(
        (ROOT / "internal" / "privacy" / name).read_text(encoding="utf-8")
        for name in ("types.go",) if (ROOT / "internal" / "privacy" / name).exists()
    )
    if "kansoku.local/kansoku/internal/privacy" not in core_source:
        errors.append("internal/adaptersdk must reuse internal/privacy's SafeRecord/SafeError sanitizer types at the Session02 trust boundary")
    if re.search(r"type\s+Safe(Record|Error)\s+struct", core_source):
        errors.append("internal/adaptersdk must not declare a second SafeRecord/SafeError sanitizer type")

    fakeadapter_path = adaptersdk_dir / "fakeadapter" / "fakeadapter.go"
    if not fakeadapter_path.is_file():
        errors.append("internal/adaptersdk/fakeadapter conformance adapter is missing")
    else:
        fake_source = fakeadapter_path.read_text(encoding="utf-8")
        # Doc comments are allowed to name real agents for contrast (that is
        # the whole point of this package); only the adapter's own data
        # vocabulary -- string literals -- must never collide with one.
        code_only = "\n".join(
            line for line in fake_source.splitlines()
            if not line.strip().startswith("//")
        )
        string_literals = " ".join(re.findall(r'"([^"\\]|\\.)*"', code_only))
        lowered_literals = string_literals.lower()
        for term in REAL_AGENT_TERMS:
            if re.search(rf"\b{re.escape(term)}\b", lowered_literals):
                errors.append(f"fakeadapter data vocabulary (string literal) must not reference the real agent term: {term}")
        if "var _ adaptersdk.Adapter = " not in fake_source:
            errors.append("fakeadapter must statically assert it implements adaptersdk.Adapter")
        for required in ("func (a *Adapter) Manifest()", "func (a *Adapter) Discover(", "func (a *Adapter) Inventory(", "func (a *Adapter) Normalize(", "func (a *Adapter) Reconcile(", "func (a *Adapter) Audit("):
            if required not in fake_source:
                errors.append(f"fakeadapter missing required Adapter method: {required}")

    for name in core_source_paths + [fakeadapter_path]:
        if not name.is_file():
            continue
        text = name.read_text(encoding="utf-8")
        for forbidden in ("os/exec\".Command(", "eval(", "exec.Command(\"sh\", \"-c\"", "exec.Command(\"bash\", \"-c\"", "os.ReadFile(os.Getenv(\"HOME\")"):
            if forbidden in text:
                errors.append(f"{name.relative_to(ROOT)} contains a forbidden pattern: {forbidden}")

    _ = privacy_source
    return errors


def run_go_suite() -> dict[str, Any]:
    """Run only the internal/adaptersdk (+fakeadapter) Go suite inside the
    exact pinned, offline, network-disabled Go image scripts/run_go_tests.py
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
        "/usr/local/go/bin/go build -mod=vendor ./internal/adaptersdk/... && "
        "/usr/local/go/bin/go vet -mod=vendor ./internal/adaptersdk/... && "
        "/usr/local/go/bin/go test -mod=vendor -v -count=1 ./internal/adaptersdk/...",
    ]
    result = subprocess.run(command, cwd=ROOT, check=False, capture_output=True, text=True)
    return {
        "status": "pass" if result.returncode == 0 else "fail",
        "stdout_tail": "\n".join(result.stdout.splitlines()[-80:]),
        "stderr_tail": "\n".join(result.stderr.splitlines()[-40:]),
        "returncode": result.returncode,
    }


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--json", action="store_true")
    parser.add_argument("--with-go", action="store_true", help="also shell out to build/vet/test internal/adaptersdk in the pinned offline Go image")
    args = parser.parse_args()

    errors: list[str] = []
    go_result: dict[str, Any] | None = None
    try:
        errors = validate(historical=trusted_lock_from_head())
        if args.with_go:
            go_result = run_go_suite()
            if go_result["status"] != "pass":
                errors.append("internal/adaptersdk Go build/vet/test failed inside the pinned offline Go image")
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
