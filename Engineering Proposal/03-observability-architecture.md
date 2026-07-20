# Session 03 — Core observability architecture

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

- Canonical envelope and event taxonomy.
- Evidence/confidence/completeness model.
- Idempotency, correlation and reconciliation rules.
- OTLP, hook and replay ingress spikes using one shared fixture.
- Unknown-schema quarantine and visible incident.
- Capability-level coverage report.

## Exit gate

The same logical fixture arriving through multiple lanes produces one fact with multiple evidence
records; duplicates and reordering do not inflate metrics; removing one lane lowers completeness
without deleting facts; unknown versions create a visible degraded state.

