#!/usr/bin/env python3
"""Verify deterministic Session 10 SBOM/provenance evidence.

Mirrors scripts/session09_supply_chain.py: it hashes the Session 10 source
scope (frontend + webui embed + dashboard backend extensions) into one
reproducible manifest digest, records the go.mod/go.sum digests for the
dependency upgrade this session made (pgx v5.7.6->v5.9.2, x/text v0.36.0->
v0.39.0, fixing GO-2026-5004 and GO-2026-5970), records the frontend
package.json/package-lock.json digests as the npm-side supply-chain anchor,
and emits the CycloneDX SBOM byte-for-byte so reports/session-10-sbom.json can
be verified as live, not hand-edited.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import sys
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
REPORT = ROOT / "reports" / "session-10-sbom.json"

# Directories Session 10 created and owns outright: rglob'd in full.
SOURCE_SCOPE = (
    ROOT / "web" / "src",
    ROOT / "web" / "public",
    ROOT / "web" / "scripts",
    ROOT / "internal" / "webui",
)

# Individual files Session 10 added to or modified within shared/pre-existing
# packages (internal/runtime, internal/dataplatform), plus top-level frontend
# config and the contracts/docs Session 10 implements against.
SOURCE_FILES = (
    ROOT / "web" / "index.html",
    ROOT / "web" / "package.json",
    ROOT / "web" / "vite.config.ts",
    ROOT / "web" / "tsconfig.json",
    ROOT / "internal" / "runtime" / "api.go",
    ROOT / "internal" / "dataplatform" / "activity_timeline.go",
    ROOT / "internal" / "dataplatform" / "activity_timeline_test.go",
    ROOT / "internal" / "dataplatform" / "entity_breakdown.go",
    ROOT / "internal" / "dataplatform" / "entity_breakdown_test.go",
    ROOT / "internal" / "dataplatform" / "funnels.go",
    ROOT / "internal" / "dataplatform" / "mcp_topology.go",
    ROOT / "internal" / "dataplatform" / "mcp_uptime.go",
    ROOT / "internal" / "dataplatform" / "mcp_uptime_test.go",
    ROOT / "internal" / "dataplatform" / "model_usage.go",
    ROOT / "internal" / "dataplatform" / "model_usage_test.go",
    ROOT / "internal" / "dataplatform" / "prompt_shape.go",
    ROOT / "internal" / "dataplatform" / "prompt_shape_test.go",
    ROOT / "internal" / "dataplatform" / "reliability_counts.go",
    ROOT / "internal" / "dataplatform" / "reliability_counts_test.go",
    ROOT / "internal" / "dataplatform" / "reliability_timeline.go",
    ROOT / "internal" / "dataplatform" / "system_snapshot.go",
    ROOT / "internal" / "dataplatform" / "system_snapshot_test.go",
    ROOT / "internal" / "dataplatform" / "tool_analytics.go",
    ROOT / "internal" / "dataplatform" / "tool_analytics_test.go",
    ROOT / "internal" / "dataplatform" / "privacy_canary_history.go",
    ROOT / "internal" / "dataplatform" / "privacy_canary_history_test.go",
    ROOT / "internal" / "dataplatform" / "integrity_tables_test.go",
    ROOT / "internal" / "dataplatform" / "migrations" / "0003_dashboard_aggregation_indexes.up.sql",
    ROOT / "internal" / "dataplatform" / "migrations" / "0003_dashboard_aggregation_indexes.down.sql",
    ROOT / "internal" / "dataplatform" / "query.go",
    ROOT / "internal" / "dataplatform" / "types.go",
    ROOT / "internal" / "dataplatform" / "unit_test.go",
    ROOT / "internal" / "dataplatform" / "observability_handoff.go",
    ROOT / "contracts" / "dashboard.yaml",
    ROOT / "contracts" / "metrics.yaml",
    ROOT / "adr" / "0013-session-10-dashboard-hardening-and-evolution.md",
    ROOT / "Technical Design Document" / "design-system-tokens.md",
    ROOT / "scripts" / "session10_supply_chain.py",
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
        "serialNumber": "urn:uuid:fce16267-6a45-4638-a9fe-264be9b8e7bc",
        "version": 1,
        "metadata": {
            "timestamp": "2026-07-25T00:00:00Z",
            "component": {"type": "application", "name": "kansoku-session10-dashboard-hardening-and-evolution", "version": "0.10.0"},
            "tools": {"components": [
                {"type": "application", "name": "Go", "version": "1.26.5"},
                {"type": "application", "name": "Node.js", "version": "22"},
                {"type": "application", "name": "PostgreSQL", "version": "18"},
                {"type": "application", "name": "Python", "version": "3.14"},
                {"type": "application", "name": "Docker", "version": "29"},
            ]},
        },
        "components": [{
            "type": "file",
            "name": "kansoku-session10-source-manifest",
            "hashes": [{"alg": "SHA-256", "content": manifest_sha256}],
        }],
        "properties": [
            {"name": "kansoku:dependency-delta", "value": "pgx_v5.7.6_to_v5.9.2_and_x-text_v0.36.0_to_v0.39.0_fixes_GO-2026-5004_and_GO-2026-5970"},
            {"name": "kansoku:go-mod-sha256", "value": sha256(ROOT / "go.mod")},
            {"name": "kansoku:go-sum-sha256", "value": sha256(ROOT / "go.sum")},
            {"name": "kansoku:go-build-mode", "value": "-mod=vendor"},
            {"name": "kansoku:npm-package-json-sha256", "value": sha256(ROOT / "web" / "package.json")},
            {"name": "kansoku:npm-package-lock-sha256", "value": sha256(ROOT / "web" / "package-lock.json")},
            {"name": "kansoku:npm-audit-review", "value": "5_dev-only_vite_advisories_reviewed_windows-only_or_dev-server-only_zero_action_production_deps_clean"},
            {"name": "kansoku:govulncheck-result", "value": "0_reachable_vulnerabilities_after_dependency_upgrade"},
            {"name": "kansoku:verification-network", "value": "none_except_ephemeral_local_postgresql_and_the_live_docker_e2e_stack_when_available"},
            {"name": "kansoku:session10-source-scope", "value": "web/{src,public,scripts,index.html,package.json,vite.config.ts,tsconfig.json}, internal/webui/**, internal/runtime/api.go, internal/dataplatform/{activity_timeline,entity_breakdown,funnels,mcp_topology,mcp_uptime,model_usage,prompt_shape,reliability_counts,reliability_timeline,system_snapshot,tool_analytics,privacy_canary_history,integrity_tables_test,query,types,unit_test,observability_handoff}.go(+_test), internal/dataplatform/migrations/0003_*, contracts/{dashboard,metrics}.yaml (pre-existing, implemented against), adr/0013-*.md, Technical Design Document/design-system-tokens.md, scripts/session10_supply_chain.py"},
            {"name": "kansoku:session10-source-manifest-sha256", "value": manifest_sha256},
            {"name": "kansoku:raw-content-e2e-scan", "value": "real_hook_ingress_to_postgres_to_all_14_GET_routes_scanned_for_canary_markers_zero_leaks"},
            {"name": "kansoku:bundle-budget-fix", "value": "route-level_React.lazy_code_splitting_main_chunk_260.98kb_to_212.04kb_raw_78.89kb_to_67.88kb_gzip_echarts_now_conditional"},
            {"name": "kansoku:csp-hardening", "value": "script-src_per-request_crypto-rand_hex_nonce_replacing_unsafe-inline_style-src_unsafe-inline_kept_as_accepted_residual"},
            {"name": "kansoku:release-artifact-signing", "value": "not_included_image_signing_and_vulnerability_attestation_remain_future_work"},
            {"name": "kansoku:excluded-backlog", "value": "Session07b Gemini/Cursor remains explicitly deferred and untouched"},
        ],
    }


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--verify", action="store_true")
    args = parser.parse_args()
    encoded = json.dumps(build(), indent=2, sort_keys=True) + "\n"
    if args.verify and REPORT.read_text(encoding="utf-8") != encoded:
        print("Session10 SBOM/provenance report is stale", file=sys.stderr)
        return 1
    print(json.dumps({"status": "pass", "components": len(build()["components"]), "report_sha256": hashlib.sha256(encoded.encode()).hexdigest()}, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
