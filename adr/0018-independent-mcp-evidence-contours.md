# ADR 0018 — Independent MCP evidence contours

- Status: accepted
- Date: 2026-07-26
- Owners: Kansoku adapters/data-platform/runtime/dashboard
- Extends: ADR 0008, ADR 0015, ADR 0017

## Context

MCP configuration, protocol connection, primitive exposure and calls are different observations.
Treating a configured server as connected or an `isError=false` result as user-task success would
fabricate semantics. Agent sources also differ: Codex App Server can expose typed MCP items while
Claude's default-safe telemetry may redact third-party identity.

The live backup gate exposed a second consistency boundary: `pg_dump` and a later table-count query
can observe different committed states while ingestion is active, producing a valid archive with
an invalid manifest.

## Decision

1. Store server inventory, advertised primitives, connection transitions and call assertions as
   independent, closed, append-only contours linked to the existing component graph.
2. Own the metadata-only `MCPEvidenceFrame` in `internal/adaptersdk`. Adapter packages map their
   versioned source into that frame; data-platform queries and UI contain no agent-name branch.
3. Never retain arguments, results, error text, URLs, commands, environment, credentials, schemas,
   descriptions or resource URIs. Unknown enum values fail visibly.
4. Count `completed` only for MCP `isError=false`; keep execution, protocol, timeout,
   cancellation, denial, transport loss and incomplete terminal distinct. None proves user-task
   success.
5. Compute uptime only between observed transitions. A configured server survives runtime source
   loss while connection/call contours become `not_observed`.
6. Require complete pagination before exposure or no-demand populations are exact. Preserve the
   inventory row and publish exclusions when a client ignores `nextCursor`.
7. Produce native backups and their table-count/version manifest from one exported
   repeatable-read PostgreSQL snapshot.

## Consequences

- Server and tool profiles can reconcile independent denominators without collapsing missing
  evidence into zero.
- Claude evidence shares the generic model when exact default-safe identity exists; redacted
  identity remains not observed.
- Backup/restore verification is stable under concurrent ingestion.
- A direct MCP proxy is not introduced; its content exposure and operational interference require
  a separate threat model.

## Rejected alternatives

- Infer connection or calls from configuration.
- Treat temporal proximity, tool display name or protocol completion as task success/ownership.
- Enable Claude detailed telemetry automatically to obtain identity.
- Persist raw JSON-RPC and redact later.
- Extrapolate uptime to range boundaries.
- Query backup counts after `pg_dump` on a different database snapshot.
