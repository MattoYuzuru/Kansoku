# TDD 12 — Incident workbench and safe quarantine

## State and boundaries

Implemented and live-verified on 2026-07-26. Docker/PostgreSQL migration, production restart,
keyset page walks, query plans, repeated isolated restore verification, ten-sink privacy and
headless-browser checks pass. Session 12 extends `internal/observability`, `internal/integrity`,
`internal/dataplatform`, `internal/runtime` and `web/` while preserving the Session 02 privacy
boundary and all existing telemetry.

The system MUST retain one conceptual incident identity even if migration compatibility requires
multiple physical tables temporarily. No migration may rewrite, delete or synthesize historical
agent telemetry to make the new read model look complete.

## Contracts to add or revise

- `contracts/incidents/model.yaml`: identity, detector lifecycle, triage lifecycle, occurrence and
  evidence-reference schema;
- `contracts/incidents/quarantine.yaml`: metadata-only structural manifest and prohibited fields;
- `contracts/data-platform/query-contract.yaml`: incident list/detail/quarantine cursor queries and
  budgets;
- `contracts/dashboard.yaml`: Reliability tabs and incident detail route;
- `contracts/integrity/incident-and-health.yaml`: unified recovery and audit behavior;
- `contracts/privacy/sinks.yaml`: debug bundle and incident API as mandatory zero-canary sinks;
- metric/formula registrations for open, recurring, age, recovery time and affected populations.

Every changed closed registry receives a version transition and append-only policy-lock entry.

## Durable model

The implementation SHOULD converge new incident writes on the rich integrity model and preserve
legacy ingress rows through a compatibility projection. If a new canonical table is selected, its
minimum shape is:

```text
incident_id
incident_key_fingerprint
detector_state
triage_state
capability_id
installation_id/source_id nullable with explicit value_state
failure_class/severity
first_seen_at/last_seen_at/resolved_at
occurrence_count
affected_interval
adapter_version/schema_version/parser_version
recovery_criteria
created_at/updated_at
```

Occurrence evidence is append-only and bounded:

```text
incident_occurrence_id
incident_id
observed_at
evidence_ref
schema_fingerprint
safe_error_class
record_count/byte_count
idempotency_key
```

An occurrence replay with the same idempotency key MUST NOT increment counts. Repeated independent
observations update `last_seen_at` and `occurrence_count` transactionally.

Triage state is `new|acknowledged|investigating|action_ready`. Detector state is
`open|recovering|resolved`. Triage writes never change detector state. Existing `user_notes` remain
bounded metadata; the API must reject content-bearing notes or move free-form notes behind a later
separately threat-modeled feature.

## Structural quarantine manifest

The manifest contains only:

- source kind and safe source instance pseudonym;
- signal kind and safe event/type name;
- sorted structural field paths after privacy categorization;
- primitive type set and bounded array/object shape;
- schema fingerprint and parser/schema/adapter version;
- record/byte counts and first/last observed timestamps;
- classification and safe rejection reason;
- candidate adapter/schema IDs when deterministic;
- incident/evidence references.

Unknown path segments are either rejected, replaced with a closed category or HMAC-pseudonymized
per segment. Values, raw key strings judged sensitive, protobuf/JSON bodies and excerpts are
forbidden. The fingerprint function reuses the integrity structural drift contract instead of
inventing a second hash algorithm.

## Incident key and grouping

The default key is:

```text
installation value-state + source value-state + capability +
failure class + schema fingerprint + adapter major compatibility range
```

Unknown installation/source is represented with explicit value state, not the literal placeholder
as a normal identity. Similar fingerprints MAY be linked as candidates but MUST NOT be merged
without a deterministic rule. Version changes can open a related incident rather than overwrite the
old one.

## API

Read routes:

- `GET /api/v1/incidents?state=&triage=&adapter=&source=&capability=&failure=&from=&to=&cursor=&limit=`
- `GET /api/v1/incidents/{opaque_id}`
- `GET /api/v1/incidents/{opaque_id}/occurrences?cursor=&limit=`
- `GET /api/v1/quarantine?fingerprint=&source=&from=&to=&cursor=&limit=`
- `GET /api/v1/quarantine/{opaque_id}`
- `GET /api/v1/incidents/{opaque_id}/debug-bundle?format=json|markdown`

Pagination is keyset-based, ordered by `(last_seen_at DESC, incident_id DESC)` or
`(observed_at DESC, occurrence_id DESC)`. Cursors are opaque, signed/versioned and contain no raw
paths or unpseudonymized identity. Limits are bounded and query budgets are registered.

Mutation routes for triage require the existing mutation bearer and CSRF:

- acknowledge;
- set investigating/action-ready;
- update bounded structured note categories.

There is no `resolve` mutation route.

Every response declares coverage state, exclusions, page completeness and formula versions where
applicable.

## Debug bundle

The bundle is generated from allowlisted typed fields, not database row serialization. It includes:

- incident and related safe IDs;
- source/capability/version matrix;
- occurrence counts and intervals;
- structural manifest;
- recovery criteria;
- relevant repository contract/fixture locators;
- recommended read-only SQL and validation commands;
- instructions to create a sanitized fixture.

It excludes raw SQL parameters, host paths, config values, user notes not explicitly categorized,
prompts, responses, tool content and raw upstream error messages.

## UI

Reliability keeps the main route and adds URL-addressable tabs. Tables support keyboard navigation,
stable cursors, filters and a visible total state (`exact`, `lower_bound`, `unknown`). Incident
profiles link every degraded metric back to the affected interval and capability. Quarantine rows
show shape summaries, not JSON previews.

All internal Reliability tab, profile, back and pagination links use the shared Wouter navigation
path. Query-string changes therefore update the workbench without replacing the document; direct
URLs, browser Back and full refresh preserve the same URL-addressable state.

## Audit and recovery

The daily audit verifies:

- open incidents have recent occurrence or explicit stale classification;
- occurrence counts reconcile with evidence;
- structural fingerprints have a parser disposition;
- resolved incidents have fresh positive evidence later than the last failure;
- orphan occurrences/details do not exist;
- pagination/query budgets pass;
- debug bundle privacy canary passes.

A parser deployment does not resolve an incident by itself. A supported new occurrence must pass
normalization and the targeted audit/reconciliation check.

## Migration and backup

Migration first creates new structures and compatibility views, then backfills only incident
metadata from existing incident tables. It never invents missing installation/source identities and
never deletes the legacy rows in Session 12. Backup/restore verification includes incidents,
occurrences, structural manifests and triage state.

## Tests

- legacy ingress and integrity migration fixtures;
- deduplication/replay/concurrent occurrence tests;
- cursor stability under inserts and exact filter tests;
- unknown/prohibited field-path property tests;
- ten-sink raw-content canary including debug bundle and browser responses;
- parser fix plus later recovery audit;
- backup/restore count and lineage reconciliation;
- browser tests for empty, open, recurring, resolved, partial and degraded states;
- live DB before/after reconciliation queries.

## Exit gate

The Engineering Proposal exit gate is executable. The reconciliation report includes exact legacy
and canonical counts, every migration exclusion, query plans/budgets, privacy scan results and a
live unknown-schema-to-recovery proof.

## Implemented shape and evidence boundary

- `internal/runtime/migrations/0002_incident_workbench` creates occurrence/manifests after the
  data-platform and integrity migrations; `internal/integrity/migrations/0006_incident_triage`
  adds only detector/triage separation and its page index.
- New ingress unknown-schema writes keep their compatibility row and append one occurrence per
  unique idempotency key. Existing quarantine metadata is backfilled with explicit unlinked
  lineage because the legacy table cannot prove a relationship.
- Recovery requires a durable normalized `source.observed` event newer than the failure and a
  passing targeted audit later than that event. Both references persist on the incident.
- The daily Stage 7 audit checks orphans, non-legacy aggregate/detail reconciliation, invalid
  manifests, stale opens and recent recovery proof.
- Retention removes occurrence/manifests only after the confirmed configured horizon, increments
  an aggregate exclusion count transactionally, and never deletes the incident identity/history.
- Query functions use registered PostgreSQL `statement_timeout` ceilings plus client wall-clock
  checks. Live `EXPLAIN ANALYZE` stayed below 0.6 ms for the three workbench page shapes on the
  measured appliance.
- A migration-era manifest remains explicitly unlinked until a fresh observation proves the exact
  same quarantine identity, source kind and schema fingerprint. That observation may replace only
  the `inc_unlinked_*` relation and a value-free `not_observed` protocol shape; it cannot merge or
  reinterpret any unrelated legacy record.
- Restore verification is defined against the immutable archive, its manifest and compiled
  migration ledgers. It never compares an old archive with later mutable rollups in the source
  database; repeated verification therefore remains stable while ingestion continues.

## Bounded streaming UI reconciliation (2026-07-30)

`useInfiniteIncidents` and `useInfiniteQuarantine` pass only backend-issued opaque signed cursors as
TanStack Query page parameters. Filters are part of the query key; changing one removes any legacy
cursor from the URL and starts a new keyset sequence. Pages are flattened in arrival order and
sliced to a 200-row DOM ceiling.

`LoadMoreControl` observes a non-semantic sentinel with a 240 px root margin and invokes the same
fetch function as its visible button. It disconnects while loading, after `has_more=false`, or when
the DOM ceiling is reached. Environments without IntersectionObserver keep the button path.

Reliability filter forms prevent native submission and use Wouter navigation. Detector/triage use
the shared keyboard and ARIA Dropdown contract. Session storage is keyed by the local query string
and restores scroll on the next animation frame; it stores neither cursor contents separately nor
telemetry values.

Queries are enabled by the visible URL tab. Health loads coverage, reliability counts and the
collection snapshot; Incidents loads only its keyset page; Quarantine loads only its keyset page.
Profiles enable only their typed detail/occurrence/bundle queries. Hidden tabs do not consume the
appliance's fixed 120-request/minute local safety budget or contend with the visible query budget.

`TestIncidentWorkbenchReplayPaginationProfilesAndDebugBundle` remains the backend reconciliation
gate for an unknown version-pinned fixture: one manifest and incident, replay-safe aggregate
counts, append-only occurrences, deterministic lineage replacement, stable concurrent-insert
pagination, filter attribution and cursor-tamper rejection. Profile and manifest responses retain
safe adapter, source schema, parser, schema fingerprint and event-type fields only.
