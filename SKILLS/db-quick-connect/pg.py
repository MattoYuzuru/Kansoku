#!/usr/bin/env python3
"""Quick, credential-safe Postgres access for local Kansoku debugging.

Never hardcodes a password. Connection details are resolved, in order, from:

  1. KANSOKU_TEST_POSTGRES_DSN - set by scripts/validate_runtime.py /
     validate_data_platform.py / validate_integrity.py when an ephemeral,
     host-reachable Postgres 18 is already up for the postgres_integration
     test suite. If present, it is used as-is via `psql <dsn>`.
  2. Otherwise, `docker compose -f deploy/compose.yaml exec postgres psql ...`
     against the dev stack brought up per README.md's "Быстрый старт". This is
     the only path in: deploy/compose.yaml gives the postgres service no
     host-reachable port (kansoku-internal is `internal: true`). The local
     unix-socket connection docker exec makes inside the container uses trust
     auth, so no password is ever needed here. deploy/secrets/database_password
     is read only by the kansoku binary itself, per deploy/runtime-config.json's
     secret_files map.

Usage:
  python3 SKILLS/db-quick-connect/pg.py schemas
  python3 SKILLS/db-quick-connect/pg.py tables [schema]     # default: public
  python3 SKILLS/db-quick-connect/pg.py query "<SQL>"
"""
from __future__ import annotations

import os
import re
import subprocess
import sys
from pathlib import Path

DEPLOY_DIR = Path(__file__).resolve().parents[2] / "deploy"
DB_NAME = "kansoku"
DB_USER = "kansoku"
IDENT_RE = re.compile(r"^[A-Za-z_][A-Za-z0-9_]*$")


def run_sql(sql: str) -> int:
    dsn = os.environ.get("KANSOKU_TEST_POSTGRES_DSN")
    if dsn:
        cmd = ["psql", dsn, "-c", sql]
        return subprocess.run(cmd).returncode
    cmd = [
        "docker", "compose", "-f", "compose.yaml", "exec", "-T", "postgres",
        "psql", "-U", DB_USER, "-d", DB_NAME, "-c", sql,
    ]
    return subprocess.run(cmd, cwd=DEPLOY_DIR).returncode


def main(argv: list[str]) -> int:
    if not argv:
        print(__doc__)
        return 2
    cmd, *rest = argv

    if cmd == "schemas":
        return run_sql("SELECT schema_name FROM information_schema.schemata ORDER BY 1;")

    if cmd == "tables":
        schema = rest[0] if rest else "public"
        if not IDENT_RE.match(schema):
            print(f"invalid schema name: {schema!r}", file=sys.stderr)
            return 2
        return run_sql(
            "SELECT table_schema, table_name FROM information_schema.tables "
            f"WHERE table_schema = '{schema}' ORDER BY 1, 2;"
        )

    if cmd == "query":
        if not rest:
            print("query requires a SQL string argument", file=sys.stderr)
            return 2
        return run_sql(rest[0])

    print(f"unknown command: {cmd}", file=sys.stderr)
    print(__doc__)
    return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
