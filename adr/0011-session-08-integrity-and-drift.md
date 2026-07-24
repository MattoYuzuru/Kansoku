# ADR 0011: Session 08 integrity, drift and daily audit engine

- Status: accepted
- Date: 2026-07-23
- Owners: Session 08 core architecture

## Context

Sessions 01-07 built the product contracts, the privacy boundary, the canonical observability
envelope/lifecycle/ingress/reconciliation engine, the PostgreSQL 18 data platform, the Adapter SDK,
and two real adapters (Codex, Claude) plus two fictional conformance adapters (Loomwright,
Wayfinder). None of that work answers a different question: is collection still correct today? An
agent can be upgraded, a hook can be silently disabled, an OTel exporter can point at the wrong
endpoint, a parser can start silently dropping a new event type, and every prior session's machinery
will keep running without ever telling anyone that it stopped seeing what it should be seeing.
Session 08 exists to make that failure class observable through one orchestrated daily audit,
described in full in "Engineering Proposal/08-integrity-drift-and-daily-audit.md" and
"Technical Design Document/08-integrity-drift-and-daily-audit.md".

This is also the first session that needs a durable scheduled job. There is no scheduler, cron,
advisory lock, or workflow state machine anywhere in the repository before this session. The audit
must run once a day, must never run twice concurrently, must survive a crash mid-run without
resuming from unknown state, and must additionally run in a reduced mode on startup and after any
agent/adapter/fixture/formula-registry version change. Building this reliably from scratch, in Go,
using only what the repository already has (the stdlib and the existing `*pgxpool.Pool` from
`internal/dataplatform`) rather than introducing a new external job-scheduling dependency is the
central technical decision this ADR records.

This session reuses, rather than reinvents, five prior boundaries:

- `internal/observability/types.go`'s existing `Watermark` and `Incident` types (and the
  `Completeness`/`SourceLifecycle` enums, and `contracts/observability/reconciliation.yaml`'s exact
  silence classification: `eligible_activity`-stalled opens a gap incident, `true_inactivity` sets no
  gap incident, `eligibility_unknown` stays unknown after a threshold) are the ONE watermark/incident
  concept in the repository; this session emits into that same concept, never a second one.
- `internal/dataplatform`'s already-exposed `ApplyRetention`, `CreateBackup`,
  `VerifyBackupChecksum`/`CountRows`/`RestoreBackup`, `RepairQueueDepth` and `BucketStart` functions
  are the only backup/retention/rollup-freshness mechanism this session's storage/operations audit
  stage calls; no second backup or retention path is declared.
- `internal/adaptersdk`'s `Registry.IDs()`/`Get()`/`Manifest()` and the already-declared
  `Adapter.Audit(ctx, target, mode) []CheckResult` method (with its `AuditMode`
  passive/fixture_replay/live_canary and `CheckResult` `CheckID`/`CapabilityID`/`Mode`/`Status`/
  `DetailRef`/`ObservedAt` shape) is the one audit entrypoint every adapter already exposes; the
  engine drives that method generically, adding no second per-adapter audit entrypoint and no
  hardcoded agent-name branch in its own core files.
- `internal/observability/routes.go`'s generic `/v1/hooks/{adapter}/{event}` ingress (with its
  existing `fixture-agent`, `codexadapter.AdapterID` and `claudeadapter.AdapterID` cases) is the one
  public ingress the synthetic pipeline probe sends its uniquely tagged safe record through; no
  parallel test-only ingress path is created.
- `internal/privacy`'s `SafeRecord`/`SafeError`/`ExtractPromptFeatures` sanitizer remains the only
  trust boundary any audit evidence, incident detail record, or drift fingerprint may cross; this
  session's structural-only drift fingerprints are computed from field names/paths/primitive types
  after the same prohibited-field categorization, never from sampled values.

Because the sequential-checkpointed-stage pattern worked reliably for Sessions 06 and 07 after
earlier single-shot attempts crashed on transient corporate-VPN-related API errors, Session 08 is
built the same way: contracts/ADR skeleton, scheduler/state-machine core, discovery/config/freshness
checks, schema/parser/synthetic-pipeline checks, reconciliation/storage/incident-lifecycle checks,
health API/drift-fingerprints/live-canary/fault-injection suite, and a final validators/reports/
doc-sync/full-suite stage.

## Decision

1. The authoritative Session 08 contract is the closed registry set in
   `contracts/integrity/{audit-run-and-schedule,drift-fingerprint-and-schema,incident-and-health,
   fault-injection-and-live-canary}.yaml`, locked by `contracts/integrity-policy-locks.yaml` using
   the identical append-only semantic-digest mechanism already used for `contracts/privacy`,
   `contracts/observability`, `contracts/data-platform`, `contracts/adapter-sdk`, `contracts/codex`,
   `contracts/claude` and `contracts/cross-agent`. No prior trusted lock entry in any of those seven
   earlier files is edited; this session only appends new lock entries scoped to the four new
   integrity registry files.
2. The daily scheduler ("daily-integrity" workflow) is built on a PostgreSQL session-scoped advisory
   lock (`pg_try_advisory_lock`) acquired through `internal/dataplatform`'s existing `*pgxpool.Pool`,
   never a new external job-scheduling dependency (no cron daemon, no message queue, no third-party
   scheduler library). A session-scoped lock releases automatically when the holding connection
   closes, which is exactly the crash-safety property a single-writer guarantee needs without a
   manual unlock step. If a future session finds this insufficient and genuinely needs a new
   dependency, ADR precedent (Session 04 vendoring pgx) requires it be vendored properly into
   `go.mod`/`go.sum` and disclosed honestly in that session's SBOM — this session finds no such need.
3. `contracts/integrity/audit-run-and-schedule.yaml` declares the audit-run state machine
   (`scheduled -> running -> passed|degraded|failed|cancelled`, with degraded/failed evaluating the
   incident model rather than passed), the 11 daily-audit stages as an ordered, idempotent,
   timeout-bounded stage registry, the `full`/`reduced` run modes and their trigger conditions
   (`scheduled_daily`, `startup`, `version_change_detected`, `manual_operator_request`), and the
   crash-recovery rule that a stale `running` row is marked `interrupted` on process start and never
   silently resumed — the next eligible trigger starts a brand-new `audit_run` row and retries only
   checks lacking fresh positive evidence.
4. `contracts/integrity/drift-fingerprint-and-schema.yaml` declares five fingerprint kinds
   (executable version, config-recipe fingerprint, adapter version, fixture version, formula-registry
   version) plus the event-schema fingerprint (event/type names, field paths, primitive types after
   prohibited-field categorization), each reusing an existing identity field
   (`SourceRef.SchemaFingerprint`, `PlanSHA256`, `SeedFormulaVersion`, committed fixture bytes) rather
   than inventing a parallel one, and states unconditionally that no fingerprint kind ever samples,
   stores or hashes a field VALUE — only names, paths and primitive types survive.
5. `contracts/integrity/incident-and-health.yaml` states explicitly that `internal/observability`'s
   existing `Incident` struct remains the one Go incident concept. Session 08 aliases that exact
   type for its PostgreSQL scheduler projection; where it needs additional fields (installation_id,
   source_id, failure_class, affected_interval, check_evidence_ref, recovery_criteria, and similar),
   it persists a session-08-owned extension row keyed 1:1 by `IncidentID` rather than forking
   `Incident` into a competing struct. The incident key is
   `installation + source + capability + failure-class` from a
   closed `failure_class` vocabulary. It also declares nine health dimensions (configuration,
   connectivity, event freshness, schema compatibility, parser fixture status, reconciliation
   coverage, privacy canary, live-canary age/result, storage/rollup health), each independently
   green/yellow/red/gray, with gray as the mandatory default before any check has run and green
   requiring an actual runtime check (this run or a still-fresh, explicitly aged prior run), never an
   assumption. It states that these health dimensions are DERIVED FROM `internal/adaptersdk`'s
   existing `CapabilityState` plus `Watermark`/`Incident` evidence, joined by `capability_id` — never
   a second, competing per-capability enum that conflicts with `CapabilityState`.
6. `contracts/integrity/fault-injection-and-live-canary.yaml` is the closed fault-injection catalog:
   one entry per TDD "Fault-injection tests" bullet and per Engineering Proposal "Failure modes to
   detect" bullet, each naming its target detection claim, expected detection SLO in seconds,
   expected incident failure class and evidence level. The 21 claims are exactly partitioned into
   17 component classifiers, 2 deterministic mutation integrations and 2 runtime-required scenarios.
   Component classifiers prove classification semantics but never claim end-to-end SLO; only a real
   mutation/runtime harness may measure SLO, at the actual durable `Incident.OpenedAt`. It also declares the live-canary
   recipe schema (command as an argv list never a shell string, fixture workspace, uniquely namespaced
   canary skill/plugin/local-MCP-echo tool, expected event DAG, max turns/tokens/cost/duration,
   cooldown, cleanup, namespace exclusion from personal usage metrics) and its
   `disabled_by_default_gate`: every recipe has `enabled=false` until both explicit credentials and
   explicit recorded consent exist, with no code path silently flipping that gate, matching the
   Session 06/07 precedent that Codex/Claude ship fixture-based canaries, not live-CLI execution.
7. `contracts/README.md` describes these four registries and their lock file and includes the
   validator, full-suite, privacy and Session 08 supply-chain commands.
8. This session was executed as sequential checkpointed stages (contracts/ADR skeleton; scheduler core
   and audit-run state machine; discovery/config and freshness checks; schema/parser drift and
   synthetic pipeline checks; reconciliation and storage/operations/privacy checks plus incident
   lifecycle; health API, drift fingerprints, live-canary harness and fault-injection suite;
   validators/Python tests/reports/doc-sync/full-suite verification), the same pattern that worked
   reliably for Sessions 06 and 07. Each stage begins by reading current repository state rather than
   assuming a clean slate, and treats any inconsistency it finds as a defect to fix or regenerate
   correctly, never as scaffolding to build on top of uncritically.

## Consequences

- The Go implementation (`internal/integrity`, by naming precedent with
  `internal/dataplatform`/`internal/adaptersdk`) must reuse `internal/dataplatform`'s existing
  `*pgxpool.Pool` connection, never open a second database connection pool, and must call
  `ApplyRetention`/`CreateBackup`/`VerifyBackupChecksum`/`CountRows`/`RestoreBackup`/
  `RepairQueueDepth`/`BucketStart` directly rather than reimplementing backup/retention/rollup-
  freshness logic a second time.
- The audit engine's core files must drive every registered adapter through
  `Registry.IDs()`/`Get()`/`Manifest()`/`Audit()` generically; a future stage that adds a hardcoded
  `if adapterID == "codex"` branch inside the engine's own core files (as opposed to inside an
  adapter's own package) would be a regression against this ADR's decision and against ADR 0008's
  identical zero-agent-name-branch invariant for `internal/adaptersdk`.
- The synthetic pipeline probe must send its uniquely tagged safe record through the same public
  `/v1/hooks/{adapter}/{event}`, `/v1/logs`, `/v1/traces` and `/v1/metrics` routes
  `internal/observability/routes.go` already exposes, traverse the Session 04 event/evidence and
  rollup/query path, and expire only its exact HMAC-identified test namespace so it never pollutes
  real personal usage metrics or removes a concurrent unrelated fixture record.
- Session 08 tables use an `integrity_` namespace, including
  `integrity_audit_runs`/`integrity_audit_checks`; Session 04 already owns different
  `audit_runs`/`audit_checks` tables in the same schema. Both migration ledgers are checked for an
  exact embedded version/checksum match.
- Production uses one validated assembly boundary. Missing mandatory stages, conformance-only
  synthetic wiring, incomplete storage dependencies, enabled live canaries without durable cooldown
  state, and absent report signing fail before a scheduler loop starts.
- Every advertised detection claim in `fault-injection-and-live-canary.yaml`'s catalog must have
  evidence matching its declared level. Component-classifier coverage is not runtime SLO evidence;
  deterministic mutation and runtime-required claims must use actual mutation/runtime execution and
  measure the persisted incident's `OpenedAt`. A missing or mismatched claim is a contract violation
  this session's exit gate rejects; there is no aggregate 21-fault runtime claim.
- The live-canary recipe/budget/cooldown/cleanup machinery is built structurally in this session but
  is not exercised against a real external agent process or a real account in this environment; it
  ships disabled by default, matching the Session 06/07 canary-fixture-not-live-CLI precedent, and any
  future session that wants to actually enable it must first satisfy the explicit
  credentials-and-consent gate this ADR records, not bypass it.
- Future changes inherit an explicit obligation to keep
  `contracts/integrity/*`, `contracts/integrity-policy-locks.yaml`, this ADR, and eventually
  `internal/integrity`'s scheduler/checks/health-API/fault-injection-suite code mutually consistent at
  every checkpoint, not only at the final stage.

## Rejected alternatives

- **Introduce a third-party job-scheduling library (e.g. a cron-expression parser package, a
  distributed task queue) instead of a PostgreSQL advisory lock:** would add a new external
  dependency for a problem the existing `*pgxpool.Pool` already solves cleanly; a session-scoped
  advisory lock gives exactly the single-writer-plus-automatic-crash-release property this session
  needs without a new vendored package, and the repository's existing precedent (ADR 0007 vendoring
  pgx only once, for the data platform itself) argues against a second infrastructure dependency for
  a problem solvable with what is already vendored.
- **Give the audit engine its own database connection pool separate from
  `internal/dataplatform`'s:** would double the connection-management surface and risk the two pools
  disagreeing about schema/migration state; reusing the existing pool keeps exactly one source of
  truth for PostgreSQL connectivity.
- **Model Session 08's incidents as a brand-new `AuditIncident` type instead of extending the existing
  `internal/observability.Incident`:** would create two divergent incident concepts in the same
  repository, exactly what the task brief's "this is the ONE incident concept" instruction forbids; a
  keyed 1:1 extension record achieves the additional fields this session needs without forking the
  original type.
- **Collapse health dimensions into one aggregate score:** explicitly rejected by both the Engineering
  Proposal ("avoid one magic score") and the TDD ("green/yellow/red/gray is derived and never stored
  as the only evidence"); nine independently-evidenced dimensions let the dashboard answer why and
  which metrics are affected, which one score cannot.
- **Let a passing scheduled run implicitly close an incident on the mere absence of a new failing
  check:** rejected because absence of evidence (a check that did not run, a stage that was skipped in
  reduced mode) is indistinguishable from genuine recovery unless a fresh check for that exact
  incident key actually reports pass; the recovery rule requires fresh positive evidence for the same
  reason `contracts/observability/reconciliation.yaml`'s `source_return` rule already requires it for
  source health.
- **Ship the live canary enabled by default with a conservative budget, reasoning that the budget
  itself is the safety control:** rejected because credentials and consent are a categorically
  different kind of gate than a budget; a budget bounds cost once the canary is already allowed to
  run, but only explicit consent answers whether it should run against this user's real account at
  all. This is also consistent with the Session 06/07 precedent that Codex/Claude ship no live-CLI
  canary execution at all yet.
- **Attempt Session 08 as one continuous single-shot run:** the sequential-checkpointed-stage pattern
  already proved necessary for Sessions 06 and 07 after single-shot crashes on a transient
  corporate-VPN-related API failure mode; there is no new reason to believe Session 08 — arguably the
  most structurally novel session so far, introducing the repository's first durable scheduler — is
  less exposed to the same failure mode.

## Residual risks and deliberate boundaries

1. **Live provider execution remains opt-in and unclaimed.** Session 08 implements and tests the
   recipe, argv, namespace, credentials/consent gate, budgets, cooldown, exact DAG and cleanup in
   simulation only. It never starts a real external agent or touches a real account/repository.
2. **Report signing is local integrity evidence.** HMAC-SHA256 detects tampering with a report under
   the device key; it is not public-key release provenance or an externally verifiable attestation.
3. **`inventory_cache_miscount` retains the reviewed `permission_denied` class.** The detector now
   rejects a `CachedOnly` node represented by an `enabled_for` edge, but a future policy version may
   introduce a narrower class if operational evidence justifies it.
4. **External notification delivery and dashboard presentation are later-session work.** Session 08
   persists bounded incident and decomposed health evidence; Session 09/10 own serving, desktop
   notification and opt-in webhook UX.
5. **Session 07b remains explicitly excluded.** Gemini/Cursor backlog work was neither revived nor
   changed while building the generic integrity engine.
6. **Integrity metadata is not yet in the Session 04 logical backup payload.** Stage 9 verifies the
   existing data-platform backup/restore mechanism without pretending otherwise. Session 09 must
   extend disaster-recovery coverage before audit/report/incident history is treated as restorable.
7. **Bounded PostgreSQL/privacy runtime verification completed.** The full tagged integrity suite
   passed on pinned PostgreSQL 18, including both deterministic mutation integrations; the
   data-platform runtime validator and standalone ten-sink privacy canary also passed. This does not
   substitute for the still-unexecuted DB-restart/failed-restore fault transitions or a real provider
   canary, and no aggregate 21-fault runtime claim is made.
