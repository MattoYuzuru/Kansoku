# ADR 0009: Session 06 Codex adapter

- Status: accepted
- Date: 2026-07-22
- Owners: Session 06 core architecture

## Context

Session 05 built `internal/adaptersdk` and proved, with a fictional "Loomwright" adapter, that the
`Registry`/`HostView`/inventory/`ChangePlan` machinery can host a real agent with zero agent-name
branch in core code. Session 06 must register the first genuinely real adapter — Codex — against
that exact same registry, and its exit gate is deliberately harder than a conformance proof: Codex's
documented telemetry does not necessarily expose a dedicated skill-activation event, so the adapter
must reconcile four independently-capability-scoped sources (`codex.hook`, `codex.otel`,
`codex.rollout`, `codex.inventory`) without ever collapsing partial, ambiguous or inferred evidence
into a false native exact activation count, and without ever letting one broken source silently look
like "zero usage" for the whole session.

This session reuses, rather than reinvents, three prior boundaries:

- `internal/privacy`'s `SafeRecord`/`SafeError` sanitizer (Session 02) remains the only trust
  boundary any Codex source may cross; no second sanitizer is declared.
- `contracts/privacy/installer.yaml`'s existing `codex.user_otel` target (Session 02) is reused
  verbatim for the OTel plan; this session only adds a new `codex.user_hook` target for the observer
  hook, because no hook target existed yet.
- `contracts/observability/ingress.yaml`'s existing generic `hook_http` route template
  (`/v1/hooks/{adapter}/{event}`, Session 03) is reused verbatim by substituting `codex` as the
  adapter path segment; `internal/observability/routes.go`'s only prior hook route was the
  `fixture-agent` example, and Session 06 must wire real Codex routes through that same generic
  mechanism rather than a parallel one.

Because two prior single-shot attempts at this session crashed partway through on a transient
corporate-VPN-related API error and lost significant work, Session 06 is deliberately built as a
sequence of separately checkpointed stages (contracts, Go implementation, canary/fixtures/tests,
ADR/reports, validator/Python tests, README/ROADMAP updates, final full-suite verification), each of
which must leave the repository in a consistent, independently-inspectable state before the next
stage begins.

## Decision

1. The authoritative Codex contract is the closed registry set in
   `contracts/codex/{manifest,hooks-and-otel,rollout-and-inventory,skill-evidence-and-reconciliation}.yaml`,
   locked by `contracts/codex-policy-locks.yaml` using the identical append-only semantic-digest
   mechanism already used for `contracts/privacy`, `contracts/observability`, `contracts/data-platform`
   and `contracts/adapter-sdk`. No prior trusted lock entry in any of those four earlier files is
   edited; Session 06 only appends new lock entries scoped to the four new Codex registry files.
2. `contracts/codex/manifest.yaml` declares Codex's `adapter_id`, agent detection
   (`codex` executable, `CODEX_HOME` state-root env var, documented default state root, `cli`/
   `ide_extension`/`app` surfaces), the installation-discovery step sequence (version probe without
   login/auth output, `CODEX_HOME`-before-default resolution, surface detection without merging
   installations solely by shared state root, config/hook/skill/plugin fingerprinting without
   recording values, supported-version/recipe-version recording), and reuses
   `internal/adaptersdk`'s existing `MaxManifestConfigEntries`/`MaxManifestConfigDepth`/
   `MaxManifestConfigString` parse bounds and the closed `capability_ids` vocabulary from
   `contracts/adapter-sdk/capabilities.yaml` verbatim; it declares no new capability id and no new
   parse limit.
3. `contracts/codex/hooks-and-otel.yaml` specifies `codex.hook` (supported events `SessionStart`,
   `UserPromptSubmit`, `PreToolUse`, `PostToolUse`, `SubagentStart`, `SubagentStop`, `Stop`, each
   independently version-manifested), the hook helper's bounded-stdin/in-memory-feature/allowlisted-
   field/short-timeout/never-block/bounded-0600-spool contract, a new `codex.user_hook` installer
   target scoped to user-level config only by default, and an explicit hook trust/enabled-state audit
   rule that forbids silent bypass or repair — only audit-and-remediate. It specifies `codex.otel` by
   reusing the existing `codex.user_otel` installer target and `contracts/observability/ingress.yaml`'s
   OTLP-safe-attribute allowlist verbatim, mapping documented `codex.conversation_starts`,
   `codex.user_prompt`, `codex.tool_decision`, `codex.tool_result` and related events onto canonical
   event types only when the active version manifest actually contains that event name.
4. `contracts/codex/rollout-and-inventory.yaml` specifies `codex.rollout` as a checkpointed JSONL
   importer (file identity, inode/file-id where available, byte offset, first/last record
   fingerprint, rotation/truncation detection) that never writes to the Codex session tree, defaults
   to safe-structured-fields-only historical parsing with an explicit opt-in for transient in-memory
   content feature extraction (never a durable raw-content write) and an explicit user disable
   switch, and quarantines corrupt/unknown schema records as metadata-only incidents. It specifies
   `codex.inventory` by reusing `contracts/adapter-sdk/inventory-graph.yaml`'s closed `source_scopes`/
   `node_kinds`/`edge_kinds`/cache-separation/identity-merge vocabulary verbatim, bounding repository
   scans to known active projects or an explicit user target, and computing discoverability-pressure
   fields (description byte/character totals, scope precedence, duplicate/disabled flags, catalog
   pressure risk) that are always labeled `inferred` unless direct session/source evidence promotes a
   skill to `exposed`.
5. `contracts/codex/skill-evidence-and-reconciliation.yaml` is the closed evidence model: five
   evidence kinds (explicit user invocation, `SKILL.md` load evidence, agent-declared use, uniquely
   owned helper execution, semantic opportunity classifier) each bound to one canonical event type and
   one evidence tier from `contracts/observability/lifecycles.yaml`'s existing tier vocabulary, an
   explicit "no false exact count" dashboard rule, an explicit ambiguous-ownership rule that never
   converts an ambiguous helper/MCP call into `component.invoked`, and the explicit
   native-exact-activation prohibition that is this session's central exit-gate invariant. It also
   carries the full source-to-canonical mapping table, the six per-session reconciliation
   comparisons, the canary fixture/execution-constraint design, the required test list, and a
   structured `exit_gate` object so later stages (validator, tests, reconciliation report) can check
   against one place rather than re-deriving the exit gate from prose.
6. Every Codex hook route is declared as a substitution into the existing generic
   `contracts/observability/ingress.yaml` `hook_http` protocol route template
   (`/v1/hooks/{adapter}/{event}` with `adapter=codex`), not a new protocol entry; a later
   implementation stage must wire `internal/observability/routes.go`'s Codex routes through that same
   mux mechanism the `fixture-agent` example route already uses, never a parallel HTTP server or a
   second auth mechanism.
7. Every source (`codex.hook`, `codex.otel`, `codex.rollout`, `codex.inventory`) is independently
   capability-scoped: the registries state explicitly that disabling or breaking one source degrades
   only the capability ids that source backs, never the others, and never collapses to a
   plausible-looking zero for the whole session. This is asserted as its own field
   (`independent_capability_degradation` / per-source "degrades only its own capability") rather than
   left implicit in prose, so a later validator can check for the exact guarantee text.
8. This session is executed as sequential checkpointed stages rather than one continuous run, because
   two prior single-shot attempts crashed partway through on a transient corporate-VPN-related API
   error. Each stage begins by reading current repository state (`git status`, existing
   `contracts/codex/*`, existing `internal/observability` code) rather than assuming a clean slate,
   and treats any inconsistency it finds (for example, a lock file referencing a registry file that
   does not exist) as a defect to fix or regenerate correctly, never as scaffolding to build on top of
   uncritically.

## Consequences

- Session 07 (Claude/Gemini/next agents) becomes the second and third real `Adapter` registrations
  against the same `Registry`; if either needs a `codex.user_otel`/`codex.user_hook`-shaped installer
  target of its own, it must add a new target entry to `contracts/privacy/installer.yaml`'s `targets`
  list the same way Session 06 added `codex.user_hook`, never edit or reinterpret Codex's entries.
- `internal/observability/routes.go` must grow real `/v1/hooks/codex/{event}` handlers for every
  event `contracts/codex/hooks-and-otel.yaml` declares, reusing the same `Ingestor`/`OTLPReceiver`
  plumbing the `fixture-agent` example route already demonstrates; a future session adding a Claude or
  Gemini hook route that instead stands up a second HTTP mux or a second bearer-auth mechanism would
  be a regression against this ADR's decision, not a normal extension.
- The `codex.user_hook` installer target's `ChangePlan` construction must still go through
  `internal/adaptersdk`'s existing `BuildChangePlan`/`installer.Plan`/`PlanSHA256` machinery exactly as
  ADR 0008 requires for any write-capable adapter; Session 06 introduces no second apply/rollback path.
- The evidence-tier and native-exact-activation-prohibition invariants recorded here are the
  authoritative text later Go code, the validator, and the dashboard rendering rules must all match
  word-for-word where the validator greps for it; a future coherent contract edit that quietly removes
  "never represents inferred...as a native exact activation" would be exactly the kind of drift this
  ADR's lock mechanism exists to catch.
- Because this is a staged build, later stages inherit an explicit obligation to keep `contracts/codex/*`,
  `contracts/codex-policy-locks.yaml`, this ADR, and eventually `internal/observability`'s Codex
  registration mutually consistent at every checkpoint, not only at the final stage.

## Rejected alternatives

- **Redefine `codex.user_otel` inside a new `contracts/codex/` target instead of reusing
  `contracts/privacy/installer.yaml`'s existing entry:** would create two divergent descriptions of
  the same OTel plan and violate the append-only-trust-boundary spirit of `contracts/README.md`;
  `hooks-and-otel.yaml` explicitly states it declares no second OTel installer target and only maps
  documented events onto the existing one.
- **Give Codex its own `/v1/codex/hooks/{event}`-shaped ingress route instead of substituting into
  the existing generic `hook_http` template:** would duplicate Session 03's already-reviewed
  ingress/durability/auth contract for no reason; the generic route already parameterizes on
  `{adapter}`, and Session 06 needs only to supply `codex` as that parameter and add the concrete
  route handlers in a later stage.
- **Treat any observed helper or MCP call as an exact skill invocation for a simpler dashboard
  story:** would violate the session's central exit-gate invariant and actively mislead users about
  Codex's actual skill-activation behavior, which TDD 06 explicitly warns does not have a guaranteed
  native event; the closed evidence-tier/ambiguous-ownership rules exist specifically to prevent this
  shortcut.
- **Attempt Session 06 as one continuous single-shot run again:** already failed twice on a transient
  corporate-VPN-related API error mid-session, discarding large amounts of completed work each time;
  sequential checkpointed stages bound the blast radius of any future transient failure to one stage's
  work, and each stage's first action is reading current repository state rather than assuming
  anything beyond what a prior stage actually committed to disk.
- **Skip a standalone `scripts/validate_codex.py` and rely only on `go test ./...` plus the existing
  `scripts/validate_adapter_sdk.py`:** would not independently verify the Codex-specific registry/lock
  digest binding or the exact evidence-tier/degradation-guarantee text the way every other session's
  standalone validator already does; a later stage still adds this validator, matching the established
  per-session pattern.

## Known gaps (explicitly recorded, not silently dropped)

1. **No Go implementation yet.** This stage delivers only the closed `contracts/codex/*` registries,
   `contracts/codex-policy-locks.yaml`, this ADR and the `contracts/README.md` update. The actual
   `internal/observability` Codex adapter registration, hook route wiring, rollout importer, inventory
   graph builder and reconciliation logic are deferred to the next checkpointed stage.
2. **No fixtures, canary or tests yet.** `tests/fixtures/session-06/*`, the
   `kansoku-canary-skill`/local-echo-MCP canary scenario, and the required test list
   (`skill-evidence-and-reconciliation.yaml required_tests`) are declared as a contract obligation
   here but have no concrete fixture bytes or Go/Python test code yet.
3. **No validator yet.** `scripts/validate_codex.py` is referenced by `contracts/README.md`'s
   check-command block as a placeholder; the script itself does not exist until a later stage writes
   it, mirroring `scripts/validate_adapter_sdk.py`'s structure.
4. **No live canary evidence yet.** `skill-evidence-and-reconciliation.yaml`'s `canary` section
   describes the intended fixture project and execution constraints, but no actual canary run,
   compatibility-matrix version entry, or live-evidence artifact exists yet; the exit gate's
   "backed by fixtures and live evidence" requirement is unmet until a later stage produces both.
5. **`codex.user_hook` has no real filesystem writer yet.** The installer target is declared in
   `contracts/codex/hooks-and-otel.yaml`, but — consistent with every prior session's installer
   scope — no code in this stage or any completed stage performs a real write to a user's Codex
   configuration; that remains gated behind `internal/installer`'s existing preview/consent/
   simulate-only machinery until a session explicitly promotes real writes, matching ADR 0002's
   support-governance gate.
6. **Historical transcript privacy trade-off is a policy decision, not yet an enforced code path.**
   The opt-in transient content-feature-extraction mode is fully specified in
   `contracts/codex/rollout-and-inventory.yaml`, but its in-memory-only/never-durable guarantee has no
   Go implementation to verify yet; a later stage's tests must include a prohibited-content canary
   proving the guarantee holds in code, not only in the contract text.
