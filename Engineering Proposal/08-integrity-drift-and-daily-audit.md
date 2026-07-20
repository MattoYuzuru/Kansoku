# Session 08 — Integrity, drift and daily audit

## Purpose

Make collection correctness observable. The daily job is a single orchestrated audit that checks
many contracts, not a single brittle endpoint request.

## Failure modes to detect

- agent upgraded and event schema changed;
- hook was removed, disabled, untrusted or now points at the wrong endpoint;
- OTel exporter stopped, endpoint/protocol changed or queue is stuck;
- transcript location/format/permissions changed;
- inventory scanner counts cache versions as enabled components;
- parser silently ignores a new event type;
- source clocks drift or events arrive out of order;
- database/rollup/retention job falls behind;
- active agent sessions exist but Kansoku observes no expected events;
- canary succeeds at the agent but fails to reach one downstream stage.

## Daily audit stages

1. Inventory agents, versions, surfaces and config fingerprints.
2. Verify configured endpoints/hooks without mutating them.
3. Check source watermarks and distinguish inactivity from collection silence.
4. Replay bundled parser fixtures for active adapter versions.
5. Send synthetic local hook/OTLP events through the entire storage path.
6. Reconcile recent sessions across all available sources.
7. Check unknown schemas, quarantine, duplicates and ingest lag.
8. Validate rollup freshness/formula versions/database integrity.
9. Verify retention, disk budget, backup age and latest restore test.
10. Optionally run one live agent canary per configured budget.
11. Persist a signed/versioned audit report and raise incidents.

The scheduler also runs a reduced audit on startup and immediately after detecting an agent/adapter
version change. Documentation/changelog refresh can be weekly and allowlisted; runtime evidence has
priority over prose.

## Live canary design

A deterministic fixture project contains a uniquely named canary skill, plugin-like component and
local MCP echo tool. The non-interactive agent receives a harmless bounded request expected to
trigger a known event chain. Canaries have token/cost/time budgets, never touch user repositories
and are disabled until credentials and explicit consent exist.

## Health model

Avoid one magic score. Show a status plus decomposed dimensions:

- configuration;
- connectivity;
- event freshness;
- schema compatibility;
- parser fixture status;
- reconciliation coverage;
- privacy canary;
- live-canary age/result;
- storage/rollup health.

Green means supported contracts passed, not “we assume everything is fine”. Yellow marks partial or
stale evidence; red marks observed breakage; gray means no supported evidence.

## Alerting

Dashboard incidents are mandatory. Local desktop notifications are optional. External webhooks are
opt-in egress and contain only incident metadata. Alerts deduplicate, track first/last seen, affected
interval/capabilities and recovery evidence.

## Deliverables

- Durable daily scheduler and audit state machine.
- Agent/version/schema fingerprints and source watermarks.
- Passive probes, synthetic pipeline tests and live-canary harness.
- Reconciliation rules with expected-event models.
- Incident lifecycle, notification policy and coverage timeline.
- Fault-injection suite for every detection claim.

## Exit gate

For every supported source, tests intentionally break endpoint, hook, permissions, schema,
watermark, parser, rollup and storage. Each failure is detected within its SLO, identifies the
affected metric interval, never leaks canary content and automatically records recovery only after
fresh evidence.

