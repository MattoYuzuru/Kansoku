#!/usr/bin/env python3
from __future__ import annotations

import gzip
import json
import shutil
import subprocess
import time
from pathlib import Path
from typing import Any


ROOT = Path(__file__).resolve().parents[3]
HERE = Path(__file__).resolve().parent
OUTPUT = HERE / "raw-results.json"


def run(*args: str) -> tuple[float, subprocess.CompletedProcess[str]]:
    started = time.perf_counter()
    result = subprocess.run(args, cwd=HERE, text=True, capture_output=True, check=True)
    return time.perf_counter() - started, result


def bundle_sizes(name: str) -> dict[str, Any]:
    directory = HERE / "dist" / name
    files = [path for path in directory.rglob("*") if path.is_file()]
    measured = []
    for path in files:
        content = path.read_bytes()
        measured.append({
            "path": str(path.relative_to(directory)),
            "bytes": len(content),
            "gzip_bytes": len(gzip.compress(content, compresslevel=9)),
        })
    return {
        "files": measured,
        "total_bytes": sum(item["bytes"] for item in measured),
        "total_gzip_bytes": sum(item["gzip_bytes"] for item in measured),
        "javascript_bytes": sum(item["bytes"] for item in measured if item["path"].endswith(".js")),
        "javascript_gzip_bytes": sum(item["gzip_bytes"] for item in measured if item["path"].endswith(".js")),
    }


def accessibility_surface(source: Path) -> dict[str, bool]:
    text = source.read_text(encoding="utf-8") + (HERE / "src" / "AccessibleTable.jsx").read_text(encoding="utf-8")
    return {
        "chart_has_accessible_name": "aria-label" in text,
        "data_table_equivalent": "<table>" in text and "<caption>" in text,
        "keyboard_native_controls": "<button" in text,
        "status_announced": 'role="status"' in text,
        "reduced_motion_css": "prefers-reduced-motion" in (HERE / "src" / "styles.css").read_text(encoding="utf-8"),
    }


def main() -> None:
    if shutil.which("npm") is None:
        raise SystemExit("npm is required")
    lock = HERE / "package-lock.json"
    if not lock.exists():
        raise SystemExit("package-lock.json is required; benchmark inputs must be immutable")
    install_seconds, install = run("npm", "ci", "--ignore-scripts", "--no-audit", "--no-fund")
    builds = {}
    for name in ("echarts", "uplot"):
        build_seconds, result = run("npm", "run", f"build:{name}")
        builds[name] = {
            "build_seconds": round(build_seconds, 6),
            "build_output": result.stdout.splitlines()[-12:],
            "bundle": bundle_sizes(name),
            "accessibility_surface": accessibility_surface(HERE / "src" / ("AppECharts.jsx" if name == "echarts" else "AppUPlot.jsx")),
        }
    _, npm_tree = run("npm", "ls", "--depth=0", "--json")
    dependencies = {name: value.get("version") for name, value in json.loads(npm_tree.stdout).get("dependencies", {}).items()}
    audit_process = subprocess.run(
        ["npm", "audit", "--json"], cwd=HERE, text=True, capture_output=True, check=False
    )
    audit = json.loads(audit_process.stdout)
    output = {
        "schema_version": "kansoku.session-01-frontend-benchmark/1",
        "retrieved_at": "2026-07-21",
        "node_version": subprocess.run(["node", "--version"], text=True, capture_output=True, check=True).stdout.strip(),
        "npm_version": subprocess.run(["npm", "--version"], text=True, capture_output=True, check=True).stdout.strip(),
        "immutable_inputs": {"package_lock_sha256": __import__("hashlib").sha256(lock.read_bytes()).hexdigest()},
        "install_seconds": round(install_seconds, 6),
        "install_output_tail": install.stdout.splitlines()[-8:],
        "resolved_dependencies": dependencies,
        "npm_audit": {
            "exit_code": audit_process.returncode,
            "vulnerabilities": audit.get("metadata", {}).get("vulnerabilities", {}),
            "total_dependencies": audit.get("metadata", {}).get("dependencies", {}).get("total")
        },
        "results": builds,
        "capability_review": {
            "echarts": {"dense_time_series": "native", "funnel": "native", "linked_filtering": "native group/connect plus app state", "export": "native SVG/PNG data URL", "accessibility": "requires Kansoku-authored ARIA summaries and data tables"},
            "uplot": {"dense_time_series": "native and compact", "funnel": "custom DOM implementation", "linked_filtering": "custom application state", "export": "custom canvas export", "accessibility": "requires Kansoku-authored ARIA summaries and data tables"}
        },
        "limitations": [
            "Bundle measurements are production Vite builds without source maps.",
            "The spike checks the accessibility surface statically; Session 10 owns browser/assistive-technology testing.",
            "No remote fonts, CDN assets, analytics, or telemetry are included."
        ]
    }
    OUTPUT.write_text(json.dumps(output, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    print(OUTPUT.relative_to(ROOT))


if __name__ == "__main__":
    main()
