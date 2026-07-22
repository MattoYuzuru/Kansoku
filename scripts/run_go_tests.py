#!/usr/bin/env python3
"""Run the stdlib-only Go suite in the immutable, offline Session 01 toolchain."""

from __future__ import annotations

import os
import subprocess
import sys
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
GO_IMAGE = "golang@sha256:1ecb7edf62a0408027bd5729dfd6b1b8766e578e8df93995b225dfd0944eb651"


def command() -> list[str]:
    return [
        "docker", "run", "--rm", "--network", "none", "--read-only", "--cap-drop", "ALL",
        "--security-opt", "no-new-privileges", "--user", f"{os.getuid()}:{os.getgid()}",
        "--tmpfs", "/tmp:rw,exec,nosuid,nodev,mode=1777",
        "--mount", f"type=bind,src={ROOT},dst=/src,readonly", "--workdir", "/src",
        "--env", "GOCACHE=/tmp/go-cache", "--env", "GOTMPDIR=/tmp/go-tmp", "--env", "HOME=/tmp/home",
        GO_IMAGE, "sh", "-c",
        "mkdir -p /tmp/go-cache /tmp/go-tmp /tmp/home && /usr/local/go/bin/go version && cc --version | head -1 && /usr/local/go/bin/go test -mod=vendor ./... && /usr/local/go/bin/go vet -mod=vendor ./... && CGO_ENABLED=1 /usr/local/go/bin/go test -mod=vendor -race ./internal/...",
    ]


def main() -> int:
    result = subprocess.run(command(), cwd=ROOT, check=False)
    return result.returncode


if __name__ == "__main__":
    sys.exit(main())
