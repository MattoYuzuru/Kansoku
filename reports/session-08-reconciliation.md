# Session 08 reconciliation — integrity, drift and daily audit

Date: 2026-07-24

Status: implementation and bounded runtime validation complete. The full integrity suite passed on
pinned PostgreSQL 18; the data-platform runtime validator and standalone privacy canary also pass.
The complete 21-fault exit gate remains partial only because DB restart and failed restore require
separate runtime transitions, and no real provider canary was run.

## Scope reconciled

- The 11-stage daily audit is durable in PostgreSQL, guarded by a session-scoped advisory lock and bounded by per-stage timeouts.
- Audit-check identity includes `source_id` separately from the closed adapter capability ID.
- Passive endpoint/hook verification, watermark classification, generic adapter fixture audit,
  local synthetic probe, cross-source reconciliation, unknown-schema/duplicate/lag checks,
  rollup/database/storage/privacy checks, simulated live-canary machinery and signed/versioned
  report persistence are wired as explicit stages.
- Production assembly rejects missing stages, conformance-only Stage 5 wiring, in-memory schema
  compatibility state, incomplete Stage 8/9 dependencies, enabled live-canary recipes without
  durable authorization/cooldown/cleanup and registry capability validation, and report signing
  without a named device-local key.
- Stage 5 sends one hook record plus OTLP log/span/metric records through the public handler, selects
  them by exact HMAC-pseudonymized identity, verifies four exact event/evidence pairs written by the
  same production ingress-to-PostgreSQL handoff, runs only their two targeted rollup repairs,
  verifies the budgeted query result and cleans its exact FileStore/PostgreSQL namespace, including
  `rollup_status` and partially-created dimensions after a first-insert failure.
- Structural drift fingerprints contain names/types/hashes only, reject prohibited durable field
  paths, filter revalidation by source/capability/adapter and advance their baseline only after a
  passing targeted audit.
- Health remains nine decomposed `green/yellow/red/gray` dimensions with no numeric magic score; gray is the default and green requires fresh passing evidence.
- Incidents deduplicate by installation/source/capability/failure class and close only after a later fresh passing check.

## Fault evidence (2 runtime fault claims pending)

The executable catalog contains exactly 21 claims partitioned into 17 component classifiers,
2 deterministic mutation integrations and 2 runtime-required scenarios. The 17 matched
`TestFaultComponent_<fault_id>` cases assert classification, affected interval and recovery
semantics, but intentionally do not assert end-to-end SLO.

The PostgreSQL-tagged spool-corruption and production synthetic-handoff mutation integrations perform
the real component mutation, route production detection through the scheduler and atomic Stage 11,
measure detection from the persisted incident `opened_at`, and prove recovery only after a later
scheduler pass. Both passed in the full pinned PostgreSQL 18 tagged suite. DB restart and failed
restore remain runtime-required. Therefore Session 08 makes no aggregate claim that all 21 faults
passed measured runtime SLO.

No real provider canary was run. Session 08 validates recipe argv, fixture namespace, consent,
credential-presence, durable explicit consent, measured turns/tokens/cost/duration budgets,
PostgreSQL-persisted cooldown, adapter registry validation, precise missing/extra/misordered event
DAG classification, non-cooperative observer timeout and bounded panic-safe cleanup entirely in
simulation.

## Verification

- All eight validators' static contract portions: pass. Integrity policy validation also compares the whole
  semantic document digest with independent authoritative constants and, once committed, the lock
  prefix read from `git show HEAD`.
- `python3 scripts/validate_data_platform.py --runtime-only`: pass on pinned PostgreSQL 18.
- Python contract suite passes, including 13 Session 08 independent/coherent-mutation tests.
- `internal/integrity` default Go tests pass, including the exact 17 component classifier cases.
- The 2 PostgreSQL-tagged measured mutation integrations pass; the 2 runtime-required fault
  scenarios remain pending. No aggregate 21-fault runtime claim is made.
- `CGO_ENABLED=1 go test -race ./internal/integrity`: pass.
- Repository `go vet ./...`: pass.
- Repository `go test ./...` compiled and passed every package except five platform/sandbox-only
  cases: two strict spool-mode/path tests, two loopback-listener tests denied by the sandbox, and
  one secure-keyfile backend test unsupported on this host.
- Full pinned PostgreSQL 18
  `go test -tags postgres_integration -v -count=1 ./internal/integrity/...`: pass in approximately
  2.03 seconds, including Stage 5 first-insert cleanup, migration/FK upgrade, atomic Stage 11,
  scheduler/crash recovery, strict report, fault-incident lifecycle, durable live-canary state and
  both deterministic mutation integrations.
- `python3 scripts/run_privacy_canary.py --verify-report`: pass with zero canary/secret matches
  across all 10 accepted and 10 rejected sink cases.
- Session 03, 04 and 08 deterministic SBOM/provenance verification: pass; Session 08 dependency
  delta is zero and its scoped source-manifest digest covers all Session 08 implementation,
  migrations, tests, contracts, fixtures and validator sources.
- Final Session 08 scoped source-manifest SHA-256:
  `28ceb55a8b4683b8118edc53c4af161b12a3955e88663181ffe6d3da6e89ef55`.
- `git diff --check`: pass.

## Residual risks

- HMAC report signing is device-local integrity evidence, not release-artifact/public-key signing.
- Live provider behavior remains unclaimed until a user explicitly opts in with credentials and a version-bounded recipe.
- DB restart and failed restore remain explicitly runtime-required fault cases; neither is inferred
  from the passing steady-state PostgreSQL suite.
- Session 04's logical backup format still covers the data-platform tables, not the new integrity
  audit/report/incident tables. Stage 9 verifies the existing backup/restore boundary honestly;
  extending disaster-recovery coverage to integrity metadata is a known Session 09 runtime task.
- External notifications and dashboard presentation remain later-session work; Session 08 persists the metadata-only incident/health substrate.
- Session 07b (Gemini/Cursor) remains deliberately deferred and was not revived or modified.
