# Session 03 — Core observability architecture

## Status

Implemented on 2026-07-21 for the synthetic fixture agent. The closed envelope/lifecycle/ingress/
reconciliation registries, typed Go spike and shared fixture prove the automated exit gate. This is
not an adapter support claim. ADR 0006 records the bounded file durability boundary and the explicit
OTLP gzip conformance gap.

## Purpose

Define a source-independent system that can ingest high-fidelity native telemetry and still detect
what it cannot see. The core must not encode “Claude has a Skill tool” or “Codex uses rollout JSONL”
as universal truths.

## Proposed architecture

```text
agent native OTel ───────────────┐
global lifecycle hooks ─────────┤
session/transcript importers ───┼─> ingress -> sanitize -> normalize -> reconcile -> store
inventory/config scanners ──────┤                                      |
process/filesystem probes ──────┘                                      v
                                                               rollups + API + UI
```

Each lane has different evidence strength. Native explicit activation outranks a transcript
inference; transcript evidence may fill historical gaps but cannot silently overwrite native facts.

## “100%” strategy

Absolute observation of arbitrary proprietary agents is impossible. Kansoku instead offers
coverage tiers:

1. **Tier A — corroborated:** native event plus an independent lifecycle/transcript source.
2. **Tier B — native:** documented OTel/hook/tool event with healthy contract checks.
3. **Tier C — reconstructed:** transcript/config/file evidence with deterministic parser.
4. **Tier D — inferred:** probabilistic opportunity or usage inference.
5. **Unsupported:** no trustworthy source; shown explicitly.

For supported capabilities the target is zero silent loss. Sequence gaps, unknown schemas,
watermark stalls and cross-source mismatch produce incidents and incomplete time ranges.

## Source lifecycle

`discovered -> configured -> connected -> producing -> reconciled`, with degraded/disabled/error
branches. “No events” is only healthy when Kansoku also sees no eligible agent activity.

## Event lifecycle

`received -> sanitized -> validated -> normalized -> deduplicated -> correlated -> reconciled ->
rolled_up`. Failed events go to metadata-only quarantine with reason and schema fingerprint; raw
prohibited content never goes to quarantine.

## Correlation philosophy

Native session/turn/call IDs are preserved as scoped identifiers. When missing, Kansoku correlates
using bounded time windows and source-specific stable metadata, always retaining confidence and
candidate ambiguity. It never invents a single match when multiple candidates remain.

## Heavy fallback options

These are evaluated but not silently enabled:

- launcher wrappers that enforce telemetry configuration and capture process/version/session edges;
- local model/API proxy for authoritative request/token metadata;
- filesystem event observation for transcript freshness;
- OS audit integrations (EndpointSecurity/eBPF/ETW) for process/file evidence;
- instrumented forks or upstream patches for open-source agents;
- enterprise/admin export APIs for cloud-only surfaces.

These improve coverage but carry security, portability, entitlement or maintenance costs. They are
optional adapter capabilities, not MVP requirements.

## Deliverables

- Canonical envelope and event taxonomy in `contracts/observability/envelope.yaml` and
  `Technical Design Document/canonical-event-contract.md`.
- Evidence/confidence/completeness and source/event lifecycle models in
  `contracts/observability/lifecycles.yaml`.
- Idempotency, correlation, watermarks and reconciliation rules in
  `contracts/observability/reconciliation.yaml`.
- Authenticated OTLP HTTP/gRPC binary protobuf, hook HTTP and read-only checkpointed transcript
  spikes using `tests/fixtures/session-03/shared-scenario.json`.
- Metadata-only unknown-schema quarantine, durable degraded incident and contradiction history.
- Exact capability-level exit evidence in `reports/session-03-reconciliation.md`.

## Exit gate

The same logical fixture arriving through multiple lanes produces one fact with multiple evidence
records; duplicates and reordering do not inflate metrics; removing one lane lowers completeness
without deleting facts; unknown versions create a visible degraded state.

The automated gate is executable through `scripts/validate_observability.py`, Python mutation tests
and `internal/observability` Go tests. OTLP remains Experimental: uncompressed binary protobuf is
implemented for logs, metrics and traces, while mandatory gzip support is deliberately blocked by
the current reviewed privacy policy and is not overclaimed.

## 2026-07-25 native OTel reconciliation

Real adapters read per-record `event.name`, translate typed bounded attributes, and quarantine only
genuinely unknown or drifted schemas. Documented metadata-only records use `source.observed`;
they are not silently dropped, projected as a second tool/model operation, or reported as schema
drift. Native prompt IDs are pseudonymized for turn correlation and safe values carry the explicit
`observed` state.

## 2026-07-28 durability incident amendment

PostgreSQL is the authoritative durable fact/evidence store. The local JSON state contains only
bounded watermarks, importer checkpoints and replay metadata; it is not a compatibility copy of
the historical fact corpus. Acceptance occurs only after PostgreSQL or the bounded per-lane
emergency spool owns the sanitized record. A checkpoint write failure after that boundary degrades
local replay health but cannot turn an already durable fact into a rejected ingest.

Unknown OTLP records are metadata-only, fingerprint-bounded and occurrence-rate-limited. An
unknown record does not abort later supported records in the same batch, and exact replay remains
idempotent. Retryable status is reserved for transient durability/backpressure failure.
