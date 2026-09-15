#!/usr/bin/env python3
"""Replay the exact Claude Code 2.1.220 `skill_activated` wire shape.

The 2026-08-01 audit injector
(`reports/artifacts/2026-08-01-component-audit/evidence/skills/otlp_skill_inject.py`)
sends `skill.name` alone. That is enough to observe defect A's symptom but not
enough to reproduce defect A-bis, which needs `plugin.name` (so the owner
prefix is applied) together with `skill.source` (so the resolver's scope filter
is poisoned). This script sends the full captured attribute set:

    event.name="skill_activated"  skill.name="<plugin>:<skill>"
    plugin.name="<plugin>"        skill.source="plugin"
    invocation_trigger="claude-proactive"  marketplace.name="<marketplace>"

The wire codec is imported verbatim from the audit injector rather than
duplicated, so the two artifacts can never drift apart.

Usage:
  python3 otlp_skill_wire_replay.py <endpoint> <bearer> <session_id> \
      <plugin>:<skill> [<plugin>:<skill> ...]
"""

import pathlib
import sys
import time
import urllib.error
import urllib.request

AUDIT = (
    pathlib.Path(__file__).resolve().parents[1]
    / "2026-08-01-component-audit" / "evidence" / "skills"
)
sys.path.insert(0, str(AUDIT))
from otlp_skill_inject import build_request, log_record  # noqa: E402

MARKETPLACE = "yuzuru-engineering"


def main() -> int:
    if len(sys.argv) < 5:
        print(__doc__)
        return 2
    endpoint, bearer, session_id = sys.argv[1], sys.argv[2], sys.argv[3]
    qualified = sys.argv[4:]

    now_ns = int(time.time() * 1_000_000_000)
    records = []
    for index, name in enumerate(qualified):
        plugin = name.split(":", 1)[0] if ":" in name else ""
        attributes = [
            ("event.name", "skill_activated"),
            ("session.id", session_id),
            ("skill.name", name),
            ("invocation_trigger", "claude-proactive"),
            ("event.sequence", str(index + 1)),
        ]
        if plugin:
            attributes += [
                ("plugin.name", plugin),
                ("skill.source", "plugin"),
                ("marketplace.name", MARKETPLACE),
            ]
        records.append(log_record(attributes, now_ns + index * 1_000_000))

    payload = build_request("claude-code", "com.anthropic.claude_code.events", records)
    request = urllib.request.Request(
        endpoint.rstrip("/") + "/v1/logs",
        data=payload,
        headers={
            "Content-Type": "application/x-protobuf",
            "Authorization": "Bearer " + bearer,
        },
        method="POST",
    )
    try:
        with urllib.request.urlopen(request, timeout=20) as response:
            print("HTTP", response.status, response.read()[:400])
    except urllib.error.HTTPError as error:
        print("HTTP", error.code, error.read()[:400])
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
