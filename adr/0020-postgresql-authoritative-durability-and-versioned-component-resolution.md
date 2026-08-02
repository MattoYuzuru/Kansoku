# ADR 0020: PostgreSQL-authoritative durability and versioned component resolution

- Status: Accepted
- Date: 2026-07-28
- Decision owners: SRE and implementation
- Incident: compatibility mirror saturation and false-green skill/plugin evidence

## Context

Production ingestion synchronously rewrote and fsynced the complete compatibility
`mirror/state.json` before PostgreSQL. At 67,107,702 of 67,108,864 bytes this path was 1.1 KiB from
backpressure, consumed up to 108% of one CPU and 390–629 MiB RSS, and was invisible to health.
PostgreSQL itself had no 64 MiB limit. Component evidence also lacked kind and qualified ownership,
so plugin assertions polluted skill exclusions and unresolved observations could not be safely
re-linked when inventory changed.

## Decision

1. PostgreSQL is the authoritative durable store for normalized facts, evidence, quarantine
   metadata and projections. A sanitized record is acknowledged only after PostgreSQL or a bounded
   per-source emergency spool owns it.
2. Local durable JSON is a compact 4 MiB checkpoint store containing only bounded watermarks,
   importer offsets and replay metadata. `spool_max_bytes` remains a 64 MiB per-lane emergency
   limit and is never reused as a database or mirror limit.
3. Legacy mirror cutover is backup, checksum/lineage reconciliation, durable report and atomic
   archive. Kansoku does not delete the archive; deletion requires a separate preview and
   confirmation.
4. Database capacity uses a configurable 5 GiB soft budget with 70/85/95 percent advisory
   thresholds. Filesystem, backups, indexes, temporary bytes, checkpoints and spools are separate
   observations. WAL/headroom stays `not_observed` when the application role cannot measure it.
5. Fresh component assertions carry component kind, qualified/owner identity, identity source,
   invocation mode, upstream identity HMAC and resolution version. Assertions remain immutable;
   inventory changes append resolution history and consumers read a current-resolution view.
6. Agent-specific mapping remains in adapters. Claude 2.1.197 native skill/plugin metadata and
   Codex 0.145.0 bridge/rollout evidence map to the shared component contract. Missing remote
   orchestration remains unsupported/not-configured, never numeric zero.
7. Normal `serve` supervises explicit Codex App Server streams on the existing authenticated
   ingress listener. Rejection counters persist in PostgreSQL across restarts. A failed derived
   projection retains the typed sanitized retry record in its bounded lane spool and retries every
   15 seconds while health remains degraded.
8. A pending projection receipt also temporarily retains the closed normalized Event/Evidence
   metadata in PostgreSQL, capped at 32 KiB and deleted after success. This closes the crash window
   where the canonical fact commits but no spool frame survives. Repair is bounded,
   preview/approval-gated and does not reinsert canonical facts/evidence or inflate source replay
   counts. Historical receipts are not rewritten or guessed.
9. Codex App Server `plugin/read` is an adapter-owned metadata surface, not an invocation signal.
   Matching request/response IDs produce daily-bucketed, position-independent plugin
   requested/installed/enabled evidence and owner-qualified child metadata. Raw upstream IDs are
   HMAC-only and all content-bearing fields are discarded before the safe sink.
10. Codex plugin ownership inventory may read the explicitly mounted 0.145.0 local plugin cache
    layout, but cache presence never implies enabled state. Only one unambiguous cache version may
    enrich a configured `plugin@marketplace`; multiple versions stay cache-only. Cache children
    remain out of installed/enabled projections, and bounded scan failure is a visible source
    coverage gap rather than a partial graph labeled complete. Cross-profile duplicates collapse
    only when identity, version, enabled state and the full bundle fingerprint are exact; divergent
    profiles remain colliding candidates.
11. Every Codex state root is bound to one stable logical installation shared by inventory and the
    rollout watcher. Emitted source/scope identity uses that binding and versioned resolution is
    installation-scoped. A syntactically valid marker is retained as unresolved `requested` while
    inventory is absent instead of being lost at checkpoint advance. An omitted legacy ID falls
    back deterministically instead of selecting the latest database installation. App Server
    producers for the same logical installation must send that same ID explicitly; conflicting
    file bindings fail visibly before emission.
12. Runtime collectors activate only in long-running `serve`, before listener binding. Constructing
    the shared appliance for backup, restore verification, export/import, diagnostics or explicit
    evidence one-shots must not scan agent state or mutate unrelated inventory/source health.
    Narrow operational Compose profiles intentionally omit those mounts and may write only state
    owned by their requested operation.
13. Source freshness is a first-class health floor. A degraded/unknown runtime source, watermark
    gap/inactivity/missing commit or source clock more than five minutes in the future makes overall
    health at least degraded. Configured-but-not-observed and explicitly unsupported sources remain
    exclusions, not fabricated failures or zeros.

## Migration and rollback

Runtime migration `0003_durability_capacity_state` and dataplatform migrations
`0012_observability_projection_receipts`, `0013_component_identity_resolution` and
`0014_projection_repair_inputs` are additive. Runtime `0004_projection_repair_approval` only widens
the approval-operation vocabulary.
The P0 application must not be rolled back to a build that reopens the archived full mirror on the
critical path. Safe rollback is: stop ingestion, preserve the PostgreSQL and mirror backups, deploy
an application version that understands the new tables, then verify replay and health. The 0013
down migration can remove its view/history/columns only after application rollback; it never
rewrites historical assertions. The `0014` and runtime `0004` down migrations are non-destructive:
older binaries ignore their additions, while removing pending inputs or narrowing historical
approval rows could lose recovery/audit evidence. Legacy archives are retained through rollback.

Live restart reconciliation on 2026-07-30 found that migration `0012` had been applied with one
additional trailing newline before its first trusted commit. The SQL statements and resulting
schema are byte-for-byte identical after removal of that final newline, but the checksum ledger
correctly failed a clean rebuild. Runtime migration loading therefore accepts only the explicit
`0012` applied/committed checksum pair; it does not normalize whitespace or weaken checksum
validation for any other version or checksum. A unit test locks both members of the pair and proves
an arbitrary mismatch still fails closed.

## Consequences

- Fact growth moves to PostgreSQL while local checkpoint size is bounded by source/importer
  cardinality.
- A PostgreSQL outage can use only the bounded sanitized emergency spool; full spool or unavailable
  durability is visible and rejects retryably.
- Capacity health may be critical while PostgreSQL is writable, including when Docker free space is
  below the 25 GiB operating recommendation.
- Re-resolution can improve unresolved evidence without falsifying its original decision.
- Exact Codex App Server evidence applies only to sessions explicitly routed through the
  supervised endpoint; ordinary CLI evidence remains requested/reconstructed.
- Installed Codex plugin bundles can now resolve owner-qualified skill evidence from the local
  read-only catalog. The internal cache path is version-observed, not a promised stable upstream
  interface, so a future layout change degrades `codex.inventory` until reviewed.
- Stable installation binding prevents inventory, reconstructed CLI evidence and exact App Server
  evidence from splitting into unrelated current-resolution populations after restart.
- Backup/restore and other one-shot commands cannot downgrade serving collector health merely
  because their intentionally narrower containers have no agent-state mounts.
- A source failure or clock-skewed freshness record cannot be hidden behind an otherwise writable
  PostgreSQL/database-capacity pass.
- Claude runtime activation is intentionally excluded on this host by operator direction. Existing
  historical evidence is retained; absence of a new canary remains `not_observed`, never zero.
