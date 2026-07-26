# Kansoku contract registries

These registries are the versioned product boundary established by Session 01. Files use the JSON
subset of YAML 1.2 so the bootstrap validator can run with the Python standard library only. Later
generators may emit native YAML without changing the schemas.

Run all contract checks with:

```sh
python3 scripts/validate_contracts.py
python3 scripts/validate_privacy.py
python3 scripts/validate_observability.py
python3 scripts/validate_data_platform.py
python3 scripts/validate_adapter_sdk.py
python3 scripts/validate_codex.py
python3 scripts/validate_claude.py
python3 scripts/validate_integrity.py
python3 scripts/validate_incidents.py
python3 -m unittest discover -s tests -v
python3 scripts/run_go_tests.py
python3 scripts/run_privacy_canary.py
python3 scripts/session03_supply_chain.py --verify
python3 scripts/session04_supply_chain.py --verify
python3 scripts/session08_supply_chain.py --verify
```

Session 02 adds the closed privacy/security registry set under `contracts/privacy/`:

- `threat-model.yaml` and `data-classes.yaml` define protected assets, abuse cases and treatments;
- `ingress.yaml` fixes decoder bounds and exact typed durable/error/log allowlists;
- `sinks.yaml` makes database, logs, traces, durable/retry queues, quarantine, errors, dashboard
  traffic, export and backup mandatory zero-canary scopes;
- `installer.yaml` and `host-access.yaml` bind per-target preview/consent/race/rollback and every
  permitted host/config access;
- `deployment.yaml` and `retention.yaml` define local HTTP, container, egress, deletion and backup
  controls.

The executable Session 02 boundary is stdlib-only Go. Every privacy registry is recursively closed
and its canonical aggregate SHA-256 is embedded in the privacy, installer and HTTP packages; a
registry/runtime drift fails validation. That self-updated aggregate is a drift check, not a policy
trust anchor. `contracts/privacy-policy-locks.yaml` independently binds versioned canonical
semantic digests for all eight registries and preserves trusted-history entries append-only;
review-controlled exact invariants reject coherent registry/runtime/checksum weakenings.
`SafeRecord`, `SafeError` and every nested type are exact
typed allowlists; source maps never cross the sanitizer. The installer package exposes typed
Codex/Claude/Gemini/Cursor plan builders, effective-setting/canary verification and virtual apply/
rollback/removal only; it contains no agent-config filesystem writer.

Registry changes require a schema or formula version change, deterministic fixtures, and an update
to the relevant proposal/TDD. Existing entries in `formula-version-locks.yaml` are append-only after
their first trusted commit; privacy-policy locks follow the same reviewed version-transition rule.
In an archive or before the first lock commit, the checked-out policy lock is the deterministic
bootstrap authority. Afterward, protected review/CI must provide the external trusted revision;
local validation cannot resist simultaneous malicious replacement of validator, lock, tests and
history. A support label is a
capability-and-version claim, never a brand-wide claim. Supported and Beta both require an explicitly
parsed and ordered capability version range, typed evidence receipts bound to the exact
adapter/capability/range tuple and validator-recomputed canonical fixture bytes, and two independent
approved human review receipts bound to the same tuple and verified fixtures. Session sequencing
never bypasses that public-claim gate.

Session 03 adds four closed registries under `contracts/observability/`: canonical envelope,
source/event lifecycle, ingress protocols/durability, and reconciliation/watermarks/recovery. Their
versioned semantic digests live in `contracts/observability-policy-locks.yaml`; independent exact
invariants prevent a coherent lock edit from removing ambiguity, privacy, durable acknowledgement,
HTTP/gRPC protobuf, three-lane or metadata-only-quarantine requirements. The synthetic shared
scenario is `tests/fixtures/session-03/shared-scenario.json`. ADR 0006 limits the current durable
writer to a bounded single-process fsync/rename spike and records that OTLP gzip remains an explicit
conformance gap rather than a support claim.

Session 04 adds four closed registries under `contracts/data-platform/`: schema, rollups,
query-contract and retention. Their versioned semantic digests live in
`contracts/data-platform-policy-locks.yaml`; independent exact invariants prevent a coherent lock
edit from weakening the pinned PostgreSQL 18 engine digest, the partition-drop-only retention
mechanism, the percentile method/forbidden-averaging clause, the budgeted query id set/ceilings, or
the restore-test cleanup requirement. `scripts/validate_data_platform.py` starts an ephemeral,
pinned-digest Postgres container to reconcile a synthetic reference dataset exactly, enforce query
budgets on both the server and client side, and verify lineage-preserving backup/restore. ADR 0007
records mergeable percentile sketches, million-event query-budget evidence, time-range preset
resolution and cost-formula computation as explicit downstream gaps rather than silent scope drops.

Session 05 adds four closed registries under `contracts/adapter-sdk/`: manifest/parse-limits/
external-protocol, capability model/state machine, inventory entity-graph and discovery-safety/
change-plan/CLI-concept. Their versioned semantic digests live in
`contracts/adapter-sdk-policy-locks.yaml`; independent exact invariants prevent a coherent lock edit
from weakening the no-agent-name-branch/brand-binding invariant, the closed capability/node/edge
vocabulary, the never-scan-the-home-directory and no-code-execution discovery safety rules, the
HostView guarantee that no adapter ever receives a database credential or unscoped filesystem
handle, or the requirement that `ChangePlan` apply/rollback/removal reuse `internal/installer`'s
existing `Plan`/`Approval`/`SimulateApply`/`SimulateRollback`/`SimulateRemove`/`PlanSHA256` machinery
instead of a second parallel mechanism. `scripts/validate_adapter_sdk.py` cross-checks
`internal/adaptersdk` and its `fakeadapter` conformance adapter against those same registries and can
optionally build/vet/test that package inside the same pinned, offline Go image
`scripts/run_go_tests.py` uses. ADR 0008 records the fake "Loomwright" adapter as the proof that core
routing never branches on an adapter/agent name, and lists external-process/Wasm adapter execution,
the compatibility registry's live version-drift enforcement and the `kansoku doctor`/`configure`/
`adapter verify` CLI surface as explicit downstream gaps.

Session 06 adds four closed registries under `contracts/codex/`: adapter manifest/discovery, hooks-
and-OTel source mapping, rollout-and-inventory, and skill-evidence-and-reconciliation. Their versioned
semantic digests live in `contracts/codex-policy-locks.yaml`, following the identical append-only lock
structure `contracts/adapter-sdk-policy-locks.yaml` established. Codex is the first real
`internal/adaptersdk` registration: it reuses the existing `codex.user_otel` installer target from
`contracts/privacy/installer.yaml` verbatim rather than redefining it, adds only a new
`codex.user_hook` installer target for the observer hook, and routes every Codex hook event through
the already-generic `/v1/hooks/{adapter}/{event}` ingress declared in
`contracts/observability/ingress.yaml` instead of a parallel ingress mechanism. Independent exact
invariants prevent a coherent lock edit from weakening the hook trust/enabled-state audit-only rule,
the never-scan-the-home-directory and no-code-execution discovery safety rules carried over from
Session 05, the requirement that every source (`codex.hook`, `codex.otel`, `codex.rollout`,
`codex.inventory`) fail visibly and independently rather than reporting a silent zero, or the
prohibition on ever representing inferred or reconstructed-tier Codex skill evidence as a native exact
activation. `scripts/validate_codex.py` cross-checks `internal/observability`'s Codex adapter
registration and its fixtures against these registries. ADR 0009 records the sequential checkpointed
build order this session used and lists the concrete evidence, canary and CLI gaps this stage leaves
open.

Session 07 adds four closed registries under `contracts/claude/`: adapter manifest/discovery, hooks-
and-OTel source mapping, transcript-and-inventory, and skill-evidence-and-reconciliation, locked by
`contracts/claude-policy-locks.yaml` following the identical append-only structure
`contracts/codex-policy-locks.yaml` established. It also adds two closed registries under
`contracts/cross-agent/` — a second, differently-shaped fictional fixture-agent ("Wayfinder": no OTel
source, a "recipe" component vocabulary, non-UUID session identifiers, an unsupported token capability
and one deliberately unknown event schema) and the Codex+Claude cross-agent invariant scenario — locked
by `contracts/cross-agent-policy-locks.yaml`. Claude is the second real `internal/adaptersdk`
registration: it reuses the existing `claude.user_otel` installer target from
`contracts/privacy/installer.yaml` and `internal/installer/protocol.go`'s existing `BuildClaudePlan`
verbatim rather than redefining either, adds only a new `claude.user_hook` installer target for the
observer hook, and routes every Claude hook event through the already-generic
`/v1/hooks/{adapter}/{event}` ingress declared in `contracts/observability/ingress.yaml` by adding a new
`internal/observability/routes.go` dispatch case alongside the existing `fixture-agent` and `codex`
cases, never a parallel ingress mechanism and never colliding with the reserved `fixture-agent` literal
adapter id. Independent exact invariants prevent a coherent lock edit from weakening the hook trust/
enabled-state audit-only rule, the never-scan-the-home-directory and no-code-execution discovery safety
rules carried over from Session 05, the requirement that every source (`claude.hook`, `claude.otel`,
`claude.transcript`, `claude.inventory`) fail visibly and independently rather than reporting a silent
zero, the prohibition on ever representing inferred or reconstructed-tier Claude skill evidence as a
native exact activation, or the unconditional-strip rule that rejects prompt text, assistant response
text, tool input/output/parameters, transcript paths and raw API bodies before any durable write even
when Claude's documented detailed telemetry settings are enabled upstream. Gemini CLI and the Cursor
probe from the original TDD/proposal 07 grouping are explicitly out of scope for Session 07 and are
deferred to Session 07b; `contracts/cross-agent/` carries no Gemini or Cursor material of any kind yet.
`scripts/validate_claude.py` cross-checks `internal/observability`'s Claude adapter registration and its
fixtures against these registries. ADR 0010 records the sequential checkpointed build order this session
used, the scope narrowing that deferred Gemini/Cursor to Session 07b, and lists the concrete evidence,
canary and CLI gaps this stage leaves open.

Session 08 adds four closed registries under `contracts/integrity/`: audit-run state machine and
schedule (`audit-run-and-schedule.yaml`), structural-only drift fingerprints
(`drift-fingerprint-and-schema.yaml`), incident/health composition (`incident-and-health.yaml`), and
the closed fault-injection catalog plus live-canary recipe schema
(`fault-injection-and-live-canary.yaml`). Their versioned semantic digests live in
`contracts/integrity-policy-locks.yaml`, following the identical append-only lock structure every
earlier policy-lock file established; no prior trusted entry in any of the seven earlier lock files is
edited. This is the first session that needs a durable scheduled job — the daily audit workflow is
built on a PostgreSQL session-scoped advisory lock acquired through `internal/dataplatform`'s existing
`*pgxpool.Pool`, never a new external job-scheduling dependency, so a crashed process releases the lock
automatically and a stale `running` audit_run row is marked interrupted rather than silently resumed.
The audit engine reuses, rather than reinvents, five prior boundaries: `internal/observability`'s
existing `Watermark`/`Incident` types and silence classification (this is the ONE incident concept in
the repository; Session 08 extends it with a keyed 1:1 detail record, never a second competing type),
`internal/dataplatform`'s existing `ApplyRetention`/`CreateBackup`/`VerifyBackupChecksum`/`CountRows`/
`RestoreBackup`/`RepairQueueDepth`/`BucketStart` functions for its storage/operations checks,
`internal/adaptersdk`'s `Registry.IDs()`/`Get()`/`Manifest()`/`Adapter.Audit()` for driving every
registered adapter generically with zero hardcoded agent-name branch in the engine's own core files,
`internal/observability/routes.go`'s existing generic `/v1/hooks/{adapter}/{event}` ingress for the
synthetic pipeline probe's uniquely tagged safe record, and `internal/privacy`'s `SafeRecord`/
`SafeError` sanitizer as the only trust boundary any audit evidence or drift fingerprint may cross —
drift fingerprints are computed from structural metadata (event/type names, field paths, primitive
types) only, never sampled values. Nine independently-evidenced health dimensions
(configuration/connectivity/event-freshness/schema-compatibility/parser-fixture-status/
reconciliation-coverage/privacy-canary/live-canary-age-result/storage-rollup-health) are each
green/yellow/red/gray, deriving from — never duplicating — `internal/adaptersdk`'s existing
`CapabilityState`; gray is the mandatory default before any check has run, and green requires an
actual runtime check, never an assumption. `contracts/integrity/fault-injection-and-live-canary.yaml`'s
closed 21-entry catalog binds each claim to one explicit evidence level: 17 component classifiers
that do not assert end-to-end SLO, 2 PostgreSQL-tagged deterministic mutation integrations that
measure actual scheduler/Stage-11 persistence at `Incident.OpenedAt`, and 2 runtime-required
scenarios (DB restart and failed restore). The catalog is not reported as an aggregate 21-fault
runtime pass; both mutation integrations passed on pinned PostgreSQL 18, while the two
runtime-required scenarios remain pending until their corresponding harness executes;
its live-canary recipe schema is fully specified but ships disabled by default pending explicit
credentials and consent, matching the Session 06/07 precedent that Codex/Claude ship fixture-based
canaries rather than live-CLI execution. `scripts/validate_integrity.py` cross-checks
`internal/integrity`'s scheduler, audit stages, health API and fault-injection suite against these
registries. ADR 0011 records the PostgreSQL-advisory-lock scheduler decision, the sequential
checkpointed build order this session uses, and the live-canary-disabled-by-default scope boundary
explicitly.

Session 12 adds the unified incident and metadata-only quarantine registries under
`contracts/incidents/`, bound by the append-only `contracts/incidents-policy-locks.yaml`.
`scripts/validate_incidents.py` verifies detector/triage separation, occurrence idempotency,
signed-keyset pagination, fresh-audit recovery, the absence of a manual resolve route, and the
sanitized unknown-schema fixture. Incident list/detail, occurrence, quarantine and debug-bundle
queries are registered in the data-platform query-budget contract. The incident API and both debug
bundle formats are mandatory zero-canary privacy subsurfaces.
