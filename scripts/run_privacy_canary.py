#!/usr/bin/env python3
"""Run the Go canary and independently search every emitted safe sink."""

from __future__ import annotations

import argparse
import hashlib
import base64
import json
import os
import re
import subprocess
import sys
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
FIXTURE = ROOT / "tests" / "fixtures" / "session-02" / "raw-canary-input.json"
GO_IMAGE = "golang@sha256:1ecb7edf62a0408027bd5729dfd6b1b8766e578e8df93995b225dfd0944eb651"
SECRET_PATTERNS = {
    "openai_key": re.compile(rb"sk-[A-Za-z0-9_-]{16,}"),
    "github_token": re.compile(rb"gh[pousr]_[A-Za-z0-9]{20,}"),
    "aws_access_key": re.compile(rb"AKIA[0-9A-Z]{16}"),
    "private_key": re.compile(rb"-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----"),
    "bearer": re.compile(rb"bearer\s+[A-Za-z0-9._~+/-]{16,}", re.IGNORECASE),
}


def run() -> dict[str, object]:
    fixture = json.loads(FIXTURE.read_text(encoding="utf-8"))
    canaries = {key: value.encode() for key, value in {**fixture["canaries"], **fixture["transformed_canaries"]}.items()}
    command = [
        "docker", "run", "--rm", "--network", "none", "--read-only", "--cap-drop", "ALL",
        "--security-opt", "no-new-privileges", "--user", f"{os.getuid()}:{os.getgid()}",
        "--tmpfs", "/tmp:rw,exec,nosuid,nodev,mode=1777",
        "--mount", f"type=bind,src={ROOT},dst=/src,readonly",
        "--workdir", "/src", "--env", "GOCACHE=/tmp/go-cache", "--env", "GOTMPDIR=/tmp/go-tmp", "--env", "HOME=/tmp/home",
        GO_IMAGE, "sh", "-c",
        "mkdir -p /tmp/go-cache /tmp/go-tmp /tmp/home && /usr/local/go/bin/go run ./cmd/privacy-canary --fixture /src/tests/fixtures/session-02/raw-canary-input.json --emit-sinks-base64",
    ]
    process = subprocess.run(command, cwd=ROOT, check=False, capture_output=True, text=True)
    if process.returncode != 0:
        raise RuntimeError(f"canary command failed: {process.stderr.strip()}")
    report = json.loads(process.stdout)
    payloads = report.pop("sink_payloads_base64", {})
    rejection_payloads = report.pop("rejection_sink_payloads_base64", {})
    expected_sinks = set(item["id"] for item in report["sinks"])
    if set(payloads) != expected_sinks:
        raise RuntimeError("emitted sink set differs from report")
    if set(rejection_payloads) != expected_sinks:
        raise RuntimeError("rejection sink set differs from report")
    evidence_by_id = {item["id"]: item for item in report["sinks"]}
    rejection_evidence_by_id = {item["id"]: item for item in report["rejection_sinks"]}
    decoded: dict[str, bytes] = {}
    external_matches: dict[str, list[str]] = {}
    secret_matches: dict[str, list[str]] = {}
    for sink_id in sorted(payloads):
            raw = base64.b64decode(payloads[sink_id], validate=True)
            decoded[sink_id] = raw
            json.loads(raw)
            rejected_raw = base64.b64decode(rejection_payloads[sink_id], validate=True)
            json.loads(rejected_raw)
            for artifact_kind, artifact in (("accepted", raw), ("rejected", rejected_raw)):
                for canary_id, canary in canaries.items():
                    if canary in artifact:
                        external_matches.setdefault(f"{sink_id}:{artifact_kind}", []).append(canary_id)
                for pattern_id, pattern in SECRET_PATTERNS.items():
                    if pattern.search(artifact):
                        secret_matches.setdefault(f"{sink_id}:{artifact_kind}", []).append(pattern_id)
            evidence = evidence_by_id[sink_id]
            if evidence["bytes"] != len(raw) or evidence["sha256"] != hashlib.sha256(raw).hexdigest():
                raise RuntimeError(f"sink evidence mismatch for {sink_id}")
            rejection_evidence = rejection_evidence_by_id[sink_id]
            if rejection_evidence["bytes"] != len(rejected_raw) or rejection_evidence["sha256"] != hashlib.sha256(rejected_raw).hexdigest():
                raise RuntimeError(f"rejection sink evidence mismatch for {sink_id}")
    if external_matches or secret_matches:
        raise RuntimeError(f"independent sink scan failed: canaries={external_matches}, secrets={secret_matches}")
    export_bytes = decoded["export"]
    backup = json.loads(decoded["backup"])
    if backup.get("export_sha256") != hashlib.sha256(export_bytes).hexdigest():
        raise RuntimeError("backup checksum does not bind the safe export bytes")
    restored_export = base64.b64decode(backup.get("export_bytes_base64", ""), validate=True)
    if restored_export != export_bytes:
        raise RuntimeError("isolated backup does not restore the exact safe export bytes")
    database = json.loads(decoded["database"])
    records = database.get("records", [])
    expected_record_fields = {"record_id", "idempotency_key", "adapter_id", "adapter_version", "source_schema_id", "schema_fingerprint", "observed_at", "received_at", "confidence", "event_type", "outcome", "value_state", "model", "tool", "component_mentions", "prompt_features", "redaction_counts", "lineage"}
    if not records or set(records[0]) != expected_record_fields:
        raise RuntimeError("safe record field assertion failed")
    source_paths = [
        Path("internal/privacy/types.go"), Path("internal/privacy/sanitizer.go"), Path("internal/privacy/strict_json.go"),
        Path("internal/privacy/features.go"), Path("internal/privacy/sinks.go"), Path("cmd/privacy-canary/main.go"),
    ]
    source_hasher = hashlib.sha256()
    for relative in source_paths:
        resolved = (ROOT / relative).resolve(strict=True)
        if ROOT.resolve() not in resolved.parents or resolved.is_symlink() or not resolved.is_file():
            raise RuntimeError(f"unsafe evidence source path: {relative}")
        source_hasher.update(relative.as_posix().encode() + b"\0" + resolved.read_bytes() + b"\0")
    generator_hash = hashlib.sha256((ROOT / "scripts/run_privacy_canary.py").read_bytes() + (ROOT / "cmd/privacy-canary/main.go").read_bytes()).hexdigest()
    report.update({
        "generator": "scripts/run_privacy_canary.py",
        "fixture": "tests/fixtures/session-02/raw-canary-input.json",
        "fixture_sha256": hashlib.sha256(FIXTURE.read_bytes()).hexdigest(),
        "source_revision": "sha256:" + source_hasher.hexdigest(),
        "toolchain_image": "docker.io/library/golang:1.26.5-bookworm",
        "toolchain_digest": GO_IMAGE.split("@", 1)[1],
        "generator_sha256": generator_hash,
    })
    report["independent_external_scan"] = {
        "canary_matches": 0, "secret_format_matches": 0,
        "accepted_artifacts_scanned": 10, "rejection_artifacts_scanned": 10,
        "backup_exact_bytes_match": True, "backup_checksum_match": True,
        "safe_record_exact_fields": True,
    }
    return report


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--verify-report", action="store_true")
    parser.add_argument("--write-report", action="store_true")
    args = parser.parse_args()
    try:
        report = run()
        if args.write_report:
            (ROOT / "reports" / "session-02-canary-results.json").write_text(
                json.dumps(report, indent=2, sort_keys=True) + "\n", encoding="utf-8"
            )
        if args.verify_report:
            persisted = json.loads((ROOT / "reports" / "session-02-canary-results.json").read_text(encoding="utf-8"))
            differences = [key for key, value in report.items() if persisted.get(key) != value]
            if differences:
                raise RuntimeError(f"committed canary evidence differs in fields: {differences}")
    except (OSError, ValueError, RuntimeError, subprocess.SubprocessError) as exc:
        print(str(exc), file=sys.stderr)
        return 1
    print(json.dumps(report, indent=2, sort_keys=True))
    return 0


if __name__ == "__main__":
    sys.exit(main())
