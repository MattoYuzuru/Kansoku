# TDD 10 — Frontend, hardening and release

## Frontend stack

Baseline: TypeScript, React, a lightweight router/query cache, Apache ECharts and an internal token/
component layer. Build static assets into the Go binary/image. No CDN, external fonts, remote source
maps or analytics.

Final ADR compares bundle/accessibility/performance and may choose a smaller framework if it meets
linked filtering and complex chart requirements.

## Route model

```text
/
/activity
/prompts
/agents
/agents/:id
/models
/components/skills
/components/plugins
/components/mcp
/tools
/reliability
/privacy
/system
/settings
```

Routes consume capability/metric registries so unsupported panels render intentionally. Agent-specific
pages MAY add namespaced panels supplied by adapter metadata, but common navigation stays canonical.

## Global query state

URL-encoded safe state includes preset/custom range, timezone, comparison, agent/project/model/
component filters and bucket resolution. Sensitive raw paths/IDs are not placed in URLs; use opaque
IDs/aliases. One shared date engine resolves half-open ranges and sprint configuration.

### Implemented adaptive timeline slice (2026-07-26)

The first dashboard build rendered only sparse UTC days and froze `to` when a page mounted. That
made 24-hour, 7-day and 30-day presets look identical and left a tab opened across midnight on the
previous date even while newer facts were durable. The production slice now applies one shared
contract across the bespoke timeline endpoints:

- the browser sends its IANA timezone and a closed `hourly|daily|weekly|monthly` granularity;
- 24 hours uses hourly buckets, 7/30 days use daily buckets, six months uses ISO-calendar weeks,
  and 12 months/five years use calendar months;
- PostgreSQL computes each exact aggregate and percentile from raw facts with timezone-aware
  `date_trunc`; already-computed percentiles are never averaged in the browser;
- the browser renders every bucket intersecting the half-open range and merges sparse rows onto
  it; a missing row is `null`, never a fabricated zero;
- the live upper boundary refreshes every 30 seconds, 60 seconds or five minutes according to
  resolution while the tab is visible, and refreshes immediately on focus;
- tooltip formatting rounds presentation to at most two fractional digits while preserving API
  and database precision.

Long-range validation is resolution-aware: a five-year monthly request is valid, while an hourly
request remains capped at 31 days. The current “Last 5 years” preset is intentionally labeled as
such; a true retention-derived `all_time` boundary remains a separate server metadata feature.

The production binary embeds `internal/webui/dist`, not the adjacent Vite output directory
directly. Dashboard releases therefore use `web/scripts/build-and-embed.sh`, and the Docker build
fails when `web/dist` and `internal/webui/dist` differ. A backend rebuild can no longer silently
ship a stale frontend bundle.

## Response/view states

Every panel handles:

- loading and stale-while-revalidate;
- exact complete data;
- partial/degraded with affected interval overlay;
- unsupported capability;
- unknown/no eligible evidence;
- true empty/zero;
- query error and incident link.

Blank chart and “0” are never generic fallbacks.

The 2026-07-30 containment pass makes this contract executable at three boundaries:

- collection-bearing Skill and Plugin profile responses serialize absent collections as `[]`;
- the query boundary normalizes legacy cached/mixed-version `null` collections, and merge helpers
  still fail safe if an unnormalized payload reaches them;
- query failures render a privacy-safe Retry/Back state, route render failures retain the
  `AppShell`, and a root boundary leaves a non-empty recovery surface if the shell itself fails.

## Core visual specifications

### Activity timeline

Stacked counts/area with p50/p95 prompt-size band, version/incident annotations and comparison ghost
series. Adaptive buckets retain exact totals.

### Hour × weekday heatmap

Local timezone grid, selectable metric, accessible table equivalent and DST explanation. Tooltips
show count, median/p95 and completeness.

### Component funnel

The original Session 10 design used installed/enabled/exposed/invoked/loaded/executed/succeeded as
one funnel. The 2026-07-26 evidence review found that model invalid for instruction components:
inventory availability, runtime selection/load, child activity and terminal outcome do not form one
universally observable sequence. Session 14 therefore replaces this panel with independent
Availability and Runtime evidence summaries, while Session 20 owns any later Optimization plane.
Until that contract migration lands, unsupported later stages remain explicit and are never divided
or rendered as zero.

### Component explorer

Sortable virtualized table: trend sparkline, lifecycle counts, last used, success, opportunity
recall, source/version, completeness and cold/stale reason. Drill-down shows co-activation and
evidence without transcript text.

Session 14 removes universal success and opportunity columns from this common table. Outcome is
shown only for a shared component-specific terminal contract; opportunity remains absent until the
Session 20 research gate passes.

### Reliability timeline

Rows per agent/source/capability with complete/partial/degraded/unknown intervals, audit markers and
incidents. This is the first place to validate a suspicious usage dip.

### MCP/tool analytics

Server tree, calls/success/errors/latency, connection uptime, approvals and top safe error classes.
No raw arguments/results.

## Percentage component

A single shared component requires numerator/denominator, formula ID/version, sample size,
completeness and comparison basis. It suppresses percentage/trend when denominator unknown or below
registered minimum and always displays raw counts.

## Accessibility and design system

- WCAG AA contrast, keyboard access, visible focus and semantic landmarks;
- chart data tables/ARIA summaries and non-color status patterns;
- reduced-motion support;
- dark/light/system themes using local CSS tokens;
- responsive desktop-first layouts down to tablet width;
- locale-ready number/date formatting; initial UI language decision recorded separately.

## Frontend performance

- route-level code splitting where it reduces initial bundle;
- server aggregation/downsampling, no million-point browser payloads;
- virtualized large tables;
- request cancellation and cache keys including formula/completeness version;
- performance budget for bundle, first render and chart interaction;
- visual regression fixtures for DST, gaps, unknown and high-cardinality data.

## Final hardening gates

### Security/privacy

CSP, dependency/SBOM review, XSS via aliases/error strings, CSRF/auth, no prohibited API fields,
browser network capture and raw canary scan.

### Reliability/data

End-to-end canaries, reconciliation, unknown schema, late data, migration/restore, formula golden
tests and no-silent-zero UI review.

### Performance/operations

Reference load, seven-day soak, disk forecast, backup restore, container restart, ARM64/x86_64 and
supported browser matrix.

### Release

Pinned/signed images where infrastructure permits, checksums, SBOM, changelog, compatibility matrix,
fresh-install test, rollback guide and known gaps. Never claim “all agents” or “100%” without a
capability/version evidence record.

## Post-release evolution

- Adapter SDK compatibility tests run in CI.
- Newly observed unknown fingerprints become issues linked to sanitized structure metadata.
- Metrics/formulas are versioned; deprecated panels retain historical explanation.
- Optional local panel-usage counters store only panel IDs to guide UI pruning.
- Quarterly review checks official agent contracts, dependency health, privacy model and unused
  metrics/adapters.
- Incident fixes always add fixtures/fault tests.

## Exit gate

All supported routes pass accessibility, privacy, reliability, performance and visual-state tests;
formula/evidence drill-down is universal; release/restore is reproducible; the project has a safe
adapter and metric evolution process beyond the initial ten sessions.

## Contract-backed glossary route (2026-07-29)

`web/scripts/gen-routes.mjs` reads both `contracts/dashboard.yaml` and
`contracts/glossary.yaml`. It generates the route registry plus a typed term registry at build
time. `/glossary` renders only those generated definitions, supports local search and stable term
anchors, and performs no API call or external request. Contextual info links use the same anchors.
The route is lazy-loaded and remains within the existing read-only dashboard authorization model.

## Reliability and model formula reconciliation (2026-07-30)

`model.error_ratio/1` is materialized in `ModelUsageResponse.error_ratio_metric`. The backend sums
daily failure, terminal and excluded populations before division. Each daily row also carries its
own numerator, denominator and exclusions for exact table/export reconciliation. A zero numerator
with a non-zero terminal denominator is numeric zero; an absent denominator keeps the value null.

`collection_health_snapshot/2` returns separate typed fields for receive-to-commit latency,
active-source observation age, evidence replays, late/backfill candidates and declared clock skew.
Its population is accepted plus quarantined input, and its exclusion map counts accepted events
whose durable receive/commit timestamps are unavailable. Receive-to-commit therefore serializes as
absent/null and renders `not_observed`; it is not derived from source timestamps.

The late/backfill candidate query excludes declared clock-skew events and uses
`ingested_at - observed_at > 5 minutes`. Observation age is a p95 over active source watermarks,
not an event latency percentile. Replay count comes from evidence rows in the selected interval.
These populations remain visibly separate in Reliability.

The historical `collection.ingest_latency_seconds/1` lock is retained. The current registry and
formula fixture use `collection.ingest_latency_seconds/2` with
`p95(durable_commit_at - received_at)` and the explicit
`receive_or_commit_timestamp_not_observed` exclusion.

The Models query selects the latest price-bound estimate for all token rows once and joins that
bounded relation. It does not execute a lateral `cost_estimates` scan per response. On the
measured live 7,988-response range, the old plan touched about 1.1 million shared buffers and took
1,849.465 ms; the set-based plan touched 961 shared buffers and took 74.816 ms. A 5,000-response
PostgreSQL regression fixture remains below the unchanged 150 ms query budget and reconciles exact
request, costed-request and cost totals.
