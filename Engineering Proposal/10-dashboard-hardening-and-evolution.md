# Session 10 — Dashboard, hardening and evolution

## Purpose

Present a large metric set without creating a wall of charts, then review the whole product as a
privacy-sensitive observability system and establish an infinite improvement loop.

## Information architecture

### 1. Overview

Collection health strip, current-period KPIs, activity/tokens trend, component activation funnel,
top changes, anomalies and active incidents. Usage never appears without coverage.

### 2. Activity and prompts

Prompt timeline, weekday/hour heatmap, calendar heatmap, payload percentile bands, sessions/turns,
active/idle duration and latency funnels.

### 3. Agents and models

Fleet/version inventory, usage share, token/context/cost breakdown, model switches/fallbacks,
latency/errors and version markers.

### 4. Components

Separate tabs for skills, plugins, MCP and tools. Lifecycle funnels, cold/unused/stale lists,
co-activation matrix, plugin component tree, MCP health/latency/failures and opportunity recall.

Planning reconciliation (2026-07-26): Sessions 14–16 supersede the assumption that every component
shares one lifecycle funnel. Skills use separate availability/runtime evidence planes, plugins use
bundle/load plus child usage, and MCP separates inventory, connection and calls. Opportunity recall
is deferred to the Session 20 research gate rather than appearing as a generic unsupported panel.

### 5. Reliability

Coverage timeline, source watermarks, reconciliation mismatches, unknown schemas, daily audit,
canaries, incidents and adapter compatibility.

### 6. Privacy and system

Data classes, retention, redactions, egress, host access, backup/restore, collector overhead,
database growth and slow queries.

## Global interaction model

Every page uses one time-range control: day, week, configurable sprint, month, six months, year,
all time or custom. It supports timezone, agent/project/component filters, previous-period
comparison and shareable local URLs without sensitive values.

Drill-down keeps context: chart → bucket → sessions → event/evidence metadata. Raw content is never
available. Hover/click shows formula, counts, completeness and confidence. `Unknown`, `unsupported`
and `degraded` have distinct accessible visuals.

## Visualization choices

- lines/stacked areas for time and composition;
- percentile bands rather than only averages;
- hour × weekday heatmaps and calendar heatmaps for rhythm;
- funnels for component lifecycle;
- tables with sparklines for ranking and exact values;
- matrices for co-activation;
- treemap/tree for plugin-bundled components;
- scatter for tokens vs latency/outcome;
- timeline annotations for versions/incidents;
- top-N + Other instead of unreadable pie charts.

Percentages show raw counts and formula. Color is never the only status cue.

## Style direction

Quiet, dense and deliberate: neutral dark/light themes, excellent typography, restrained accent
colors, generous spacing between analytic sections and compact tables where precision matters. No
external fonts/CDNs. Responsive desktop-first layout, keyboard navigation and WCAG AA target.

## Hardening review

- independent security/privacy review;
- fuzzing and parser/adversarial payload tests;
- migration and restore drills;
- performance/load/soak results against SLOs;
- accessibility and browser compatibility;
- adapter version matrix and unsupported claims audit;
- docs/fresh-install test on a clean machine;
- dependency/SBOM/image/signing plan;
- failure UX: no blank charts or silent fallback to zero.

## Evolution loop

After the ten sessions, improvements continue through evidence:

- prioritize adapters by discovered local demand;
- turn incidents into fixtures and contract tests;
- measure dashboard panel usefulness locally without user tracking;
- version metrics/formulas rather than rewriting history;
- upstream telemetry gaps to agent vendors where possible;
- add opt-in experiments for skill descriptions/workflows;
- periodically prune metrics that do not answer a developer question.

## Deliverables

- Production design system and responsive dashboard.
- Full chart/table/formula registry and empty/degraded states.
- Accessibility, privacy, security, performance and restore review reports.
- Signed/pinned release artifacts and installation guide.
- Public adapter/spec documentation and post-1.0 backlog.

## Exit gate

Every metric in the MVP catalog has a clear home, formula and completeness display; no page exposes
prohibited data; fresh-install and restore tests pass; supported adapter claims match live evidence;
the dashboard remains fast on all standard ranges; residual risks and future work are explicit.

## 2026-07-29 terminology amendment

Operations includes a searchable Glossary route generated from the canonical glossary contract.
Skills, Plugins and System metrics link directly to relevant definitions instead of relying on
unexplained labels or a second frontend-only term registry. Plain definitions cover lifecycle
states, catalog variants, plugin child attribution, database/checkpoint budgets, Docker storage,
backpressure, mirrors, `fsync` and capacity forecasts.

## 2026-07-30 reliability-formula amendment

The Models error ratio is the ratio of field sums across the selected interval:
`failed terminal operations / all terminal operations`. It is never an unweighted mean of daily
ratios. The response and KPI expose the formula version, numerator, denominator, excluded
non-terminal/unknown outcomes and completeness.

Collection health no longer labels `ingested_at - observed_at` as live ingest latency. That
quantity mixes source age, backfill and clock skew. The UI instead keeps five explicit signals:

- receive-to-durable-commit p95, currently `not_observed` because a per-event durable receive and
  commit pair is not stored;
- active-source observation age p95;
- evidence replay count;
- arrival gaps over the versioned five-minute late/backfill candidate threshold;
- events explicitly marked with source clock skew.

The canonical compatibility metric `collection.ingest_latency_seconds` advances to formula version
2 and means receive-to-durable-commit latency. Version 1 remains locked as historical semantics.
Until durable timestamps exist, version 2 has an explicit exclusion and must not render a numeric
zero.
