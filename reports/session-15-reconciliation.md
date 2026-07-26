# Session 15 reconciliation — MCP observatory

Date: 2026-07-26

Status: **live exit gate green**

## Acceptance result

Session 15 implements independent MCP inventory, connection and call contours without raw protocol
content. Migration 0010 adds server, primitive, connection and call assertion tables with lineage,
adapter/schema versions, confidence and idempotency. The existing component graph supplies
server/tool identities and relations.

The API/UI surface now includes the MCP list, server profile, primitive list and tool profile:

```text
/components/mcp
/components/mcp/{server_id}
/components/mcp/{server_id}/tools/{tool_id}
/api/v1/components/mcp
/api/v1/components/mcp/{server_id}
/api/v1/components/mcp/{server_id}/tools
/api/v1/components/mcp/{server_id}/tools/{tool_id}
```

## Live protocol and agent proof

The namespaced local no-op server negotiated MCP `2025-11-25`, served two paginated tools, emitted
`notifications/tools/list_changed`, returned one `isError=false` result and one deterministic
`isError=true` result. Separate metadata-only assertions cover JSON-RPC error, timeout,
cancellation, policy denial, transport loss and incomplete terminal.

An ephemeral real Codex CLI `0.145.0` run used `gpt-5.6-luna` with medium reasoning. Without
approval bypass the real tool call was denied as a user cancellation. With the bounded bypass
flag, the success tool emitted actual `mcp_tool_call` start/completed items and completed; the
error-first fixture emitted start/completed with failed status. Raw JSONL passed only through an
in-memory allowlist and was never written to repository or durable storage.

Codex 0.145.0 did not request the fixture's second `tools/list` page. Kansoku preserves this as an
incomplete pagination exclusion. The direct protocol client did traverse both pages, proving the
fixture and protocol semantics independently. Claude Code 2.1.197 shares the generic adapter-owned
frame when exact default-safe identity is present; redacted identity remains not observed and
detailed telemetry was not enabled.

## Exact reconciliation

The PostgreSQL fixture proves:

```text
servers=1 primitives=2 connection transitions=5
call starts=7 paired terminals=7 policy denials=1
observable uptime=23s connected=20s ratio=86.9565%
completed=1 execution_error=1 protocol_error=1 timeout=1
cancelled=1 denied=1 transport_lost=1 incomplete=1
fixture p95=57.5ms population=7/7
```

The production generic live canary accepted 23 idempotent records. Its API/database profile
reconciled one server, two primitives, five connection transitions, seven starts, seven paired
terminals and one denial; observable/connected time was 23/20 seconds and live p95 was 87.5 ms.
Replay did not inflate any contour. Injecting an inventory-only partial second server produced
population 1/2 with one incomplete-pagination exclusion while preserving configured inventory and
changing only connection/call support to `not_observed`.

## Privacy, recovery and production

The native privacy canary scanned ten accepted and ten rejection sinks:

```text
content canary matches=0
secret-format matches=0
backup checksum/exact bytes=true
```

Additional MCP bridge scans covered secret-shaped arguments, results, error text, URL, command,
environment, auth and resource URI. Database and production-log scans found zero raw marker
matches.

Migration 0010 was applied to production at `2026-07-26 16:37:53.885369+00`. The tested image
before the final source reconciliation was
`sha256:7cce3c4a4b98d57ada33ea3032898959be8c3cc919227d8508fe240b0ab33aec`
and started healthy at `2026-07-26T17:02:16.644140762Z`.

Backup `backup_8c96219fca64dd4bcf4bf46fc5d1c6b8` (archive SHA-256
`5036b1ae22a98b47d862f6670b352959350c451619fa43361609674244abd209`) captured schema migration
0010 and exact MCP counts 1 server, 2 primitives, 5 connections and 15 call assertions. Two
independent isolated restore-verification runs passed.

The first concurrent backup attempt exposed a race: `pg_dump` and manifest counts used different
snapshots. The final implementation exports one read-only repeatable-read snapshot and uses it for
both the custom dump and all manifest queries. The tagged PostgreSQL suite proves backup/restore
under native runtime conditions.

## Verification

```text
python3 scripts/validate_mcp.py
python3 scripts/validate_component_evidence.py
python3 scripts/validate_codex.py
python3 scripts/validate_claude.py
python3 scripts/validate_data_platform.py --contracts-only --json
python3 scripts/validate_data_platform.py --runtime-only --json
python3 -m unittest discover -s tests -p 'test_*.py'  # 164 pass
python3 scripts/run_privacy_canary.py
go vet ./...
go test ./...
npm --prefix web run typecheck
web/scripts/build-and-embed.sh
```

Headless Chrome verified the production MCP list and server profile against the API. The final pass
also covers the tool route introduced during reconciliation.

## Debug scaffolding and residual risks

- The no-op MCP fixture remains as deterministic test infrastructure. It has no network egress and
  reads no user repository.
- The closed `mcp-evidence` CLI remains as a bounded live-canary/debug handoff; it cannot carry raw
  content.
- Codex App Server is experimental and 0.145.0 ignored `nextCursor`; unknown future shapes
  quarantine visibly.
- Claude exact MCP/tool identity is unsupported when the default-safe source redacts it.
- Protocol/server version, capability details, request/result byte distributions, progress and
  plugin ownership remain `not_observed` unless a reviewed native source supplies them.
- Token/cost attribution and user-task success are explicitly unsupported.
- A direct MCP observer/proxy remains deferred because it is invasive.
- `npm audit` reports one high-severity development-dependency advisory; no forced dependency
  rewrite was made in this scoped session.
