#!/usr/bin/env python3
"""Verify deterministic Session 08 SBOM/provenance evidence."""

from __future__ import annotations

import argparse
import hashlib
import json
import sys
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
REPORT = ROOT / "reports" / "session-08-sbom.json"
SOURCE_SCOPE = (
    ROOT / "internal" / "integrity",
    ROOT / "contracts" / "integrity",
    ROOT / "tests" / "fixtures" / "session-08",
)
SOURCE_FILES = (
    ROOT / "internal" / "observability" / "ingest.go",
    ROOT / "internal" / "observability" / "otlp.go",
    ROOT / "internal" / "observability" / "store.go",
    ROOT / "internal" / "dataplatform" / "observability_handoff.go",
    ROOT / "internal" / "dataplatform" / "partitions.go",
    ROOT / "internal" / "privacy" / "classification.go",
    ROOT / "contracts" / "integrity-policy-locks.yaml",
    ROOT / "scripts" / "validate_integrity.py",
    ROOT / "scripts" / "session08_supply_chain.py",
    ROOT / "tests" / "test_integrity_contracts.py",
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
        "serialNumber": "urn:uuid:7cbd8db5-6b90-4e38-98b6-64f8cf34f408",
        "version": 1,
        "metadata": {
            "timestamp": "2026-07-23T00:00:00Z",
            "component": {"type": "application", "name": "kansoku-session08-integrity-drift-audit", "version": "0.8.0"},
            "tools": {"components": [
                {"type": "application", "name": "Go", "version": "1.26.5"},
                {"type": "application", "name": "PostgreSQL", "version": "18"},
                {"type": "application", "name": "Python", "version": "3.14"},
            ]},
        },
        "components": [{
            "type": "file",
            "name": "kansoku-session08-source-manifest",
            "hashes": [{"alg": "SHA-256", "content": manifest_sha256}],
        }],
        "properties": [
            {"name": "kansoku:dependency-delta", "value": "none_session08_adds_no_new_third_party_dependency"},
            {"name": "kansoku:go-mod-sha256", "value": sha256(ROOT / "go.mod")},
            {"name": "kansoku:go-sum-sha256", "value": sha256(ROOT / "go.sum")},
            {"name": "kansoku:go-build-mode", "value": "-mod=vendor"},
            {"name": "kansoku:verification-network", "value": "none_except_ephemeral_local_postgresql_when_available"},
            {"name": "kansoku:session08-source-scope", "value": "internal/integrity/**, internal/observability/{ingest,otlp,store}.go, internal/dataplatform/{observability_handoff,partitions}.go, internal/privacy/classification.go, contracts/integrity/**, contracts/integrity-policy-locks.yaml, tests/fixtures/session-08/**, tests/test_integrity_contracts.py, scripts/validate_integrity.py, scripts/session08_supply_chain.py"},
            {"name": "kansoku:session08-source-manifest-sha256", "value": manifest_sha256},
            {"name": "kansoku:live-canary-execution", "value": "simulation_only_no_real_provider_process_credentials_or_network"},
            {"name": "kansoku:excluded-backlog", "value": "Session07b Gemini/Cursor remains explicitly deferred and untouched"},
            {"name": "kansoku:provenance-scope", "value": "unsigned source/module inventory; release artifact signing remains Session09/10"},
        ],
    }


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--verify", action="store_true")
    args = parser.parse_args()
    encoded = json.dumps(build(), indent=2, sort_keys=True) + "\n"
    if args.verify and REPORT.read_text(encoding="utf-8") != encoded:
        print("Session08 SBOM/provenance report is stale", file=sys.stderr)
        return 1
    print(json.dumps({"status": "pass", "components": len(build()["components"]), "report_sha256": hashlib.sha256(encoded.encode()).hexdigest()}, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
