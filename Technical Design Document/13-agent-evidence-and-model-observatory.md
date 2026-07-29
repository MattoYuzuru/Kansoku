# TDD 13 — Agent evidence bridge and model observatory

## Status

Implemented and live-verified on 2026-07-26. See `reports/session-13-reconciliation.md`.

## Boundary

Session 13 introduces a generic adapter-owned evidence bridge and a complete agent/model profile.
It MUST NOT add Codex, Claude or any provider name to canonical event, data-platform, runtime query
or dashboard branching. Adapter registration and capability dispatch remain the only brand-aware
boundary.

## Contracts

Add or revise:

- `contracts/adapter-sdk/evidence-bridge.yaml`;
- adapter capability IDs for bridge inventory/lifecycle only if existing IDs cannot express them;
- `contracts/codex/app-server-bridge.yaml`;
- relevant Claude source mappings with exact version gates;
- session/model/installation dimensional rules in data-platform schema/query contracts;
- agent detail routes/panels in `contracts/dashboard.yaml`;
- bridge health/reconciliation checks in integrity contracts;
- model cost formulas and price-snapshot lineage.

`SOURCES.md` records retrieval date, locally observed versions and whether evidence is documentation,
fixture, passive live observation or end-to-end verified.

## Generic `EvidenceBridge`

The design is interface-oriented; exact Go names may change after the implementation spike:

```go
type EvidenceBridge interface {
    Manifest() BridgeManifest
    Probe(ctx context.Context, host *HostView, installation Installation) ProbeResult
    Connect(ctx context.Context, target BridgeTarget, sink SafeAssertionSink) error
    Checkpoint(ctx context.Context) BridgeCheckpoint
    Health(ctx context.Context) BridgeHealth
}
```

`BridgeManifest` declares:

- adapter ID and bridge ID/version;
- supported agent version range;
- protocol/schema versions;
- capability assertions it may emit;
- safe field allowlist and prohibited surfaces;
- permissions and target scope;
- frame/message/time/restart/reconnect limits;
- idempotency and checkpoint strategy;
- fixture and canary IDs.

Core supervises lifecycle, bounds, health and safe assertion acceptance. The adapter owns protocol
translation. A bridge never receives a pool, SQL API, mutation bearer or unrestricted filesystem
handle.

## Codex App Server bridge

The initial bridge is opt-in and version-pinned. It MAY consume:

- initialization/server version metadata;
- `skills/list` and `skills/changed`;
- explicit typed skill turn inputs;
- plugin inventory/read metadata needed for identity only;
- `mcpServerStatus/list`;
- typed `mcpToolCall` identity/status;
- item start/completion and thread/turn/session identifiers required for correlation.

It MUST discard before logs, queues and persistence:

- prompts and agent messages;
- reasoning;
- tool/MCP arguments, results and raw errors;
- file contents and unredacted paths;
- resource contents/URIs unless reduced to approved opaque identity;
- environment/config values;
- any unknown fields.

An invalid owned frame creates metadata-only quarantine and a bridge capability incident. Known
service methods and responses not owned by the bridge are filtered without quarantine. The bridge
does not fall back to parsing raw rollout content.

The bridge connects to an explicitly configured local App Server target. It does not start, stop or
reconfigure Codex without a later ChangePlan. Reconnect uses bounded exponential backoff and a
durable safe checkpoint only where the source protocol supports replay; otherwise gaps remain
visible.

## Claude bridge/evidence lane

Claude Code remains primarily OTel/hook based unless a separately documented richer local interface
is proven. Dedicated `skill_activated`, `plugin_loaded`, `mcp_server_connection` and typed tool
events are mapped by version. `OTEL_LOG_TOOL_DETAILS` is not enabled by Kansoku's default plan
because it widens upstream exposure. Redacted third-party identity stays redacted or unresolved.

## Safe assertion and deduplication

Every accepted bridge assertion carries:

- source instance and lane;
- installation/surface/session/turn safe IDs as applicable;
- canonical subject and event type;
- observed time and source sequence;
- adapter/bridge/schema versions;
- evidence tier and confidence;
- idempotency key;
- native correlation IDs after scoped pseudonymization;
- value state.

Logical fact identity is lane-independent. Evidence identity includes the lane and native source
record identity. OTel, hook and bridge observations can corroborate one fact but cannot create
multiple model requests or component invocations.

Contradictory terminal outcomes open an incident and retain both evidence records. Ambiguous
installation/session correlation remains plural.

## Installation and display identity

The durable installation key remains opaque. A display record contains:

- adapter-defined provider/display name;
- surface kind;
- optional user alias;
- observed version;
- pseudonymous short installation suffix for diagnostics;
- completeness and source provenance.

The provider is never derived from the model. Unknown adapter identity displays `Unknown agent`
with the opaque suffix and incident link.

## Dimensional propagation

The data model MUST make installation attribution queryable for sessions, prompts, model operations,
tool calls and component evidence. Preferred order:

1. exact native installation/session relation;
2. exact event foreign key;
3. deterministic source-instance relation;
4. bounded candidate relations;
5. unresolved.

No batch migration may force a single installation onto ambiguous historical facts. Query formulas
separate exact, candidate/ambiguous and excluded populations.

## Agent/model API

- agent list with alias/provider/surface/version/source health;
- agent detail summary and time series;
- per-agent model usage table;
- per-agent × model request/token/cost/latency/error breakdown;
- source/capability matrix;
- linked component/tool/MCP and incident summaries.

All range routes use the shared timezone/granularity contract. p95 is computed from raw facts or a
registered mergeable distribution, never by averaging percentiles.

Cost rows require model ID, exact token categories, matching price snapshot and formula version.
Unknown pricing or incomplete token splits are excluded with counts. Subscription billing is never
claimed.

## Health and audit

Track bridge discovered/configured/connected/producing/reconciled independently. Audit:

- bridge version/schema compatibility;
- checkpoint/source sequence gaps;
- duplicated logical facts across lanes;
- unresolved installation/session rate;
- source freshness relative to eligible agent activity;
- price/formula freshness;
- canary chain and privacy sinks.

## Conformance fixture

Add a differently shaped fake bridge whose protocol and names do not resemble Codex or Claude. It
must populate the same safe assertion sink and agent profile without editing core routing. This is
the proof of extensibility.

## Live verification

Run a bounded non-user-repository canary with:

- Codex `gpt-5.6-sol`;
- Codex `gpt-5.6-terra`;
- Claude Code when locally available;
- the fake bridge.

The live run records exact start/end UTC, local executable versions, bridge/source health and DB
before/after counts. It never assumes model availability; an unavailable target is reported rather
than silently substituted.

## Tests

- bridge manifest/schema/permission conformance;
- App Server sanitized typed fixtures and unknown schema;
- reconnect/checkpoint/duplicate/out-of-order/property tests;
- cross-lane fact/evidence reconciliation;
- ambiguous installation/session fixtures;
- cost/latency formula goldens;
- agent list/profile browser and URL-state tests;
- ten-sink content canary;
- source-loss and contradiction incidents;
- production image rebuild and live DB reconciliation.

## Exit gate

The Engineering Proposal gate is proven by fixtures and live evidence. A bridge can be removed
without losing OTel-backed history, two lanes never double-count one fact, and a third fake adapter
bridge works without core agent-name branches.

## Reconciliation notes

- `event_id`/`fact_key` remain lane-independent; `event_evidence.evidence_id` is lane-dependent.
  A canonical event conflict therefore preserves the first fact while still accepting independent
  evidence. Only a repeated evidence key increments `replay_count`.
- Fresh projections populate exact installation columns and attribution relations. Historical rows
  use exact event lineage at query time; migration 0008 performs no forced historical backfill.
- Core provider fallback is the adapter identity. No provider is inferred from a model and the
  former agent-name provider switch was removed.
- The first App Server implementation accepts only the locally generated Codex 0.145.0 schema
  subset documented in `SOURCES.md`. Invalid owned skill/plugin frames become metadata-only bridge
  incidents; known service methods and unowned responses are ignored as multiplexed traffic.

Normal runtime wiring was completed on 2026-07-29. `CodexAppServerIngress` is an authenticated
request-scoped supervisor, not a Codex launcher or transparent CLI observer. Trusted orchestration
binds an opaque installation ID after SafeRecord validation; no installation is read from frame
content. Source health is `configured` without a stream, `producing/observed` after accepted typed
records, and `degraded` after owned rejection or sink failure.

Bridge `0.2.0` demultiplexes `plugin/read` alongside concurrent `skills/list` requests. Because a
JSON-RPC metadata response has no source timestamp, plugin and skills-list snapshots use a UTC-day
bucket and position-independent evidence key. Reconnect/retry in the same day increments only the
existing evidence replay count; a later day is a new bounded inventory observation. `plugin/read`
emits plugin `requested` and conditional installed/enabled assertions, plus conditional
installed/enabled child assertions for safe skill/MCP/hook/app identities. Children carry the
owner-qualified plugin identity. Unrepresentable names are retained as HMAC-only redacted
assertions rather than silently dropped. Metadata read never means plugin invoked/loaded.

## Versioned component resolution (2026-07-28)

Dataplatform migration `0013_component_identity_resolution` additively adds component kind,
qualified/owner identity metadata, invocation mode, upstream identity hash and resolution version.
It creates append-only `component_assertion_resolution_history` and
`component_assertion_current_resolution`. Inventory scans re-run the namespace-aware resolver only
for unresolved/ambiguous/redacted evidence and append a new decision; historical assertions are
never updated. Down migration removes the additive view/history/columns but cannot reconstruct
consumer behavior that relied on newer resolution semantics, so production rollback is
application-first and retains a pre-migration backup.
