#!/usr/bin/env python3
"""Create or verify the deterministic Session 04 module/SBOM evidence.

Session 04 introduces exactly one new direct dependency to the vendored
build, `github.com/jackc/pgx/v5`, plus its resolved runtime transitives
(`pgpassfile`, `pgservicefile`, `puddle/v2`, `golang.org/x/crypto`,
`golang.org/x/sync`). Every OTLP/gRPC-related module from Session 03
(`go.opentelemetry.io/proto/otlp`, `google.golang.org/grpc`, etc.) is already
covered by `reports/session-03-sbom.json` and is intentionally excluded here
so the two reports do not silently duplicate or drift on the same component.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import re
import sys
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
REPORT = ROOT / "reports" / "session-04-sbom.json"
TOOLCHAIN_IMAGE = "docker.io/library/golang:1.26.5-bookworm"
TOOLCHAIN_DIGEST = "sha256:1ecb7edf62a0408027bd5729dfd6b1b8766e578e8df93995b225dfd0944eb651"
POSTGRES_IMAGE = "postgres@sha256:9a8afca54e7861fd90fab5fdf4c42477a6b1cb7d293595148e674e0a3181de15"

# The exact module set this session added to go.mod/vendor. Anything already
# inventoried by reports/session-03-sbom.json is deliberately out of scope.
SESSION04_MODULES = {
    "github.com/jackc/pgx/v5",
    "github.com/jackc/pgpassfile",
    "github.com/jackc/pgservicefile",
    "github.com/jackc/puddle/v2",
    "golang.org/x/crypto",
    "golang.org/x/sync",
}


def sha256(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def tree_sha256(paths: list[Path], base: Path) -> str:
    digest = hashlib.sha256()
    for path in sorted(paths):
        relative = path.relative_to(base).as_posix()
        digest.update(relative.encode() + b"\0" + path.read_bytes() + b"\0")
    return digest.hexdigest()


def components() -> list[dict[str, str]]:
    sums: dict[tuple[str, str], str] = {}
    for line in (ROOT / "go.sum").read_text(encoding="utf-8").splitlines():
        parts = line.split()
        if len(parts) == 3 and not parts[1].endswith("/go.mod"):
            sums[(parts[0], parts[1])] = parts[2]
    result = []
    for line in (ROOT / "vendor" / "modules.txt").read_text(encoding="utf-8").splitlines():
        match = re.fullmatch(r"# ([^ ]+) ([^ ]+)", line)
        if not match:
            continue
        module, version = match.groups()
        if module not in SESSION04_MODULES:
            continue
        checksum = sums.get((module, version))
        if not checksum:
            raise RuntimeError(f"vendored module has no content sum: {module}@{version}")
        result.append({"type": "library", "name": module, "version": version, "go_sum": checksum, "purl": f"pkg:golang/{module}@{version}"})
    missing = SESSION04_MODULES - {item["name"] for item in result}
    if missing:
        raise RuntimeError(f"expected Session04 modules missing from vendor/modules.txt: {sorted(missing)}")
    return sorted(result, key=lambda item: item["name"])


def build() -> dict[str, object]:
    source_files = [path for path in (ROOT / "internal" / "dataplatform").rglob("*") if path.is_file()]
    source_files += [path for path in (ROOT / "contracts" / "data-platform").glob("*.yaml") if path.is_file()]
    source_files += [ROOT / "contracts" / "data-platform-policy-locks.yaml", ROOT / "tests" / "fixtures" / "session-04" / "replay-scenario.json"]
    return {
        "bomFormat": "CycloneDX",
        "specVersion": "1.6",
        "serialNumber": "urn:uuid:c9f6f3fa-3f2e-4c1a-9d0e-9e6b1f0b9a04",
        "version": 1,
        "metadata": {
            "timestamp": "2026-07-22T00:00:00Z",
            "component": {"type": "application", "name": "kansoku-session04-data-platform", "version": "0.4.0"},
            "tools": {"components": [{"type": "application", "name": "Go", "version": "1.26.5"}, {"type": "application", "name": "PostgreSQL", "version": "18"}]},
        },
        "components": components(),
        "properties": [
            {"name": "kansoku:toolchain-image", "value": TOOLCHAIN_IMAGE},
            {"name": "kansoku:toolchain-digest", "value": TOOLCHAIN_DIGEST},
            {"name": "kansoku:database-image-digest", "value": POSTGRES_IMAGE},
            {"name": "kansoku:go-mod-sha256", "value": sha256(ROOT / "go.mod")},
            {"name": "kansoku:go-sum-sha256", "value": sha256(ROOT / "go.sum")},
            {"name": "kansoku:session04-source-sha256", "value": tree_sha256(source_files, ROOT)},
            {"name": "kansoku:verification-network", "value": "none_for_go_toolchain_isolated_bridge_for_ephemeral_postgres"},
            {"name": "kansoku:go-build-mode", "value": "-mod=vendor"},
            {"name": "kansoku:excludes", "value": "OTLP/gRPC modules already inventoried by reports/session-03-sbom.json are out of scope here"},
            {"name": "kansoku:provenance-scope", "value": "unsigned source/module inventory; release image signing and vulnerability scan remain Session09/10"},
        ],
    }


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--write", action="store_true")
    parser.add_argument("--verify", action="store_true")
    args = parser.parse_args()
    try:
        value = build()
        encoded = json.dumps(value, indent=2, sort_keys=True) + "\n"
        if args.write:
            REPORT.write_text(encoded, encoding="utf-8")
        if args.verify:
            if REPORT.read_text(encoding="utf-8") != encoded:
                raise RuntimeError("Session04 SBOM/provenance report is stale")
        print(json.dumps({"status": "pass", "components": len(value["components"]), "report_sha256": hashlib.sha256(encoded.encode()).hexdigest()}, sort_keys=True))
    except (OSError, ValueError, RuntimeError) as exc:
        print(str(exc), file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
