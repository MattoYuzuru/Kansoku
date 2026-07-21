#!/usr/bin/env python3
from __future__ import annotations

import hashlib
import json
import platform
import subprocess
import time
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
HERE = Path(__file__).resolve().parent


def run_script(relative: str) -> dict[str, object]:
    started = time.perf_counter()
    try:
        result = subprocess.run(
            ["python3", str(HERE / relative)],
            cwd=ROOT,
            text=True,
            capture_output=True,
            check=True,
        )
    except subprocess.CalledProcessError as exc:
        raise RuntimeError(
            f"benchmark {relative} failed\nstdout:\n{exc.stdout}\nstderr:\n{exc.stderr}"
        ) from exc
    return {
        "script": relative,
        "elapsed_seconds": round(time.perf_counter() - started, 6),
        "stdout": result.stdout.splitlines(),
        "stderr": result.stderr.splitlines(),
    }


def main() -> None:
    started_at = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
    artifact_names = [
        "backend/raw-results.json",
        "database/raw-results.json",
        "frontend/raw-results.json",
    ]
    runs = [
        run_script("backend/run_benchmark.py"),
        run_script("database/run_benchmark.py"),
        run_script("frontend/run_benchmark.py"),
    ]
    indexed_artifacts = []
    for name in artifact_names:
        path = HERE / name
        content = path.read_bytes()
        indexed_artifacts.append({
            "path": str(path.relative_to(ROOT)),
            "sha256": hashlib.sha256(content).hexdigest(),
            "bytes": len(content),
        })
    manifest = {
        "schema_version": "kansoku.session-01-benchmark-run/1",
        "started_at": started_at,
        "finished_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
        "host": {
            "machine": platform.machine(),
            "platform": platform.platform(),
            "python": platform.python_version(),
            "privacy": "No username, hostname, serial number, hardware UUID, environment value, or filesystem path recorded."
        },
        "mode": "full_run",
        "runs": runs,
        "artifacts": indexed_artifacts,
    }
    path = HERE / "run-manifest.json"
    path.write_text(json.dumps(manifest, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    print(path.relative_to(ROOT))


if __name__ == "__main__":
    main()
