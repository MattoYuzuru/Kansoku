# Canonical event contract

## Goals

- Normalize heterogeneous agents without erasing source semantics.
- Make duplicates, gaps, inference and unknown schemas explicit.
- Prevent prohibited content from crossing the trusted ingress boundary.
- Support replay and formula evolution without rewriting source history.

## Envelope v1

Illustrative JSON; the authoritative schema will be generated and versioned in code.

```json
{
  "spec_version": "kansoku.event/1",
  "event_id": "01J...",
  "event_type": "component.invoked",
  "emitted_at": "2026-07-20T10:03:12.123Z",
  "observed_at": "2026-07-20T10:03:12.140Z",
  "ingested_at": "2026-07-20T10:03:12.180Z",
  "source": {
    "adapter_id": "codex",
    "adapter_version": "0.1.0",
    "source_kind": "otel_log",
    "source_schema": "codex.tool_result/observed-fingerprint",
    "installation_id": "ins_...",
    "native_event_id": "optional-scoped-id",
    "sequence": 42
  },
  "scope": {
    "device_id": "dev_...",
    "agent_installation_id": "ain_...",
    "surface_id": "sur_...",
    "project_id": "prj_...",
    "session_id": "ses_...",
    "turn_id": "trn_...",
    "parent_event_id": null
  },
  "subject": {
    "kind": "skill",
    "component_id": "cmp_...",
    "component_version_id": "cmv_..."
  },
  "measurements": {
    "duration_ms": 12,
    "success": true
  },
  "evidence": {
    "tier": "native",
    "confidence": 1.0,
    "completeness": "complete",
    "redactions": []
  },
  "attributes": {
    "invocation_mode": "implicit"
  }
}
```

`attributes` is an allowlisted, versioned map. Unknown source fields do not automatically enter it.

## Event namespaces

- `inventory.*`: agent/component discovered, enabled, disabled, upgraded, removed, exposed.
- `session.*`: started, resumed, compacted, stopped.
- `turn.*`: started, completed, interrupted.
- `prompt.*`: submitted metadata; never content.
- `model.*`: request, response, fallback, error and token/cost measurements.
- `component.*`: opportunity, invoked, loaded, executed, succeeded, failed.
- `tool.*`: advertised, called, approved, denied, succeeded, failed, timed_out.
- `mcp.*`: configured, connected, disconnected, tool inventory and auth failure.
- `change.*`: files/diff/tests/commit/PR metadata when safely exposed.
- `collector.*`: source heartbeat, gap, parse failure, drift, audit and incident lifecycle.
- `privacy.*`: redaction count, prohibited-field rejection and retention action.

## Identity

- Internal IDs are ULID/UUID-style opaque values.
- Native identifiers are scoped by adapter + installation + source kind; never assumed globally
  unique.
- Filesystem/project identities use a keyed HMAC over canonicalized path plus device scope. The key
  is not stored in PostgreSQL. Users MAY assign a display alias.
- Components use a canonical identity tuple: kind, ecosystem, publisher/source, package/plugin,
  declared name and installation scope. Same-name skills remain distinct and may have a collision
  relationship.
- Model IDs preserve provider-reported value plus a normalized family alias; price lookup never
  rewrites the reported value.

## Time

- Store UTC timestamps with microsecond precision.
- `emitted_at` is source time, `observed_at` is collector boundary, `ingested_at` is durable accept.
- If source time is absent/untrusted, mark `timestamp_quality` and use observed time without hiding
  the substitution.
- Late events update affected rollups; source watermarks determine completeness.

## Idempotency

Preferred key:

```text
adapter_id + installation_id + source_kind + native_event_id + event_type
```

Fallback key is HMAC of a source-specific canonical tuple excluding ingestion time. A unique index
stores the key. Replays attach new evidence/replay metadata but do not increment facts.

## Evidence and confidence

| Tier | Meaning | Default confidence ceiling |
|---|---|---:|
| corroborated | Independent sources agree on one fact | 1.0 |
| native | Stable documented explicit event | 1.0 |
| reconstructed | Deterministic transcript/config parser | 0.95 |
| inferred | Heuristic/classifier result | < 0.90 |
| unsupported | No trustworthy evidence | N/A |

Confidence is not averaged blindly. Reconciliation rules are capability-specific and retain all
evidence. Contradiction creates an incident rather than selecting the most convenient value.

## Completeness

`complete`, `partial`, `degraded`, `unknown`, `unsupported` apply per capability and time interval.
Completeness depends on adapter contract, source health, watermark, schema support and reconciliation,
not merely the presence of events.

## Sanitization

Ingress uses an allowlist per source schema. Prohibited fields are discarded before durable queues,
quarantine or logs. The sanitizer emits only category/count metadata. Payloads over limits are
rejected with safe diagnostics. Error messages contain field paths, not values.

## Unknown schema behavior

1. Identify source/version/fingerprint without storing content.
2. Run generic prohibited-field removal.
3. Persist metadata-only quarantine record with count/size/fingerprint/reason.
4. Mark capability degraded from first affected timestamp.
5. Alert and require an adapter/fixture update.
6. Never silently pass unknown maps into analytics.

## Schema evolution

- Additive optional fields are backward compatible after allowlist review.
- Semantic changes require a new event/spec or formula version.
- Adapters declare min/max supported source versions and observed fingerprints.
- Database migrations never reinterpret historical facts without a versioned replay and audit log.

