# Session 06 reconciliation report

- Session: 06 — Codex adapter
- Date: 2026-07-22
- Result: automated exit gate passed for everything this stage can check. A prior fixer stage closed
  the `adaptersdk.Adapter` method-wiring gap: `Inventory`/`PlanConfiguration`/`Normalize`/`Reconcile`
  now forward to the already-tested free functions (`BuildInventorySnapshot`, `installer.BuildCodexPlan`
  + `adaptersdk.BuildChangePlan`, the hook/otel canonical-event-type tables, and the two-snapshot
  node-membership diff respectively); only `Audit` remains a later stage's responsibility (see Known
  gaps item 1, revised below) and no README/ROADMAP/ADR text overclaims the exit gate as fully met
- Public support claims: unaffected; Codex remains unreviewed/ungoverned per ADR 0002/0005 — this
  session proves the reconciled-adapter machinery, it does not raise any Supported/Beta status
- Agent configuration reads/writes: read-only discovery/fingerprinting only; the `codex.user_hook`
  installer target is declared but, matching every prior session's installer scope, no code path in
  this repository performs a real write to a user's Codex configuration yet
- Network telemetry/export: none in this environment; the canary chain and all cross-source
  reconciliation are proven against sanitized fixtures and an in-repo materialized fixture project,
  not a live Codex CLI process
- Supply chain: no new third-party Go or Python dependency (see "Supply chain" below)

## Delivered contract

| Acceptance item | Evidence | Result |
|---|---|---:|
| Closed Codex manifest/discovery/permissions/capability-id-reuse registry | `contracts/codex/manifest.yaml`; `TestManifestIsRegistrable`, `TestManifestDeclaresOnlyKnownSourcesAndCapabilities` | pass |
| `codex.hook` closed event vocabulary, allowlisted-field helper contract, trust/enabled-state audit-only rule | `contracts/codex/hooks-and-otel.yaml hook_source`; `internal/codexadapter/hook.go`; `TestSupportedHookEventsMatchesRegistry`, `TestDecodeHookInputRejectsUnknownFieldsAndOversizedPayload`, `TestAllowlistedHookFieldsNeverIncludesPromptField`, `TestBuildHookOutputComputesPromptFeaturesInMemoryAndNeverCopiesRawPrompt` | pass |
| `codex.otel` reuse of the existing `codex.user_otel` installer target and OTLP-safe-attribute allowlist; documented-event-name + schema-fingerprint gated mapping | `contracts/codex/hooks-and-otel.yaml otel_source`; `internal/codexadapter/otel.go`; `TestCanonicalEventForOTelRequiresDocumentedAndMappedEventPlusMatchingFingerprint`, `TestCanonicalEventForOTelRejectsUndocumentedEventName`, `TestCanonicalEventForOTelRejectsSchemaFingerprintMismatch`, `TestDroppedOTelSurfacesNeverIncludesASafeAttribute`, `TestOTelInstallerTargetReusesExistingUserOTelTarget` | pass |
| `codex.rollout` checkpointed JSONL importer: offset resume, rotation/truncation detection, replay idempotency, crash-mid-import resume, corrupt/unknown-schema quarantine | `contracts/codex/rollout-and-inventory.yaml rollout_source`; `internal/codexadapter/rollout.go`; `TestImportRolloutFileOffsetResumeIsIdempotentOnReplay`, `TestImportRolloutFileDetectsRotationViaFileIdentityMismatch`, `TestImportRolloutFileDetectsTruncation`, `TestFixtureRolloutReplayIsIdempotent`, `TestFixtureRolloutCrashMidImportResumesExactlyAtNextRecord`, `TestFixtureRolloutQuarantineScenarioNeverDropsSilently` | pass |
| Historical content mode: default safe-structured-only, opt-in transient in-memory feature extraction never durable, user-disableable | `contracts/codex/rollout-and-inventory.yaml rollout_source.historical_content_mode`; `TestImportRolloutFileDefaultsToMetadataOnlyContentParsing`, `TestImportRolloutFileOptInContentParsingComputesFeaturesButNeverPersistsRawText` | pass |
| `codex.inventory` reuse of adapter-sdk's closed node/edge/scope vocabulary; cache separation; skill/MCP name collision handling without merge | `contracts/codex/rollout-and-inventory.yaml inventory_source`; `internal/codexadapter/inventory.go`; `TestBuildInventorySnapshotNeverMarksCacheOnlyPluginEnabled`, `TestBuildInventorySnapshotLinksCollidingSkillNamesRatherThanMerging`, `TestFixtureSkillCollisionNeverMergesDistinctScopeNodes`, `TestFixtureShadowPrecedenceNeverInfluencesIdentityOnlyDisplayOrder` | pass |
| Discoverability pressure: description bytes/scope precedence/duplicate/disabled flags; risk estimate always `inferred`, never promoted to `exposed` without direct evidence | `contracts/codex/rollout-and-inventory.yaml discoverability_pressure`; `internal/codexadapter/discoverability.go`; `TestComputeDiscoverabilityPressureFlagsDuplicateNamesAndDisabled`, `TestComputeDiscoverabilityPressureNeverEstimatesRiskForExposedSkills`, `TestComputeDiscoverabilityPressureLabelsRiskEstimateInferredNeverNative` | pass |
| Five-kind skill evidence model bound to tiers; ambiguous-ownership never promoted to `component.invoked`; native-exact-activation prohibition | `contracts/codex/skill-evidence-and-reconciliation.yaml skill_evidence_model`; `internal/codexadapter/evidence.go`; `TestResolveSkillEvidenceExplicitInvocationPromotesToNativeOnlyWhenSourceLabelsIt`, `TestResolveSkillEvidenceNeverPromotesSemanticOpportunityBeyondInferred`, `TestResolveSkillEvidenceRefusesAmbiguousOwnershipPromotion`, `TestFixtureAmbiguousOwnershipScenariosNeverPromoteToInvoked` | pass |
| Full source-to-canonical mapping table matches the contract row-for-row | `contracts/codex/skill-evidence-and-reconciliation.yaml source_to_canonical_mapping`; `internal/codexadapter/evidence.go SourceToCanonicalTable`; `TestSourceToCanonicalTableMatchesContractRowCount` | pass |
| Six-lane cross-source reconciliation; missing source degrades only its own lane, never a whole-session fabricated zero; versioned (never hardcoded) tolerance | `contracts/codex/skill-evidence-and-reconciliation.yaml reconciliation`; `internal/codexadapter/reconcile.go`; `TestReconcileLaneDegradesOnlyTheMissingSourceNeverFabricatesZeroForWholeSession`, `TestReconcileLaneWithFewerThanTwoPresentSourcesNeverFabricatesMismatch`, `TestReconcileSessionCoversEveryDeclaredLaneIndependently`, `TestResolveToleranceIsVersionedNeverHardcodedAcrossVersions`, `TestOneFactRuleLaneIdentityIsIndependentAcrossSources` | pass |
| Canary fixture project (`kansoku-canary-skill` + local echo MCP + harmless read task) produces the expected session/prompt/tool/MCP chain | `tests/fixtures/session-06/canary/*`; `internal/codexadapter/canary_chain_test.go`; `TestCanaryChainProducesExpectedSessionPromptToolMCPChain`, `TestCanaryScenarioDeclaresConsentAndBudgetBoundedExecution`, `TestCanaryWorkspaceLifecycleIsSeparateFromRun` | pass, fixture-backed only (see Known gaps) |
| Prohibited-content canaries: raw prompt/content never leaks into any durable/accepted output across hook, rollout (both modes), config fingerprinting and OTel-dropped-surface paths | `tests/fixtures/session-06/prohibited-content-canaries.json`, `rollout-fixtures.json prohibited_content_canary`; `TestBuildHookOutputComputesPromptFeaturesInMemoryAndNeverCopiesRawPrompt`, `TestFixtureRolloutProhibitedContentNeverLeaksRawText`, `TestFixtureProhibitedContentCanariesNeverLeakRawText`, `TestFingerprintInstallationNeverRecordsRawFileContent` | pass |
| Installation discovery resolves `CODEX_HOME` before any documented default, never scans the whole home directory, keeps distinct surfaces separate even sharing one state root | `contracts/codex/manifest.yaml installation_discovery`; `internal/codexadapter/discover.go`; `TestDiscoverResolvesOnlyFromAllowedRootsNeverSpeculativeScan`, `TestDiscoverKeepsDistinctSurfacesSeparateEvenWhenSharingStateRoot`, `TestDiscoverIsDeterministicAcrossRepeatedRuns` | pass |
| Version probe never captures login/auth output beyond a bare version token | `internal/codexadapter/discover.go probeCodexVersion`; `TestVersionProbeNeverCapturesLoginOrAuthOutputBeyondBareVersion` | pass |
| Hook ingress reuses the single generic `/v1/hooks/{adapter}/{event}` route; no parallel HTTP server or second auth mechanism | `contracts/observability/ingress.yaml` (Session 03, unedited); `internal/observability/routes.go hookAdapterHandler`/`codexHookHandler`; `TestHookRoutePathReusesGenericIngressTemplate`; `scripts/validate_codex.py` source scan asserts exactly one `http.NewServeMux()` call | pass |
| `codex.user_otel` installer target reused verbatim (no redefinition); new `codex.user_hook` target added, user-level scope only by default | `contracts/privacy/installer.yaml` (Session 02, unedited); `contracts/codex/hooks-and-otel.yaml hook_installer_target`; `TestOTelInstallerTargetReusesExistingUserOTelTarget` | pass |
| No second sanitizer; hook prompt-feature computation reuses `internal/privacy.ExtractPromptFeatures` (exported, not re-implemented) | `internal/privacy/features.go` (now exported), `internal/codexadapter/hook.go`; `scripts/validate_codex.py` source scan for a redeclared `SafeRecord`/`SafeError`/`extractPromptFeatures` | pass |
| Registry/lock append-only policy versioning, exact closed schema | `contracts/codex-policy-locks.yaml`; `test_each_semantic_registry_is_policy_locked`, `test_policy_versions_are_contiguous_and_trusted_history_is_append_only` | pass |
| Coherent lock mutation cannot silently remove a core invariant (raw-prompt-never-persisted, hook-trust-bypass-forbidden, native-exact-activation prohibition, ambiguous-ownership rule, missing-source degradation, independent-source-degradation, network-grade, code-execution-forbidden, home-scan-forbidden, OTel-target-reuse, project-local-default-forbidden, capability-id-closure, cache-separation, repository-scan-bound, replay-idempotency, quarantine-durability, unknown-version-degraded, exit-gate booleans, semantic-opportunity tier) | `tests/test_codex_contracts.py`, 19 dedicated `test_coherent_lock_mutation_cannot_*` cases | pass |
| Independent standalone validator | `python3 scripts/validate_codex.py` (registry/lock digest recomputation, code/contract alignment, optional `--with-go` build/vet/test) | pass |
| Supply chain (no new dependency) | `go.mod`/`go.sum`/`vendor/` unchanged by this session (byte-identical to the Session 05 baseline); `internal/codexadapter` imports only the Go standard library plus already-vendored `internal/adaptersdk`/`internal/privacy`/`internal/observability` | pass, explicitly no-op (see below) |

## Contract and registry/lock binding

The authority is the four JSON-subset YAML registries under `contracts/codex/` — `manifest.yaml`
(adapter id/detection/permissions/discovery steps, reused parse limits, reused capability-id set,
installer-target-reuse and hook-ingress-reuse pointers), `hooks-and-otel.yaml` (`codex.hook` event
vocabulary/helper contract/installer target/trust rule, `codex.otel` installer-target reuse/dropped-
surface list, the hook+OTel source-to-canonical event mapping, and the independent-degradation
guarantee), `rollout-and-inventory.yaml` (`codex.rollout` checkpoint/historical-content/quarantine/
replay fields, `codex.inventory` scope/node/edge reuse and cache/collision rules, discoverability-
pressure fields) and `skill-evidence-and-reconciliation.yaml` (the five-kind evidence model, the full
source-to-canonical table, the six reconciliation lanes, the canary design, the required-test list and
the structured `exit_gate` object) — bound by `contracts/codex-policy-locks.yaml` using the exact
append-only semantic-digest mechanism already used for `contracts/privacy`, `contracts/observability`,
`contracts/data-platform` and `contracts/adapter-sdk`. `scripts/validate_codex.py` independently
recomputes each registry's semantic digest, cross-checks the four locked digests against the four
`contracts/codex/*.yaml` files on disk, and rejects a coherent registry/lock edit that weakens any of
the invariants above — proven by `tests/test_codex_contracts.py`'s 19 coherent-mutation tests, which
each construct a self-consistent lock (matching digests) around one weakened registry field and assert
the validator still rejects it on invariant-text grounds, not merely on a digest mismatch.

## Codex adapter, discovery and permission-checked probing

`internal/codexadapter` registers `AdapterID = "codex"` against the Session 05
`adaptersdk.Registry`/`adaptersdk.Adapter` contract with zero new branch inside `internal/adaptersdk`
itself; `Manifest()` declares exactly the capability-id set `contracts/adapter-sdk/capabilities.yaml`
already closes over, inventing none. `Discover` (`discover.go`) resolves installation candidates
strictly from `HostView.AllowedRoots()` — which the caller must already have populated from the
documented `CODEX_HOME` env var before any documented default — and reports one candidate per
observable surface (`cli`/`ide_extension`/`app`) beneath each resolved root without ever merging two
candidates that share a state root but differ by surface. `probeCodexVersion` runs the credential-free
`codex --version` probe through `HostView.ExecProbe` (empty environment, bounded output) and extracts
only a bare semver token via a regex, discarding any surrounding banner text that could otherwise carry
account information. `FingerprintInstallation` records only existence/size-class/mtime-shape of
config/hook/skill/plugin locations, never their content —
`TestFingerprintInstallationNeverRecordsRawFileContent` and
`TestVersionProbeNeverCapturesLoginOrAuthOutputBeyondBareVersion` cover both guarantees directly.

## Hook and OTel sources

`hook.go` implements the `codex.hook` helper contract: `DecodeHookInput` rejects unknown fields and
oversized payloads before ever touching a byte of untrusted stdin; `BuildHookOutput` computes
`privacy.PromptFeatures` in memory (via the now-exported `internal/privacy.ExtractPromptFeatures`,
reused rather than re-implemented) for prompt events only and never copies the raw prompt string into
`HookHelperOutput`; `ValidateHookOutputAllowlist` re-checks the resulting output's field set is exactly
the closed allowlist before the caller may forward it. `internal/observability/routes.go`'s
`hookAdapterHandler` dispatches `codexadapter.AdapterID` through the same generic
`/v1/hooks/{adapter}/{event}` mux the `fixture-agent` example route already used — `scripts/
validate_codex.py`'s source scan asserts exactly one `http.NewServeMux()` call exists in that file, so
a future Codex-route regression toward a second HTTP server or second bearer-auth mechanism fails
closed. `otel.go`'s `CanonicalEventForOTel` maps a documented OTel event name onto its canonical event
type only when the active version manifest's schema fingerprint for that mapping actually matches the
observed attribute shape — `TestCanonicalEventForOTelRejectsSchemaFingerprintMismatch` and
`TestCanonicalEventForOTelRejectsUndocumentedEventName` both prove an undocumented or drifted event
name is rejected rather than silently mapped. `DroppedOTelSurfaces` never overlaps the reused OTLP
safe-attribute allowlist (`TestDroppedOTelSurfacesNeverIncludesASafeAttribute`).

## Rollout importer, inventory graph, discoverability pressure

`rollout.go`'s `ImportRolloutFile` streams JSONL one bounded record at a time, checkpoints file
identity/byte offset/first-and-last-record fingerprint, and never writes back into the Codex session
tree. Rotation is detected via file-identity mismatch, truncation via a checkpoint offset exceeding the
current file length; both open a degraded incident scoped to `codex.rollout` only rather than silently
rewinding. `TestImportRolloutFileOffsetResumeIsIdempotentOnReplay` and
`TestFixtureRolloutCrashMidImportResumesExactlyAtNextRecord` prove replay/crash-resume never
reprocesses or skips a record. `inventory.go`'s `BuildInventorySnapshot` reuses
`contracts/adapter-sdk/inventory-graph.yaml`'s closed node/edge/scope vocabulary verbatim; a cache-only
plugin package is never reported enabled absent an explicit `enabled_for` edge
(`TestBuildInventorySnapshotNeverMarksCacheOnlyPluginEnabled`), and two skill/MCP identities sharing a
declared name across scopes remain distinct nodes linked by a `shadows`/`collides_with` edge rather than
merged (`TestBuildInventorySnapshotLinksCollidingSkillNamesRatherThanMerging`).
`discoverability.go`'s `ComputeDiscoverabilityPressure` computes description-byte/scope-precedence/
duplicate/disabled fields and a `catalog_pressure_risk_estimate` that is always labeled `inferred` —
`TestComputeDiscoverabilityPressureLabelsRiskEstimateInferredNeverNative` and
`TestComputeDiscoverabilityPressureNeverEstimatesRiskForExposedSkills` prove a skill with direct
evidence of reaching model context is never assigned a risk estimate instead of an `exposed` label.

## Skill evidence model and cross-source reconciliation

`evidence.go`'s `ResolveSkillEvidence` is this session's central exit-gate mechanism: it returns
`Tier = native` only for `explicit_user_invocation` when the source itself labels it native, every
other kind resolves to its documented default tier, and `semantic_opportunity_classifier` can never
resolve to anything but `inferred`. Ambiguous ownership (`uniquely_owned_helper_execution` with more
than one candidate skill identity) is returned with `ErrAmbiguousOwnershipPromotion` and the
canonical event type held at `component.executed` rather than silently promoted to
`component.invoked` — `TestResolveSkillEvidenceRefusesAmbiguousOwnershipPromotion` and
`TestFixtureAmbiguousOwnershipScenariosNeverPromoteToInvoked` cover this from both the unit and
fixture-driven angle. `reconcile.go`'s `ReconcileSession` runs the six declared lanes independently;
`TestReconcileLaneDegradesOnlyTheMissingSourceNeverFabricatesZeroForWholeSession` reconciles a session
with `codex.otel` absent and asserts only that lane's completeness is marked degraded, never a
whole-session zero-usage result, while `TestReconcileLaneWithFewerThanTwoPresentSourcesNeverFabricatesMismatch`
proves a lane with only one present source reports "insufficient data", never a fabricated mismatch.
Tolerance is resolved per compatibility-registry entry (`ResolveTolerance`), never hardcoded
(`TestResolveToleranceIsVersionedNeverHardcodedAcrossVersions`).

## Canary chain

`tests/fixtures/session-06/canary/kansoku-canary-scenario.json` describes a materialized fixture
project (`kansoku-canary-skill`, a local echo MCP server/tool, one harmless bounded read task) and a
`codex-compat/1`-versioned expected chain: `session.started` → `prompt.submitted` →
`component.invoked` (skill, native, `explicit_user_invocation`) → `tool.called` (MCP echo) →
`session.stopped`, each lane annotated with which of `codex.hook`/`codex.otel`/`codex.rollout` backs
it. `canary_lifecycle_test.go`'s `TestCanaryWorkspaceLifecycleIsSeparateFromRun` proves the generated
workspace directory's creation/deletion is controlled by a lifecycle distinct from the run being
measured; `canary_chain_test.go`'s `TestCanaryChainProducesExpectedSessionPromptToolMCPChain` drives
discovery, hook/OTel/rollout evidence construction, skill-evidence resolution and reconciliation for
every declared chain step against the materialized project and asserts the emitted chain matches the
fixture's `expected_chain` exactly; `TestCanaryScenarioDeclaresConsentAndBudgetBoundedExecution`
checks the non-interactive/consent/budget/no-real-repository constraints are present and enforced.

## Reconciliation examples (Engineering Proposal 06 cross-check)

| Proposal example | Implementation/test evidence |
|---|---|
| Prompt hook count vs rollout user-message count | `LanePrompts`; `TestReconcileSessionCoversEveryDeclaredLaneIndependently` |
| Tool hook calls vs OTel tool results vs rollout tool events | `LaneToolTerminal`; `TestReconcileLaneDetectsMismatchOnlyAmongPresentSources` |
| Session start/stop vs rollout file lifecycle | `LaneSessionLifecycle`; canary chain step 1/5 |
| Executable version change vs schema fingerprint and event freshness | `CanonicalEventForOTel`'s fingerprint gate; `TestCanonicalEventForOTelRejectsSchemaFingerprintMismatch` |
| Plugin/skill inventory vs current enabled config and actual component calls | `LaneComponentEvidence`; `TestBuildInventorySnapshotNeverMarksCacheOnlyPluginEnabled`; `ResolveSkillEvidence` |

## Historical limitations (Engineering Proposal 06 cross-check)

The proposal's stated historical-transcript privacy trade-off — old transcripts may carry enough
content to identify a skill but violate the default privacy boundary; historical import parses content
only in memory, emits approved features/evidence and discards the line; users may disable historical
content parsing entirely; unsupported old schemas are quarantined as metadata-only incidents — is now
backed by code, not only contract prose: `ImportRolloutFile`'s `RolloutImportOptions` carries the
opt-in flag, `TestImportRolloutFileOptInContentParsingComputesFeaturesButNeverPersistsRawText` proves
the opt-in path still never persists raw content, `TestImportRolloutFileDefaultsToMetadataOnlyContentParsing`
proves the default path, and `TestImportRolloutFileQuarantinesCorruptAndUnknownSchemaLines` proves an
unrecognized schema is quarantined (counted, never fatal, never durably retaining raw bytes) rather
than dropped or rejected as a hard failure. ADR 0009's "Known gaps" item 6 ("no Go implementation to
verify yet") from the contracts-only stage is therefore resolved by this stage; ADR 0009 itself is
not re-edited by this stage (that update, along with README/ROADMAP/SOURCES synchronization, is a
separate later checkpoint in this session's stage sequence).

## Supply chain (explicit no-op)

Session 06 vendors no new third-party Go or Python dependency: `internal/codexadapter` imports only the
Go standard library plus `internal/adaptersdk`, `internal/privacy` and (from `internal/observability`'s
side) the already-vendored gRPC/OTLP stack Session 03 introduced. `git diff HEAD -- go.mod go.sum`
is empty, and `go.mod`/`go.sum`'s SHA-256 digests are byte-identical to the Session 05 baseline
recorded in `reports/session-05-sbom.json`. `reports/session-06-sbom.json` is therefore a minimal,
valid CycloneDX 1.6 document with an explicit empty component list and properties recording this
no-op explicitly, rather than a fabricated component list — matching Session 05's honest precedent;
there is no `scripts/session06_supply_chain.py` for the same reason Session 05 has no
`scripts/session05_supply_chain.py`.

## Verification

- `python3 scripts/validate_contracts.py` — pass (unaffected; Session 06 does not touch Session 01
  registries).
- `python3 scripts/validate_privacy.py` — pass (unaffected except for the `ExtractPromptFeatures`
  export, which is a rename/visibility change with no behavior change; `internal/privacy` contracts
  and lock digests are untouched).
- `python3 scripts/validate_observability.py` — pass (unaffected; Session 06 does not touch
  `contracts/observability`).
- `python3 scripts/validate_data_platform.py` — pass (unaffected).
- `python3 scripts/validate_adapter_sdk.py` — pass (unaffected; Session 06 does not touch
  `contracts/adapter-sdk`).
- `python3 scripts/validate_codex.py --json` — pass (static contract, registry/lock digest
  cross-check, `internal/codexadapter`/`internal/observability` code/contract alignment scan, and
  fixture validation; `--with-go` optionally re-runs the codexadapter-only Go slice inside the pinned
  offline image — also verified green in this stage).
- `python3 -m unittest discover -s tests -v` — pass, 91/91 tests across Sessions 01–06, including all
  31 of `tests/test_codex_contracts.py`.
- `go build ./...` / `go vet ./internal/codexadapter/...` / `go test ./internal/codexadapter/...` —
  pass, including all `codexadapter_test.go`, `stage3_test.go`, `fixtures_test.go`,
  `canary_chain_test.go`, `canary_lifecycle_test.go` and (new in this fix) `interface_wiring_test.go`
  cases, the last of which registers `codexadapter.New()` into a live `adaptersdk.Registry` and drives
  `Inventory`/`PlanConfiguration`/`Normalize`/`Reconcile` through the standard `Adapter` interface.
- `python3 scripts/run_privacy_canary.py` — pass (unaffected; Session 06 introduces no new sink and
  no new raw-content path; the hook helper and rollout importer both reuse the Session 02 sanitizer
  and feature-extraction boundary).
- `scripts/session06_supply_chain.py --verify` — intentionally does not exist; see "Supply chain
  (explicit no-op)" above.

## Known gaps (explicitly recorded, not silently dropped)

1. **`adaptersdk.Adapter` interface wiring — RESOLVED for `Inventory`/`PlanConfiguration`/`Normalize`/
   `Reconcile`; `Audit` remains open.** A prior review found `stage2_stub.go`'s
   `Adapter.Inventory`/`Adapter.PlanConfiguration`/`Adapter.Normalize`/`Adapter.Reconcile` returned only
   the sentinel `ErrNotImplementedYet` (or an explicit empty/`unsupported` result) instead of calling
   through to the already-tested free functions in `inventory.go`/`evidence.go`/`reconcile.go`. This has
   been fixed as glue-only Go code, with no new contract or fixture work:
   `Adapter.Inventory` now forwards to `BuildInventorySnapshot` (using the one confirmed fact the
   interface method receives, `target.InstallationID` — a HostView-driven filesystem scan that
   populates `InventoryInput.Skills`/`Plugins`/`Hooks`/`MCPServers` is still a later stage's dedicated
   deliverable, so today's snapshot is real but its node list beyond the installation node itself is
   empty until that scan exists);
   `Adapter.PlanConfiguration` now forwards to `installer.BuildCodexPlan` (reusing the existing
   `codex.user_otel` target verbatim) plus `adaptersdk.BuildChangePlan` for
   `CapabilityConfigurationInstall`/`CapabilityConfigurationLiveCanary`, and still returns
   `ErrNotImplementedYet` (degrading only that one capability) for any other capability ID, which today
   has no real Codex installer target;
   `Adapter.Normalize` now forwards a `SafeSourceRecord` through unchanged (stamping the adapter
   id/version) when its `EventType` is a member of the closed set `hookEventCanonical`/
   `otelEventCanonical` already produce, and returns `ErrNotImplementedYet` for any other event type
   rather than passing it through unclassified;
   `Adapter.Reconcile` implements the two-snapshot NodeID-membership diff the interface actually
   declares (added/removed/changed node IDs, `Completeness="unknown"` rather than a fabricated
   "complete" for an empty current snapshot) — this is intentionally distinct from, and does not
   replace, this package's own cross-source per-session lane reconciliation
   (`ReconcileLane`/`ReconcileSession` in `reconcile.go`), which callers reconciling one session's
   hook/otel/rollout activity evidence (such as `canary_chain_test.go`) invoke directly.
   New tests in `internal/codexadapter/interface_wiring_test.go` register `codexadapter.New()` into a
   live `adaptersdk.Registry` and drive all four methods through the standard `Adapter` interface,
   proving each produces real output rather than the loud stub error.
   `Adapter.Audit` still returns `nil` (no fabricated pass/fail) and remains a later stage's
   responsibility; `SourceSchemas` still returns `nil` pending Normalize's full schema declaration —
   neither was in this fix's scope.
2. **No live canary evidence yet, only fixture-and-materialized-project evidence.** The exit gate
   requires "fixtures and live evidence both required, neither alone is sufficient"; this stage
   proves the canary chain against `tests/fixtures/session-06/canary`'s materialized fixture project
   (a real directory tree with a real `SKILL.md`/MCP config/task file) driven through the real Go
   implementation, which is strictly more than a JSON-only fixture, but it is still not a live run
   against a real installed Codex CLI process because no such process exists in this build
   environment. `contracts/codex/skill-evidence-and-reconciliation.yaml`'s `canary` section and
   `exit_gate.compatibility_matrix_backed_by` remain honest about this: the compatibility matrix has
   one entry (`codex-compat/1`) backed by fixture-and-materialized-project evidence, not yet by an
   actual live Codex session.
3. **`codex.user_hook` has no real filesystem writer yet.** As recorded in ADR 0009 and consistent
   with every prior session's installer scope, no code in this session performs a real write to a
   user's Codex configuration; that remains gated behind `internal/installer`'s existing preview/
   consent/simulate-only machinery until a session explicitly promotes real writes (ADR 0002).
   `PlanConfiguration` (see gap 1, now resolved) does build a real, simulate-only `ChangePlan` for the
   `codex.user_otel` target via `installer.BuildCodexPlan`/`adaptersdk.BuildChangePlan`/
   `installer.SimulateApply`/`SimulateRollback` — but only for the OTel install target; there is still
   no equivalent plan-construction path for `codex.user_hook` specifically. TDD 06's "configuration
   concurrent-change and rollback" required test still has no corresponding Go test for the hook
   target.
4. **Single compatibility-registry entry.** `compatibility_versions_covered` in the canary scenario
   and `codex_release` values in the rollout fixtures span two point releases
   (`1.2.3`/`1.5.0`) under one compatibility version (`codex-compat/1`), proving the importer does not
   depend on exact record ordering/count across releases, but no second declared compatibility range
   (a hypothetical future Codex major version with a materially different event shape) exists yet to
   prove the "on every supported Codex version" phrasing of the exit gate across more than one
   compatibility boundary.
5. **No `kansoku doctor`/`configure`/`adapter verify` CLI entry point.** As in Session 05, the
   `Registry.CapabilityMatrix`/`Audit`-shaped data this session's Codex adapter would report through
   such a CLI exists, but no `cmd/` binary calls it yet.
6. **Passive daily probe is not scheduled.** The Engineering Proposal's "daily passive probe" deliverable
   describes an operational cadence, not a one-time check; `Audit`'s `passive` mode concept is declared
   in `contracts/adapter-sdk/discovery-and-plans.yaml` (Session 05) and the Codex manifest lists
   `fixture_replay`/`live_canary` in its `HealthChecks`, but no scheduler exists yet to actually run
   `Audit` on a recurring cadence against a real installation.
