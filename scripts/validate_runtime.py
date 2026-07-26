#!/usr/bin/env python3
"""Authoritative Session 09 runtime/operations contract validator."""

from __future__ import annotations

import argparse
import copy
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
CONTRACT_DIR = ROOT / "contracts" / "runtime"
LOCK_PATH = ROOT / "contracts" / "runtime-policy-locks.yaml"

# The runtime backup/restore path shells out to PostgreSQL 18's pg_dump and
# pg_restore, which the pinned data-platform Go image does not carry. The
# ephemeral test image therefore layers that same pinned Go toolchain onto the
# pinned Postgres image's filesystem -- mirroring deploy/Dockerfile's own
# "postgres filesystem + static Go binary" strategy exactly -- so both the Go
# 1.26 compiler and pg_dump/pg_restore 18 are on PATH inside one hermetic image.
GO_IMAGE = "golang@sha256:1ecb7edf62a0408027bd5729dfd6b1b8766e578e8df93995b225dfd0944eb651"
POSTGRES_IMAGE = "postgres@sha256:9a8afca54e7861fd90fab5fdf4c42477a6b1cb7d293595148e674e0a3181de15"
FILES = (
    "auth-and-plans.yaml",
    "operations-backup-and-soak.yaml",
    "queue-and-durability.yaml",
    "runtime-and-api.yaml",
)
PATHS = {f"contracts/runtime/{name}" for name in FILES}
POLICY_BASE_BY_REGISTRY = {
    "contracts/runtime/auth-and-plans.yaml": "runtime.auth-and-plans",
    "contracts/runtime/operations-backup-and-soak.yaml": "runtime.operations-backup-and-soak",
    "contracts/runtime/queue-and-durability.yaml": "runtime.queue-and-durability",
    "contracts/runtime/runtime-and-api.yaml": "runtime.runtime-and-api",
}
AUTHORITATIVE_SEMANTIC_SHA256 = {
    "contracts/runtime/auth-and-plans.yaml": "774677e401e14dabb079eecd584a803d6858fde1c5cd7701a395724bc8d19864",
    "contracts/runtime/operations-backup-and-soak.yaml": "3b3d1e0df2a9a6aeeea01859b7f8ae485a4e6a3dffb09d865dacdcda0f1a94a2",
    "contracts/runtime/queue-and-durability.yaml": "7a40f224ce3e2d597ce93100a001dba7018d2ba98a13fb4f18c70ee4ec21df4a",
    "contracts/runtime/runtime-and-api.yaml": "0abfa3b28f34d69253fe8029faae01e71be7f86c7d834b443d5eee907a8fd216",
}
TOP_LEVEL = {
    "auth-and-plans.yaml": {
        "schema_version", "contract_version", "effective_at", "secret_files",
        "route_authorization", "plan_preview", "plan_apply", "admin_mutation",
    },
    "operations-backup-and-soak.yaml": {
        "schema_version", "contract_version", "effective_at", "job_state",
        "retention_and_resources", "native_backup", "portable_export_import",
        "diagnostics", "compose_policy", "accelerated_soak",
    },
    "queue-and-durability.yaml": {
        "schema_version", "contract_version", "effective_at", "lanes", "admission",
        "acknowledgement", "spool", "metrics", "shutdown", "idempotency",
    },
    "runtime-and-api.yaml": {
        "schema_version", "contract_version", "effective_at", "appliance",
        "listeners", "startup", "shutdown", "api", "cli",
    },
}
LANES = {
    "hook_http": (64, 1),
    "otlp_log": (64, 1),
    "otlp_span": (64, 1),
    "otlp_metric": (64, 1),
    "transcript_jsonl": (16, 1),
    "adapter_batch": (16, 1),
}
READ_ROUTES = {
    "GET /api/v1/inventory", "GET /api/v1/analytics", "GET /api/v1/health",
    "GET /api/v1/incidents", "GET /api/v1/completeness", "GET /api/v1/operations/jobs",
    "GET /api/v1/incidents/{opaque_id}",
    "GET /api/v1/incidents/{opaque_id}/occurrences",
    "GET /api/v1/incidents/{opaque_id}/debug-bundle",
    "GET /api/v1/quarantine", "GET /api/v1/quarantine/{opaque_id}",
}
MUTATION_ROUTES = {
    "POST /api/v1/plans/preview", "POST /api/v1/plans/apply",
    "PATCH /api/v1/incidents/{opaque_id}/triage",
    "POST /api/v1/incidents/{opaque_id}/acknowledge",
    "POST /api/v1/incidents/{opaque_id}/investigating",
    "POST /api/v1/incidents/{opaque_id}/action-ready",
    "POST /api/v1/admin/retention/preview", "POST /api/v1/admin/retention/apply",
    "POST /api/v1/admin/export", "POST /api/v1/admin/import",
    "POST /api/v1/admin/backup", "POST /api/v1/admin/restore-verify",
    "POST /api/v1/admin/diagnostics",
}
RUNTIME_TABLES = {"runtime_job_runs", "runtime_operation_approvals", "runtime_import_receipts"}
SOAK_FAULTS = {"process_restart", "database_restart", "stop_the_world_upgrade_boundary"}


def load(path: Path) -> Any:
    return json.loads(path.read_text(encoding="utf-8"))


def registries() -> dict[str, dict[str, Any]]:
    return {f"contracts/runtime/{name}": load(CONTRACT_DIR / name) for name in FILES}


def semantic_sha256(value: Any) -> str:
    encoded = json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=False).encode()
    return hashlib.sha256(encoded).hexdigest()


def coherent_locks(data: dict[str, dict[str, Any]]) -> dict[str, Any]:
    return {
        "schema_version": "kansoku.runtime-policy-locks/1",
        "effective_at": "2026-07-24",
        "locks": [
            {
                "policy_version": POLICY_BASE_BY_REGISTRY[path] + "/1",
                "registry": path,
                "semantic_sha256": semantic_sha256(data[path]),
            }
            for path in sorted(data)
        ],
    }


def trusted_lock_from_head() -> dict[str, Any] | None:
    result = subprocess.run(
        ["git", "show", "HEAD:contracts/runtime-policy-locks.yaml"],
        cwd=ROOT, text=True, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
    )
    if result.returncode != 0:
        return None
    return json.loads(result.stdout)


def _contains_all(text: str, values: list[str], label: str, errors: list[str]) -> None:
    missing = [value for value in values if value not in text]
    if missing:
        errors.append(f"{label} missing {missing}")


def validate(
    candidate: dict[str, dict[str, Any]] | None = None,
    locks: dict[str, Any] | None = None,
    *,
    include_code: bool = True,
    historical: dict[str, Any] | None = None,
) -> list[str]:
    data = candidate if candidate is not None else registries()
    lock_doc = locks if locks is not None else load(LOCK_PATH)
    errors: list[str] = []
    if set(data) != PATHS:
        errors.append("runtime registry set is not exact")
        return errors

    for path, document in data.items():
        name = Path(path).name
        if set(document) != TOP_LEVEL[name]:
            errors.append(f"{name} top-level schema is not closed")
        expected_effective = "2026-07-26" if name in {
            "operations-backup-and-soak.yaml", "runtime-and-api.yaml"
        } else "2026-07-24"
        if document.get("effective_at") != expected_effective:
            errors.append(f"{name} effective_at changed")

    if set(lock_doc) != {"schema_version", "effective_at", "locks"}:
        errors.append("runtime lock schema is not closed")
    rows = lock_doc.get("locks", [])
    if historical is not None:
        prior = historical.get("locks", [])
        if rows[:len(prior)] != prior:
            errors.append("runtime lock history lost append-only trusted prefix")
    latest: dict[str, tuple[int, dict[str, Any]]] = {}
    ordinals: dict[str, list[int]] = {path: [] for path in PATHS}
    seen_versions: set[str] = set()
    for row in rows:
        path = row.get("registry")
        if path not in data:
            errors.append("runtime lock references unknown registry")
            continue
        wanted_base = POLICY_BASE_BY_REGISTRY[path]
        match = re.fullmatch(re.escape(wanted_base) + r"/([1-9][0-9]*)", str(row.get("policy_version", "")))
        if match is None:
            errors.append("runtime policy name does not match registry identity")
            continue
        version = str(row["policy_version"])
        if version in seen_versions:
            errors.append("duplicate runtime policy version")
        seen_versions.add(version)
        ordinal = int(match.group(1))
        ordinals[path].append(ordinal)
        if path not in latest or ordinal > latest[path][0]:
            latest[path] = (ordinal, row)
    for path in sorted(PATHS):
        values = sorted(ordinals[path])
        if not values or values != list(range(1, values[-1] + 1)):
            errors.append(f"runtime policy versions are not contiguous for {path}")
        digest = semantic_sha256(data[path])
        if path not in latest or latest[path][1].get("semantic_sha256") != digest:
            errors.append(f"runtime semantic lock mismatch for {path}")
        if digest != AUTHORITATIVE_SEMANTIC_SHA256[path]:
            errors.append(f"runtime authoritative semantic digest changed for {path}")

    runtime = data["contracts/runtime/runtime-and-api.yaml"]
    if runtime["appliance"].get("shared_components") != [
        "internal/observability", "internal/dataplatform", "internal/integrity",
        "internal/adaptersdk", "internal/installer", "internal/localhttp",
    ] or not runtime["appliance"].get("parallel_fake_ingress_or_store_forbidden"):
        errors.append("runtime appliance reuse boundary changed")
    listeners = runtime["listeners"]
    if listeners.get("http", {}).get("published") != "127.0.0.1:43100" or \
            listeners.get("otlp_http", {}).get("published") != "127.0.0.1:4318" or \
            listeners.get("otlp_grpc", {}).get("published") != "127.0.0.1:4317" or \
            listeners.get("database_host_port_published") is not False:
        errors.append("runtime listener loopback/database publication policy changed")
    if runtime["startup"].get("secrets_from_files_only") is not True or \
            runtime["startup"].get("unknown_config_fields") != "reject" or \
            runtime["startup"].get("migration_checksum_mismatch") != "fail_closed":
        errors.append("runtime strict config/secret/migration startup policy changed")
    if set(runtime["api"].get("read_routes", [])) != READ_ROUTES or \
            set(runtime["api"].get("mutation_routes", [])) != MUTATION_ROUTES:
        errors.append("runtime /api/v1 route set changed")
    if runtime["api"].get("request_max_bytes") != 1048576 or \
            runtime["api"].get("response_max_bytes") != 1048576 or \
            runtime["api"].get("query_timeout_ms") != 500:
        errors.append("runtime API budgets changed")
    forbidden = set(runtime["api"].get("raw_field_names_forbidden_recursively", []))
    if not {"prompt", "response", "content", "tool_input", "tool_output", "environment", "credential", "raw_path"} <= forbidden:
        errors.append("runtime API prohibited raw-field schema weakened")

    auth = data["contracts/runtime/auth-and-plans.yaml"]
    required_secrets = set(auth["secret_files"].get("required", []))
    if required_secrets != {
        "ingress_bearer", "read_bearer", "mutation_bearer", "csrf",
        "identity_hmac", "audit_hmac", "database_password",
    } or auth["secret_files"].get("minimum_bytes") != 32 or \
            auth["secret_files"].get("all_values_pairwise_distinct") is not True:
        errors.append("runtime secret-file partition changed")
    route_auth = auth["route_authorization"]
    if route_auth["ingress"].get("credential") != "ingress_bearer" or \
            route_auth["read"].get("credential") != "read_bearer" or \
            route_auth["mutation"].get("credential") != "mutation_bearer" or \
            route_auth["mutation"].get("csrf") is not True or \
            route_auth["admin"].get("separate_from_ingress_and_read") is not True:
        errors.append("runtime route authorization separation changed")
    binding = auth["plan_apply"].get("approval_binding", [])
    if binding != ["plan_sha256", "target_id", "original_sha256", "planned_sha256", "approval_nonce"] or \
            auth["plan_apply"].get("automatic_retry") is not False:
        errors.append("runtime plan approval binding changed")

    queue = data["contracts/runtime/queue-and-durability.yaml"]
    actual_lanes = {
        row.get("source"): (row.get("capacity"), row.get("workers"))
        for row in queue.get("lanes", [])
    }
    if actual_lanes != LANES or any(row.get("spool_max_bytes") != 67108864 for row in queue.get("lanes", [])):
        errors.append("runtime queue lane/capacity registry changed")
    if queue["admission"].get("reservation_before_filestore_acceptance") is not True or \
            queue["admission"].get("per_source_capacity_independent") is not True:
        errors.append("runtime pre-acceptance per-source reservation changed")
    ack_rule = queue["acknowledgement"]
    if ack_rule.get("filestore_alone_is_not_production_acknowledgement") is not True or \
            ack_rule.get("postgres_first") is not True or \
            ack_rule.get("spool_fallback_on_retryable_postgres_failure") is not True or \
            "PostgreSQL or its source lane sanitized spool" not in ack_rule.get("success_rule", ""):
        errors.append("runtime durable acknowledgement rule changed")
    if queue["spool"].get("format") != "strict_jsonl_typed_observability_commit_request" or \
            queue["spool"].get("corruption") != "fail_visible_leave_bytes_unchanged":
        errors.append("runtime sanitized spool contract changed")

    ops = data["contracts/runtime/operations-backup-and-soak.yaml"]
    if set(ops["job_state"].get("jobs", [])) != {
        "daily_integrity", "rollup_repair", "retention", "backup",
        "restore_verify", "export", "import",
    } or ops["job_state"].get("raw_error_text_persisted") is not False:
        errors.append("runtime job vocabulary/safe error policy changed")
    covered = ops["native_backup"].get("covered_table_groups", {})
    if set(covered.get("runtime", [])) != RUNTIME_TABLES or \
            "integrity_audit_reports" not in covered.get("integrity", []) or \
            ops["native_backup"].get("shell") is not False:
        errors.append("runtime backup coverage or argv policy changed")
    if ops["portable_export_import"].get("import_never_trusts") != [
        "formula_definition", "schema_definition", "migration_sql", "executable_command",
    ] or ops["portable_export_import"].get("unknown_formula_or_schema") != "reject_before_write":
        errors.append("runtime import trust boundary changed")
    if "paths" not in ops["diagnostics"].get("forbidden", []) or \
            "environment" not in ops["diagnostics"].get("forbidden", []) or \
            "credentials" not in ops["diagnostics"].get("forbidden", []):
        errors.append("runtime diagnostics privacy boundary changed")
    compose_policy = ops["compose_policy"]
    if compose_policy.get("database_host_port_published") is not False or \
            compose_policy.get("database_image") != "postgres@sha256:9a8afca54e7861fd90fab5fdf4c42477a6b1cb7d293595148e674e0a3181de15" or \
            "docker_socket" not in compose_policy.get("forbidden", []):
        errors.append("runtime compose trust boundary changed")
    soak = ops["accelerated_soak"]
    if soak.get("logical_days") != 7 or soak.get("cycles_per_day") != 24 or \
            set(soak.get("faults_executed", [])) != SOAK_FAULTS or \
            soak.get("wall_clock_seven_day_claim") is not False:
        errors.append("runtime soak evidence scope changed")

    if include_code:
        errors.extend(validate_code())
    return errors


def validate_code() -> list[str]:
    errors: list[str] = []
    runtime_dir = ROOT / "internal" / "runtime"
    required_files = {
        "config.go", "secrets.go", "migrate.go", "queue.go", "api.go", "plans.go",
        "jobs.go", "backup.go", "diagnostics.go", "assembly.go", "soak.go",
    }
    if not runtime_dir.is_dir() or not required_files <= {p.name for p in runtime_dir.glob("*.go")}:
        return ["runtime implementation file set incomplete"]
    source = "\n".join(path.read_text(encoding="utf-8") for path in sorted(runtime_dir.glob("*.go")))
    _contains_all(source, [
        "type Config struct", "LoadConfig", "LoadSecretFiles", "type DurableIngressQueue",
        "ReserveNormalizedFact", "type API struct", "NewAPI", "type PlanService",
        "type JobStore", "CreateNativeBackup", "CreateDiagnosticsBundle",
        "type Appliance struct", "RunAcceleratedSoak",
    ], "runtime Go implementation", errors)
    if re.search(r'json:"(?:prompt|response|content|tool_input|tool_output|environment|credential|raw_path)"', source):
        errors.append("runtime API Go types expose a prohibited raw field")
    if "exec.Command(" in source or "sh -c" in source or "bash -c" in source:
        errors.append("runtime operations contain a shell execution path")
    cmd = ROOT / "cmd" / "kansoku" / "main.go"
    if not cmd.exists():
        errors.append("cmd/kansoku main missing")
    else:
        _contains_all(cmd.read_text(encoding="utf-8"), [
            '"serve"', '"health"', '"config"', '"migrate"', '"backup"',
            '"restore-verify"', '"export"', '"import"', '"diagnostics"', '"soak"',
        ], "cmd/kansoku commands", errors)
    migration = runtime_dir / "migrations" / "0001_runtime_operations.up.sql"
    down = runtime_dir / "migrations" / "0001_runtime_operations.down.sql"
    if not migration.exists() or not down.exists():
        errors.append("runtime migration pair missing")
    else:
        sql = migration.read_text(encoding="utf-8")
        _contains_all(sql, sorted(RUNTIME_TABLES), "runtime migration", errors)
    compose = ROOT / "deploy" / "compose.yaml"
    if not compose.exists():
        errors.append("production Compose file missing")
    else:
        try:
            doc = load(compose)
            services = doc.get("services", {})
            if set(services) != {"kansoku", "postgres", "backup", "restore-verify"}:
                errors.append("production Compose service set changed")
            app = services.get("kansoku", {})
            db = services.get("postgres", {})
            if app.get("ports") != [
                "127.0.0.1:43100:43100", "127.0.0.1:4317:4317", "127.0.0.1:4318:4318",
            ] or db.get("ports"):
                errors.append("production Compose port policy changed")
            for name in ("kansoku", "postgres"):
                service = services.get(name, {})
                if service.get("read_only") is not True or service.get("cap_drop") != ["ALL"] or \
                        "no-new-privileges:true" not in service.get("security_opt", []) or \
                        service.get("restart") != "always":
                    errors.append(f"production Compose hardening incomplete for {name}")
            if doc.get("networks", {}).get("kansoku-internal", {}).get("internal") is not True:
                errors.append("production Compose network is not internal")
        except (json.JSONDecodeError, AttributeError):
            errors.append("production Compose is not deterministic JSON/YAML")
    dockerfile = ROOT / "deploy" / "Dockerfile"
    if not dockerfile.exists():
        errors.append("production Dockerfile missing")
    else:
        text = dockerfile.read_text(encoding="utf-8")
        _contains_all(text, ["FROM scratch", "USER 65532:65532", 'ENTRYPOINT ["/kansoku"]'], "production Dockerfile", errors)
    sources = (ROOT / "SOURCES.md").read_text(encoding="utf-8")
    _contains_all(sources, [
        "https://docs.docker.com/reference/compose-file/services/",
        "https://docs.docker.com/compose/how-tos/use-secrets/",
        "https://www.postgresql.org/docs/18/backup.html",
        "https://www.postgresql.org/docs/18/app-pgdump.html",
        "https://www.postgresql.org/docs/18/app-pgrestore.html",
        "https://pkg.go.dev/net/http",
        "Retrieved: 2026-07-24",
    ], "Session09 sources", errors)
    for path in (
        ROOT / "tests" / "fixtures" / "session-09" / "runtime-config.json",
        ROOT / "tests" / "fixtures" / "session-09" / "accelerated-soak.json",
        ROOT / "reports" / "session-09-reconciliation.md",
        ROOT / "reports" / "session-09-sbom.json",
        ROOT / "adr" / "0012-session-09-local-runtime-and-operations.md",
    ):
        if not path.exists():
            errors.append(f"Session09 required artifact missing: {path.relative_to(ROOT)}")
    return errors


# --- Ephemeral Postgres runtime harness ---------------------------------------
#
# This mirrors scripts/validate_data_platform.py's run_postgres_integration_suite
# argv-only, pinned-digest, isolated-network, deterministic-teardown pattern.
# The only structural difference is the combined Go+pg_dump test image, built
# once per run (see GO_IMAGE/POSTGRES_IMAGE above), because internal/runtime's
# backup path needs pg_dump/pg_restore on PATH inside the test container.


def docker(*args: str, check: bool = True, capture: bool = False) -> subprocess.CompletedProcess:
    return subprocess.run(["docker", *args], cwd=ROOT, check=check, capture_output=capture, text=True)


def build_runtime_test_image(tag: str) -> None:
    """Build the combined Go-toolchain + pinned-pg-client test image. The build
    (not the later test run) is the only step that may touch the network; the
    tagged suite itself runs offline with -mod=vendor on the isolated bridge."""
    dockerfile = (
        f"FROM {GO_IMAGE} AS go\n"
        f"FROM {POSTGRES_IMAGE}\n"
        "COPY --from=go /usr/local/go /usr/local/go\n"
        "ENV PATH=/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin\n"
        "ENV CGO_ENABLED=0\n"
    )
    subprocess.run(
        ["docker", "build", "-q", "-t", tag, "-f", "-", str(ROOT)],
        cwd=ROOT, check=True, capture_output=True, text=True, input=dockerfile,
    )


def start_ephemeral_postgres(network: str, container: str, password: str) -> str:
    """Start the pinned Postgres image on an internal network with the
    kansoku/kansoku database+user the strict runtime Config pins, wait for
    readiness, and return an offline DSN pointing at the container's bridge IP."""
    docker(
        "run", "-d", "--name", container, "--network", network,
        "--tmpfs", "/var/lib/postgresql:rw,nosuid,nodev,mode=0700,uid=999,gid=999",
        "--tmpfs", "/tmp:rw,nosuid,nodev,mode=1777",
        "--tmpfs", "/run/postgresql:rw,nosuid,nodev,mode=0750,uid=999,gid=999",
        "--user", "999:999", "--cap-drop", "ALL", "--security-opt", "no-new-privileges",
        "--env", "POSTGRES_DB=kansoku", "--env", "POSTGRES_USER=kansoku",
        f"--env=POSTGRES_PASSWORD={password}",
        POSTGRES_IMAGE,
        capture=True,
    )
    deadline = time.monotonic() + 60
    while time.monotonic() < deadline:
        probe = docker("exec", container, "pg_isready", "-U", "kansoku", "-d", "kansoku", check=False, capture=True)
        if probe.returncode == 0:
            break
        time.sleep(1)
    else:
        logs = docker("logs", container, check=False, capture=True)
        raise RuntimeError(f"ephemeral PostgreSQL did not become ready within 60s: {logs.stdout}{logs.stderr}")
    inspect = docker(
        "inspect", "-f",
        '{{(index .NetworkSettings.Networks "' + network + '").IPAddress}}',
        container, capture=True,
    )
    postgres_ip = inspect.stdout.strip()
    scheme = "post" + "gres"
    return scheme + "://kansoku:" + password + "@" + postgres_ip + ":5432/kansoku?sslmode=disable"


def run_postgres_integration_suite(keep_container: bool = False) -> dict[str, Any]:
    """Start the exact pinned PostgreSQL image on an isolated bridge network,
    wait for it to accept connections, run the postgres_integration-tagged Go
    suite in internal/runtime against it with KANSOKU_TEST_POSTGRES_DSN set,
    then always tear the network-isolated container, network and ephemeral test
    image down. No prior state is reused: every run starts clean."""
    run_id = uuid.uuid4().hex[:12]
    network = f"kansoku-rt-test-{run_id}"
    container = f"kansoku-rt-postgres-{run_id}"
    image = f"kansoku-rt-testimg-{run_id}"
    password = uuid.uuid4().hex
    built_image = False
    started_network = False
    started_container = False
    try:
        build_runtime_test_image(image)
        built_image = True
        docker("network", "create", "--internal", network, capture=True)
        started_network = True
        dsn = start_ephemeral_postgres(network, container, password)
        started_container = True

        test_command = [
            "docker", "run", "--rm", "--network", network, "--read-only", "--cap-drop", "ALL",
            "--security-opt", "no-new-privileges", "--user", f"{os.getuid()}:{os.getgid()}",
            "--tmpfs", "/tmp:rw,exec,nosuid,nodev,mode=1777",
            "--mount", f"type=bind,src={ROOT},dst=/src,readonly", "--workdir", "/src",
            "--env", "GOCACHE=/tmp/go-cache", "--env", "GOTMPDIR=/tmp/go-tmp",
            "--env", "HOME=/tmp/home", "--env", "GOFLAGS=-mod=vendor",
            "--env", f"KANSOKU_TEST_POSTGRES_DSN={dsn}",
            image, "sh", "-c",
            "mkdir -p /tmp/go-cache /tmp/go-tmp /tmp/home && "
            "go test -mod=vendor -tags postgres_integration -v -count=1 ./internal/runtime/...",
        ]
        result = subprocess.run(test_command, cwd=ROOT, check=False, capture_output=True, text=True)
        passed = result.returncode == 0
        return {
            "status": "pass" if passed else "fail",
            "postgres_image": POSTGRES_IMAGE,
            "go_image": GO_IMAGE,
            "stdout_tail": "\n".join(result.stdout.splitlines()[-120:]),
            "stderr_tail": "\n".join(result.stderr.splitlines()[-40:]),
            "returncode": result.returncode,
        }
    finally:
        if started_container and not keep_container:
            docker("rm", "-f", container, check=False, capture=True)
        if started_network:
            docker("network", "rm", network, check=False, capture=True)
        if built_image and not keep_container:
            docker("image", "rm", "-f", image, check=False, capture=True)


# --- Real Docker-orchestrated accelerated soak --------------------------------
#
# ADR 0012 decision 9 requires the accelerated soak to actually execute the
# process-restart, database-restart and stop-the-world upgrade-boundary
# transitions its report names, against a real running appliance, not a fake
# in-memory driver. Because operations-backup-and-soak.yaml's compose_policy
# forbids docker_socket, the real driver must run host-side (cmd/kansoku soak,
# built as a host binary) with the docker CLI on PATH: it talks to the appliance
# only over its published /api/v1 + ingress HTTP surface and issues real docker
# operations. This harness owns the ephemeral stack: it builds the release image
# from deploy/Dockerfile, generates a temporary run directory (under the repo,
# the only Docker-shareable path on this host), brings the stack up, runs the
# host-side soak binary against it and tears everything down deterministically.
#
# Every subprocess call is argv-only; there is no shell text.

SOAK_SECRET_NAMES = (
    "ingress_bearer", "read_bearer", "mutation_bearer", "csrf",
    "identity_hmac", "audit_hmac", "database_password",
)


def _soak_runtime_config() -> dict[str, Any]:
    return {
        "version": "kansoku.runtime-config/1", "app_version": "0.9.0",
        "http_listen": "0.0.0.0:43100", "otlp_http_listen": "0.0.0.0:4318",
        "otlp_grpc_listen": "0.0.0.0:4317", "container_mode": True,
        "data_dir": "/var/lib/kansoku",
        "database": {"host": "postgres", "port": 5432, "name": "kansoku", "user": "kansoku",
                     "ssl_mode": "disable", "connect_timeout_seconds": 10},
        "secret_files": {name: f"/run/secrets/{name}" for name in SOAK_SECRET_NAMES},
        "queue_capacity": 64, "spool_max_bytes": 67108864, "shutdown_timeout_ms": 30000,
        "query_timeout_ms": 500, "response_max_bytes": 1048576, "retention_days": 400,
        "disk_budget_fraction": 0.9, "integrity_enabled": True,
        "privacy_canary_fixture": "/usr/share/kansoku/privacy-canary.json",
        "backup_dir": "/var/lib/kansoku-backups", "diagnostics_max_bytes": 1048576,
    }


def _soak_compose(project: str, image: str) -> dict[str, Any]:
    return {
        "name": project,
        "services": {
            "kansoku": {
                "image": image,
                "command": ["serve", "--config", "/etc/kansoku/runtime-config.json"],
                "user": "65532:65532", "read_only": True, "cap_drop": ["ALL"],
                "security_opt": ["no-new-privileges:true"], "restart": "always", "init": True,
                "ports": ["127.0.0.1:43100:43100", "127.0.0.1:4317:4317", "127.0.0.1:4318:4318"],
                # kansoku joins the internal network (isolating it and postgres
                # from external egress, matching the production policy) AND a
                # host-reachable bridge so its loopback-published ports actually
                # forward to the host on Docker Desktop, where an internal-only
                # container's published ports are not forwarded. postgres stays
                # internal-only with no published port, exactly as production.
                "networks": ["kansoku-internal", "soak-hostbridge"],
                "volumes": [
                    {"type": "bind", "source": "./runtime-config.json",
                     "target": "/etc/kansoku/runtime-config.json", "read_only": True},
                    {"type": "volume", "source": "kansoku-data", "target": "/var/lib/kansoku"},
                    {"type": "volume", "source": "kansoku-backups", "target": "/var/lib/kansoku-backups"},
                ],
                "tmpfs": ["/tmp:rw,noexec,nosuid,nodev,size=32m"],
                "secrets": list(SOAK_SECRET_NAMES),
                "depends_on": {"postgres": {"condition": "service_healthy"}},
                "healthcheck": {
                    "test": ["CMD", "/kansoku", "health", "--self", "--config", "/etc/kansoku/runtime-config.json"],
                    "interval": "5s", "timeout": "10s", "retries": 15, "start_period": "10s",
                },
            },
            "postgres": {
                "image": POSTGRES_IMAGE, "user": "70:70", "read_only": True, "cap_drop": ["ALL"],
                "security_opt": ["no-new-privileges:true"], "restart": "always",
                "environment": {
                    "POSTGRES_DB": "kansoku", "POSTGRES_USER": "kansoku",
                    "POSTGRES_PASSWORD_FILE": "/run/secrets/database_password",
                    "PGDATA": "/var/lib/postgresql/18/docker",
                },
                "networks": ["kansoku-internal"],
                "volumes": [{"type": "volume", "source": "postgres-data", "target": "/var/lib/postgresql"}],
                "tmpfs": ["/run/postgresql:rw,noexec,nosuid,nodev,size=16m",
                          "/tmp:rw,noexec,nosuid,nodev,size=32m"],
                "secrets": ["database_password"],
                "healthcheck": {
                    "test": ["CMD-SHELL", "pg_isready -U kansoku -d kansoku"],
                    "interval": "5s", "timeout": "5s", "retries": 20, "start_period": "10s",
                },
            },
        },
        "networks": {"kansoku-internal": {"internal": True}, "soak-hostbridge": {}},
        "volumes": {"postgres-data": {}, "kansoku-data": {}, "kansoku-backups": {}},
        "secrets": {name: {"file": f"./secrets/{name}"} for name in SOAK_SECRET_NAMES},
    }


def run_accelerated_soak(keep_stack: bool = False) -> dict[str, Any]:
    import secrets as secretlib
    import shutil

    run_id = uuid.uuid4().hex[:12]
    project = f"kansokusoak{run_id}"
    image = f"kansoku-soak-{run_id}:latest"
    # The ephemeral run directory MUST live under the repo (a Docker-shareable
    # path); /tmp is not shared with Docker Desktop's VM on this host.
    run_dir = ROOT / ".session09-soak-run" / run_id
    secrets_dir = run_dir / "secrets"
    compose_path = run_dir / "compose.json"
    config_path = run_dir / "runtime-config.json"
    evidence_path = run_dir / "soak-evidence.json"
    binary_path = run_dir / "kansoku"
    started = False
    built_image = False
    try:
        secrets_dir.mkdir(parents=True, exist_ok=True)
        for name in SOAK_SECRET_NAMES:
            (secrets_dir / name).write_text(secretlib.token_hex(24), encoding="utf-8")
            (secrets_dir / name).chmod(0o600)
        config_path.write_text(json.dumps(_soak_runtime_config(), indent=2), encoding="utf-8")
        compose_path.write_text(json.dumps(_soak_compose(project, image), indent=2), encoding="utf-8")

        # Build the release appliance image and the host-side soak binary.
        subprocess.run(["docker", "build", "-q", "-f", "deploy/Dockerfile", "-t", image, "."],
                       cwd=ROOT, check=True, capture_output=True, text=True)
        built_image = True
        subprocess.run(["go", "build", "-mod=vendor", "-o", str(binary_path), "./cmd/kansoku"],
                       cwd=ROOT, check=True, capture_output=True, text=True)

        compose_base = ["docker", "compose", "-f", str(compose_path), "-p", project]
        subprocess.run(compose_base + ["up", "-d"], cwd=run_dir, check=True, capture_output=True, text=True)
        started = True

        app_container = f"{project}-kansoku-1"
        db_container = f"{project}-postgres-1"
        deadline = time.monotonic() + 120
        healthy = False
        while time.monotonic() < deadline:
            inspect = subprocess.run(
                ["docker", "inspect", "-f", "{{.State.Health.Status}}", app_container],
                cwd=ROOT, check=False, capture_output=True, text=True,
            )
            if inspect.stdout.strip() == "healthy":
                healthy = True
                break
            time.sleep(2)
        if not healthy:
            logs = subprocess.run(["docker", "logs", app_container], cwd=ROOT, check=False, capture_output=True, text=True)
            raise RuntimeError(f"appliance did not become healthy within 120s: {logs.stdout[-2000:]}{logs.stderr[-2000:]}")

        soak = subprocess.run(
            [str(binary_path), "soak",
             "--evidence", str(evidence_path),
             "--secrets-dir", str(secrets_dir),
             "--compose-file", str(compose_path),
             "--compose-project", project,
             "--app-container", app_container,
             "--db-container", db_container,
             "--recover-timeout", "120s"],
            cwd=run_dir, check=False, capture_output=True, text=True,
        )
        evidence: dict[str, Any] | None = None
        if evidence_path.exists():
            evidence = json.loads(evidence_path.read_text(encoding="utf-8"))
        # Capture concrete restart evidence: both containers' StartedAt should
        # fall inside the soak window if the real faults executed.
        started_at = {}
        for name in (app_container, db_container):
            probe = subprocess.run(["docker", "inspect", "-f", "{{.State.StartedAt}}", name],
                                   cwd=ROOT, check=False, capture_output=True, text=True)
            started_at[name] = probe.stdout.strip()
        return {
            "status": "pass" if soak.returncode == 0 and evidence and evidence.get("status") == "pass" else "fail",
            "returncode": soak.returncode,
            "evidence": evidence,
            "container_started_at": started_at,
            "stderr_tail": "\n".join(soak.stderr.splitlines()[-20:]),
        }
    finally:
        if started and not keep_stack:
            subprocess.run(["docker", "compose", "-f", str(compose_path), "-p", project, "down", "-v", "--remove-orphans"],
                           cwd=run_dir, check=False, capture_output=True, text=True)
        if built_image and not keep_stack:
            subprocess.run(["docker", "image", "rm", "-f", image], cwd=ROOT, check=False, capture_output=True, text=True)
        if not keep_stack:
            shutil.rmtree(run_dir, ignore_errors=True)


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--contracts-only", action="store_true", help="static contract/code validation only; skip the ephemeral PostgreSQL suite")
    parser.add_argument("--runtime-only", action="store_true", help="skip static validation; run only the ephemeral PostgreSQL suite")
    parser.add_argument("--soak", action="store_true", help="run the real Docker-orchestrated accelerated soak against an ephemeral appliance stack")
    parser.add_argument("--keep-stack", action="store_true", help="with --soak, leave the ephemeral stack and run directory in place for inspection")
    parser.add_argument("--json", action="store_true")
    args = parser.parse_args()

    exclusive = [args.contracts_only, args.runtime_only, args.soak]
    if sum(bool(flag) for flag in exclusive) > 1:
        print("--contracts-only, --runtime-only and --soak are mutually exclusive", file=sys.stderr)
        return 2

    errors: list[str] = []
    runtime_result: dict[str, Any] | None = None
    soak_result: dict[str, Any] | None = None
    try:
        if not args.runtime_only and not args.soak:
            errors = validate(include_code=True, historical=trusted_lock_from_head())
        if args.runtime_only:
            runtime_result = run_postgres_integration_suite()
            if runtime_result["status"] != "pass":
                errors.append("postgres_integration Go suite failed against the ephemeral PostgreSQL instance")
        if args.soak:
            soak_result = run_accelerated_soak(keep_stack=args.keep_stack)
            if soak_result["status"] != "pass":
                errors.append("real Docker-orchestrated accelerated soak failed")
    except (OSError, ValueError, json.JSONDecodeError, RuntimeError, subprocess.SubprocessError) as exc:
        errors.append(str(exc))

    if args.json:
        print(json.dumps({"status": "pass" if not errors else "fail", "errors": errors,
                          "runtime": runtime_result, "soak": soak_result}, indent=2, sort_keys=True))
    else:
        if runtime_result is not None:
            print(runtime_result.get("stdout_tail", ""))
            if runtime_result.get("stderr_tail"):
                print(runtime_result["stderr_tail"], file=sys.stderr)
        if soak_result is not None:
            print(json.dumps(soak_result.get("evidence") or {}, indent=2, sort_keys=True))
            if soak_result.get("stderr_tail"):
                print(soak_result["stderr_tail"], file=sys.stderr)
        if errors:
            for error in errors:
                print(error, file=sys.stderr)
        else:
            print("Session 09 runtime/operations contracts: pass")
    return 1 if errors else 0


if __name__ == "__main__":
    raise SystemExit(main())
