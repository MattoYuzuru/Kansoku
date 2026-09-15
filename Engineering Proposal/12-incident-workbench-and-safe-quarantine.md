# Session 12 — Incident workbench and safe quarantine

## Status

Implemented and live-verified on 2026-07-26. Contract, unit, PostgreSQL integration, migration,
query-plan, backup/restore, ten-sink privacy, production restart and headless-browser gates pass.
The live migration preserved the 14 legacy quarantine fingerprints and two legacy ingress
incidents. Fresh exact evidence deterministically replaces only a migration-era `inc_unlinked_*`
manifest link and value-free structural-shape gap; it never reinterprets unrelated legacy rows.

## Purpose

Turn Reliability into the single place where a user or a debugging agent can understand every
collection failure, schema drift, reconciliation mismatch and audit regression without exposing
the payload that caused it. An incident is useful only when it identifies the affected capability,
evidence, interval, recovery condition and next safe diagnostic action.

## Confirmed baseline

Kansoku currently exposes one `/api/v1/incidents` list assembled from two durable projections:
ingress `incidents`/`schema_quarantine_metadata` and the richer
`integrity_incidents`/`integrity_incident_details`. The API returns open incidents only, caps the
result at 500 without a cursor and gives ingress incidents placeholder installation/source values.
Reliability shows a compact list but no incident profile, occurrence history or quarantine browser.

The live appliance inspected on 2026-07-26 contained 14 unknown-schema fingerprints and two open
ingress incidents. Those records are evidence of a useful safety net, not permission to persist the
unknown payload.

## Product decisions

1. Reliability owns the incident workbench; no new top-level navigation item is introduced.
2. There is one conceptual incident model. Existing physical projections may remain during a
   migration, but the public read model, identity, lifecycle and recovery semantics are unified.
3. Unknown payloads remain metadata-only. Raw JSON, prompts, responses, tool input/output, source
   code, environment values, credentials and unredacted paths never enter quarantine.
4. Triage state is distinct from detector state. A user can acknowledge or investigate an incident
   without falsely resolving the underlying failure.
5. Recovery requires fresh positive evidence after the latest failure. A button or agent may not
   directly mark a detector incident resolved.

## User experience

Reliability gains three coordinated views:

- **Health:** current decomposed collection health and affected capabilities;
- **Incidents:** cursor-paginated open and historical incidents with filters;
- **Quarantine:** grouped unknown schema observations and their safe structural manifests.

An incident profile shows first/last seen, occurrence count, affected interval, installation,
source, capability, adapter/schema versions, evidence tier, safe failure class, recovery criteria,
related fingerprints, audit history and completeness impact. It can produce a metadata-only JSON or
Markdown debug bundle and a copyable agent prompt.

## Safe structural manifest

The manifest records event/type names, allowlisted field paths, primitive types, cardinality-safe
shape information, byte/record counts and a deterministic fingerprint. Unknown or prohibited path
segments are categorized or hashed before persistence. Values are never stored. A parser fix that
needs a concrete payload uses an explicitly supplied sanitized fixture; Kansoku cannot replay data
it deliberately did not retain.

## Agent-assisted investigation

Session 12 provides a read-only debug prompt and evidence bundle. It does not launch an agent,
modify parsers, restart services or write agent configuration. Automated remediation belongs to
Session 19 after the read-only workbench has proved trustworthy.

## Alternatives rejected

- **Raw JSONB quarantine:** highest debugging fidelity, but directly violates the privacy boundary
  and makes unknown schemas the least-governed durable data.
- **A new top-level Incidents page:** duplicates Reliability and fragments the first diagnostic
  path a user needs when a metric looks suspicious.
- **Resolve on acknowledgement:** hides continuing failures and breaks audit recovery semantics.
- **Only keep counts:** safe but not actionable; structural fingerprints, lineage and recovery
  criteria are required to turn incidents into fixtures and fixes.

## Deliverables

- unified incident/query/API contract with keyset pagination and filters;
- safe structural quarantine manifest and occurrence history;
- Reliability list, quarantine browser and incident detail route;
- metadata-only debug bundle and prompt generator;
- incident-aware daily audit and retention/backup behavior;
- fixture, replay, migration, privacy, pagination, browser and live recovery tests;
- reconciliation report with residual unknown-schema fingerprints.

## Exit gate

One unknown canary schema opens one deduplicated incident, repeated delivery increments its
occurrence history without count inflation, every list page is stable under concurrent inserts, the
profile exposes complete safe lineage, and a later supported event plus audit closes the incident.
The ten-sink privacy canary is absent from database, logs, queues, API, export, backup and debug
bundle. Historical telemetry is preserved through migration.

## Implementation reconciliation

The compatibility read model remains a typed SQL union over the ingress and integrity projections;
Session 12 does not rewrite either historical incident table. Runtime migration `0002` owns the
cross-projection occurrence and structural-manifest tables because it runs after both earlier
migration ledgers. Legacy quarantine rows have no incident foreign key, so their manifests receive
an explicit deterministic `inc_unlinked_*` identity instead of an invented relationship.

Recovery now records both the normalized `source.observed` event ID and the later passing audit-run
ID. Triage routes cannot set detector state and no resolve route exists. Fixed OTLP protocol
descriptor names may form an observed structural shape; sources without a value-free descriptor
remain `not_observed`. Confirmed retention expires detail while preserving aggregate incident
counts and an explicit occurrence-detail exclusion count.

## 2026-07-30 workbench amendment

Incident and quarantine lists consume the existing signed keyset cursors through bounded infinite
queries. The viewport sentinel may request the next page, while a keyboard-accessible Load more
button is always the fallback. At most 200 rows are mounted; the user must refine URL-addressable
filters beyond that boundary.

Detector and triage filters use the shared ARIA Dropdown. Text filters, tab changes, profile links,
Back and refresh stay inside Wouter navigation without replacing the document. Scroll position is
stored per non-sensitive Reliability URL in session storage. Query failures render an alert
instead of an endless loading state.

The existing version-pinned unknown-schema integration fixture remains the acceptance proof:
repeat delivery creates one incident and one manifest, adds a distinct occurrence, reconciles the
durable aggregate count, preserves source/adapter/schema/parser/event-type attribution and rejects
a tampered cursor. No incident is deleted or mass-resolved, and Claude is not launched.
