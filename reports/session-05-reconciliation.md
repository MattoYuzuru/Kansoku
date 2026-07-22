# Session 05 reconciliation report

- Session: 05 — Adapter SDK and inventory
- Date: 2026-07-22
- Result: automated exit gate passed; Session 06 (Codex adapter) may begin from the committed/
  reviewed result
- Public support claims: unaffected; this session registers exactly one adapter, the synthetic
  "Loomwright" conformance fixture, and never raises any real adapter's Supported/Beta status
- Agent configuration reads/writes: none against a real agent; the fake adapter's `PlanConfiguration`
  deliberately returns an explicit unimplemented-write error rather than mutating anything
- Network telemetry/export: none; Session 05 adds no new external runtime (no database, no network
  service) and no new third-party Go or Python dependency

## Delivered contract

| Acceptance item | Evidence | Result |
|---|---|---:|
| Closed adapter manifest/parse-limits/protocol registry | `contracts/adapter-sdk/manifest.yaml`; `TestManifestSchemaValidation` | pass |
| Bounded, non-executing manifest parsing (entries/depth/string ceilings, duplicate-key rejection, unknown-field rejection) | `ParseManifest` in `internal/adaptersdk/manifest.go`; `TestManifestParsingRejectsUnknownFieldsDuplicatesAndOversize`, `TestManifestParsingNeverExecutesCode` | pass |
| Capability model (14 capability ids, 5 states, evidence tiers, no-brand-branch invariant) | `contracts/adapter-sdk/capabilities.yaml`; `TestRegistryRoutesByCapabilityIDNotAgentName` | pass |
| Inventory entity graph (13 node kinds, 8 edge kinds, 7 source scopes, identity/cache/pseudonymization rules) | `contracts/adapter-sdk/inventory-graph.yaml`; `TestInventorySnapshotUsesDistinctVocabularyAndPathPseudonyms` | pass |
| Discovery safety rules (documented roots only, never a home-directory scan, no code execution, credential-free probes, cache paths labeled separately) | `contracts/adapter-sdk/discovery-and-plans.yaml`; `TestDiscoveryResolvesFromDocumentedRootOnlyAndNeverEscapesIt`, `TestDiscoveryIsDeterministicAcrossRepeatedRuns` | pass |
| `HostView` permission-checked read/exec probes only, no database credential or unscoped filesystem handle | `internal/adaptersdk/hostview.go`; `TestHostViewRejectsDisallowedExecProbes`, `TestHostViewExecProbeOutputIsBoundedAndCredentialFree`, `TestHostViewRequiresAbsoluteAllowedRootsAndLongPseudonymKey`, `TestAdapterInterfaceNeverExposesDatabaseCredentials` | pass |
| Path pseudonymization reuses the Session 02 HMAC construction; no raw path is ever durable | `internal/adaptersdk/hostview.go PseudonymizePath`; `TestPseudonymizePathNeverLeaksRawPathBytes` | pass |
| `Normalize` accepts only sanitized records (`privacy.SafeRecord`/`SafeError` alias, no generic payload map) | `internal/adaptersdk/adapter.go`; `TestProhibitedContentNeverReachesSafeRecordDurableFields`, `TestUnsupportedEventTypeIsRejectedNotSilentlyPassedThrough` | pass |
| Reconciliation of two `InventorySnapshot`s is a pure diff, idempotent and non-mutating | `internal/adaptersdk/adapter.go Reconcile`; `TestReconcileIsIdempotentAndDeterministic` | pass |
| `ChangePlan` reuses `internal/installer`'s `Plan`/`Approval`/`SimulateApply`/`SimulateRollback`/`SimulateRemove`/`PlanSHA256` verbatim, no parallel apply/rollback mechanism | `internal/adaptersdk/plan.go BuildChangePlan`; `contracts/adapter-sdk/discovery-and-plans.yaml change_plan_reuse` | pass |
| Registry routes/iterates by adapter-registered ID only, zero agent-name conditional in core | `internal/adaptersdk/adapter.go Registry`; `TestRegistryRoutesByCapabilityIDNotAgentName`; `scripts/validate_adapter_sdk.py` grep-cross-check for forbidden type/string switches | pass |
| Audit/health check surface (passive/fixture_replay/live_canary modes, pass/fail/skipped_unsupported statuses) | `internal/adaptersdk/adapter.go Audit`; `TestAuditProducesPassResultsForDeclaredCapabilities` | pass |
| Unknown agent version defaults to degraded, never silently healthy | `contracts/adapter-sdk/manifest.yaml unknown_agent_version_policy`; `tests/fixtures/session-05/loomwright-conformance.json unknown_version_behavior`; `test_unknown_agent_version_cannot_be_silently_treated_as_healthy` | pass |
| Fake external-vocabulary conformance adapter ("Loomwright"/`loomctl`/`weave`·`shuttle`·`thread`/`loom`·`spool`·`thread`) shares no substring with Codex/Claude/Gemini/Cursor | `internal/adaptersdk/fakeadapter/fakeadapter.go`; `scripts/validate_adapter_sdk.py validate_code_and_fixture` string-literal scan; `test_fake_adapter_vocabulary_stays_out_of_the_real_agent_term_set` | pass |
| Registry/lock append-only policy versioning, exact closed schema | `contracts/adapter-sdk-policy-locks.yaml`; `test_each_semantic_registry_is_policy_locked`, `test_policy_versions_are_contiguous_and_trusted_history_is_append_only` | pass |
| Coherent lock mutation cannot silently remove a core invariant (no-agent-name-branch, brand-binding, permission scoping, ChangePlan reuse) | `test_coherent_lock_mutation_cannot_remove_no_agent_name_branch_invariant`, `test_coherent_lock_mutation_cannot_remove_brand_binding_rule`, `test_coherent_lock_mutation_cannot_remove_permission_scoping`, `test_change_plan_reuse_of_installer_machinery_cannot_be_silently_dropped` | pass |
| Deterministic discovery/inventory/normalization/reconciliation fixtures | `tests/fixtures/session-05/loomwright-conformance.json` | pass |
| Independent standalone validator | `python3 scripts/validate_adapter_sdk.py` (registry/lock digest recomputation, code/contract alignment, optional `--with-go` build/vet/test) | pass |
| Supply chain (no new dependency) | `go.mod`/`go.sum`/`vendor/` unchanged by this session; `internal/adaptersdk` imports only the Go standard library plus already-vendored `internal/privacy`/`internal/installer` | pass, explicitly no-op (see below) |

## Adapter contract and registry/lock binding

The authority is the four JSON-subset YAML registries under `contracts/adapter-sdk/` — `manifest.yaml`
(manifest fields, execution forms, parse limits, external-protocol sketch, compatibility-registry
fields, unknown-version policy), `capabilities.yaml` (14 capability ids, 5-state machine, evidence
tiers, no-brand-branch invariant), `inventory-graph.yaml` (13 node kinds, 8 edge kinds, identity/
cache-separation/pseudonymization rules) and `discovery-and-plans.yaml` (discovery safety rules,
`HostView` guarantee, `InstallationCandidate`/`ChangePlan` fields, `ChangePlan` reuse decision, audit
modes, CLI concepts, third-party acceptance checklist) — bound by
`contracts/adapter-sdk-policy-locks.yaml` using the exact append-only semantic-digest mechanism
already used for `contracts/privacy`, `contracts/observability` and `contracts/data-platform`.
`scripts/validate_adapter_sdk.py` independently recomputes each registry's semantic digest and
rejects a coherent registry/lock edit that weakens the no-agent-name-branch/brand-binding invariant,
the closed capability/node/edge/source-scope vocabulary, the never-scan-the-home-directory and
no-code-execution discovery rules, the `HostView` credential-free guarantee, or the `ChangePlan`
reuse-of-`internal/installer` decision — proven by `tests/test_adapter_sdk_contracts.py`'s coherent-
mutation tests, which construct a self-consistent lock (matching digests) around each weakened
registry and assert the validator still rejects it on invariant-text grounds, not merely on a digest
mismatch.

## Core SDK, HostView and manifest parsing

`internal/adaptersdk` (`adapter.go`, `types.go`, `manifest.go`, `hostview.go`, `plan.go`) implements
the exact TDD 05 `Adapter` interface (`Manifest`/`Discover`/`Inventory`/`PlanConfiguration`/
`SourceSchemas`/`Normalize`/`Reconcile`/`Audit`) plus every closed type the registries declare:
`Manifest`, `AgentDetection`, `SourceDescriptor`, `Permissions`, `InstallationCandidate`,
`Installation`, `Node`/`Edge`/`InventorySnapshot`, `ChangePlan`, `ReconcileScope`/`ReconcileResult`,
`AuditMode`, `CheckResult`. `Registry.Register`/`Get`/`IDs`/`CapabilityMatrix` never inspect an
adapter ID string for a known agent brand and never type-switch or string-switch on which concrete
`Adapter` is registered — `TestRegistryRoutesByCapabilityIDNotAgentName` proves the registry answers
capability-matrix and lookup queries purely from the registered manifest, and
`scripts/validate_adapter_sdk.py`'s source scan additionally fails closed if that guarantee's
documenting comment text (`never inspects the ID string for a known agent`, `type-switches`,
`string-switches on which concrete Adapter`) is ever removed from the core package.

`ParseManifest` decodes a manifest as bounded, inert JSON only, walking it generically so a future
struct-field addition cannot bypass the ceiling: it rejects duplicate object keys, unknown fields, and
any value exceeding `MaxManifestConfigEntries`/`MaxManifestConfigDepth`/`MaxManifestConfigString` — the
same bound pattern `internal/installer/protocol.go` already enforces for agent configuration plans.
`TestManifestParsingRejectsUnknownFieldsDuplicatesAndOversize` exercises all three limits plus
duplicate-key/unknown-field rejection; `TestManifestParsingNeverExecutesCode` asserts a manifest string
field that looks like a shell command is returned as inert data and never executed.

`HostView` (`hostview.go`) is the only surface an `Adapter` touches the host through. `NewHostView`
requires an already-resolved, absolute allowed-root set (never a home-directory default) and a closed
exec allowlist. `ReadProbe` refuses to resolve any path — including a symlink whose target resolves
outside an allowed root — outside that root (`ErrOutsideAllowedRoots`); `ExecProbe` refuses any binary
not on the allowlist (`ErrDisallowedExec`), runs with an explicit empty environment (never the
inherited parent environment) and truncates output to a bounded byte ceiling before returning it.
`PseudonymizePath` is the only durable representation of a filesystem path an adapter may place in
`Node.PathPseudonym`: an HMAC-SHA256 over a device-scoped key, the same construction class as
`internal/privacy`'s `Lineage` pseudonymization. `TestHostViewRejectsDisallowedExecProbes`,
`TestHostViewExecProbeOutputIsBoundedAndCredentialFree`,
`TestHostViewRequiresAbsoluteAllowedRootsAndLongPseudonymKey`,
`TestPseudonymizePathNeverLeaksRawPathBytes` and `TestAdapterInterfaceNeverExposesDatabaseCredentials`
cover these guarantees; `scripts/validate_adapter_sdk.py` additionally scans for the literal forbidden
patterns (`exec.Command("sh", "-c"`, `exec.Command("bash", "-c"`, `os.ReadFile(os.Getenv("HOME")`,
`eval(`) across `internal/adaptersdk` and `fakeadapter` source and fails if any appear.

`adaptersdk.SafeSourceRecord`/`adaptersdk.CanonicalEvent` are type aliases of `privacy.SafeRecord`, not
a second sanitizer — `internal/adaptersdk` declares no generic payload/attributes map an adapter could
smuggle a raw prompt, tool input/output, source code or file path through.
`TestProhibitedContentNeverReachesSafeRecordDurableFields` feeds `Normalize` a raw event containing a
prompt/path pair drawn from the fixture's `prohibited_content_canary.sample_raw_event` and asserts the
resulting `CanonicalEvent` contains neither string.
`TestUnsupportedEventTypeIsRejectedNotSilentlyPassedThrough` asserts an unrecognized native event type
is rejected with `unsupported_loomwright_event_type` rather than silently normalized or dropped.

## ChangePlan reuse of internal/installer

`adaptersdk.BuildChangePlan` (`plan.go`) takes an `installer.Plan` and binds `ChangePlan.PlanID` to
`installer.PlanSHA256(installerPlan)` so the two can never drift; `Apply`/`Rollback`/`Remove` are left
to `installer.SimulateApply`/`SimulateRollback`/`SimulateRemove` with the same underlying `Plan` and an
`Approval` bound to it. Session 05 adds no parallel apply/rollback code path — this is asserted both by
`contracts/adapter-sdk/discovery-and-plans.yaml change_plan_reuse` and by
`scripts/validate_adapter_sdk.py`'s source check that `plan.go` imports
`kansoku.local/kansoku/internal/installer` and references `installer.PlanSHA256`.
`test_change_plan_reuse_of_installer_machinery_cannot_be_silently_dropped` proves a coherent registry/
lock mutation that replaces this reuse decision with a description of an invented parallel mechanism is
rejected.

## Inventory graph and reconciliation

`Node`/`Edge`/`InventorySnapshot` implement the exact entity/edge kinds
`contracts/adapter-sdk/inventory-graph.yaml` declares. Two nodes sharing a `DeclaredName` but differing
`SourceScope` or `Fingerprint` are never merged; `TestInventorySnapshotUsesDistinctVocabularyAndPathPseudonyms`
builds a Loomwright-vocabulary snapshot (`loom`/`spool`/`thread` component kinds, `LOOMWRIGHT_HOME`-
rooted path pseudonyms) and asserts no node's fields contain a raw filesystem path. `Reconcile` derives
added/removed/changed node sets by diffing two immutable snapshots without mutating either;
`TestReconcileIsIdempotentAndDeterministic` reconciles the fixture's `previous_*`/`current_*` node/
fingerprint sets twice and asserts a byte-identical `ReconcileResult` (`expected_added`=`["node_d"]`,
`expected_removed`=`["node_c"]`, `expected_changed`=`["node_b"]`) both times.

## Fake conformance adapter

`internal/adaptersdk/fakeadapter` implements the full `Adapter` interface for a fictional agent
"Loomwright": executable `loomctl`, state-root env var `LOOMWRIGHT_HOME`, event vocabulary
(`weave.begun`/`shuttle.passed`/`thread.completed`/`weave.completed`) and component vocabulary
(`loom`/`spool`/`thread`) share no substring with Codex, Claude Code, Gemini CLI or Cursor.
`scripts/validate_adapter_sdk.py`'s `validate_code_and_fixture` strips comments (which are allowed to
name real agents for contrast documentation) and scans only the package's string literals for the four
real-agent terms, failing if any collide; `test_fake_adapter_vocabulary_stays_out_of_the_real_agent_term_set`
and `test_fixture_adapter_id_never_collides_with_a_real_agent_name` cover this from the Python side.
`PlanConfiguration` deliberately returns an explicit unimplemented-write error instead of fabricating a
second install mechanism — the fake adapter proves discovery/inventory/normalization/reconciliation/
audit conformance, not a second configuration-write path. `TestDiscoveryResolvesFromDocumentedRootOnlyAndNeverEscapesIt`
resolves the state root purely from `LOOMWRIGHT_HOME`, asserts exactly the fixture's
`expected_candidate_count`/`expected_detection_method`, and asserts a probe against
`outside_root_probe` fails; `TestDiscoveryIsDeterministicAcrossRepeatedRuns` re-runs discovery and
asserts byte-identical candidate output. `TestAuditProducesPassResultsForDeclaredCapabilities` runs the
fixture-replay audit mode across the declared capability set and asserts every check reports `pass` or
`skipped_unsupported`, never a silently-dropped capability.

## Supply chain (explicit no-op)

Session 05 vendors no new third-party Go or Python dependency: `internal/adaptersdk` and its
`fakeadapter` package import only the Go standard library plus `internal/privacy` and
`internal/installer`, both already vendored by prior sessions. `go.mod`, `go.sum` and `vendor/` are
therefore unchanged by this session (their only diff in the working tree is the already-committed
Session 04 `pgx/v5` addition). Because there is nothing new to inventory,
`reports/session-05-sbom.json` is a minimal, valid CycloneDX 1.6 document with an explicit empty
component list and a property recording why, rather than a fabricated component list — and there is no
`scripts/session05_supply_chain.py`, matching the task instruction to skip that script when no new
dependency was actually vendored. Unlike Session 04 (which starts an ephemeral, pinned-digest
PostgreSQL container), Session 05 adds no new external runtime, so `scripts/validate_adapter_sdk.py`
has no container harness — its optional `--with-go` flag re-runs `internal/adaptersdk`'s build/vet/test
inside the same pinned, network-disabled Go image `scripts/run_go_tests.py` already uses, for
standalone use of this validator only; the full-repo `go test ./...` sweep remains authoritative.

## Verification

- `python3 scripts/validate_contracts.py` — pass (unaffected; Session 05 does not touch Session 01
  registries).
- `python3 scripts/validate_privacy.py` — pass (unaffected; Session 05 does not touch
  `contracts/privacy`).
- `python3 scripts/validate_observability.py` — pass (unaffected; Session 05 does not touch
  `contracts/observability`).
- `python3 scripts/validate_data_platform.py` — pass (unaffected; ephemeral-PostgreSQL
  `postgres_integration` Go suite: 13/13 tests pass).
- `python3 scripts/validate_adapter_sdk.py --json` — pass (static contract, registry/lock digest
  cross-check, `internal/adaptersdk`/`fakeadapter` code/contract alignment scan, and fixture
  validation; `--with-go` optionally re-runs the adaptersdk-only Go slice inside the pinned offline
  image).
- `python3 -m unittest discover -s tests -v` — pass, 60/60 tests across Sessions 01–05, including all
  of `tests/test_adapter_sdk_contracts.py`.
- `python3 scripts/run_go_tests.py` — pass; the default `go test ./...` sweep and the isolated-network
  Go image sweep are both green across `internal/adaptersdk`, `internal/adaptersdk/fakeadapter` (no
  test files; conformance tests live in the parent package to exercise the registered `Adapter`),
  `internal/dataplatform`, `internal/installer`, `internal/localhttp`, `internal/observability` and
  `internal/privacy`.
- `python3 scripts/run_privacy_canary.py` — pass (unaffected; Session 05 introduces no new sink and no
  new raw-content path).
- `python3 scripts/session03_supply_chain.py --verify` — pass (unaffected).
- `python3 scripts/session04_supply_chain.py --verify` — pass (unaffected).
- `scripts/session05_supply_chain.py --verify` — intentionally does not exist; see "Supply chain
  (explicit no-op)" above.

## Known gaps (explicitly recorded, not silently dropped)

1. **External-process/Wasm/container adapter execution.** Only the `builtin` execution form is wired
   through the in-process `Registry`. The versioned subprocess handshake (line-delimited framed JSON or
   gRPC over a Unix socket, environment allowlist, crash-restart budget, unsigned-by-default labeling)
   TDD 05 describes is declared in `manifest.yaml external_protocol` but has no Go implementation yet.
   Deferred to the session that first needs a non-Go-native or genuinely third-party adapter.
2. **Compatibility/fixture registry persistence.** `manifest.yaml compatibility_registry_fields`
   defines the shape (`agent_version_range`, `source_schema_fingerprints`, `fixture_coverage`,
   `last_passive_audit_at`, `last_live_audit_at`, `known_gaps`, `setup_recipe_version`), but no store or
   lookup exists yet; `Audit` in this session runs a fixed fixture-replay check set rather than
   consulting a real per-version compatibility record. A future session must add the store before an
   "unknown version defaults to degraded" claim can be automated for a real adapter rather than reviewed
   manually.
3. **`kansoku doctor`/`configure`/`adapter verify` CLI.** The concepts and their read-only/plan-only/
   fixture-and-canary contracts are declared in `discovery-and-plans.yaml cli_concepts`, but no `cmd/`
   binary exists yet; `Registry.CapabilityMatrix` is the data `doctor` would render, with no entry point
   calling it yet. Deferred to the session that first needs an operator-facing CLI.
4. **Live canary and third-party signing.** `AuditLiveCanary` is a declared `AuditMode`, but the fake
   adapter's `Audit` only ever runs a fixture-replay-shaped check; no real live-agent canary exists
   because there is no real adapter yet. Signed adapter package distribution remains explicitly deferred
   per `manifest.yaml external_protocol.distribution`, matching TDD 05.
5. **No real agent fixture, runtime canary or agent configuration was observed or changed.** As with
   every prior session, all discovery/inventory/normalization/reconciliation evidence in this session is
   the synthetic "Loomwright" fixture only; public Supported/Beta governance for any real adapter
   remains blocked and requires the version-bounded evidence and two independent approved human reviews
   ADR 0002/0005 already require.
6. **Single conformance adapter.** The exit gate calls for "a" fake external-vocabulary adapter passing
   the full suite, which this session delivers; it does not exercise two simultaneously-registered
   adapters or a name/vocabulary collision between two *fake* adapters, only between the fake adapter and
   the four real agent name terms. A second fixture adapter is left to the session that first needs to
   prove cross-adapter non-interference (likely Session 07, which adds a second/third real adapter).
