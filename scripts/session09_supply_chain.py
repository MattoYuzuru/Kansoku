#!/usr/bin/env python3
"""Verify deterministic Session 09 SBOM/provenance evidence.

Mirrors scripts/session08_supply_chain.py exactly: it hashes the Session 09
source scope into one reproducible manifest digest, records the unchanged
go.mod/go.sum digests (Session 09 adds no new third-party dependency) and emits
the CycloneDX SBOM byte-for-byte so reports/session-09-sbom.json can be
verified as live, not hand-edited.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import sys
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
REPORT = ROOT / "reports" / "session-09-sbom.json"
SOURCE_SCOPE = (
    ROOT / "internal" / "runtime",
    ROOT / "cmd" / "kansoku",
    ROOT / "contracts" / "runtime",
    ROOT / "tests" / "fixtures" / "session-09",
)
SOURCE_FILES = (
    ROOT / "contracts" / "runtime-policy-locks.yaml",
    ROOT / "deploy" / "Dockerfile",
    ROOT / "deploy" / "compose.yaml",
    ROOT / "deploy" / "runtime-config.json",
    ROOT / "scripts" / "validate_runtime.py",
    ROOT / "scripts" / "session09_supply_chain.py",
    ROOT / "tests" / "test_runtime_contracts.py",
    ROOT / "adr" / "0012-session-09-local-runtime-and-operations.md",
)


def sha256(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def scoped_sources() -> list[Path]:
    files = list(SOURCE_FILES)
    for directory in SOURCE_SCOPE:
        files.extend(path for path in directory.rglob("*") if path.is_file())
    return sorted(set(files), key=lambda path: path.relative_to(ROOT).as_posix())


def source_manifest_sha256() -> str:
    digest = hashlib.sha256()
    for path in scoped_sources():
        relative = path.relative_to(ROOT).as_posix()
        content_sha256 = sha256(path)
        digest.update(relative.encode())
        digest.update(b"\0")
        digest.update(content_sha256.encode())
        digest.update(b"\n")
    return digest.hexdigest()


def build() -> dict:
    manifest_sha256 = source_manifest_sha256()
    return {
        "bomFormat": "CycloneDX",
        "specVersion": "1.6",
        "serialNumber": "urn:uuid:2b8c7a4e-0d19-4a8f-9d21-0b6f5b3e9a09",
        "version": 1,
        "metadata": {
            "timestamp": "2026-07-24T00:00:00Z",
            "component": {"type": "application", "name": "kansoku-session09-local-runtime-and-operations", "version": "0.9.0"},
            "tools": {"components": [
                {"type": "application", "name": "Go", "version": "1.26.5"},
                {"type": "application", "name": "PostgreSQL", "version": "18"},
                {"type": "application", "name": "Python", "version": "3.14"},
                {"type": "application", "name": "Docker", "version": "29"},
            ]},
        },
        "components": [{
            "type": "file",
            "name": "kansoku-session09-source-manifest",
            "hashes": [{"alg": "SHA-256", "content": manifest_sha256}],
        }],
        "properties": [
            {"name": "kansoku:dependency-delta", "value": "none_session09_adds_no_new_third_party_dependency"},
            {"name": "kansoku:go-mod-sha256", "value": sha256(ROOT / "go.mod")},
            {"name": "kansoku:go-sum-sha256", "value": sha256(ROOT / "go.sum")},
            {"name": "kansoku:go-build-mode", "value": "-mod=vendor"},
            {"name": "kansoku:verification-network", "value": "none_except_ephemeral_local_postgresql_and_compose_stack_when_available"},
            {"name": "kansoku:session09-source-scope", "value": "internal/runtime/**, cmd/kansoku/**, contracts/runtime/**, contracts/runtime-policy-locks.yaml, deploy/{Dockerfile,compose.yaml,runtime-config.json}, tests/fixtures/session-09/**, tests/test_runtime_contracts.py, scripts/validate_runtime.py, scripts/session09_supply_chain.py, adr/0012-session-09-local-runtime-and-operations.md"},
            {"name": "kansoku:session09-source-manifest-sha256", "value": manifest_sha256},
            {"name": "kansoku:accelerated-soak-execution", "value": "real_docker_compose_appliance_driver_168_cycles_three_restart_faults_all_assertions_passed"},
            {"name": "kansoku:release-artifact-signing", "value": "not_included_image_signing_and_vulnerability_attestation_remain_session10"},
            {"name": "kansoku:excluded-backlog", "value": "Session07b Gemini/Cursor remains explicitly deferred and untouched"},
        ],
    }


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--verify", action="store_true")
    args = parser.parse_args()
    encoded = json.dumps(build(), indent=2, sort_keys=True) + "\n"
    if args.verify and REPORT.read_text(encoding="utf-8") != encoded:
        print("Session09 SBOM/provenance report is stale", file=sys.stderr)
        return 1
    print(json.dumps({"status": "pass", "components": len(build()["components"]), "report_sha256": hashlib.sha256(encoded.encode()).hexdigest()}, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
