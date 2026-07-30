# Wave C reconciliation — agent profiles

Date: 2026-07-30  
Scope: R06 Agent profiles  
Implementation commits: `41a5925`, `8ed89b9`

## Exit-gate result

- The saved Chrome harness opened all five range-active agent profiles. All five profile API
  requests returned HTTP 200; there were no failed requests or uncaught runtime exceptions.
- Direct sequential requests for the largest 30-day profiles completed in 62.46–93.84 ms. The
  largest valid browser resource duration was 131.7 ms. The
  `agent_profile_range <= 200 ms` contract was retained without increasing the timeout.
- A PostgreSQL integration test exports a repeatable-read snapshot, inserts a concurrent fact after
  export, proves it is absent from every first-response contour, and proves the next profile request
  sees it.
- `agent_id` and `adapter_id` are distinct API fields. Profile/list collections are initialized to
  `[]`.
- Installation class is explicit metadata with provenance. Live reconciliation found two `real`,
  three `canary` and one `fixture` installation. No canary or historical telemetry was deleted.

## Cost and display reconciliation

Provider-reported and public-API-equivalent estimates are separate populations:

| Adapter | Provider-reported requests | Provider micros | API-estimated requests | API-equivalent micros |
|---|---:|---:|---:|---:|
| Claude | 1,564 | 103,730,107 | 0 | 0 |
| Codex | 0 | 0 | 4,655 | 608,160,771 |

The values above are a point-in-time live reconciliation and will grow with collection. The UI
does not add the lanes together and explicitly says that Codex API-equivalent estimates are not
subscription billed spend. Token composition and grouped cost charts expose coverage counts.

## Resource, privacy and retention review

- Migration 0016 added approximately 33 MiB of partition child indexes at reconciliation time
  (about 24 MiB for event activity and 8.9 MiB for evidence-source reads). This is additive storage;
  retention of normalized facts is unchanged.
- Six short read-only contour transactions can each use up to 16 MiB `work_mem` only for operators
  that require it. Pool bounds, per-contour 200 ms timeouts and disabled parallel gather bound
  request-local resource amplification.
- The implementation reads normalized metadata and counters only. It adds no raw prompts,
  responses, source, tool payloads, environment values, credentials or filesystem paths.
- Migrations are additive and do not rewrite historical fact/evidence rows.

## Validation evidence

- `go test ./internal/dataplatform ./internal/runtime -race -count=1`
- `python3 scripts/validate_data_platform.py`
- `python3 scripts/validate_runtime.py --runtime-only`
- frontend component, normalization, typecheck, accessibility, build and embed/dist parity
- saved live Chrome harness, including class filtering and profile chart assertions

Residual risk: the 200 ms proof is for the current appliance dataset and existing synthetic
contract scale. Index size and pool wait time must continue to be audited as retention grows.
