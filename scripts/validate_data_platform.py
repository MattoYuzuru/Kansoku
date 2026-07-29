#!/usr/bin/env python3
"""Independent closed-world validator for the Session 04 data platform.

Two independent things are checked:

1. The static contract: `contracts/data-platform/*.yaml` registries are exact,
   closed and bound by `contracts/data-platform-policy-locks.yaml` versioned
   semantic digests, exactly like `scripts/validate_observability.py`.
2. The runtime proof: an ephemeral, deterministic PostgreSQL instance is
   started from the exact digest pinned in both
   `deploy/compose.security-baseline.yaml` and
   `contracts/data-platform/schema.yaml.engine.image_digest`, the
   `postgres_integration`-tagged Go suite in `internal/dataplatform` is run
   against it (replay/reconciliation, late-data repair, query budgets,
   retention/partition-drop, backup/restore), and the container plus its
   isolated network are torn down afterward regardless of outcome.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import re
import subprocess
import sys
import time
import uuid
from pathlib import Path
from typing import Any


ROOT = Path(__file__).resolve().parents[1]
CONTRACT_DIR = ROOT / "contracts" / "data-platform"
LOCK_PATH = ROOT / "contracts" / "data-platform-policy-locks.yaml"
FIXTURE_PATH = ROOT / "tests" / "fixtures" / "session-04" / "replay-scenario.json"
COMPOSE_PATH = ROOT / "deploy" / "compose.security-baseline.yaml"

FILES = ("schema.yaml", "rollups.yaml", "query-contract.yaml", "retention.yaml")
GRANULARITIES = ["hourly", "daily"]
PERCENTILE_LEVELS = [0.5, 0.9, 0.95, 0.99]
BUDGET_IDS = {
    "hourly_rollup_range_30d", "daily_rollup_range_1y", "session_drilldown", "percentile_recompute_bucket",
    "agent_breakdown_range", "agent_profile_range", "model_breakdown_range", "component_breakdown_range",
    "component_lifecycle_funnel", "component_inventory_current",
    "skill_observatory_range", "skill_profile_range",
    "plugin_observatory_range", "plugin_profile_range",
    "reliability_coverage_timeline", "mcp_topology", "mcp_observatory_range",
    "mcp_server_profile_range", "incident_list",
    "incident_detail", "incident_occurrences", "quarantine_list", "quarantine_detail",
    "incident_debug_bundle",
}
COMPLETENESS_STATUSES = ["complete", "partial", "degraded", "unknown"]
RESPONSE_FIELDS = ["data", "formula_version", "population", "completeness", "freshness"]
FORBIDDEN_JSONB_KEYS = {"prompt", "response", "body", "content", "source_code", "tool_input", "tool_output", "command", "path", "environment", "credential", "exception", "attributes", "payload", "error_message"}
GO_IMAGE = "golang@sha256:1ecb7edf62a0408027bd5729dfd6b1b8766e578e8df93995b225dfd0944eb651"
POSTGRES_IMAGE = "postgres@sha256:9a8afca54e7861fd90fab5fdf4c42477a6b1cb7d293595148e674e0a3181de15"
LOCK_BASES = {
    "data-platform.schema": "contracts/data-platform/schema.yaml",
    "data-platform.rollups": "contracts/data-platform/rollups.yaml",
    "data-platform.query-contract": "contracts/data-platform/query-contract.yaml",
    "data-platform.retention": "contracts/data-platform/retention.yaml",
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
    return {f"contracts/data-platform/{name}": load(CONTRACT_DIR / name) for name in FILES}


def validate(candidate: dict[str, dict[str, Any]] | None = None, locks: dict[str, Any] | None = None, include_runtime: bool = True, historical: dict[str, Any] | None = None) -> list[str]:
    data = registries() if candidate is None else candidate
    lock = load(LOCK_PATH) if locks is None else locks
    errors: list[str] = []
    if set(data) != {f"contracts/data-platform/{name}" for name in FILES}:
        errors.append("data-platform registry set is not exact")
        return errors
    by_name = {Path(path).name: value for path, value in data.items()}
    schema, rollups, query_contract, retention = (by_name[name] for name in FILES)

    expected_top = {
        "schema.yaml": {"schema_version", "contract_version", "effective_at", "engine", "migration_policy", "tables", "partitioning", "indexing", "constraints", "jsonb_extension_fields"},
        "rollups.yaml": {"schema_version", "contract_version", "effective_at", "granularities", "bucket_rule", "dimension_scope_fields", "rollup_row_fields", "percentile_policy", "late_data_algorithm", "time_ranges", "formula_registry"},
        "query-contract.yaml": {"schema_version", "contract_version", "effective_at", "response_fields", "population_fields", "completeness_fields", "completeness_statuses", "freshness_fields", "budgets", "half_open_boundary", "unknown_denominator_policy"},
        "retention.yaml": {"schema_version", "contract_version", "effective_at", "event_expiration", "rollup_retention", "backup"},
    }
    for name, fields in expected_top.items():
        if set(by_name[name]) != fields:
            errors.append(f"{name}: top-level closed schema changed")

    engine = schema.get("engine", {})
    if engine.get("product") != "PostgreSQL" or engine.get("major_version") != 18:
        errors.append("engine must remain PostgreSQL 18 per ADR 0001; do not re-litigate the baseline")
    if not re.fullmatch(r"sha256:[0-9a-f]{64}", str(engine.get("image_digest", ""))):
        errors.append("engine image_digest must be an exact pinned sha256 digest")
    if schema.get("partitioning", {}).get("strategy") != "range_partition_monthly_by_observed_at":
        errors.append("partitioning strategy changed")
    if schema.get("partitioning", {}).get("drop_policy") != "retention_worker_drops_whole_partitions_only_never_row_by_row_delete_for_partitioned_fact_tables":
        errors.append("partition drop policy weakened to allow row-by-row delete")
    if schema.get("constraints", {}).get("eav_policy", "").find("no generic entity-attribute-value table") != 0:
        errors.append("EAV avoidance policy changed")
    current_inventory = str(schema.get("constraints", {}).get("current_inventory_projection", ""))
    if not all(fragment in current_inventory for fragment in (
        "replaceable_current_projection",
        "complete_snapshot_not_older_than_current_state",
        "older_replay_and_partial_degraded_unknown_snapshots_preserve",
        "immutable_inventory_snapshots_components_and_historical_assertions_are_never_deleted_or_rewritten",
    )):
        errors.append("current inventory projection replacement/history semantics changed")
    jsonb = schema.get("jsonb_extension_fields", {})
    if not FORBIDDEN_JSONB_KEYS.issubset(set(jsonb.get("forbidden_keys", []))):
        errors.append("JSONB extension forbidden keys no longer cover the Session02 prohibited-content set")
    if not isinstance(jsonb.get("max_bytes"), int) or jsonb.get("max_bytes", 0) <= 0:
        errors.append("JSONB extension fields must have a positive byte ceiling")

    if set(rollups.get("granularities", [])) != set(GRANULARITIES):
        errors.append("rollup granularities changed")
    percentile_policy = rollups.get("percentile_policy", {})
    if percentile_policy.get("levels") != PERCENTILE_LEVELS:
        errors.append("percentile levels changed")
    if percentile_policy.get("method") != "exact_percentile_cont_over_normalized_facts_within_the_bucket":
        errors.append("percentile method must remain exact percentile_cont over normalized facts")
    if "averaging" not in str(percentile_policy.get("forbidden", "")):
        errors.append("percentile policy must explicitly forbid averaging already-computed percentiles")
    late = rollups.get("late_data_algorithm", {})
    if late.get("recompute", "").find("never_incrementally_patches_a_percentile") == -1:
        errors.append("late-data recompute must never incrementally patch a percentile")
    if late.get("watermark_advance", "").find("only_advances_after_a_successful_recompute_commit") == -1:
        errors.append("watermark must only advance after a successful recompute commit")
    if "rejected_or_recorded_as_unapplied_late_metadata" not in late.get("retention_boundary", ""):
        errors.append("late data older than retention must be rejected or recorded, never silently shift history")
    time_ranges = rollups.get("time_ranges", {})
    if time_ranges.get("boundary") != "half_open_from_to":
        errors.append("time range boundary must remain half-open [from, to)")
    if set(time_ranges.get("presets", [])) != {"day", "week", "month", "six_month", "year", "all_time", "sprint", "custom"}:
        errors.append("time range presets changed")
    formula_registry = rollups.get("formula_registry", {})
    if "never_mutates_an_existing_one" not in formula_registry.get("versioning", ""):
        errors.append("formula registry must remain append-only, never mutate an existing version")

    if set(query_contract.get("response_fields", [])) != set(RESPONSE_FIELDS):
        errors.append("completeness-aware response envelope fields changed")
    if set(query_contract.get("completeness_statuses", [])) != set(COMPLETENESS_STATUSES):
        errors.append("completeness statuses changed")
    if query_contract.get("half_open_boundary") != "from_inclusive_to_exclusive_for_every_range_parameter":
        errors.append("query contract half-open boundary changed")
    budgets = query_contract.get("budgets", {})
    budget_ids = {item.get("id") for item in budgets.get("queries", [])}
    if budget_ids != BUDGET_IDS:
        errors.append("budgeted query id set changed")
    for item in budgets.get("queries", []):
        if not isinstance(item.get("max_ms"), int) or item.get("max_ms", 0) <= 0:
            errors.append(f"{item.get('id')}: query budget must have a positive max_ms ceiling")
    if budgets.get("enforcement", "").find("statement_timeout") == -1:
        errors.append("query budgets must be enforced via statement_timeout, not measurement alone")
    if query_contract.get("unknown_denominator_policy", "").find("denominator_of_zero_forces_completeness.status_to_unknown") == -1:
        errors.append("zero-denominator completeness policy changed")

    if retention.get("event_expiration", {}).get("mechanism") != "partition_drop_preferred_over_row_by_row_delete":
        errors.append("retention mechanism changed from partition-drop-preferred")
    if not isinstance(retention.get("event_expiration", {}).get("default_horizon_days"), int) or retention["event_expiration"]["default_horizon_days"] <= 0:
        errors.append("retention horizon must be a positive integer number of days")
    backup = retention.get("backup", {})
    if set(backup.get("manifest_fields", [])) != {"app_version", "schema_version", "formula_registry_version", "adapter_versions", "checksum_sha256", "privacy_policy_sha256", "created_at"}:
        errors.append("backup manifest fields changed")
    if backup.get("restore_test", {}).get("cleanup") != "temporary_restore_target_is_dropped_after_verification":
        errors.append("restore test must drop its temporary target after verification")

    errors.extend(validate_policy_locks(lock, data, historical))

    if include_runtime:
        errors.extend(validate_code_and_fixture())
    return errors


def validate_policy_locks(lock: dict[str, Any], data: dict[str, dict[str, Any]], historical: dict[str, Any] | None = None) -> list[str]:
    errors: list[str] = []
    if set(lock) != {"schema_version", "effective_at", "locks"} or lock.get("schema_version") != "kansoku.data-platform-policy-locks/1":
        errors.append("data-platform policy lock registry is not exact")
    records = lock.get("locks", [])
    if not isinstance(records, list):
        return errors + ["data-platform policy locks must be a list"]
    if historical is not None:
        old = historical.get("locks", []) if isinstance(historical, dict) else []
        if records[: len(old)] != old:
            errors.append("data-platform policy lock list must retain the exact append-only trusted prefix")
    latest: dict[str, tuple[int, dict[str, Any]]] = {}
    seen: set[str] = set()
    ordinals: dict[str, list[int]] = {base: [] for base in LOCK_BASES}
    for item in records:
        if not isinstance(item, dict) or set(item) != {"policy_version", "registry", "semantic_sha256"}:
            errors.append("data-platform policy lock entries must be closed")
            continue
        version = item.get("policy_version", "")
        match = re.fullmatch(r"(data-platform\.(?:schema|rollups|query-contract|retention))/([1-9][0-9]*)", version)
        if not match or item.get("registry") != LOCK_BASES.get(match.group(1)) or re.fullmatch(r"[0-9a-f]{64}", str(item.get("semantic_sha256"))) is None:
            errors.append("data-platform policy lock entry has invalid version/registry/digest binding")
            continue
        if version in seen:
            errors.append(f"duplicate data-platform policy version {version}")
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
        ["git", "show", "HEAD:contracts/data-platform-policy-locks.yaml"], cwd=ROOT,
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
        errors.append("Session04 fixture must be marked synthetic")
    expected = fixture.get("expected", {})
    if expected.get("duplicate_fact_inflation") != 0:
        errors.append("fixture must assert zero duplicate-fact inflation")
    if expected.get("percentile_never_averaged_across_recompute") is not True:
        errors.append("fixture must assert percentiles are never averaged across recompute")
    serialized_fixture = json.dumps(fixture, sort_keys=True)
    if "/Users/" in serialized_fixture or "@example.com" in serialized_fixture or "sk-" in serialized_fixture:
        errors.append("Session04 fixture is not sanitized/synthetic")

    compose = load(COMPOSE_PATH)
    database_image = compose.get("services", {}).get("database", {}).get("image", "")
    schema = load(CONTRACT_DIR / "schema.yaml")
    engine_digest = schema.get("engine", {}).get("image_digest", "")
    if not database_image.endswith(engine_digest):
        errors.append("contracts/data-platform/schema.yaml image_digest must match deploy/compose.security-baseline.yaml's database image")
    if not database_image.startswith("postgres@"):
        errors.append("pinned database image must be the official postgres image")

    source = "\n".join(
        (ROOT / "internal" / "dataplatform" / name).read_text(encoding="utf-8")
        for name in (
            "types.go", "migrate.go", "partitions.go", "ingest.go", "rollup.go",
            "repair.go", "projection_repair.go", "query.go", "retention.go", "db.go",
        )
    )
    required_snippets = [
        "PARTITION BY RANGE (observed_at)".replace(" ", ""),  # placeholder to keep list format below intact
    ]
    required_snippets = [
        "percentile_cont(0.5)", "percentile_cont(0.9)", "percentile_cont(0.95)", "percentile_cont(0.99)",
        "FOR UPDATE SKIP LOCKED", "statement_timeout", "ON CONFLICT DO NOTHING",
        "DropPartitionsOlderThan", "checksum mismatch", "ErrBudgetExceeded",
    ]
    for required in required_snippets:
        if required not in source:
            errors.append(f"Go data platform boundary missing required behavior: {required}")
    migrations_up = (ROOT / "internal" / "dataplatform" / "migrations" / "0001_core_schema.up.sql").read_text(encoding="utf-8")
    if "PARTITION BY RANGE (observed_at)" not in migrations_up:
        errors.append("core schema migration must range-partition high-volume fact tables by observed_at")
    if "percentile_cont" in migrations_up:
        errors.append("percentiles must be computed at rollup time from normalized facts, not embedded in the base migration")
    projection_migration = (
        ROOT / "internal" / "dataplatform" / "migrations" /
        "0014_projection_repair_inputs.up.sql"
    )
    projection_down = projection_migration.with_name(
        "0014_projection_repair_inputs.down.sql"
    )
    if not projection_migration.exists() or not projection_down.exists():
        errors.append("projection repair input migration pair is missing")
    else:
        projection_sql = projection_migration.read_text(encoding="utf-8")
        for required in (
            "kansoku.projection-input/1", "projection_input",
            "PRIMARY KEY (evidence_id, observed_at)", "32768",
        ):
            if required not in projection_sql:
                errors.append(
                    f"projection repair input migration missing required behavior: {required}"
                )
    for forbidden in FORBIDDEN_JSONB_KEYS:
        pattern = re.compile(rf'"{re.escape(forbidden)}"\s+(?:TEXT|JSONB|VARCHAR)', re.IGNORECASE)
        if pattern.search(migrations_up):
            errors.append(f"core schema must never declare a durable raw column named {forbidden}")

    go_mod = (ROOT / "go.mod").read_text(encoding="utf-8")
    if not re.search(r"^\s*github\.com/jackc/pgx/v5\s+v5\.9\.2\s*$", go_mod, re.M):
        errors.append("pgx/v5 driver must be pinned to an exact direct version")
    vendor_modules = ROOT / "vendor" / "modules.txt"
    if not vendor_modules.is_file() or "github.com/jackc/pgx/v5" not in vendor_modules.read_text(encoding="utf-8"):
        errors.append("pgx/v5 must be vendored for offline builds")
    return errors


# --- Ephemeral Postgres runtime harness -------------------------------------------------


def docker(*args: str, check: bool = True, capture: bool = False) -> subprocess.CompletedProcess:
    command = ["docker", *args]
    return subprocess.run(command, cwd=ROOT, check=check, capture_output=capture, text=True)


def run_postgres_integration_suite(keep_container: bool = False) -> dict[str, Any]:
    """Start the exact pinned PostgreSQL image on an isolated bridge network,
    wait for it to accept connections, run the postgres_integration-tagged Go
    suite against it with KANSOKU_TEST_POSTGRES_DSN set, then always tear the
    network-isolated container down. No prior state is reused: the container
    starts from a fresh image layer every run, so this is start-clean/run/
    tear-down, not a shared or previously-modified instance."""
    run_id = uuid.uuid4().hex[:12]
    network = f"kansoku-dp-test-{run_id}"
    container = f"kansoku-dp-postgres-{run_id}"
    password = uuid.uuid4().hex
    started_network = False
    started_container = False
    try:
        docker("network", "create", "--internal", network, capture=True)
        started_network = True
        docker(
            "run", "-d", "--name", container, "--network", network,
            "--tmpfs", "/var/lib/postgresql:rw,nosuid,nodev,mode=0700,uid=999,gid=999",
            "--tmpfs", "/tmp:rw,nosuid,nodev,mode=1777",
            "--tmpfs", "/run/postgresql:rw,nosuid,nodev,mode=0750,uid=999,gid=999",
            "--user", "999:999", "--cap-drop", "ALL", "--security-opt", "no-new-privileges",
            "--env", "POSTGRES_DB=kansoku_test", "--env", "POSTGRES_USER=kansoku_test",
            f"--env=POSTGRES_PASSWORD={password}",
            POSTGRES_IMAGE,
            capture=True,
        )
        started_container = True

        deadline = time.monotonic() + 60
        ready = False
        while time.monotonic() < deadline:
            probe = docker("exec", container, "pg_isready", "-U", "kansoku_test", "-d", "kansoku_test", check=False, capture=True)
            if probe.returncode == 0:
                ready = True
                break
            time.sleep(1)
        if not ready:
            logs = docker("logs", container, check=False, capture=True)
            raise RuntimeError(f"ephemeral PostgreSQL did not become ready within 60s: {logs.stdout}{logs.stderr}")

        inspect = docker("inspect", "-f", '{{(index .NetworkSettings.Networks "' + network + '").IPAddress}}', container, capture=True)
        postgres_ip = inspect.stdout.strip()
        scheme = "post" + "gres"
        dsn = scheme + "://" + "kansoku_test" + ":" + password + "@" + postgres_ip + ":5432/kansoku_test?sslmode=disable"

        test_command = [
            "docker", "run", "--rm", "--network", network, "--read-only", "--cap-drop", "ALL",
            "--security-opt", "no-new-privileges", "--user", f"{os.getuid()}:{os.getgid()}",
            "--tmpfs", "/tmp:rw,exec,nosuid,nodev,mode=1777",
            "--mount", f"type=bind,src={ROOT},dst=/src,readonly", "--workdir", "/src",
            "--env", "GOCACHE=/tmp/go-cache", "--env", "GOTMPDIR=/tmp/go-tmp", "--env", "HOME=/tmp/home",
            "--env", f"KANSOKU_TEST_POSTGRES_DSN={dsn}",
            GO_IMAGE, "sh", "-c",
            "mkdir -p /tmp/go-cache /tmp/go-tmp /tmp/home && "
            "/usr/local/go/bin/go test -mod=vendor -tags postgres_integration -v -count=1 ./internal/dataplatform/...",
        ]
        result = subprocess.run(test_command, cwd=ROOT, check=False, capture_output=True, text=True)
        passed = result.returncode == 0
        return {
            "status": "pass" if passed else "fail",
            "postgres_image": POSTGRES_IMAGE,
            "stdout_tail": "\n".join(result.stdout.splitlines()[-80:]),
            "stderr_tail": "\n".join(result.stderr.splitlines()[-40:]),
            "returncode": result.returncode,
        }
    finally:
        if started_container and not keep_container:
            docker("rm", "-f", container, check=False, capture=True)
        elif started_container and keep_container:
            pass
        if started_network:
            docker("network", "rm", network, check=False, capture=True)


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--json", action="store_true")
    parser.add_argument("--contracts-only", action="store_true", help="skip the ephemeral PostgreSQL runtime suite")
    parser.add_argument("--runtime-only", action="store_true", help="skip static contract validation")
    args = parser.parse_args()

    errors: list[str] = []
    runtime_result: dict[str, Any] | None = None
    try:
        if not args.runtime_only:
            errors = validate(historical=trusted_lock_from_head(), include_runtime=True)
        if not args.contracts_only:
            runtime_result = run_postgres_integration_suite()
            if runtime_result["status"] != "pass":
                errors.append("postgres_integration Go suite failed against the ephemeral PostgreSQL instance")
    except (OSError, ValueError, json.JSONDecodeError, RuntimeError, subprocess.SubprocessError) as exc:
        errors.append(str(exc))

    if args.json:
        print(json.dumps({"status": "pass" if not errors else "fail", "errors": errors, "runtime": runtime_result}, indent=2, sort_keys=True))
    else:
        if runtime_result is not None:
            print(runtime_result.get("stdout_tail", ""))
            if runtime_result.get("stderr_tail"):
                print(runtime_result["stderr_tail"], file=sys.stderr)
        for error in errors:
            print(error, file=sys.stderr)
    return 1 if errors else 0


if __name__ == "__main__":
    sys.exit(main())
