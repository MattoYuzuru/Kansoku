# Session 07 reconciliation report

- Session: 07 — Claude adapter and next-agent portability proof (Claude-only scope; Gemini CLI and
  the Cursor probe from the original combined TDD/proposal 07 grouping are explicitly deferred to
  **Session 07b** and are not evaluated in this report — see "Explicit scope exclusions" below)
- Date: 2026-07-23
- Result: automated exit gate passes for everything this session's stages actually built. Claude is
  registered as the second real `internal/adaptersdk` adapter with zero new agent-name branch inside
  `internal/adaptersdk`'s own core files; a second, differently-shaped fictional fixture-agent
  ("Wayfinder") proves that invariant a third time (after Session 05's "Loomwright" and Session 06's
  Codex registration); the Codex+Claude cross-agent invariant test asserts only on canonical
  capability IDs and canonical event types, never on an agent-id string comparison
- Public support claims: unaffected — Claude remains unreviewed/ungoverned per ADR 0002/0005; this
  session proves the reconciled-adapter machinery and the unconditional detailed-telemetry strip, it
  does not raise any Supported/Beta status. `contracts/claude/skill-evidence-and-reconciliation.yaml`'s
  `exit_gate.support_label_governance` clause is upheld literally: no README/ROADMAP/ADR text in this
  session asserts a Production or Beta label ahead of the fixture/test evidence actually produced here
- Agent configuration reads/writes: read-only discovery/fingerprinting only; the new `claude.user_hook`
  installer target is declared but, matching every prior session's installer scope, no code path in
  this repository performs a real write to a user's Claude Code configuration yet (see Known gaps)
- Network telemetry/export: none in this environment; every hook/OTel/inventory/reconciliation/
  cross-agent guarantee is proven against sanitized fixtures and Go unit tests, not a live Claude Code
  CLI/IDE-extension/app process
- Supply chain: no new third-party Go or Python dependency (see "Supply chain" below)

## Delivered contract

| Acceptance item | Evidence | Result |
|---|---|---:|
| Closed Claude manifest/discovery/permissions/capability-id-reuse registry | `contracts/claude/manifest.yaml`; `TestManifestSchemaValidationAndDistinctAdapterID`-equivalent Go registration tests in `claudeadapter_test.go` | pass |
| `claude.hook` closed seven-event vocabulary (`SessionStart`/`UserPromptSubmit`/`PreToolUse`/`PostToolUse`/`SubagentStart`/`SubagentStop`/`Stop`), allowlisted-field helper contract, path pseudonymization, trust/enabled-state audit-only rule | `contracts/claude/hooks-and-otel.yaml hook_source`; `internal/claudeadapter/hook.go` (`SupportedHookEvents`, `AllowlistedHookFields`, `DecodeHookInput`, `BuildHookOutput`, `pseudonymizePath`, `ValidateHookOutputAllowlist`, `HookRoutePath`); `claudeadapter_test.go` hook-focused subtests | pass |
| `claude.otel` reuse of the existing `claude.user_otel` installer target and OTLP-safe-attribute allowlist verbatim; documented `skill.name`/`plugin.name`/`agent.name`/token/cost attribute mapping; unconditional detailed-telemetry strip regardless of upstream `OTEL_LOG_*` settings | `contracts/claude/hooks-and-otel.yaml otel_source`; `internal/claudeadapter/otel.go` (`DocumentedOTelEvents`, `OTLPSafeAttributes`, `DroppedOTelSurfaces`, `CanonicalEventForOTel`, `ExpectedOTelAttributeFingerprint`); `claudeadapter_test.go` OTel-focused subtests | pass |
| `claude.transcript` checkpointed-JSONL-importer contract: file identity/offset/fingerprint/rotation/truncation checkpoint fields, never-writes-back rule, historical-content opt-in/user-disable, corrupt/unknown-schema quarantine, replay idempotency | `contracts/claude/transcript-and-inventory.yaml transcript_source` (contract fully specified) | contract-level pass; **no Go JSONL importer exists yet** — see Known gaps item 1 |
| `claude.inventory` reuse of adapter-sdk's closed node/edge/scope vocabulary; cache separation; plugin-bundled-component relationship never reported standalone/unowned; bounded repository scan | `contracts/claude/transcript-and-inventory.yaml inventory_source`; `internal/claudeadapter/inventory.go BuildInventorySnapshot`; `stage3_test.go` inventory subtests | pass |
| Seven-kind skill evidence model bound to tiers; ambiguous-ownership never promoted to `component.invoked`; native-exact-activation prohibition; `semantic_opportunity_classifier` bound to `inferred` only | `contracts/claude/skill-evidence-and-reconciliation.yaml skill_evidence_model`; `internal/claudeadapter/evidence.go` (`ResolveSkillEvidence`, `ErrAmbiguousOwnershipPromotion`, `ErrUnknownSkillEvidenceKind`, `AllSkillEvidenceKinds`); `stage3_test.go` evidence subtests | pass |
| Full source-to-canonical mapping table (9 rows) matches the contract row-for-row | `contracts/claude/skill-evidence-and-reconciliation.yaml source_to_canonical_mapping`; `internal/claudeadapter/evidence.go SourceToCanonicalTable` | pass |
| Eight-lane cross-source reconciliation; missing source degrades only its own lane, never a whole-session fabricated zero; versioned (never hardcoded) tolerance | `contracts/claude/skill-evidence-and-reconciliation.yaml reconciliation`; `internal/claudeadapter/reconcile.go` (`AllReconciliationLanes`, `ReconcileLane`, `ReconcileSession`, `ResolveTolerance`); `stage3_test.go` reconciliation subtests | pass |
| Installation discovery resolves the three documented Claude settings roots (`claude_user_settings`/`claude_project_settings`/`claude_managed_settings`) first, never scans the whole home directory, never references an undocumented `CLAUDE_HOME`-shaped env var, keeps distinct surfaces separate | `contracts/claude/manifest.yaml installation_discovery`/`agent_detection`; `internal/claudeadapter/discover.go` (`Discover`, `ConfigRootUserSettings`/`ConfigRootProjectSettings`/`ConfigRootManagedSettings`, `probeClaudeVersion`, `FingerprintInstallation`) | pass |
| Version probe/fingerprinting never records raw config content or captures beyond a bare version token | `internal/claudeadapter/discover.go FingerprintInstallation`/`probeClaudeVersion`; `claudeadapter_test.go` discovery subtests | pass |
| Hook ingress reuses the single generic `/v1/hooks/{adapter}/{event}` route; no parallel HTTP server or second auth mechanism; never collides with the reserved `fixture-agent` literal adapter id | `contracts/observability/ingress.yaml` (Session 03, unedited); `internal/observability/routes.go hookAdapterHandler`/`claudeHookHandler`; `scripts/validate_claude.py` source scan asserts exactly one `http.NewServeMux()` call and that the `fixture-agent`/`codexadapter.AdapterID` cases remain untouched | pass |
| `claude.user_otel` installer target reused verbatim (no redefinition); `internal/installer/protocol.go`'s existing `BuildClaudePlan` reused verbatim; new `claude.user_hook` target added, user-level scope only by default | `contracts/privacy/installer.yaml` (Session 02, unedited); `contracts/claude/hooks-and-otel.yaml hook_installer_target`; `internal/claudeadapter/stage2_stub.go PlanConfiguration` (calls `installer.BuildClaudePlan` + `adaptersdk.BuildChangePlan`) | pass |
| No second sanitizer; hook prompt-feature computation reuses `internal/privacy.ExtractPromptFeatures` (exported in Session 06, not re-implemented); raw prompt/path never copied into any durable field | `internal/privacy/features.go`; `internal/claudeadapter/hook.go BuildHookOutput`/`pseudonymizePath`; `scripts/validate_claude.py` source scan for a redeclared `SafeRecord`/`SafeError`/`extractPromptFeatures` | pass |
| `internal/adaptersdk.Adapter` interface wiring: `Manifest`/`Discover`/`Inventory`/`PlanConfiguration`/`Normalize`/`Reconcile` forward to real, already-tested free functions; `Audit`/full historical-transcript `SourceSchemas` remain a later stage's responsibility (never a fabricated pass/fail) | `internal/claudeadapter/stage2_stub.go`; `claudeadapter_test.go` interface-registration subtests | pass, with the same explicitly-recorded scope as Session 06's `Audit` gap (see Known gaps item 4) |
| Second fictional fixture-agent ("Wayfinder"): zero OTel source, `recipe` component vocabulary, non-UUID session identifiers, unsupported (never zero-populated) token/cost capability, one deliberately unknown event schema quarantined rather than dropped/guessed, zero new agent-name branch inside `internal/adaptersdk` | `contracts/cross-agent/second-fixture-agent.yaml`; `internal/adaptersdk/wayfinder/wayfinder.go`; `wayfinder_test.go`'s 16 subtests including `TestNormalizeQuarantinesTheOneUnknownSchemaEventWithoutError`, `TestManifestDeclaresTokenModelCostUnsupportedNeverFakedZero`, `TestSessionIdentifiersAreNonUUIDShortSequenceTokens`, `TestInventoryIsRegistrableAlongsideOtherAdaptersWithNoNewCoreBranch` | pass |
| Cross-agent invariant scenario: one logical scenario (`session -> prompt metadata -> skill activation -> MCP tool call -> model tokens -> success`) expressed once per real agent (Codex, Claude); assertions bind only to canonical capability IDs/event types, never to an agent-id string comparison; unsupported fields never forced to a uniform zero or equal evidence tier across agents | `contracts/cross-agent/invariant-scenario.yaml`; `internal/crossagent/crossagent_test.go`'s 7 stage subtests (`TestCrossAgentSessionStageMapsToActivitySessionsAndSessionStarted` through `TestCrossAgentSuccessStageMapsToActivitySessionsAndSessionStopped`, plus `TestAgentSpecificExtraEventSurvivesAsAllowlistedAttributeWithoutCoreChange`) | pass |
| `internal/adaptersdk` core has zero new `if agentID == "claude"`/`"wayfinder"` branch | `scripts/validate_claude.py`'s source scan over every non-test `internal/adaptersdk/*.go` file for `"claude"`/`"wayfinder"`/`AdapterID ==`/`agentID ==`/`adapterID ==` | pass |
| Prohibited-content canaries: raw prompt/tool-input/tool-output/tool-parameters/transcript-path content never leaks into any durable/accepted output across hook and OTel-dropped-surface paths, even where Claude's documented detailed telemetry could expose them | `contracts/claude/hooks-and-otel.yaml otel_source.unconditional_strip_rule`/`documented_attributes.detailed_gates_may_expose`; `internal/claudeadapter/hook.go BuildHookOutput` (never copies raw prompt/path), `otel.go DroppedOTelSurfaces` (never overlaps the safe-attribute allowlist) | pass at the unit/contract level; **no dedicated fixture-driven prohibited-content canary with detailed upstream telemetry explicitly enabled exists yet** — see Known gaps item 2 |
| Registry/lock append-only policy versioning, exact closed schema, for both `contracts/claude/*` and `contracts/cross-agent/*` | `contracts/claude-policy-locks.yaml`; `contracts/cross-agent-policy-locks.yaml`; `tests/test_claude_contracts.py`'s policy-lock-mechanics tests | pass |
| Coherent lock mutation cannot silently remove a core invariant (raw-prompt-never-persisted, path-pseudonymization, hook-trust-bypass-forbidden, unconditional-OTel-strip, OTel-target-reuse, no-parallel-ingress-route, project-local-default-forbidden, capability-id-closure, network-grade, code-execution-forbidden, home-scan-forbidden, native-exact-activation prohibition, ambiguous-ownership rule, unsupported-rendering rule, missing-source degradation, independent-source-degradation, unknown-version-degraded, quarantine-durability, replay-idempotency, cache-separation, repository-scan-bound, semantic-opportunity tier, exit-gate booleans, support-label-governance, zero-core-branch requirement, missing-token-capability-never-zeroed, unknown-schema-quarantine, participating-adapters closure, agent-id-assertion prohibition, uniform-zero/equal-tier prohibition) | `tests/test_claude_contracts.py`, 30 dedicated `test_coherent_lock_mutation_cannot_*` cases across both `contracts/claude/*` and `contracts/cross-agent/*` | pass |
| Independent standalone validator | `python3 scripts/validate_claude.py` (registry/lock digest recomputation for both `contracts/claude/*` and `contracts/cross-agent/*`, code/contract alignment, fixture checks, optional `--with-go` build/vet/test) | pass |
| Supply chain (no new dependency) | `go.mod`/`go.sum`/`vendor/` unchanged by this session (byte-identical to the Session 05/06 baseline); `internal/claudeadapter`/`internal/adaptersdk/wayfinder`/`internal/crossagent` import only the Go standard library plus already-vendored internal packages | pass, explicitly no-op (see below) |

## Contract and registry/lock binding

The authority is eight closed JSON-subset YAML registries: four under `contracts/claude/` —
`manifest.yaml` (adapter id/detection/permissions/discovery steps, reused parse limits, reused
capability-id set, installer-target-reuse and hook-ingress-reuse pointers), `hooks-and-otel.yaml`
(`claude.hook` event vocabulary/helper contract/installer target/trust rule, `claude.otel`
installer-target reuse/dropped-surface list/unconditional-strip rule, the hook+OTel
source-to-canonical event mapping, and the independent-degradation guarantee),
`transcript-and-inventory.yaml` (`claude.transcript` checkpoint/historical-content/quarantine/replay
fields, `claude.inventory` scope/node/edge reuse and cache/collision rules) and
`skill-evidence-and-reconciliation.yaml` (the seven-kind evidence model, the full
source-to-canonical table, the eight reconciliation lanes, the required-test list and the
structured `exit_gate` object, including the `support_label_governance` clause) — and two under
`contracts/cross-agent/` — `second-fixture-agent.yaml` (Wayfinder's deliberately-unlike-Loomwright
shape and required conformance checks) and `invariant-scenario.yaml` (the logical scenario, the
six-row stage-to-capability mapping, participating-adapters closure, and the assertion/rendering
rules). All eight are bound by `contracts/claude-policy-locks.yaml` and
`contracts/cross-agent-policy-locks.yaml` respectively, using the exact append-only semantic-digest
mechanism already used for `contracts/privacy`, `contracts/observability`, `contracts/data-platform`,
`contracts/adapter-sdk` and `contracts/codex`. `scripts/validate_claude.py` independently recomputes
each registry's semantic digest, cross-checks the six locked digests (four Claude, two cross-agent)
against the eight files on disk, and rejects a coherent registry/lock edit that weakens any of the
invariants above — proven by `tests/test_claude_contracts.py`'s 30 coherent-mutation tests, which
each construct a self-consistent lock (matching digests) around one weakened registry field and
assert the validator still rejects it on invariant-text grounds, not merely on a digest mismatch.

## Claude adapter, discovery and permission-checked probing

`internal/claudeadapter` registers `AdapterID = "claude"` against the Session 05
`adaptersdk.Registry`/`adaptersdk.Adapter` contract with zero new branch inside `internal/adaptersdk`
itself; `Manifest()` declares exactly the capability-id set `contracts/adapter-sdk/capabilities.yaml`
already closes over, inventing none. `Discover` (`discover.go`) resolves installation candidates
strictly from `HostView.AllowedRoots()` — populated from the three documented settings locations
(`claude_user_settings`/`claude_project_settings`/`claude_managed_settings`), never from an
undocumented `CLAUDE_HOME`-shaped variable Claude Code does not document, and never via a speculative
whole-home-directory scan — and reports one candidate per observable surface
(`cli`/`ide_extension`/`app`) beneath each resolved root without ever merging two candidates that
share a state root but differ by surface. `probeClaudeVersion` runs the credential-free `claude
--version` probe through `HostView.ExecProbe` (empty environment, bounded output) and extracts only a
bare semver token, discarding surrounding banner text. `FingerprintInstallation` records only
existence/size-class/mtime-shape of config/hook/skill/plugin locations, never their content.

## Hook and OTel sources

`hook.go` implements the `claude.hook` helper contract for all seven documented events
(`SessionStart`, `UserPromptSubmit`, `PreToolUse`, `PostToolUse`, `SubagentStart`, `SubagentStop`,
`Stop`): `DecodeHookInput` rejects unknown fields and oversized payloads before ever touching a byte
of untrusted stdin; `BuildHookOutput` computes `privacy.PromptFeatures` in memory (via
`internal/privacy.ExtractPromptFeatures`, reused rather than re-implemented) for prompt events only
and never copies the raw prompt string into `HookHelperOutput`; `pseudonymizePath` HMACs
`transcript_path`/`cwd` with the same device-scoped key `internal/privacy` already carries, so the raw
path is never forwarded or stored; `ValidateHookOutputAllowlist` re-checks the resulting output's
field set is exactly the closed allowlist before the caller may forward it.
`internal/observability/routes.go`'s `hookAdapterHandler` dispatches `claudeadapter.AdapterID`
through the exact same generic `/v1/hooks/{adapter}/{event}` mux the `fixture-agent` and `codex`
cases already use — `scripts/validate_claude.py`'s source scan asserts exactly one
`http.NewServeMux()` call exists in that file and that both prior cases remain untouched, so a future
Claude-route regression toward a second HTTP server or second bearer-auth mechanism fails closed.
`otel.go`'s `CanonicalEventForOTel` maps a documented OTel event name onto its canonical event type
only when the active fingerprint for that mapping actually matches the observed attribute shape, and
`DroppedOTelSurfaces` explicitly and unconditionally drops `log.body`/`tool_payload`/
`output_snippet`/`prompt_text`/`assistant_response_text`/`raw_api_body` regardless of what
`OTEL_LOG_USER_PROMPTS`/`OTEL_LOG_ASSISTANT_RESPONSES`/`OTEL_LOG_TOOL_DETAILS`/
`OTEL_LOG_TOOL_CONTENT`/`OTEL_LOG_RAW_API_BODIES` report upstream — this unconditional strip is
Claude's central privacy tightening over Session 06's Codex boundary, because Claude's documentation
records these settings as user-toggleable outside Kansoku's control.

## Inventory graph and skill evidence model

`inventory.go`'s `BuildInventorySnapshot` reuses `contracts/adapter-sdk/inventory-graph.yaml`'s
closed node/edge/scope vocabulary verbatim; a cache-only plugin package is never reported enabled
absent an explicit `enabled_for` edge, and a plugin-bundled component is never reported as a
standalone/unowned node when the bundling relationship is observable. `evidence.go`'s
`ResolveSkillEvidence` is this session's central exit-gate mechanism, extended from Codex's five-kind
model to seven kinds (native explicit `Skill` call, native implicit `Skill` call, OTel `skill.name`
attribution, `SKILL.md` load evidence, plugin/MCP declared use, uniquely owned helper execution,
semantic opportunity classifier): only the two native-labeled kinds resolve to `EvidenceTierNative`,
`semantic_opportunity_classifier` can never resolve to anything but `EvidenceTierInferred`, and
ambiguous ownership (`uniquely_owned_helper_execution` with more than one candidate skill identity) is
returned with `ErrAmbiguousOwnershipPromotion` rather than silently promoted to
`component.invoked`. `reconcile.go`'s eight lanes (`LanePrompts` through `LaneComponentEvidence`,
extending Codex's six with `LaneSubagentLifecycle` and `LanePluginOwnership`) run independently;
a missing source degrades only its own lane, never a whole-session fabricated zero, and tolerance is
resolved per compatibility-registry entry (`ResolveTolerance`), never hardcoded.

## Second fixture-agent (Wayfinder) and cross-agent invariant test

`internal/adaptersdk/wayfinder` registers `AdapterID = "wayfinder"` — deliberately unlike both
Session 05's "Loomwright" and Session 03's reserved `fixture-agent` conformance identity — proving a
second time that `internal/adaptersdk`'s `Registry`/`HostView`/inventory/reconciliation machinery
requires zero new agent-name branch for a differently-shaped fictional agent. Wayfinder declares zero
OTel source (only a versioned local event file), a `recipe` component vocabulary, non-UUID
`wf-session-<n>`-shaped session identifiers, an explicitly `unsupported` (never zero-populated) token/
cost capability, and exactly one deliberately unknown event schema (`recipe.mystery`) that
`Normalize` quarantines as a scoped, non-fatal incident rather than silently dropping it or guessing it
into a known canonical type — `TestNormalizeQuarantinesTheOneUnknownSchemaEventWithoutError` and
`TestUnknownSchemaEventAbsentFromDeclaredSourceSchema` prove this directly.

`internal/crossagent`'s test package expresses the logical scenario `session -> prompt metadata ->
skill activation -> MCP tool call -> model tokens -> success` once for Codex and once for Claude,
driving each agent's own real hook/OTel mapping functions against
`tests/fixtures/session-07/cross-agent-invariant-scenario.json`'s per-agent fixture branches. Every
assertion binds to a canonical capability ID or canonical event type from
`contracts/cross-agent/invariant-scenario.yaml`'s six-row `scenario_stage_to_capability_mapping`
table — fixture selection (`codex`/`claude` map keys) is the only place an agent ID string appears;
`TestCrossAgentModelTokensStageRendersUnsupportedForCodexAndNativeForClaude` specifically proves the
two agents are allowed to render different evidence tiers for the same logical stage (Codex's
documented telemetry does not expose a dedicated skill-activation event the way Claude's does) without
the test forcing either a uniform zero or an artificially equal tier across both agents.
`TestAgentSpecificExtraEventSurvivesAsAllowlistedAttributeWithoutCoreChange` proves an agent-specific
extra attribute survives normalization without requiring a new core branch.

## Supply chain (explicit no-op)

Session 07 vendors no new third-party Go or Python dependency: `internal/claudeadapter`,
`internal/adaptersdk/wayfinder` and `internal/crossagent` import only the Go standard library plus
already-vendored `internal/adaptersdk`, `internal/privacy`, `internal/installer`,
`internal/codexadapter` and (from `internal/observability`'s side) the already-vendored gRPC/OTLP
stack Session 03 introduced. `git diff HEAD -- go.mod go.sum` is empty, and `go.mod`/`go.sum`'s
SHA-256 digests (`716f9585d0…`/`ea6a111b2b…`) are byte-identical to the Session 05/06 baseline
recorded in `reports/session-05-sbom.json`/`reports/session-06-sbom.json`. `reports/session-07-sbom.json`
is therefore a minimal, valid CycloneDX 1.6 document with an explicit empty component list and
properties recording this no-op explicitly, rather than a fabricated component list — matching
Session 05/06's honest precedent; there is no `scripts/session07_supply_chain.py` for the same reason.

One incidental note: `internal/observability/routes.go` changed (a new `claude` dispatch case), so
`reports/session-03-sbom.json`'s `kansoku:session03-source-sha256` property — which hashes that
file's tree alongside the rest of `internal/observability` — was regenerated with
`python3 scripts/session03_supply_chain.py --write` in this stage, exactly as Session 06 already did
for its own `codex` case addition to the same file. This is a routine re-hash of an evidence property
whose underlying file legitimately changed, not a re-opening of Session 03's contract or lock content.

## Explicit scope exclusions

Per ADR 0010, the original TDD 07 / Engineering Proposal 07 documents group three agents together:
Claude Code, Gemini CLI and a Cursor probe. **This session evaluates Claude Code only.** Gemini CLI's
OTel/hook vocabulary (`gemini_cli.*` events, standard GenAI semantic-convention attributes) and the
Cursor probe (`workspace_roots`/`transcript_path`/prompt-bearing hook payloads) are deferred in full
to **Session 07b**, including their own independent research-and-fixture cycle. No
`contracts/gemini/`, `contracts/cursor/`, or corresponding lock files exist after this session; an
earlier, now-abandoned attempt at this session briefly created Gemini/Cursor-flavored contract and
ADR material before the scope was narrowed, and none of it is reintroduced here or by this stage.
`contracts/cross-agent/`'s `invariant-scenario.yaml` explicitly closes `participating_adapters` to
`["codex", "claude"]` and its `participating_adapters_note` field states Gemini and Cursor are
explicitly excluded from this session's scenario.

## Verification

- `python3 scripts/validate_contracts.py` — pass (unaffected; Session 07 does not touch Session 01
  registries).
- `python3 scripts/validate_privacy.py` — pass (unaffected; `internal/privacy` contracts and lock
  digests are untouched by this session).
- `python3 scripts/validate_observability.py` — pass (unaffected; Session 07 does not touch
  `contracts/observability`'s registries, only adds a new dispatch case in `routes.go` that reuses the
  existing generic route/auth).
- `python3 scripts/validate_data_platform.py` — pass (unaffected).
- `python3 scripts/validate_adapter_sdk.py` — pass (unaffected; Session 07 does not touch
  `contracts/adapter-sdk`, adding the Wayfinder fixture-agent and cross-agent scenario as new,
  separately-locked `contracts/cross-agent/*` files instead, per ADR 0010's rejected-alternatives
  reasoning).
- `python3 scripts/validate_codex.py` — pass (unaffected; Session 07 does not edit
  `contracts/codex/*`, and `internal/observability/routes.go`'s existing `codex` case is explicitly
  checked untouched by `scripts/validate_claude.py`'s own source scan).
- `python3 scripts/validate_claude.py` — pass (static contract for both `contracts/claude/*` and
  `contracts/cross-agent/*`, registry/lock digest cross-check, `internal/claudeadapter`/
  `internal/adaptersdk/wayfinder`/`internal/crossagent`/`internal/observability` code/contract
  alignment scan, and fixture validation; `--with-go` optionally re-runs the Session-07-package-only
  Go slice inside the pinned offline image — also verified green in this stage).
- `python3 -m unittest discover -s tests -v` — pass, 132/132 tests across Sessions 01–07, including
  all 41 of `tests/test_claude_contracts.py`.
- `python3 scripts/run_go_tests.py` — pass, full `go build`/`go vet`/`go test` sweep across every
  package including `internal/claudeadapter` (61 subtests), `internal/adaptersdk/wayfinder` (16
  subtests) and `internal/crossagent` (7 stage subtests + 1 extra-attribute subtest), all green.
- `python3 scripts/run_privacy_canary.py` — pass (unaffected; Session 07 introduces no new sink and
  no new raw-content path; the Claude hook helper reuses the Session 02 sanitizer and
  feature-extraction boundary at the exact same trust boundary as Codex's).
- `python3 scripts/session03_supply_chain.py --verify` — pass (regenerated with `--write` in this
  stage after `internal/observability/routes.go`'s legitimate `claude` dispatch-case addition; see
  "Supply chain" above).
- `python3 scripts/session04_supply_chain.py --verify` — pass (unaffected).
- No `scripts/session07_supply_chain.py --verify` — intentionally does not exist; see "Supply chain
  (explicit no-op)" above.

## Known gaps (explicitly recorded, not silently dropped)

1. **`claude.transcript`'s checkpointed JSONL importer has no Go implementation yet.** Unlike Codex's
   `rollout.go`, this session's `transcript.go` contains only the `sourceIDTranscript` source
   identifier so `reconcile.go`'s lanes can name `claude.transcript` as a lane input; the actual
   file-identity/offset/fingerprint/rotation/truncation-detecting importer, the historical-content
   opt-in/user-disable path, and the corrupt/unknown-schema quarantine behavior are fully specified in
   `contracts/claude/transcript-and-inventory.yaml transcript_source` but have no corresponding Go
   code or fixture-driven test in this session. This is the largest concrete gap between Claude's
   contract and Claude's implementation; a later stage must close it the same way Session 06's
   `rollout.go` closed the equivalent gap for Codex.
2. **No fixture-driven prohibited-content canary with detailed upstream telemetry explicitly
   enabled.** `DroppedOTelSurfaces`'s unconditional-strip behavior and `BuildHookOutput`'s
   never-copies-raw-prompt/path behavior are both proven by direct unit tests, and
   `contracts/claude/skill-evidence-and-reconciliation.yaml required_tests` lists
   `prohibited_content_canaries_with_detailed_upstream_telemetry_enabled` as a required test — but no
   dedicated fixture file in `tests/fixtures/session-07/` yet constructs an explicit
   "`OTEL_LOG_USER_PROMPTS=1`-shaped input, still stripped" scenario the way Session 06's
   `prohibited-content-canaries.json` did for Codex. The underlying code guarantee holds (the strip is
   unconditional in `otel.go`/`hook.go` regardless of any setting value passed in), but the canary
   fixture itself is a follow-on deliverable.
3. **No live canary evidence yet, only unit-and-fixture evidence.** The exit gate's
   `compatibility_matrix_backed_by` field requires fixtures at minimum; this stage proves every
   source/evidence/reconciliation guarantee against synthetic Go fixtures and hand-constructed inputs,
   not a live run against a real installed Claude Code process — no such process exists in this build
   environment. Unlike Session 06, this session records no committed compatibility-matrix version
   string tied to a specific fixture-and-materialized-project canary chain yet; that remains open work.
4. **`internal/adaptersdk.Adapter.Audit` still returns `nil`.** Mirroring Session 06's identical,
   already-resolved-for-other-methods gap: `stage2_stub.go`'s `Adapter.Audit` returns no
   `CheckResult` rather than a fabricated pass/fail, and remains a later stage's responsibility.
   `Manifest`/`Discover`/`Inventory`/`PlanConfiguration`/`Normalize`/`Reconcile` are all wired to real
   free functions in this session (see the Delivered-contract table above), so this gap is narrower
   than Session 06's equivalent gap was before its own fix stage.
5. **`claude.user_hook` has no real filesystem writer yet.** As recorded in ADR 0010 and consistent
   with every prior session's installer scope, no code in this session performs a real write to a
   user's Claude Code configuration; that remains gated behind `internal/installer`'s existing
   preview/consent/simulate-only machinery until a session explicitly promotes real writes (ADR 0002).
   `PlanConfiguration` builds a real, simulate-only `ChangePlan` for the `claude.user_otel` target via
   `installer.BuildClaudePlan`/`adaptersdk.BuildChangePlan` — but only for the OTel install target;
   there is still no equivalent plan-construction path for `claude.user_hook` specifically.
6. **Single compatibility-registry entry, matching Session 06's identical gap shape.** No second
   declared compatibility range (a hypothetical future Claude Code major version with a materially
   different event shape) exists yet to prove the "on every supported Claude Code version" phrasing of
   the exit gate across more than one compatibility boundary; `SOURCES.md`'s already-recorded
   `2.1.214`/`2.1.216`/`2.1.193` behavior gates and the locally-observed `2.1.197` runtime remain
   documentation-level notes rather than fixture-verified version-gated behavior in this stage.
7. **No `kansoku doctor`/`configure`/`adapter verify` CLI entry point.** As in Sessions 05/06, the
   `Registry.CapabilityMatrix`/`Audit`-shaped data this session's Claude adapter would report through
   such a CLI exists, but no `cmd/` binary calls it yet.
8. **Passive daily probe is not scheduled.** As in Session 06, `Audit`'s `passive` mode concept is
   declared in `contracts/adapter-sdk/discovery-and-plans.yaml` (Session 05), but no scheduler exists
   yet to actually run `Audit` on a recurring cadence against a real installation.
9. **Gemini CLI and the Cursor probe remain entirely out of scope, by design.** See "Explicit scope
   exclusions" above; this is not a gap in this session's own exit gate, but is recorded here so a
   reader scanning "Known gaps" sections across sessions does not mistake the absence of Gemini/Cursor
   material for an oversight. Session 07b owns closing this exclusion.
