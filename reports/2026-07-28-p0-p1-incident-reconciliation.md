# P0/P1 incident reconciliation — 2026-07-29

## Decision

The incident has three separate release decisions:

| Gate | Decision | Evidence |
|---|---|---|
| PostgreSQL-authoritative durability cutover | **GO** | The full JSON fact/evidence mirror is absent from the ingestion critical path; compact state and per-lane emergency spools are bounded; restart/replay, PostgreSQL-unavailable, spool-full and crash-boundary tests pass. |
| Local Codex skill/plugin tracking | **GO for the supported local surfaces** | Ordinary CLI produces requested/reconstructed evidence; explicitly routed App Server streams produce native exact evidence; plugin inventory/read evidence preserves owner-qualified identity; remote orchestration is explicitly `unsupported`. |
| 5 GiB capacity release / whole local appliance release | **NO-GO** | Docker has only 6,532,079,616 free bytes (6.22%). This is below both the 20% load-test stop condition and the 25–30 GiB operating recommendation, so the required 5 GiB full-volume soak was not run. Health correctly remains `critical`. |

Continued bounded local ingestion is safe under the new durability path, but this is not a capacity
release approval. Claude runtime is intentionally out of scope on this laptop by operator
direction: no Claude process was launched and no Claude configuration was changed. Static,
sanitized protocol fixtures remain regression coverage only and are not a live Claude claim.

## Implemented

- PostgreSQL is authoritative for normalized facts and evidence. Production assembly no longer
  opens the compatibility `FileStore` as a prerequisite to PostgreSQL ingestion.
- `CompactStore` persists only checkpoints, watermarks and bounded replay metadata. Its production
  budget is 4 MiB and its serialized size does not grow with the total event count.
- `spool_max_bytes` remains a 64 MiB limit for each emergency source lane; it is not a database,
  mirror or retention limit.
- A record is acknowledged after PostgreSQL durability or successful reservation in its bounded
  emergency spool. PostgreSQL projection failures retain a typed sanitized replay input and remain
  visible as degraded until idempotent completion.
- Unknown OTLP schemas use metadata-only quarantine, hourly aggregate occurrence keys and bounded
  local state. Unknown records do not abort later supported records in a mixed batch, and only real
  transient durability/backpressure failures are retryable.
- The configurable PostgreSQL soft budget is 5,368,709,120 bytes with 70/85/95 percent
  warning/degraded/critical thresholds. It is neither preallocation nor a PostgreSQL hard cap.
- Health and the System UI expose database bytes/budget/percentage/growth/exhaustion, heap,
  indexes, backup bytes, cumulative temp bytes, WAL/headroom exclusion, filesystem headroom,
  checkpoint and per-lane spool occupancy, ingest success/rejection timestamps, durable rejection
  counters, pending projections and source freshness.
- Disk preflight reports that WAL/rollback headroom and current temporary-file occupancy are not
  independently observed. It does not claim that a 5 GiB database is safe merely because the
  database soft budget is configured.
- `component_assertions` additively carries `component_kind`, `qualified_identity`,
  `identity_source`, `owner_plugin_identity`, `invocation_mode`, `upstream_identity_hash` and
  `resolution_version`.
- Resolution is versioned and append-only in
  `component_assertion_resolution_history`; the current-resolution view is derived. Re-resolution
  can link previously unresolved evidence after a later inventory snapshot without updating the
  historical assertion row.
- Skill and plugin observatories filter evidence/exclusions by `component_kind`. Qualified identity
  includes standalone scope or plugin owner; duplicate plain names are never resolved by arbitrary
  selection.
- The Codex inventory collector reads configured state roots and plugin cache/catalog metadata
  read-only. Inventory, rollout and App Server streams are bound to the same explicit installation
  ID; collectors never adopt a dynamic “latest installation”.
- The Codex rollout watcher is append-only, checkpointed, rotation/truncation aware and idempotent.
  It only persists identity metadata, event kind, lineage, versions, confidence and redaction
  counters. A `$skill` marker is `requested`; `invoked` requires independent corroboration such as
  the matching `SKILL.md` read plus native child activity.
- The rollout marker parser strips sentence punctuation after an identity while preserving valid
  internal dotted and `plugin:skill` identities. The earlier synthetic
  `search-workflow.` assertion is retained unresolved as immutable historical evidence; it was not
  rewritten or deleted.
- The Codex 0.145.0 App Server bridge demultiplexes concurrent JSON-RPC request IDs, recognizes
  known service traffic, quarantines only owned invalid frames and uses repository-pinned generated
  schema hashes. Normal `serve` supervises its authenticated bounded ingress.
- App Server `plugin/read` produces plugin requested/installed/enabled metadata and
  owner-qualified child inventory. It never fabricates plugin `invoked` or `loaded`.
- The production image builds from vendored Go dependencies with `--network=none`; runtime
  collection remains read-only toward agent directories.
- Runtime collector activation belongs only to `serve`. One-shot backup/restore/export/import,
  diagnostics and explicit evidence commands no longer overwrite inventory or unrelated source
  health when their intentionally narrow containers have no agent-state mounts.
- Source freshness is part of the overall health rank. A degraded/unknown source, watermark
  gap/inactivity/missing commit or source timestamp more than five minutes in the future makes
  `source_state` and overall health at least degraded; it cannot be hidden in exclusions.

## Applied migrations

| Ledger | Version | Semantics |
|---|---:|---|
| data platform | `0012` | Projection receipts for canonical fact/projection reconciliation. |
| data platform | `0013` | Additive component identity fields, append-only resolution history and current-resolution view; no historical assertion update. |
| data platform | `0014` | Sanitized projection-repair inputs for bounded idempotent replay. |
| runtime | `0003` | Durable ingestion counters, capacity samples, source health and mirror reconciliation state. |
| runtime | `0004` | Approval/audit state for projection-repair operations. |

All data-platform migrations `0001–0014`, runtime migrations `0001–0004` and integrity migrations
`0001–0006` are applied in the live database.

Rollback is application-first and non-destructive:

1. Stop ingestion and retain the current PostgreSQL backup and every legacy mirror artifact.
2. Roll back the application only to a binary that keeps PostgreSQL authoritative; never reopen the
   archived full mirror in the critical path.
3. `0013` down removes its derived view/history table and additive columns only after application
   rollback. It cannot recreate or rewrite historical identity.
4. Older binaries ignore `0014`/runtime `0004`; their down migrations do not delete accepted facts,
   pending sanitized repair inputs or approval history.
5. No archive deletion is part of rollback. Deletion requires a separate preview and explicit
   operator confirmation.

## Legacy mirror reconciliation

| Check | Result |
|---|---:|
| Mirror/backup/archive SHA-256 | `bafa17343b22e65c9b420f55be55245bb94c0f983937b1f9780a079ed1afaf9d` |
| Mirror bytes | 67,107,702 |
| Mirror revision | 32,610 |
| Unique mirror fact keys | 24,623 |
| Unique PostgreSQL fact keys at cutover | 24,646 |
| Mirror-only facts | 0 |
| PostgreSQL-only facts | 23 |
| Mirror evidence | 24,623 |
| PostgreSQL evidence at cutover | 24,689 |
| Mirror-only evidence | 0 |
| PostgreSQL-only evidence | 66 |
| Lineage mismatches | 0 |
| Checkpoints in legacy mirror | 0 |
| Watermarks migrated | 4 |
| Quarantine fingerprints recorded | 48 |
| Reconciliation status | `reconciled` |

`database_fact_count` is a unique fact-key count rather than a physical event-row count.
PostgreSQL-only records are expected from accepted writes after the mirror stopped advancing and
from emergency replay. Aggregate incident occurrences are excluded from row-identity comparison.

Retained artifacts:

- `mirror/legacy-backups/state.pre-p0-20260728T194057Z.json`
- `mirror/legacy-backups/legacy-backup-bafa17343b22e65c.json`
- `mirror/legacy-archive/legacy-archive-bafa17343b22e65c.json`
- `mirror/legacy-archive/mrr_bafa17343b22e65c.json`

The reconciliation report artifact is 1,012 bytes with SHA-256
`f80b4b405b8c56abb5a9b58ee58894677217b075d26b0dedd54c5e293c8c55f6`.
No mirror, backup or archive was deleted.

## Live storage and resource evidence

The after values were read from the running stack on 2026-07-29 at approximately 03:00 MSK.
Read-only collectors remain active, so row and byte counts continue to advance.

| Metric | Incident baseline | After final restart/backup |
|---|---:|---:|
| PostgreSQL events | 24,689 | 28,420 at backup snapshot; 28,561 current |
| PostgreSQL evidence | 24,689 | 28,420 at backup snapshot; 28,561 current |
| Component assertions | 863 | 1,555 |
| Resolution-history rows | 0 | 785 |
| Inventory snapshots | not recorded | 16 |
| Database bytes | about 67 MB | 84,637,375 bytes (1.58% of 5 GiB) |
| Table heap | not separated | 35,946,496 bytes |
| Indexes | not separated | 35,020,800 bytes |
| Backups | not separated | 51,365,123 bytes |
| Full JSON mirror on critical path | 67,107,702 bytes | absent; archived |
| Compact checkpoint | not applicable | 38,546 bytes / 4 MiB (0.92%) |
| Emergency spool | mirror was the actual near-full path | 0 bytes in every 64 MiB lane |
| Backpressure rejected total | not exposed | 0 |
| Durability unavailable total | not exposed | 1 historical bounded fault-test occurrence |
| Pending projections | not exposed | 0 |
| Kansoku CPU | burst up to 108% of one core | 0.00–17.67% across five final-image samples |
| Kansoku RSS | observed bursts about 390–629 MiB | 10.23–10.64 MiB |
| PostgreSQL CPU | not captured | 0.00–1.56% across the same samples |
| PostgreSQL RSS | earlier samples varied by workload | 168.9–169.2 MiB |
| Docker filesystem free | about 14.7 GiB | 6,532,079,616 bytes / 6.22% |
| Host filesystem free | about 212 GiB | 218,828,692 KiB (about 209 GiB) |

The live health endpoint is `critical`, with completeness 12/16. `source_state` is independently
`degraded`: an immutable synthetic App Server fixture carries a future source timestamp, so the
evidence-bridge watermark reports `source_clock_skewed` rather than masquerading as fresh. The
other explicit exclusions are independent WAL/rollback headroom, current temp-file occupancy
(only cumulative bytes are available), source lanes without committed freshness evidence and
unsupported remote orchestration. Database/checkpoint/spool states themselves are `pass`.

The sampled database growth rate was 72,892,416 bytes/day and the derived exhaustion estimate was
2026-10-09. This is a volatile burst-window estimate, not a capacity promise.

The required 5 GiB full load/soak was deliberately not run. Its preflight fails before workload
start because Docker free space is below 20% and below 25 GiB. No Docker Desktop allocation,
image/cache, user volume or unrelated container data was changed or pruned.

The completed scaled test used an isolated PostgreSQL database/schema, 50,000 events plus 50,000
evidence rows, a 128 MiB/120 second envelope, idempotent replay and deterministic teardown. A
separate unknown-schema storm used 2,048 repeated records, one fixed hourly aggregate key and
bounded compact state below 4 KiB; it completed in 10.22 seconds without production data writes.

## Live Codex evidence

All live assertions below use the configured installation
`ain_9cd7c4fbf5d8df4694834d7769a3747b`.

Ordinary CLI canary:

| Assertion | Mode | Identity source | Tier/confidence | Resolution |
|---|---|---|---|---|
| `requested / skill / search-workflow` | `requested` | `rollout_marker` | reconstructed / 0.85 | exact, one candidate |
| `loaded / skill / search-workflow` | `not_observed` | `rollout_skill_md_read` | reconstructed / 0.85 | exact, one candidate |
| `invoked / skill / search-workflow` | `explicit` | `rollout_corroborated` | reconstructed / 0.85 | exact, one candidate |

The canary used punctuation after `$search-workflow`, read the exact skill file and produced native
child activity. Restart/replay left exactly three logical rows with no assertion inflation.

Supervised App Server canary:

- exact typed skill input produced separate `invoked / explicit / native / 1.0 / exact` and
  `loaded / not_observed / native / 1.0 / exact` assertions;
- plugin `sre-agent@yuzuru-engineering` produced requested/installed/enabled metadata and owned
  child skill lifecycle metadata, but no fabricated plugin invocation;
- after the final restart the skill replay left two logical assertions with replay count 4 each;
  the current UTC-day plugin snapshot remained five logical assertions and its identical second
  submission/restart replay advanced replay count to 2 on each row;
- `codex.app_server` and `codex.rollout` are `producing/observed`;
- `codex.remote_orchestration` is `unsupported/unsupported`, never numeric zero.

The current Codex inventory collection is complete and owner-qualified. The latest snapshot has
116 nodes and 142 edges; `search-workflow`, `sre-agent@yuzuru-engineering` and its owned
`sre-agent@yuzuru-engineering:sre-agent` child each resolve to one exact candidate.

This supports per-invocation tracking only where Codex exposes an observable local surface. App
Server evidence is native exact only for sessions explicitly routed through that bridge. Ordinary
CLI evidence remains requested/reconstructed, and unobserved hosted orchestration remains
unsupported rather than a fabricated zero.

## Verification

Passed:

- `go test ./...`
- `go vet ./...`
- 169 Python contract/mutation tests
- all observability, data-platform, adapter-SDK, Codex, integrity, incident, plugin,
  component-evidence, privacy and runtime validators
- PostgreSQL migration/replay and 50k scaled-growth integration suites
- mixed OTLP batch: unknown record quarantined while supported skill and plugin lifecycle records
  both remained durable
- explicit/proactive/nested/plugin-owner sanitized mapping fixtures
- Codex App Server concurrent JSON-RPC demux, typed skill, plugin/read and restart replay
- ordinary CLI requested vs corroborated invocation, append/replay/rotation/truncation and
  late-inventory re-resolution
- standalone/plugin same-name collision, qualified `plugin:skill` and redacted upstream identity
- PostgreSQL unavailable, spool fallback/full, disk thresholds, checkpoint failure,
  reservation/commit crash boundary, duplicate retry and unknown-schema storm
- source-health floor regression for degraded/unknown sources, gaps/inactivity and future clock
  skew
- compact-state boundedness and PostgreSQL growth beyond the original 24k-event baseline
- privacy canary across 10 accepted/rejected sinks with zero canary or secret matches
- live PostgreSQL dump scan with zero raw prompt/path/description markers
- final path-aware live privacy scan: 29 prohibited markers checked against 33,685,417 bytes of
  `pg_dump` output and current container logs, with zero matches
- production image build with `docker build --network=none`
- current-schema native backup/restore:
  - backup `backup_5f5768566d10dcfaefb9533a1250bc87`
  - 7,549,894 bytes
  - SHA-256 `64818f2f45b7dc8bf8d63bd0073d616786dc2d0bb78edef046bcc45e45d8a96f`
  - all manifest table counts matched after isolated restore
  - restore status `pass`
  - temporary restore database removed
- one-shot assembly regression: backup/restore service construction leaves seeded complete Codex
  inventory and producing App Server health unchanged; the same invariant passed against the
  production Compose profiles before the final API-only source-health rebuild, whose assembly code
  is identical
- final production image
  `sha256:122fa28ef1dc577b413cea5f27723422ef9ae63033ac8a06347287d5183c4c1d`
  (125,251,280 bytes), built with network disabled
- embedded System capacity panel and live `/api/v1/health`

No raw prompts, model responses, source code, tool input/output, environment values, raw API
bodies, credentials, raw filesystem paths or unredacted upstream plugin IDs were found in durable
sinks. Durable records contain only bounded identity metadata/HMAC pseudonyms, counts, lifecycle
kind/mode, lineage, adapter/schema versions, confidence and redaction counters.

## Documentation reconciliation

The implementation decision and changed contracts are reconciled in:

- `adr/0020-postgresql-authoritative-durability-and-versioned-component-resolution.md`
- `Engineering Proposal/03`, `09`, `11`, `13`, `14`, `16` and `17`
- paired `Technical Design Document/03`, `09`, `11`, `13`, `14`, `16` and `17`
- `ARCHITECTURE.md`, `README.md`, `ROADMAP.md` and `SOURCES.md`
- versioned runtime, data-platform, component-evidence, Codex and privacy contracts/policy locks

## Residual executable backlog

1. Manually increase Docker VM free space to at least 25–30 GiB and at least 20%. Kansoku must not
   change Docker Desktop allocation or prune unrelated user data automatically.
2. Re-run capacity preflight. Only after it passes, preview and execute the full-volume synthetic
   soak with an explicit rate, duration, maximum database delta, WAL/backup allowance, stop if free
   space would cross 20%, and cleanup restricted to the declared synthetic namespace. The capacity
   release remains **NO-GO** until this run proves database growth, bounded compact state,
   threshold transitions, restart/replay idempotency and cleanup.
3. Add a browser-driven responsive/accessibility regression for the System capacity panel. The
   existing structural/accessibility-token validators pass, but they are not a real-browser claim.
4. Define an operator decision workflow for a projection input that remains permanently failing
   after repeated bounded replay. It must preserve the owned retry and require preview/approval;
   automatic discard is forbidden.

The current `evidence_bridge` clock-skew degradation will age out after the immutable synthetic
timestamp `2026-07-29T16:00:01Z`; it must not be cleared by rewriting the watermark or deleting
telemetry. Future live fixtures must use a source time no more than five minutes ahead of
ingestion.

Claude is not residual work on this laptop and is not a blocker for the supported local Codex
decision.
