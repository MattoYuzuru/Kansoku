# ADR 0008: Session 05 adapter SDK and inventory boundary

- Status: accepted
- Date: 2026-07-22
- Owners: Session 05 core architecture

## Context

Session 05 must make "supports future agents" a testable engineering property rather than a promise.
TDD 05 requires a typed `Adapter` interface, a permission-checked `HostView`, an immutable inventory
entity graph, a reversible `ChangePlan` and a capability-state model, and its exit gate is explicit:
a fake external-vocabulary adapter must pass the full conformance suite and appear through the same
inventory/health APIs and capability routing any built-in adapter would use, with zero new agent-name
conditional anywhere in core code outside the adapter's own registration.

This sits directly on top of two prior boundaries this session must reuse, not duplicate:
`internal/privacy`'s `SafeRecord`/`SafeError` typed sanitizer (Session 02) is the only sanitizer any
source may cross, and `internal/installer`'s `Plan`/`Approval`/`EffectiveSettings`/
`SimulateApply`/`SimulateRollback`/`SimulateRemove`/`PlanSHA256` machinery (also Session 02) is the
only reversible-configuration-change mechanism in the repository. Session 05 does not have its own
durable store yet: `internal/dataplatform` (Session 04) owns persistence, and no adapter is ever
handed a database connection string or credential.

## Decision

1. The authoritative adapter contract is the closed registry set in
   `contracts/adapter-sdk/{manifest,capabilities,inventory-graph,discovery-and-plans}.yaml`, locked by
   `contracts/adapter-sdk-policy-locks.yaml` using the same append-only semantic-digest mechanism
   already used for `contracts/privacy`, `contracts/observability` and `contracts/data-platform`.
2. `internal/adaptersdk` defines the exact TDD 05 `Adapter` interface
   (`Manifest`/`Discover`/`Inventory`/`PlanConfiguration`/`SourceSchemas`/`Normalize`/`Reconcile`/
   `Audit`), a `Registry` that looks adapters up and iterates them by their own registered manifest
   ID only, and every closed type the registries declare: `Manifest`, `AgentDetection`,
   `SourceDescriptor`, `Permissions`, `InstallationCandidate`, `Installation`, `Node`/`Edge`/
   `InventorySnapshot`, `ChangePlan`, `ReconcileScope`/`ReconcileResult`, `AuditMode`, `CheckResult`.
   `Registry.Register`/`Get`/`IDs`/`CapabilityMatrix` never inspect an adapter ID string for a known
   agent brand and never type-switch or string-switch on which concrete `Adapter` is registered;
   adding a new agent means calling `Register` with a new value, never editing a switch statement.
3. `ParseManifest` decodes a manifest as bounded, inert data only: it rejects duplicate JSON object
   keys, unknown fields, and any value exceeding the same `MaxManifestConfigEntries`/
   `MaxManifestConfigDepth`/`MaxManifestConfigString` bounds `internal/installer/protocol.go` already
   enforces for agent configuration plans, walking the manifest as generic JSON so a future field
   addition to the `Manifest` struct cannot silently bypass the ceiling. It never evaluates, executes,
   shells out to, or otherwise interprets anything a manifest field contains, including a string that
   looks like a shell command — the parser only ever produces a typed struct, never a side effect.
4. `HostView` is the only surface an `Adapter` touches the host through: `NewHostView` takes an
   already-resolved, absolute set of allowed roots (never a home-directory default) and a closed exec
   allowlist. `ReadProbe` refuses to resolve any path — not even to `stat` it — outside an allowed
   root, including a symlink whose target resolves outside that root. `ExecProbe` refuses any binary
   not on the allowlist, runs with an empty explicit environment (never the inherited parent
   environment), and truncates output to a bounded byte ceiling before it is ever returned to an
   adapter. `PseudonymizePath` is the only durable representation of a filesystem path an `Adapter`
   may place in `Node.PathPseudonym`: it is an HMAC-SHA256 output over a device-scoped key, the same
   class of construction `internal/privacy`'s `Lineage` pseudonymization already uses, and the raw
   path is never itself a durable field. `HostView` has no field or method that can return a database
   connection string, credential, or an unscoped filesystem handle.
5. `adaptersdk.SafeSourceRecord` and `adaptersdk.CanonicalEvent` are type aliases for
   `privacy.SafeRecord`, not a second sanitizer. `Normalize` receives only an already-sanitized
   `SafeRecord`; adaptersdk declares no generic payload/attributes map an adapter could smuggle a raw
   prompt, tool input/output, source code, or file path through, so the Session 02 trust boundary is
   preserved exactly, not re-implemented.
6. `ChangePlan` construction reuses `internal/installer`'s existing machinery verbatim instead of
   inventing a second apply/rollback/removal mechanism: `adaptersdk.BuildChangePlan` takes an
   `installer.Plan` (built by one of the existing `installer.BuildCodexPlan`/`BuildClaudePlan`/
   `BuildGeminiPlan`/`BuildCursorPlan` or an adapter-specific equivalent constructed the same way),
   binds `ChangePlan.PlanID` to `installer.PlanSHA256(installerPlan)` so the two can never drift, and
   leaves `Apply`/`Rollback`/`Remove` to `installer.SimulateApply`/`SimulateRollback`/`SimulateRemove`
   with the same underlying `installer.Plan` and an `installer.Approval` bound to it. Session 05 adds
   no parallel apply/rollback code path; `contracts/adapter-sdk/discovery-and-plans.yaml
   change_plan_reuse` records this decision so a future coherent contract edit cannot silently reopen
   a second mechanism.
7. Inventory is a graph, not a file list: `Node`/`Edge` implement the exact entity kinds TDD 05
   specifies (`device`, `agent_installation`, `agent_surface`, `plugin_package`, `skill_identity`,
   `mcp_server_instance`, `hook_definition`, `custom_command_definition`, `subagent_definition`,
   `cache_artifact`, ...) and the exact edge kinds (`bundles`, `provides`, `configured_in`,
   `enabled_for`, `shadows`, `collides_with`, `depends_on`, `observed_using`). Two nodes with the same
   `DeclaredName` but a different `SourceScope` or `Fingerprint` are never merged into one node; they
   remain distinct and linked by a `shadows` or `collides_with` edge. A `CacheArtifact` node is never
   reported as enabled without an explicit `enabled_for` edge to an active installation.
   `InventorySnapshot` values are immutable observations; `Reconcile` derives added/removed/changed
   node sets by diffing two snapshots and never mutates either one, so replaying the same pair twice
   yields a byte-identical `ReconcileResult`.
8. The repository ships exactly one conformance adapter, `internal/adaptersdk/fakeadapter`, for a
   fictional agent "Loomwright" whose executable (`loomctl`), state-root env var
   (`LOOMWRIGHT_HOME`), event vocabulary (`weave.begun`/`shuttle.passed`/`thread.completed`/
   `weave.completed`) and component vocabulary (`loom`, `spool`, `thread`) share no substring with
   Codex, Claude Code, Gemini CLI or Cursor. It implements the full `Adapter` interface, runs through
   the same `Registry`/`CapabilityMatrix`/`HostView` every real adapter would, and its `PlanConfiguration`
   deliberately returns an explicit unimplemented-write error rather than fabricating a second
   configuration-write path — the fake adapter proves discovery/inventory/normalization/
   reconciliation/audit conformance, not a second install mechanism.
9. `scripts/validate_adapter_sdk.py` is the independent closed-world validator for this session: it
   recomputes each registry's canonical semantic digest against
   `contracts/adapter-sdk-policy-locks.yaml`, grep-cross-checks `internal/adaptersdk` and
   `fakeadapter` source for the required declarations and invariant text (no-agent-name-branch,
   `HostView` credential-free guarantee, `installer.PlanSHA256` binding, no second `SafeRecord`
   declaration, no real-agent-name string literal in the fake adapter's own vocabulary), and validates
   `tests/fixtures/session-05/loomwright-conformance.json`. An optional `--with-go` flag additionally
   builds/vets/tests `internal/adaptersdk/...` inside the same pinned, network-disabled Go image
   `scripts/run_go_tests.py` already uses, so this validator remains runnable standalone.
10. Session 05 vendors no new third-party Go or Python dependency: `internal/adaptersdk` only imports
    the Go standard library plus `internal/privacy` and `internal/installer`, both already vendored.
    `reports/session-05-sbom.json` is therefore a minimal CycloneDX 1.6 document that explicitly
    states this and carries no fabricated component list, unlike `reports/session-04-sbom.json`
    which had a real new dependency (`pgx/v5`) to inventory.

## Consequences

- Session 06 (Codex adapter) becomes the first real `Adapter` implementation registered against this
  same `Registry`; if it needs to branch on "is this Codex" anywhere in `internal/adaptersdk` itself
  rather than inside its own package, that is a regression against this ADR's exit gate, not a normal
  extension.
- `PlanConfiguration`/`ChangePlan` for a real write-capable adapter must build a genuine
  `installer.Plan` (reusing the existing Codex/Claude/Gemini/Cursor builders where the target agent
  matches, or a new builder shaped the same way for a new agent) before calling
  `adaptersdk.BuildChangePlan`; there is no shortcut that skips `installer.Plan` construction.
- External-process, Wasm and container adapter execution forms are declared in the manifest schema
  (`execution_forms`) and sequenced (`execution_form_sequence`) but only `builtin` is wired through
  the in-process `Registry` today; a future session must add the supervised subprocess protocol
  before any non-Go-native adapter can register.
- The compatibility registry fields (`agent_version_range`, `source_schema_fingerprints`,
  `fixture_coverage`, `last_passive_audit_at`, `last_live_audit_at`, `known_gaps`,
  `setup_recipe_version`) are declared in `contracts/adapter-sdk/manifest.yaml` but there is no
  concrete Go store or lookup for them yet; a real adapter's `Audit`/health routing cannot yet
  automatically apply the "unknown version defaults to degraded" policy end-to-end without one.

## Rejected alternatives

- **Give the fake adapter a name/vocabulary that loosely echoes a real agent (e.g., "codex-like"):**
  would weaken the exact proof TDD 05's exit gate asks for — that core routing has zero agent-name
  branch. "Loomwright" and its `loomctl`/`weave`/`shuttle`/`thread`/`spool` vocabulary share no
  substring with any real adapter, which is asserted by both the Go test suite and
  `scripts/validate_adapter_sdk.py`.
- **Invent a second apply/rollback mechanism scoped to adapters:** would duplicate
  `internal/installer`'s already-reviewed preview/consent/race/rollback contract and create two
  divergent code paths for the same operation class. `adaptersdk.BuildChangePlan` reuses
  `installer.Plan`/`Approval`/`SimulateApply`/`SimulateRollback`/`SimulateRemove`/`PlanSHA256`
  verbatim instead.
- **Let `Normalize` accept a generic `map[string]any` payload for maximum adapter flexibility:** would
  reopen exactly the raw-payload leakage path Session 02's sanitizer boundary exists to close.
  `SafeSourceRecord`/`CanonicalEvent` are aliases of the closed `privacy.SafeRecord` struct, which has
  no such generic field.
- **Implement the external-process (subprocess/gRPC) adapter protocol now, ahead of a second
  built-in adapter:** TDD 05 sequences built-in adapters first and external-process second "after
  built-ins stabilize"; Session 05 has exactly one built-in-shaped fake adapter and no real
  third-party adapter demand yet, so building the supervised subprocess handshake now would be
  speculative scope with no adapter to exercise it end-to-end.
- **Skip a standalone `validate_adapter_sdk.py` and rely only on `go test ./...`:** would not
  independently verify the registry/lock digest binding or the closed-world invariant text the same
  way `scripts/validate_observability.py`/`scripts/validate_data_platform.py` already do for their
  sessions, and would not give a single standalone command proving the Session 05 contract the way
  the task's exit criteria require.

## Known gaps (explicitly recorded, not silently dropped)

1. **External-process/Wasm/container adapter execution.** Only `builtin` is wired through the
   in-process `Registry`; the versioned subprocess handshake (line-delimited framed JSON or gRPC over
   Unix socket, environment allowlist, crash-restart budget, unsigned-by-default labeling) described
   in TDD 05 is declared in the manifest schema but has no Go implementation yet. Deferred to the
   session that first needs a non-Go-native or genuinely third-party adapter.
2. **Compatibility/fixture registry persistence.** `manifest.yaml compatibility_registry_fields`
   defines the shape (`agent_version_range`, `source_schema_fingerprints`, `fixture_coverage`,
   `last_passive_audit_at`, `last_live_audit_at`, `known_gaps`, `setup_recipe_version`), but no store
   or lookup exists; `Audit` in this session runs a fixed fixture-replay check set rather than
   consulting a real per-version compatibility record. A future session must add the store before an
   "unknown version defaults to degraded" claim can be made about a real adapter automatically rather
   than by manual review.
3. **`kansoku doctor`/`configure`/`adapter verify` CLI.** The concepts and their read-only/plan-only/
   fixture-and-canary contracts are declared in `discovery-and-plans.yaml cli_concepts`, but no `cmd/`
   binary exists yet. `Registry.CapabilityMatrix` is the data `doctor` would render; no command-line
   entry point calls it yet. Deferred to the session that first needs an operator-facing CLI.
4. **Live canary and third-party signing.** `AuditLiveCanary` is a declared `AuditMode` value and the
   fake adapter's `Audit` only ever runs a fixture-replay-shaped check; no real live-agent canary
   exists because there is no real adapter yet. Signed adapter package distribution remains explicitly
   deferred per `manifest.yaml external_protocol.distribution`, matching TDD 05.
5. **No real agent fixture, runtime canary or agent configuration was observed or changed.** As with
   every prior session, all inventory/discovery/reconciliation evidence in this session is the
   synthetic "Loomwright" fixture only; public Supported/Beta governance for any real adapter remains
   blocked and requires the version-bounded evidence and two independent human reviews ADR 0002/0005
   already require.
