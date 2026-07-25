# Native OTel ingestion reconciliation — 2026-07-25

## Exit gate

New real Codex/Claude OTel records must:

- resolve the actual resource and per-record event identity;
- preserve bounded typed metadata without raw content;
- create idempotent prompt/tool/model/token/component projections;
- advance durable source freshness;
- keep normal documented exporter events out of schema-drift quarantine;
- report agent/model/component dimensions rather than fixture identities;
- make dashboard totals reconcile with PostgreSQL;
- leave pre-existing telemetry unchanged.

## Before

The live database contained:

| Table/signal | Count |
| --- | ---: |
| events / evidence | 147 / 147 |
| prompt_features | 0 |
| tool_calls | 0 |
| model_operations / token_usage | 0 / 0 |
| source_watermarks | 0 |
| schema_quarantine_metadata | 4 |

All 147 events had `value_state=unknown`; their physical installation identifier was the fixture
identifier. Activity duration repeated a full session span for every event. Overview led with a
hard-coded `Unsupported` explanation.

## Changes

- Real Codex/Claude resources and plain `event.name` dispatch.
- Typed prompt length, duration, success, token and cost translation.
- `tool_result` as the one counted tool execution; decisions and non-terminal stream records as
  non-projecting `source.observed`.
- Claude skill/plugin lifecycle mapping.
- Explicit `observed` value state for successfully mapped safe source metadata.
- Device-keyed prompt/turn pseudonym correlation.
- Idempotent prompt/tool/model/token projections and durable source watermarks.
- Session-duration aggregation once per session/day.
- Lowercase percentile JSON keys (`p50`/`p90`/`p95`/`p99`) matching the dashboard API contract.
- Legacy incident visibility restored to health and incident APIs.
- Overview now leads with accepted events, source-supplied values and unknown-schema evidence;
  event completeness is not mislabelled as collection coverage.
- Migration `0004_native_telemetry_metadata` adds nullable prompt-character and provider-cost
  columns.

## Live verification

The final low-reasoning read-only Codex exporter canary ran from
`2026-07-25T19:37:33Z` to `2026-07-25T19:37:59Z`. Its provider stream disconnected after five
retries, so the requested repository answer was not produced; the CLI still flushed OTel on exit.
For that interval PostgreSQL recorded:

- 1 `prompt.submitted`;
- 13 `tool.called`;
- 8 `model.responded`;
- 34 `source.observed`;
- 1 `session.started`;
- **0 new quarantine rows**;
- **0 turns attached to metadata-only `source.observed` rows**.

At final reconciliation time the preserved live database contained:

| Signal | Count |
| --- | ---: |
| events | 387 |
| new correctly classified `observed` events | 240 |
| preserved historical `unknown` events | 147 |
| prompt_features | 2 |
| tool_calls | 122 |
| model_operations / token_usage | 76 / 76 |
| source_watermarks | 1 |
| models | `gpt-5.6-sol` 67 operations / 11,313,555 tokens; `gpt-5.6-terra` 9 / 194,777 |

The live APIs returned `activity_timeline/2`, `prompt_shape/2` and `model_usage/2` with complete
populations for the new projections; prompt character percentiles were returned with the
frontend-compatible lowercase keys. The embedded production Overview asset contains
`Collection health`, `Accepted events` and `Source-supplied values` and no longer contains the
headline `Unsupported`.

## Automated proof

- `go test ./...` — pass.
- `python3 scripts/validate_contracts.py` — pass.
- privacy, observability, Codex, Claude and data-platform contract validators — pass.
- full Postgres `postgres_integration` suite — pass, including
  `TestObservabilityHandoffPersistsNativeTelemetryProjectionsIdempotently`.
- web TypeScript/Vite build and production embed sync — pass.
- rebuilt Compose application and PostgreSQL containers — healthy; dashboard HTTP — 200.

## Residual risks

1. The preserved 147 old events remain `unknown`; no historical telemetry was rewritten.
2. Twelve historical quarantine rows remain, including rows produced before the metadata-only
   classification correction. The final canary added zero.
3. Two legacy unknown-schema incidents remain open because the v1 legacy incident table has no
   source/fingerprint relationship that can prove which successful source recovered them.
4. Projection writes are replay-healable but are not in the same PostgreSQL transaction as the
   canonical fact.
5. Source coverage still lacks an independently observed expected-event population. The UI states
   that gap without fabricating a ratio.
6. The web install audit reported one existing high-severity dependency advisory; no dependency
   upgrade was attempted in this telemetry change.
7. The live provider canary failed at the provider stream despite successful telemetry export;
   provider/VPN reliability is outside Kansoku’s ingestion boundary.
8. Codex does not currently expose a dedicated documented skill-activation event, so its native
   OTel cannot identify which repository skill drove a tool call. Claude’s documented
   `skill_activated`/plugin mappings are implemented and fixture-tested, but were not live-verified
   in this Codex-only canary.
