#!/usr/bin/env python3
from __future__ import annotations

import concurrent.futures
import hashlib
import json
import math
import os
import re
import shutil
import subprocess
import tempfile
import time
import urllib.request
import urllib.error
from pathlib import Path
from typing import Any


ROOT = Path(__file__).resolve().parents[3]
HERE = Path(__file__).resolve().parent
OUTPUT = HERE / "raw-results.json"
REQUESTS = 400
WARMUP = 20
RECORDS_PER_REQUEST = 5
GO_IMAGE = "golang@sha256:0178a641fbb4858c5f1b48e34bdaabe0350a330a1b1149aabd498d0699ff5fb2"
PYTHON_IMAGE = "python@sha256:26730869004e2b9c4b9ad09cab8625e81d256d1ce97e72df5520e806b1709f92"
ALPINE_HELPER_IMAGE = "alpine@sha256:fd791d74b68913cbb027c6546007b3f0d3bc45125f797758156952bc2d6daf40"
SAFE_ROW_KEYS = {"received_at", "route", "record_count", "body_bytes", "schema_fingerprint"}
CANARIES = {
    "key": "KAN_S01_RAW_KEY_7f5b2c91",
    "prompt": "KAN_S01_RAW_PROMPT_5e971db4",
    "response": "KAN_S01_RAW_RESPONSE_a1625f08",
    "tool": "KAN_S01_RAW_TOOL_92a1cd63",
    "path": "KAN_S01_RAW_PATH_30d18fb6",
}


def run(*args: str, input_text: str | None = None, check: bool = True) -> subprocess.CompletedProcess[str]:
    return subprocess.run(args, input=input_text, text=True, capture_output=True, check=check)


def percentile(values: list[float], percentile_value: float) -> float:
    ordered = sorted(values)
    return ordered[max(0, math.ceil(percentile_value * len(ordered)) - 1)]


def request_payload() -> bytes:
    payload = {
        "resourceLogs": [
            {"scopeLogs": [{"logRecords": [{"timeUnixNano": str(1_000_000_000 + i)} for i in range(RECORDS_PER_REQUEST)]}]}
        ]
    }
    return json.dumps(payload, separators=(",", ":")).encode()


def adversarial_payloads() -> dict[str, bytes]:
    aliases = {
        "prompt": CANARIES["prompt"],
        "body": CANARIES["response"],
        "message": CANARIES["response"],
        "content": CANARIES["response"],
        "tool_input": CANARIES["tool"],
        "tool_output": CANARIES["tool"],
        "arguments": CANARIES["tool"],
        "command": CANARIES["tool"],
        "cwd": CANARIES["path"],
    }
    payloads = {
        alias: json.dumps(
            {"resourceLogs": [{"scopeLogs": [{"logRecords": [{alias: value}]}]}]},
            separators=(",", ":"),
        ).encode()
        for alias, value in aliases.items()
    }
    payloads["content_bearing_key"] = json.dumps(
        {"resourceLogs": [{"scopeLogs": [{"logRecords": [{CANARIES["key"]: "value"}]}]}]},
        separators=(",", ":"),
    ).encode()
    return payloads


def recursive_canary_matches(value: Any, location: str = "$") -> list[str]:
    matches: list[str] = []
    if isinstance(value, dict):
        for key, child in value.items():
            for canary_id, canary in CANARIES.items():
                if canary in str(key):
                    matches.append(f"{location}.<key>:{canary_id}")
            matches.extend(recursive_canary_matches(child, f"{location}.{key}"))
    elif isinstance(value, list):
        for index, child in enumerate(value):
            matches.extend(recursive_canary_matches(child, f"{location}[{index}]"))
    elif isinstance(value, str):
        for canary_id, canary in CANARIES.items():
            if canary in value:
                matches.append(f"{location}:{canary_id}")
    return matches


def safe_row_schema_violations(rows: list[dict[str, Any]]) -> list[str]:
    violations: list[str] = []
    for index, row in enumerate(rows):
        if set(row) != SAFE_ROW_KEYS:
            violations.append(f"row_{index}:keys")
        expected_types = {
            "received_at": str,
            "route": str,
            "record_count": int,
            "body_bytes": int,
            "schema_fingerprint": str,
        }
        for key, expected_type in expected_types.items():
            if not isinstance(row.get(key), expected_type):
                violations.append(f"row_{index}:{key}_type")
    return violations


def post(port: int, payload: bytes) -> tuple[float, int]:
    request = urllib.request.Request(
        f"http://127.0.0.1:{port}/v1/logs",
        data=payload,
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    started = time.perf_counter()
    with urllib.request.urlopen(request, timeout=10) as response:
        response.read()
        return time.perf_counter() - started, response.status


def rejection_status(port: int, payload: bytes) -> int:
    request = urllib.request.Request(
        f"http://127.0.0.1:{port}/v1/logs",
        data=payload,
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    try:
        with urllib.request.urlopen(request, timeout=10) as response:
            response.read()
            return response.status
    except urllib.error.HTTPError as exc:
        exc.read()
        return exc.code


def wait_for_health(port: int, timeout: float = 30) -> None:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        try:
            with urllib.request.urlopen(f"http://127.0.0.1:{port}/healthz", timeout=1) as response:
                if response.status == 200:
                    return
        except OSError:
            time.sleep(0.05)
    raise RuntimeError("health timeout")


def parse_port(output: str) -> int:
    match = re.search(r":(\d+)\s*$", output)
    if not match:
        raise RuntimeError(f"cannot parse docker port: {output!r}")
    return int(match.group(1))


def inspect_image(tag: str) -> dict[str, Any]:
    raw = run("docker", "image", "inspect", tag).stdout
    image = json.loads(raw)[0]
    return {"id": image["Id"], "size_bytes": image["Size"], "repo_digests": image.get("RepoDigests", [])}


def stats(name: str) -> dict[str, str]:
    raw = run("docker", "stats", "--no-stream", "--format", "{{json .}}", name).stdout.strip()
    data = json.loads(raw)
    return {"cpu_percent": data.get("CPUPerc", ""), "memory_usage": data.get("MemUsage", "")}


def benchmark(name: str, context: Path, tag: str) -> dict[str, Any]:
    build_started = time.perf_counter()
    build = run("docker", "build", "--pull", "-t", tag, str(context))
    build_seconds = time.perf_counter() - build_started
    container = f"kansoku-s01-{name}"
    volume = f"kansoku-s01-{name}-data"
    run("docker", "rm", "-f", container, check=False)
    run("docker", "volume", "rm", "-f", volume, check=False)
    run("docker", "volume", "create", volume)
    run(
        "docker", "run", "--rm", "-v", f"{volume}:/data",
        ALPINE_HELPER_IMAGE, "chown", "65532:65532", "/data",
    )
    payload = request_payload()
    with tempfile.TemporaryDirectory(prefix=f"kansoku-s01-{name}-", dir="/private/tmp") as tmp:
        sink_path = Path(tmp) / "batches.jsonl"
        started = time.perf_counter()
        run(
            "docker", "run", "-d", "--name", container,
            "--read-only", "--tmpfs", "/tmp:rw,noexec,nosuid,size=16m",
            "-e", "SINK_PATH=/data/batches.jsonl",
            "-v", f"{volume}:/data",
            "-p", "127.0.0.1::8080",
            tag,
        )
        try:
            port_result = run("docker", "port", container, "8080/tcp", check=False)
            if port_result.returncode != 0:
                state = run("docker", "inspect", "--format", "{{json .State}}", container, check=False).stdout.strip()
                log_result = run("docker", "logs", container, check=False)
                logs = (log_result.stdout + log_result.stderr).strip()
                raise RuntimeError(f"container failed before port discovery: state={state}, logs={logs}")
            port = parse_port(port_result.stdout)
            wait_for_health(port)
            cold_start_seconds = time.perf_counter() - started
            for _ in range(WARMUP):
                latency, status = post(port, payload)
                if status != 202 or latency <= 0:
                    raise RuntimeError("warmup request failed")
            rejection_statuses = {
                f"content_alias_{alias}": rejection_status(port, unsafe_payload)
                for alias, unsafe_payload in adversarial_payloads().items()
            }
            rejection_statuses["oversized_body"] = rejection_status(port, b" " * ((1 << 20) + 1))
            idle_stats = stats(container)
            request_started = time.perf_counter()
            with concurrent.futures.ThreadPoolExecutor(max_workers=8) as pool:
                samples = list(pool.map(lambda _index: post(port, payload), range(REQUESTS)))
            elapsed = time.perf_counter() - request_started
            latencies = [latency for latency, status in samples if status == 202]
            status_counts: dict[str, int] = {}
            for _latency, status in samples:
                status_counts[str(status)] = status_counts.get(str(status), 0) + 1
            load_stats = stats(container)
            stop = run("docker", "stop", "--time", "10", container)
            run("docker", "cp", f"{container}:/data/batches.jsonl", str(sink_path))
            rows = [json.loads(line) for line in sink_path.read_text(encoding="utf-8").splitlines()]
            persisted_batches = len(rows)
            persisted_records = sum(int(row["record_count"]) for row in rows)
            row_schema_violations = safe_row_schema_violations(rows)
            canary_matches = recursive_canary_matches(rows)
            retained_evidence = [rows[0], rows[-1]] if rows else []
            return {
                "implementation": name,
                "build_seconds": round(build_seconds, 6),
                "build_output_tail": build.stdout.splitlines()[-8:],
                "image": inspect_image(tag),
                "cold_start_seconds": round(cold_start_seconds, 6),
                "idle_stats": idle_stats,
                "load_stats": load_stats,
                "requests": REQUESTS,
                "concurrency": 8,
                "records_per_request": RECORDS_PER_REQUEST,
                "status_counts": status_counts,
                "rejection_statuses": rejection_statuses,
                "throughput_requests_per_second": round(REQUESTS / elapsed, 3),
                "latency_p50_seconds": round(percentile(latencies, 0.50), 6),
                "latency_p95_seconds": round(percentile(latencies, 0.95), 6),
                "latency_p99_seconds": round(percentile(latencies, 0.99), 6),
                "graceful_stop_output": stop.stdout.strip(),
                "persisted_batches": persisted_batches,
                "persisted_records": persisted_records,
                "expected_batches_including_warmup": REQUESTS + WARMUP,
                "expected_records_including_warmup": (REQUESTS + WARMUP) * RECORDS_PER_REQUEST,
                "shutdown_flush_verified": persisted_batches == REQUESTS + WARMUP,
                "persisted_field_allowlist": sorted(SAFE_ROW_KEYS),
                "persisted_row_schema_violations": row_schema_violations,
                "recursive_canary_matches": canary_matches,
                "canary_sha256": {key: hashlib.sha256(value.encode()).hexdigest() for key, value in CANARIES.items()},
                "retained_sanitized_row_evidence": retained_evidence,
                "dependency_surface": "language standard library only",
            }
        finally:
            run("docker", "rm", "-f", container, check=False)
            run("docker", "volume", "rm", "-f", volume, check=False)


def main() -> None:
    if shutil.which("docker") is None:
        raise SystemExit("docker is required")
    docker_version = run("docker", "version", "--format", "{{json .}}").stdout.strip()
    toolchains = {
        "go": {
            "version": run("docker", "run", "--rm", GO_IMAGE, "go", "version").stdout.strip(),
            "base_image": inspect_image(GO_IMAGE),
        },
        "python": {
            "version": run("docker", "run", "--rm", PYTHON_IMAGE, "python3", "--version").stdout.strip(),
            "base_image": inspect_image(PYTHON_IMAGE),
        },
        "volume_owner_helper": {"image": inspect_image(ALPINE_HELPER_IMAGE)},
    }
    results = {
        "schema_version": "kansoku.session-01-backend-benchmark/1",
        "retrieved_at": "2026-07-21",
        "host_architecture": os.uname().machine,
        "docker_version": json.loads(docker_version),
        "toolchains": toolchains,
        "workload": {"requests": REQUESTS, "warmup_requests": WARMUP, "concurrency": 8, "body_limit_bytes": 1 << 20},
        "immutable_inputs": {"go": GO_IMAGE, "python": PYTHON_IMAGE, "volume_owner_helper": ALPINE_HELPER_IMAGE},
        "results": [
            benchmark("go", HERE / "go", "kansoku-session-01-go:local"),
            benchmark("python", HERE / "python", "kansoku-session-01-python:local"),
        ],
        "limitations": [
            "The spike uses OTLP/HTTP JSON structural counting, not production protobuf decoding.",
            "The durable sink stores only sanitized batch metadata; database performance is measured separately.",
            "Container stats are point samples, not the binding 24-hour idle SLO."
        ]
    }
    OUTPUT.write_text(json.dumps(results, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    print(OUTPUT.relative_to(ROOT))


if __name__ == "__main__":
    main()
