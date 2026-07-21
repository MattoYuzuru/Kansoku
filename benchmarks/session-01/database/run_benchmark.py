#!/usr/bin/env python3
from __future__ import annotations

import concurrent.futures
import json
import os
import re
import shutil
import sqlite3
import subprocess
import tempfile
import threading
import time
from pathlib import Path
from typing import Any, Callable


ROOT = Path(__file__).resolve().parents[3]
HERE = Path(__file__).resolve().parent
OUTPUT = HERE / "raw-results.json"
PROFILES = {"personal_sample": 10_000, "enthusiast_sample": 100_000}
POSTGRES_IMAGE = "postgres@sha256:9a8afca54e7861fd90fab5fdf4c42477a6b1cb7d293595148e674e0a3181de15"
POSTGRES_CONTAINER = "kansoku-s01-postgres"
SQLITE_PERCENTILE_CONT_SQL = """
WITH ordered AS (
  SELECT duration_ms,
         row_number() OVER (ORDER BY duration_ms) - 1 AS zero_rank,
         count(*) OVER () AS population_count
  FROM events
), position AS (
  SELECT (population_count - 1) * 0.95 AS continuous_rank
  FROM ordered LIMIT 1
)
SELECT lower_value.duration_ms
       + (upper_value.duration_ms - lower_value.duration_ms)
         * (position.continuous_rank - CAST(position.continuous_rank AS INTEGER))
FROM position
JOIN ordered AS lower_value
  ON lower_value.zero_rank = CAST(position.continuous_rank AS INTEGER)
JOIN ordered AS upper_value
  ON upper_value.zero_rank = CAST(position.continuous_rank + 0.999999999999 AS INTEGER)
"""


def run(*args: str, input_text: str | None = None, check: bool = True) -> subprocess.CompletedProcess[str]:
    return subprocess.run(args, input=input_text, text=True, capture_output=True, check=check)


def elapsed(operation: Callable[[], Any]) -> tuple[float, Any]:
    started = time.perf_counter()
    result = operation()
    return time.perf_counter() - started, result


def event_rows(count: int):
    base = 1_767_225_600
    for index in range(count):
        yield (
            f"ev-{index}",
            base + (index % 86_400),
            index % 4,
            index % 5_000,
            (index * 17) % 5_000,
            "succeeded" if index % 20 else "failed",
        )


def sqlite_query(connection: sqlite3.Connection, sql: str) -> float:
    duration, rows = elapsed(lambda: connection.execute(sql).fetchall())
    if rows is None:
        raise AssertionError("query produced no result object")
    return duration


def sqlite_concurrency(path: Path, duration_seconds: float = 2.0) -> dict[str, Any]:
    stop = threading.Event()
    counts = {"reads": 0, "writes": 0, "errors": 0}
    lock = threading.Lock()

    def reader() -> None:
        connection = sqlite3.connect(path, timeout=1)
        local = 0
        errors = 0
        while not stop.is_set():
            try:
                connection.execute("SELECT count(*) FROM events WHERE agent_id = 1").fetchone()
                local += 1
            except sqlite3.Error:
                errors += 1
        connection.close()
        with lock:
            counts["reads"] += local
            counts["errors"] += errors

    def writer() -> None:
        connection = sqlite3.connect(path, timeout=1)
        local = 0
        errors = 0
        index = 2_000_000
        while not stop.is_set():
            try:
                connection.execute(
                    "INSERT OR IGNORE INTO events(idempotency_key, observed_at, agent_id, component_id, duration_ms, outcome) VALUES (?, ?, ?, ?, ?, ?)",
                    (f"concurrent-{index}", 1_767_225_600, 1, 1, 1, "succeeded"),
                )
                connection.commit()
                local += 1
                index += 1
            except sqlite3.Error:
                errors += 1
        connection.close()
        with lock:
            counts["writes"] += local
            counts["errors"] += errors

    with concurrent.futures.ThreadPoolExecutor(max_workers=5) as pool:
        futures = [pool.submit(reader) for _ in range(4)] + [pool.submit(writer)]
        time.sleep(duration_seconds)
        stop.set()
        for future in futures:
            future.result()
    counts["duration_seconds"] = duration_seconds
    counts["read_ops_per_second"] = round(counts["reads"] / duration_seconds, 3)
    counts["write_ops_per_second"] = round(counts["writes"] / duration_seconds, 3)
    return counts


def sqlite_benchmark(profile: str, count: int, directory: Path) -> dict[str, Any]:
    path = directory / f"{profile}.sqlite3"
    connection = sqlite3.connect(path)
    connection.execute("PRAGMA journal_mode=WAL")
    connection.execute("PRAGMA synchronous=FULL")
    connection.executescript(
        """
        CREATE TABLE events (
          idempotency_key TEXT PRIMARY KEY,
          observed_at INTEGER NOT NULL,
          agent_id INTEGER NOT NULL,
          component_id INTEGER NOT NULL,
          duration_ms INTEGER NOT NULL,
          outcome TEXT NOT NULL,
          formula_version TEXT
        );
        CREATE INDEX events_agent_time ON events(agent_id, observed_at);
        CREATE INDEX events_component_time ON events(component_id, observed_at);
        """
    )
    insert_seconds, _ = elapsed(
        lambda: connection.executemany(
            "INSERT INTO events(idempotency_key, observed_at, agent_id, component_id, duration_ms, outcome) VALUES (?, ?, ?, ?, ?, ?)",
            event_rows(count),
        )
    )
    connection.commit()
    replay_seconds, _ = elapsed(
        lambda: connection.executemany(
            "INSERT OR IGNORE INTO events(idempotency_key, observed_at, agent_id, component_id, duration_ms, outcome) VALUES (?, ?, ?, ?, ?, ?)",
            event_rows(min(1000, count)),
        )
    )
    connection.commit()
    rows_after_replay = connection.execute("SELECT count(*) FROM events").fetchone()[0]
    queries = {
        "agent_outcome_group": sqlite_query(connection, "SELECT agent_id, outcome, count(*) FROM events GROUP BY agent_id, outcome"),
        "component_top": sqlite_query(connection, "SELECT component_id, count(*) AS c FROM events GROUP BY component_id ORDER BY c DESC LIMIT 25"),
        "duration_p95_exact": sqlite_query(connection, SQLITE_PERCENTILE_CONT_SQL),
        "range_count": sqlite_query(connection, "SELECT count(*) FROM events WHERE agent_id = 1 AND observed_at >= 1767225600 AND observed_at < 1767312000"),
    }
    duration_p95_value = float(connection.execute(SQLITE_PERCENTILE_CONT_SQL).fetchone()[0])
    migration_seconds, _ = elapsed(lambda: connection.execute("ALTER TABLE events ADD COLUMN source_version TEXT"))
    connection.commit()
    connection.execute("PRAGMA wal_checkpoint(TRUNCATE)")
    connection.close()
    storage_bytes = path.stat().st_size
    concurrency = sqlite_concurrency(path)
    verification = sqlite3.connect(path)
    rows_after_concurrency = verification.execute("SELECT count(*) FROM events").fetchone()[0]
    verification.execute("PRAGMA wal_checkpoint(TRUNCATE)")
    verification.close()
    final_storage_bytes = path.stat().st_size
    wal = path.with_name(path.name + "-wal")
    if wal.exists():
        final_storage_bytes += wal.stat().st_size
    return {
        "engine": "sqlite",
        "profile": profile,
        "events": count,
        "version": sqlite3.sqlite_version,
        "insert_seconds": round(insert_seconds, 6),
        "insert_events_per_second": round(count / insert_seconds, 3),
        "duplicate_replay_seconds_1000": round(replay_seconds, 6),
        "rows_after_replay": rows_after_replay,
        "idempotency_verified": rows_after_replay == count,
        "query_seconds": {key: round(value, 6) for key, value in queries.items()},
        "duration_p95_value": duration_p95_value,
        "migration_add_nullable_column_seconds": round(migration_seconds, 6),
        "concurrency": concurrency,
        "storage_bytes": storage_bytes,
        "bytes_per_event": round(storage_bytes / count, 3),
        "rows_after_concurrency": rows_after_concurrency,
        "final_storage_bytes_after_concurrency": final_storage_bytes,
    }


def psql(sql: str, database: str = "postgres") -> str:
    result = run(
        "docker", "exec", "-i", POSTGRES_CONTAINER,
        "psql", "-X", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", "postgres", "-d", database,
        input_text=sql,
    )
    return result.stdout.strip()


def pg_explain_ms(sql: str) -> float:
    raw = psql(f"EXPLAIN (ANALYZE, FORMAT JSON) {sql};")
    plan = json.loads(raw)[0]["Plan"]
    return float(plan["Actual Total Time"]) / 1000.0


def postgres_benchmark(profile: str, count: int) -> dict[str, Any]:
    psql("DROP TABLE IF EXISTS events;")
    psql(
        """
        CREATE TABLE events (
          idempotency_key text PRIMARY KEY,
          observed_at timestamptz NOT NULL,
          agent_id integer NOT NULL,
          component_id integer NOT NULL,
          duration_ms integer NOT NULL,
          outcome text NOT NULL,
          formula_version text
        );
        CREATE INDEX events_agent_time ON events(agent_id, observed_at);
        CREATE INDEX events_component_time ON events(component_id, observed_at);
        """
    )
    insert_sql = f"""
        INSERT INTO events(idempotency_key, observed_at, agent_id, component_id, duration_ms, outcome)
        SELECT 'ev-' || g,
               timestamptz '2026-01-01T00:00:00Z' + ((g % 86400) * interval '1 second'),
               g % 4,
               g % 5000,
               (g * 17) % 5000,
               CASE WHEN g % 20 = 0 THEN 'failed' ELSE 'succeeded' END
        FROM generate_series(0, {count - 1}) AS g;
    """
    insert_seconds, _ = elapsed(lambda: psql(insert_sql))
    replay_sql = f"""
        INSERT INTO events(idempotency_key, observed_at, agent_id, component_id, duration_ms, outcome)
        SELECT 'ev-' || g, timestamptz '2026-01-01T00:00:00Z', g % 4, g % 5000, 1, 'succeeded'
        FROM generate_series(0, {min(999, count - 1)}) AS g
        ON CONFLICT (idempotency_key) DO NOTHING;
    """
    replay_seconds, _ = elapsed(lambda: psql(replay_sql))
    rows_after_replay = int(psql("SELECT count(*) FROM events;"))
    queries = {
        "agent_outcome_group": pg_explain_ms("SELECT agent_id, outcome, count(*) FROM events GROUP BY agent_id, outcome"),
        "component_top": pg_explain_ms("SELECT component_id, count(*) AS c FROM events GROUP BY component_id ORDER BY c DESC LIMIT 25"),
        "duration_p95_exact": pg_explain_ms("SELECT percentile_cont(0.95) WITHIN GROUP (ORDER BY duration_ms) FROM events"),
        "range_count": pg_explain_ms("SELECT count(*) FROM events WHERE agent_id = 1 AND observed_at >= '2026-01-01' AND observed_at < '2026-01-02'"),
    }
    duration_p95_value = float(psql("SELECT percentile_cont(0.95) WITHIN GROUP (ORDER BY duration_ms) FROM events;"))
    migration_seconds, _ = elapsed(lambda: psql("ALTER TABLE events ADD COLUMN source_version text;"))
    storage_bytes = int(psql("SELECT pg_total_relation_size('events');"))
    query_file = "SELECT count(*) FROM events WHERE agent_id = 1;\n"
    run("docker", "exec", "-i", POSTGRES_CONTAINER, "sh", "-c", "cat > /tmp/kansoku-query.sql", input_text=query_file)
    pgbench = run(
        "docker", "exec", POSTGRES_CONTAINER,
        "pgbench", "-n", "-c", "4", "-j", "2", "-T", "2", "-f", "/tmp/kansoku-query.sql", "-U", "postgres", "postgres",
    ).stdout
    tps_match = re.search(r"tps = ([0-9.]+)", pgbench)
    return {
        "engine": "postgresql",
        "profile": profile,
        "events": count,
        "version": psql("SHOW server_version;"),
        "insert_seconds": round(insert_seconds, 6),
        "insert_events_per_second": round(count / insert_seconds, 3),
        "duplicate_replay_seconds_1000": round(replay_seconds, 6),
        "rows_after_replay": rows_after_replay,
        "idempotency_verified": rows_after_replay == count,
        "query_seconds": {key: round(value, 6) for key, value in queries.items()},
        "duration_p95_value": duration_p95_value,
        "migration_add_nullable_column_seconds": round(migration_seconds, 6),
        "concurrency": {"clients": 4, "threads": 2, "duration_seconds": 2, "tps_including_connection_overhead": float(tps_match.group(1)) if tps_match else None, "raw": pgbench.splitlines()},
        "storage_bytes": storage_bytes,
        "bytes_per_event": round(storage_bytes / count, 3),
    }


def main() -> None:
    if shutil.which("docker") is None:
        raise SystemExit("docker is required")
    results: list[dict[str, Any]] = []
    with tempfile.TemporaryDirectory(prefix="kansoku-s01-sqlite-") as tmp:
        directory = Path(tmp)
        for profile, count in PROFILES.items():
            results.append(sqlite_benchmark(profile, count, directory))

    run("docker", "rm", "-f", POSTGRES_CONTAINER, check=False)
    start = run(
        "docker", "run", "-d", "--name", POSTGRES_CONTAINER,
        "-e", "POSTGRES_HOST_AUTH_METHOD=trust",
        "--tmpfs", "/var/lib/postgresql:rw,noexec,nosuid,size=1g",
        POSTGRES_IMAGE,
        check=False,
    )
    if start.returncode != 0:
        raise RuntimeError(f"PostgreSQL container start failed: {(start.stdout + start.stderr).strip()}")
    try:
        deadline = time.monotonic() + 60
        while time.monotonic() < deadline:
            ready = run("docker", "exec", POSTGRES_CONTAINER, "pg_isready", "-U", "postgres", check=False)
            if ready.returncode == 0:
                break
            time.sleep(0.25)
        else:
            log_result = run("docker", "logs", POSTGRES_CONTAINER, check=False)
            logs = (log_result.stdout + log_result.stderr).strip()
            state = run("docker", "inspect", "--format", "{{json .State}}", POSTGRES_CONTAINER, check=False).stdout.strip()
            raise RuntimeError(f"PostgreSQL readiness timeout: state={state}, logs={logs}")
        for profile, count in PROFILES.items():
            results.append(postgres_benchmark(profile, count))
        image = json.loads(run("docker", "image", "inspect", POSTGRES_IMAGE).stdout)[0]
        postgres_image = {"id": image["Id"], "repo_digests": image.get("RepoDigests", []), "size_bytes": image["Size"]}
    finally:
        run("docker", "rm", "-f", POSTGRES_CONTAINER, check=False)

    projections = []
    for engine in ("sqlite", "postgresql"):
        sample = next(item for item in results if item["engine"] == engine and item["profile"] == "enthusiast_sample")
        projections.append({
            "engine": engine,
            "stress_profile_logical_events": 1_000_000 * 1826,
            "uncompressed_linear_projection_bytes": round(sample["bytes_per_event"] * 1_000_000 * 1826),
            "warning": "Capacity projection only; partitioning, compression, rollups, indexes, and retention must be measured in Session 04."
        })
    output = {
        "schema_version": "kansoku.session-01-database-benchmark/1",
        "retrieved_at": "2026-07-21",
        "profiles": PROFILES,
        "results": results,
        "postgres_image": postgres_image,
        "immutable_inputs": {"postgres": POSTGRES_IMAGE},
        "quantile_semantics": {
            "method": "percentile_cont",
            "probability": 0.95,
            "interpolation": "linear between zero-based ranks floor((n-1)*p) and ceil((n-1)*p)",
            "postgresql_equivalent": "percentile_cont(0.95) WITHIN GROUP (ORDER BY duration_ms)",
        },
        "stress_projections": projections,
        "paper_evaluation": {
            "duckdb": "Retain for privacy-safe offline Parquet analysis; not the concurrent system of record.",
            "clickhouse": "Reject for MVP baseline: operational cost exceeds measured local workload need unless Session 04 invalidates PostgreSQL budgets."
        },
        "limitations": [
            "Samples represent one day at personal/enthusiast event rates, not full multi-year retention.",
            "SQLite and PostgreSQL loaders use equivalent deterministic rows but different client paths.",
            "The stress profile is projected, not materialized; Session 04 owns the million-event dataset and query plans."
        ]
    }
    OUTPUT.write_text(json.dumps(output, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    print(OUTPUT.relative_to(ROOT))


if __name__ == "__main__":
    main()
