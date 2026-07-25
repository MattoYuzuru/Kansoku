# ADR 0015 — Native OTel projections and dashboard reconciliation

- Status: accepted
- Date: 2026-07-25
- Owners: Kansoku observability/data-platform
- Extends: ADR 0006, ADR 0007, ADR 0009, ADR 0010, ADR 0014

## Context

After ADR 0014 made real Codex and Claude resources reachable, live PostgreSQL inspection still
showed 147 generic events but no `prompt_features`, `tool_calls`, `model_operations`,
`token_usage`, or `source_watermarks`. Every event also synthesized a component and turn, activity
duration multiplied each session span by its event count, and real safe metadata was normalized to
`unknown`. The dashboard therefore displayed an `ain_fixture` installation, zero prompts/models/
tokens/components, four unknown schemas, and an `Unsupported` panel before the real ingestion
facts.

Official Codex and Claude documentation, plus a bounded local capture, also established that
native attributes are typed and event-specific: prompt length, tool duration/outcome, model/token/
cost values, and Claude skill/plugin lifecycle names. The receiver previously retained strings
only and trusted the instrumentation scope where both agents put the per-record identity in
`event.name`.

## Decision

1. Read the per-record `event.name`, translate only adapter-declared bounded attributes, retain
   integer/boolean types, and quarantine unknown names or incompatible shapes as structural
   metadata without rejecting unrelated records in the same export.
2. Default a successfully mapped real OTel record to value state `observed`; absence, redaction,
   unsupported and numeric zero remain distinct. Raw bodies and content-bearing attributes remain
   outside the persistence model.
3. Count `tool_result`, not `tool_decision`, as the physical tool execution. Map completed model
   records and Claude component lifecycle records to canonical events.
4. Project normalized facts idempotently into `prompt_features`, `tool_calls`,
   `model_operations`, `token_usage`, provider/model dimensions and `source_watermarks`. Replaying
   an event may increase evidence replay count but cannot inflate any projection.
5. Preserve native `prompt.id` as a device-keyed turn pseudonym. Do not synthesize a component for
   prompt/model/session events.
6. Compute active duration once per session/day rather than once per event.
7. Keep historical rows unchanged. New correct evidence coexists with old unknown/fixture-shaped
   telemetry; reconciliation reports the residual instead of rewriting user history.
8. Replace the Overview headline `Unsupported` block with durable accepted-event, known-value and
   quarantine facts. Event-value completeness is explicitly not relabelled as source coverage.

## Consequences

- New real traffic populates the dashboard’s prompt, tool, model, token, cost, agent and component
  paths from one canonical normalized fact.
- Watermark freshness becomes observable after a committed source event.
- Migration `0004_native_telemetry_metadata` adds nullable prompt-character and provider-cost
  columns; older rows remain valid.
- Projection writes follow the canonical fact commit and are replay-healable, but are not yet one
  PostgreSQL transaction with the fact insert. A process failure in that small window requires
  replay and remains a recorded residual risk.
- Source coverage still requires an independently observed expected-event population. Kansoku
  does not fabricate that denominator from accepted events.

## Rejected alternatives

- Reinterpret generic events at query time. This would duplicate adapter parsing across every
  dashboard query and make schema drift invisible.
- Backfill or mutate the 147 historical rows. That would violate the telemetry-history contract.
- Count both tool decision and result. That deterministically doubles tool calls.
- Show event completeness as `collection.coverage_ratio`. The denominators are different.
