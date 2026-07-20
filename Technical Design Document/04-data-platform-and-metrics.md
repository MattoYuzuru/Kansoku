# TDD 04 — PostgreSQL data and analytics design

## Logical schema

### Dimensions

- `devices`, `agent_installations`, `agent_surfaces`, `agent_versions`;
- `projects` (pseudonymous identity + optional alias);
- `models`, `providers`, `price_catalog_versions`;
- `components`, `component_versions`, `component_installations`;
- `component_relations` for plugin-bundled skill/MCP/hook/command and collision/shadow links;
- `adapter_versions`, `source_instances`, `source_schema_fingerprints`.

### Activity facts

- `sessions`, `turns`, `prompt_features`;
- `events` and `event_evidence`;
- `model_operations`, `token_usage`, `cost_estimates`;
- `component_lifecycle_events`, `tool_calls`, `mcp_connections`;
- `change_outcomes` for safe edit/test/commit metadata;
- `correlations` and candidate relationships.

### Quality/operations

- `source_watermarks`, `completeness_intervals`;
- `ingest_failures`, `schema_quarantine_metadata`;
- `reconciliation_runs`, `reconciliation_mismatches`;
- `audit_runs`, `audit_checks`, `incidents`;
- `retention_policies`, `backup_runs`, `restore_tests`;
- `formula_versions`, `rollup_status`.

## Physical design

- Range-partition high-volume fact tables monthly by `observed_at`.
- Unique idempotency index scoped by source instance.
- BRIN indexes on time for large partitions; B-tree on session/component/source foreign keys.
- Keep approved low-cardinality attributes relational; sanitized source extension fields use JSONB
  only with adapter/schema version and size limit.
- Use database constraints for state enums, non-negative counts, confidence range and source lineage.
- Avoid generic EAV for primary analytics.

Exact indexes are selected from query plans under reference loads.

## Rollups

Hourly and daily tables group registered measures by a bounded dimension set. High-cardinality
ad-hoc drill-down queries raw normalized facts. Percentiles use one of:

1. exact PostgreSQL `percentile_cont` for bounded ranges;
2. mergeable sketches extension/library after measured need;
3. precomputed distribution buckets for stable prompt-size/latency panels.

Never average already-computed percentiles. Rollups carry event count, unknown count, completeness
duration and formula version.

## Late data algorithm

On commit, enqueue affected `(metric_family, bucket_start, dimension_scope)`. Worker coalesces keys,
recomputes from normalized facts and advances rollup watermark. A source event older than retention
is rejected or recorded as unapplied metadata according to policy; history is not silently shifted.

## Time ranges

All API ranges are half-open `[from,to)`. Presets resolve in user timezone:

- day/week/month/six-month/year/all-time;
- sprint with configurable start date and duration;
- custom range.

Buckets account for DST and expose UTC boundaries. Comparisons use the immediately previous equal
calendar/elapsed range as explicitly labeled.

## Formula registry

Each metric version includes SQL/template, unit, dimensions, numerator/denominator, population,
minimum sample, allowed completeness and formatting. Changes create new versions; dashboard query
records version used. Cost formulas reference price snapshot and effective date.

## Completeness-aware query contract

Every analytics response includes:

```json
{
  "data": [],
  "formula_version": "skill.activation_rate/1",
  "population": {"numerator": 12, "denominator": 20},
  "completeness": {"status": "partial", "covered_ratio": 0.92, "intervals": []},
  "freshness": {"rollup_watermark": "...", "late_events_pending": 2}
}
```

## Retention

Partition drop is preferred for event expiration. Derived rollups whose source expires remain only
if policy explicitly allows aggregate retention and are labeled as aggregate-only. Deletion plans
include backup limitations. `VACUUM`/`ANALYZE`, partition creation and disk forecast are audited.

## Backup/restore

Use PostgreSQL-native logical/physical strategy selected in Session 09. Backup manifest includes
app/schema/formula/adapter versions, checksum and privacy policy. Restore test uses isolated DB,
verifies counts/constraints/sample formulas, then deletes the temporary target.

## Test suite

- schema constraints and migration upgrade/downgrade policy;
- golden metric formulas including unknown/degraded intervals;
- DST/timezone/sprint and half-open boundary cases;
- late/reordered/duplicate events and rollup repair;
- 1M/day synthetic load query plans;
- retention/partition/backup/restore;
- pseudonymous identity collision and component version history.

## Exit gate

Reference datasets reconcile exactly, common queries meet budget, late data repairs correctly,
retention is bounded, and restore reproduces formula results with lineage.

