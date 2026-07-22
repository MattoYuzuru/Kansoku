#!/usr/bin/env python3
"""Create or verify the deterministic Session 03 module/SBOM evidence."""

from __future__ import annotations

import argparse
import hashlib
import json
import re
import sys
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
REPORT = ROOT / "reports" / "session-03-sbom.json"
TOOLCHAIN_IMAGE = "docker.io/library/golang:1.26.5-bookworm"
TOOLCHAIN_DIGEST = "sha256:1ecb7edf62a0408027bd5729dfd6b1b8766e578e8df93995b225dfd0944eb651"


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
        checksum = sums.get((module, version))
        if not checksum:
            raise RuntimeError(f"vendored module has no content sum: {module}@{version}")
        result.append({"type": "library", "name": module, "version": version, "go_sum": checksum, "purl": f"pkg:golang/{module}@{version}"})
    return sorted(result, key=lambda item: item["name"])


def build() -> dict[str, object]:
    vendor_files = [path for path in (ROOT / "vendor").rglob("*") if path.is_file()]
    source_files = [path for path in (ROOT / "internal" / "observability").glob("*.go") if path.is_file()]
    source_files += [path for path in (ROOT / "contracts" / "observability").glob("*.yaml") if path.is_file()]
    source_files += [ROOT / "contracts" / "observability-policy-locks.yaml", ROOT / "tests" / "fixtures" / "session-03" / "shared-scenario.json"]
    return {
        "bomFormat": "CycloneDX",
        "specVersion": "1.6",
        "serialNumber": "urn:uuid:8e72be61-1ec1-5f34-b89f-202607210003",
        "version": 1,
        "metadata": {
            "timestamp": "2026-07-21T00:00:00Z",
            "component": {"type": "application", "name": "kansoku-session03-core", "version": "0.3.0"},
            "tools": {"components": [{"type": "application", "name": "Go", "version": "1.26.5"}]},
        },
        "components": components(),
        "properties": [
            {"name": "kansoku:toolchain-image", "value": TOOLCHAIN_IMAGE},
            {"name": "kansoku:toolchain-digest", "value": TOOLCHAIN_DIGEST},
            {"name": "kansoku:go-mod-sha256", "value": sha256(ROOT / "go.mod")},
            {"name": "kansoku:go-sum-sha256", "value": sha256(ROOT / "go.sum")},
            {"name": "kansoku:vendor-tree-sha256", "value": tree_sha256(vendor_files, ROOT)},
            {"name": "kansoku:session03-source-sha256", "value": tree_sha256(source_files, ROOT)},
            {"name": "kansoku:verification-network", "value": "none"},
            {"name": "kansoku:go-build-mode", "value": "-mod=vendor"},
            {"name": "kansoku:provenance-scope", "value": "unsigned source/module inventory; release image signing and vulnerability scan remain Session09/10"}
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
                raise RuntimeError("Session03 SBOM/provenance report is stale")
        print(json.dumps({"status": "pass", "components": len(value["components"]), "report_sha256": hashlib.sha256(encoded.encode()).hexdigest()}, sort_keys=True))
    except (OSError, ValueError, RuntimeError) as exc:
        print(str(exc), file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
