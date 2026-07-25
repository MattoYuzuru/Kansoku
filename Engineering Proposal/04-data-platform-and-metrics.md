# Session 04 — Data platform and metrics

## Purpose

Choose a durable local data model that remains simple at hundreds or thousands of events per day,
but can also replay years of event history and compute clear percentiles, funnels and timelines.

## Workload estimate

The expected personal workload is modest: hundreds of prompts, hundreds of component activations
and thousands of low-level events per day. A generous stress profile should test 1 million events/day
and multi-year retention so design mistakes surface early, without optimizing the product into a
distributed analytics system.

## Database alternatives

### SQLite

Excellent operational simplicity and sufficient scale, but concurrent ingest, schema migrations,
continuous rollups and dashboard reads require careful single-writer design. It remains a credible
future “lite mode”.

### PostgreSQL — proposed baseline

Strong transactions, JSONB escape hatch, partitions, window functions, `percentile_cont`, mature
migrations/backups and predictable Docker operation. Plain PostgreSQL avoids an extension
dependency while leaving room for TimescaleDB after evidence.

### ClickHouse

Powerful high-volume analytics and compression, but operationally excessive for the expected local
load and less natural for mutable inventory/configuration state.

### DuckDB

Excellent offline analysis/export, less suitable as the concurrent always-on system of record.
Useful as an optional export/query format.

## Proposed data layers

1. Short-retention sanitized ingest envelope for replay/debug.
2. Append-oriented normalized event/evidence store.
3. Relational dimensions for agents, components, versions, projects and sessions.
4. Hourly/daily rollups and percentile sketches/materialized summaries.
5. Quality/coverage tables, incidents and watermarks.
6. Versioned formula/price catalogs.

Raw event volume is small enough to preserve normalized facts while rollups keep the UI fast.

## Metric correctness principles

- Store timestamps in UTC and render a user-selected timezone.
- Preserve `emitted_at`, `observed_at`, `ingested_at` and source sequence independently.
- Use half-open intervals and documented bucket alignment.
- Calculate percentiles on the correct population, not averages of percentiles.
- Recompute affected rollups after late data; expose freshness.
- Version formulas so historical charts remain explainable.
- Separate exact provider tokens from estimates.
- Never coerce unknown to zero or silently exclude degraded intervals.

## Retention and size control

Monthly partitions allow bounded deletion. Rollups may live indefinitely, normalized events follow
user policy, and ingest envelopes expire sooner. The UI projects database growth and allows a dry-run
of policy changes. Backups include schema/formula versions and are restore-tested.

## Developer-facing analyses

Prioritize the full [metrics catalog](metrics-catalog.md), especially:

- time heatmaps and percentile bands for prompts;
- component lifecycle funnels and co-activation;
- token/model/cost attribution with confidence;
- MCP/tool latency, failure and approval patterns;
- inventory churn and version markers;
- coverage/ingestion health alongside every usage chart;
- experiment cohorts for description/workflow changes.

## Deliverables

- Load generator and reference data distributions.
- PostgreSQL vs SQLite spike with measured complexity/performance.
- Logical schema and retention model.
- Formula registry with test vectors.
- Rollup/replay design and late-event behavior.
- Backup/restore and storage-budget proposal.

## Exit gate

A deterministic fixture and a million-event stress dataset produce verified counts, medians,
percentiles, funnels and completeness states; restart/replay is idempotent; common dashboard queries
meet the provisional budget; retention deletes only intended data.

## 2026-07-25 projection reconciliation

The production observability handoff now idempotently materializes prompt length, tool calls,
model operations, tokens, provider cost, component lifecycle observations and source watermarks.
Activity duration is aggregated once per session/day. Historical unknown telemetry is preserved;
replay heals a missing projection without inflating counts.
