# ADR 0010: Session 07 Claude adapter and portability proof

- Status: accepted
- Date: 2026-07-23
- Owners: Session 07 core architecture

## Context

Session 06 registered Codex as the first genuinely real `internal/adaptersdk` adapter and proved that
four independently-capability-scoped sources (`codex.hook`, `codex.otel`, `codex.rollout`,
`codex.inventory`) can be reconciled without ever collapsing partial, ambiguous or inferred evidence
into a false native exact activation count. Session 07 must register the second real adapter — Claude
Code — against that exact same registry, and its exit gate is deliberately different in shape from
Session 06's: where Codex's documented telemetry may not expose a dedicated skill-activation event at
all, Claude Code's documentation (`SOURCES.md`'s "Anthropic Claude Code" section, retrieved and
re-checked 2026-07-21) describes a native `Skill` tool call, hooks with transcript path and tool
lifecycle, and OTel attributes for `skill.name`, `plugin.name`, `agent.name`, tokens and cost. This
richer native signal set is precisely why Claude's exit gate must be stricter about privacy: the same
documentation records that Claude's detailed telemetry gates (`OTEL_LOG_USER_PROMPTS`,
`OTEL_LOG_ASSISTANT_RESPONSES`, `OTEL_LOG_TOOL_DETAILS`, `OTEL_LOG_TOOL_CONTENT`,
`OTEL_LOG_RAW_API_BODIES`) can, if a user enables them outside Kansoku's control, expose prompt text,
assistant response text, tool input/output and raw API bodies. Kansoku's proposed installer plan keeps
every one of those gates off, but the ingress boundary must strip these fields unconditionally
regardless of what the upstream agent's own settings report, because a user-toggled upstream setting is
not a control Kansoku can rely on as its trust boundary.

The original TDD 07 / Engineering Proposal 07 documents group three agents together: Claude Code,
Gemini CLI and a Cursor probe. This session's scope is deliberately narrowed to Claude Code plus a
second fictional fixture-agent proving continued core independence; Gemini CLI and the Cursor probe are
deferred to a separate **Session 07b**. This narrowing exists so that Claude adapter evidence — the
agent with the richest native skill signal and the most direct reuse of Session 02's already-declared
`claude.user_otel` installer target — lands sooner, without making Claude's delivery wait on two more
agents whose OTel/hook vocabularies (Gemini's `gemini_cli.*` OTel catalog and standard GenAI attributes,
Cursor's `workspace_roots`/`transcript_path`/prompt-bearing hook payloads) still need their own
independent research-and-fixture cycle. ROADMAP.md's dependency graph and session table are updated in
this same stage to record 07b as a new, explicitly sequenced session that does not renumber Sessions
08-10.

This session reuses, rather than reinvents, four prior boundaries:

- `internal/privacy`'s `SafeRecord`/`SafeError` sanitizer (Session 02) remains the only trust boundary
  any Claude source may cross; no second sanitizer is declared.
- `contracts/privacy/installer.yaml`'s existing `claude.user_otel` target (Session 02) — and
  `internal/installer/protocol.go`'s existing `BuildClaudePlan`, already wired to exactly that target —
  are reused verbatim for the OTel plan; this session only adds a new `claude.user_hook` target for the
  observer hook, because no hook target existed yet.
- `contracts/observability/ingress.yaml`'s existing generic `hook_http` route template
  (`/v1/hooks/{adapter}/{event}`, Session 03) is reused verbatim by substituting `claude` as the
  adapter path segment; `internal/observability/routes.go`'s `hookAdapterHandler` already has cases for
  the reserved Session 03 conformance identity `fixture-agent` and for Session 06's `codex`, and Session
  07 must add a third case for Claude through that same generic mechanism, never a parallel one, and
  never colliding with or reusing the `fixture-agent` literal adapter id.
- `internal/adaptersdk`'s `Registry`/`HostView`/inventory/`ChangePlan` machinery (Session 05) and its
  zero-agent-name-branch-in-core invariant, already proven once by the fictional "Loomwright" adapter,
  must hold a second time for Claude and a third time for this session's new fictional fixture-agent.

Because two prior single-shot attempts at Session 06 crashed partway through on a transient
corporate-VPN-related API error, and that sequential-checkpointed-stage pattern worked reliably, Session
07 is deliberately built the same way: contracts/ADR skeleton, Go discovery/hook/OTel core, inventory/
reconciliation/tests, second fixture-agent plus cross-agent conformance, validators/reports/doc-sync,
final full-suite verification — each stage beginning by reading current repository state rather than
assuming a clean slate, and treating any inconsistency it finds as a defect to fix or regenerate
correctly, never as scaffolding to build on top of uncritically.

## Decision

1. The authoritative Claude contract is the closed registry set in
   `contracts/claude/{manifest,hooks-and-otel,transcript-and-inventory,skill-evidence-and-reconciliation}.yaml`,
   locked by `contracts/claude-policy-locks.yaml` using the identical append-only semantic-digest
   mechanism already used for `contracts/privacy`, `contracts/observability`, `contracts/data-platform`,
   `contracts/adapter-sdk` and `contracts/codex`. No prior trusted lock entry in any of those five
   earlier files is edited; Session 07 only appends new lock entries scoped to the four new Claude
   registry files.
2. `contracts/claude/manifest.yaml` declares Claude's `adapter_id`, agent detection (`claude`
   executable, no dedicated state-root env var — Claude Code documents settings file locations rather
   than a `CLAUDE_HOME`-shaped variable, so discovery resolves `claude_user_settings`/
   `claude_project_settings`/`claude_managed_settings` directly — `cli`/`ide_extension`/`app`
   surfaces), the installation-discovery step sequence, the documented version-gate notes carried over
   from `SOURCES.md` (`2.1.214`, `2.1.216`, `2.1.193` behavior gates and the locally-observed,
   not-yet-fixture-verified `2.1.197` runtime), and reuses `internal/adaptersdk`'s existing
   `MaxManifestConfigEntries`/`MaxManifestConfigDepth`/`MaxManifestConfigString` parse bounds and the
   closed `capability_ids` vocabulary from `contracts/adapter-sdk/capabilities.yaml` verbatim; it
   declares no new capability id and no new parse limit.
3. `contracts/claude/hooks-and-otel.yaml` specifies `claude.hook` (supported events `SessionStart`,
   `UserPromptSubmit`, `PreToolUse`, `PostToolUse`, `SubagentStart`, `SubagentStop`, `Stop`, each
   independently version-manifested), the hook helper's bounded-stdin/in-memory-feature/path-
   pseudonymization/allowlisted-field/short-timeout/never-block/bounded-0600-spool contract, a new
   `claude.user_hook` installer target scoped to user-level config only by default, and the same
   hook trust/enabled-state audit-only rule Session 06 established. It specifies `claude.otel` by
   reusing the existing `claude.user_otel` installer target and
   `contracts/observability/ingress.yaml`'s OTLP-safe-attribute allowlist verbatim, mapping documented
   `skill.name`/`plugin.name`/`agent.name`/token/cost attributes and tool/session events onto canonical
   event types, and states an explicit unconditional-strip rule: prompt text, assistant response text,
   tool input/output and raw API bodies are rejected at ingress regardless of what
   `OTEL_LOG_USER_PROMPTS`/`OTEL_LOG_ASSISTANT_RESPONSES`/`OTEL_LOG_TOOL_DETAILS`/
   `OTEL_LOG_TOOL_CONTENT`/`OTEL_LOG_RAW_API_BODIES` report upstream.
4. `contracts/claude/transcript-and-inventory.yaml` specifies `claude.transcript` as a checkpointed
   JSONL importer (file identity, inode/file-id where available, byte offset, first/last record
   fingerprint, rotation/truncation detection) resolved from the documented local project/session
   transcript directory only — never a speculative home-directory scan and never an undocumented
   `CLAUDE_HOME`-shaped variable — that never writes to the Claude Code session tree, defaults to
   safe-structured-fields-only historical parsing with an explicit opt-in for transient in-memory
   content feature extraction and an explicit user disable switch, and maps native `Skill` tool calls to
   explicit/implicit mode only when the transcript's own field distinguishes the two. It specifies
   `claude.inventory` by reusing `contracts/adapter-sdk/inventory-graph.yaml`'s closed `source_scopes`/
   `node_kinds`/`edge_kinds`/cache-separation/identity-merge vocabulary verbatim, preserving the
   active-vs-cached distinction and the plugin-to-bundled-component (`bundles` edge) relationship
   explicitly, and bounding repository scans to known active projects or an explicit user target.
5. `contracts/claude/skill-evidence-and-reconciliation.yaml` is the closed evidence model: seven
   evidence kinds (native explicit `Skill` call, native implicit `Skill` call, OTel `skill.name`
   attribution, `SKILL.md` load evidence, plugin/MCP declared use, uniquely owned helper execution,
   semantic opportunity classifier), each bound to one canonical event type and one evidence tier from
   `contracts/observability/lifecycles.yaml`'s existing tier vocabulary, an explicit rule that
   explicit-vs-implicit mode is recorded only when Claude's own native field distinguishes it, the
   carried-over "no false exact count" and ambiguous-ownership rules, the native-exact-activation
   prohibition, and a new explicit unsupported-rendering rule (a field or capability Claude does not
   document or expose natively renders as `unsupported`, never a misleading zero). It also carries the
   full source-to-canonical mapping table, the eight per-session reconciliation comparisons (including
   plugin-ownership-vs-bundled-component-inventory and the documented multi-subagent token/cost
   double-attribution retention rule), the required test list, and a structured `exit_gate` object,
   including an explicit `support_label_governance` clause: Claude's exact support label (Production or
   Beta) is backed only by the fixture/test evidence this session and its follow-on stages actually
   produce, never asserted ahead of it.
6. Every Claude hook route is declared as a substitution into the existing generic
   `contracts/observability/ingress.yaml` `hook_http` protocol route template
   (`/v1/hooks/{adapter}/{event}` with `adapter=claude`), not a new protocol entry; a later
   implementation stage must add a `claude` case to `internal/observability/routes.go`'s
   `hookAdapterHandler` alongside its existing `fixture-agent` and `codex` cases, never a parallel HTTP
   server, a second auth mechanism, or a collision with the reserved `fixture-agent` literal adapter id.
7. Every source (`claude.hook`, `claude.otel`, `claude.transcript`, `claude.inventory`) is independently
   capability-scoped: the registries state explicitly that disabling or breaking one source degrades
   only the capability ids that source backs, never the others, and never collapses to a
   plausible-looking zero for the whole session, mirroring Session 06's `independent_capability_degradation`
   field so a later validator can check for the exact guarantee text.
8. The second fictional fixture-agent and the Codex+Claude cross-agent invariant scenario live in a new,
   independently locked `contracts/cross-agent/{second-fixture-agent,invariant-scenario}.yaml` pair
   locked by `contracts/cross-agent-policy-locks.yaml`, rather than as an addendum inside
   `contracts/adapter-sdk/*`. This keeps Session 05's four already-trusted `contracts/adapter-sdk/*`
   lock entries completely untouched (no coherent "addendum" edit risk to a prior trusted entry) while
   still giving the second-fixture-agent proof and the cross-agent assertions their own closed,
   independently versioned home. The second fixture-agent is named "Wayfinder" (`adapter_id: wayfinder`)
   and is deliberately shaped differently from both the real agents and Session 05's "Loomwright":
   zero OTel source (only a versioned local event file), a "recipe" component vocabulary, non-UUID
   session identifiers, an unsupported (never zero-populated) token capability, and exactly one
   deliberately unknown event schema (`recipe.mystery`) that must be quarantined rather than silently
   dropped or guessed into a known canonical type. The cross-agent invariant scenario represents the
   logical chain `session -> prompt metadata -> skill activation -> MCP tool call -> model tokens ->
   success` as a stage-to-capability-id mapping table and requires that its assertions bind only to
   capability ids and canonical event types, never to an agent-id string comparison in the assertion
   itself (fixture selection is the only place an agent id appears).
9. This session is executed as sequential checkpointed stages rather than one continuous run, following
   the same pattern that worked reliably for Session 06 after two prior single-shot attempts crashed on
   a transient corporate-VPN-related API error. Each stage begins by reading current repository state
   (`git status`, existing `contracts/claude/*`, existing `internal/observability`/`internal/adaptersdk`
   code) rather than assuming a clean slate, and treats any inconsistency it finds as a defect to fix or
   regenerate correctly, never as scaffolding to build on top of uncritically.
10. `ROADMAP.md` is updated in this same stage to split the original combined "Claude, Gemini and next
    agents" Session 07 entry into this session's narrower Claude-plus-portability-proof scope and a new
    Session 07b entry for Gemini/Cursor, and to update the dependency graph
    (`06 -> 07 -> 07b -> 08 -> ...`) accordingly, without renumbering Sessions 08-10.

## Consequences

- Session 07b (Gemini CLI adapter and Cursor probe) becomes the third and fourth real (or experimental,
  for Cursor) `Adapter` registrations against the same `Registry`; if either needs a
  `claude.user_otel`/`claude.user_hook`-shaped installer target of its own, it must add a new target
  entry to `contracts/privacy/installer.yaml`'s `targets` list the same way this session added
  `claude.user_hook`, never edit or reinterpret Claude's or Codex's entries. Gemini's own
  `gemini.user_otel` target already exists in `contracts/privacy/installer.yaml` from Session 02's
  design and is likewise reused verbatim, not redefined, when 07b begins.
- `internal/observability/routes.go` must grow real `/v1/hooks/claude/{event}` handlers for every event
  `contracts/claude/hooks-and-otel.yaml` declares, reusing the same `Ingestor`/`OTLPReceiver` plumbing
  the `fixture-agent` and `codex` example routes already demonstrate; a future session adding a Gemini
  or Cursor hook route that instead stands up a second HTTP mux or a second bearer-auth mechanism would
  be a regression against this ADR's decision (and against ADR 0009's identical decision for Codex), not
  a normal extension.
- The `claude.user_hook` installer target's `ChangePlan` construction must still go through
  `internal/adaptersdk`'s existing `BuildChangePlan`/`installer.Plan`/`PlanSHA256` machinery exactly as
  ADR 0008 and ADR 0009 require for any write-capable adapter; Session 07 introduces no second apply/
  rollback path.
- The Wayfinder fixture-agent and the cross-agent invariant scenario recorded here are the authoritative
  text later Go code, the validator, and the cross-agent test must all match: a future coherent contract
  edit that quietly removes the "assertions bind only to capability ids, never to agent-id string
  comparison" rule would be exactly the kind of drift this ADR's lock mechanism exists to catch.
- Because this is a staged build, later stages inherit an explicit obligation to keep
  `contracts/claude/*`, `contracts/cross-agent/*`, both new policy-lock files, this ADR, and eventually
  `internal/observability`'s Claude registration and `internal/adaptersdk`'s Wayfinder fixture-agent
  mutually consistent at every checkpoint, not only at the final stage.
- Session 07b inherits this ADR's scope-narrowing rationale as precedent: it must not retroactively
  weaken Claude's exit gate or reopen `contracts/claude/*`'s locked entries in order to make room for
  Gemini/Cursor-specific concerns; any Gemini/Cursor-specific need becomes its own new registry file and
  its own new lock entries, following this session's `contracts/cross-agent/` precedent for shared
  cross-agent material if that pattern is still the best fit at that time.

## Rejected alternatives

- **Keep Session 07 as the original combined Claude+Gemini+Cursor scope:** would make Claude's
  evidence — the agent with the richest native skill signal and the most direct reuse of an
  already-declared installer target — wait on two more agents' independent OTel/hook research and
  fixture cycles for no architectural reason; the portability proof this session's exit gate actually
  requires (core independence across two *real* differently-shaped agents plus one more fixture-agent)
  does not need three real agents to hold.
- **Fold the second fixture-agent and cross-agent invariant material into `contracts/adapter-sdk/*` as
  an in-place addendum:** would risk editing or reinterpreting one of Session 05's four already-trusted
  lock entries, violating `contracts/README.md`'s append-only policy-lock rule; a new, separately locked
  `contracts/cross-agent/*` pair achieves the same closed/consistent guarantee without that risk.
- **Redefine `claude.user_otel` or `BuildClaudePlan` inside a new `contracts/claude/` target instead of
  reusing the existing Session 02 entries verbatim:** would create two divergent descriptions of the
  same OTel plan and violate the append-only-trust-boundary spirit of `contracts/README.md`, exactly as
  ADR 0009 already rejected for Codex's `codex.user_otel`.
- **Give Claude its own `/v1/claude/hooks/{event}`-shaped ingress route instead of substituting into the
  existing generic `hook_http` template:** would duplicate Session 03's already-reviewed ingress/
  durability/auth contract for no reason, exactly as ADR 0009 already rejected for Codex.
- **Rely on Claude's detailed-telemetry settings being off as the sole privacy control:** a user can
  enable `OTEL_LOG_USER_PROMPTS`/`OTEL_LOG_ASSISTANT_RESPONSES`/`OTEL_LOG_TOOL_DETAILS`/
  `OTEL_LOG_TOOL_CONTENT`/`OTEL_LOG_RAW_API_BODIES` outside Kansoku's control; the unconditional-strip
  rule at ingress is the actual trust boundary, with Kansoku's proposed plan keeping the flags off only
  as defense in depth.
- **Attempt Session 07 as one continuous single-shot run:** the sequential-checkpointed-stage pattern
  already proved necessary for Session 06 after two single-shot crashes; there is no new reason to
  believe Session 07 is less exposed to the same transient corporate-VPN-related API failure mode.

## Known gaps (explicitly recorded, not silently dropped)

1. **No Go implementation yet.** This stage delivers only the closed `contracts/claude/*` and
   `contracts/cross-agent/*` registries, both new policy-lock files, this ADR, and the
   `contracts/README.md`/`ROADMAP.md` updates. The actual `internal/observability` Claude adapter
   registration, hook route wiring, transcript importer, inventory graph builder, reconciliation logic,
   Wayfinder fixture-agent implementation and cross-agent conformance test are deferred to later
   checkpointed stages.
2. **No fixtures, canary or tests yet.** `tests/fixtures/session-07/*`, sanitized Claude hook/OTel/
   transcript golden fixtures, the Wayfinder fixture-agent's event file, and the required test list
   (`skill-evidence-and-reconciliation.yaml required_tests`) are declared as a contract obligation here
   but have no concrete fixture bytes or Go/Python test code yet.
3. **No validator yet.** `scripts/validate_claude.py` is referenced by `contracts/README.md`'s
   check-command block as a placeholder; the script itself does not exist until a later stage writes
   it, mirroring `scripts/validate_codex.py`'s structure. There is likewise no
   `scripts/validate_cross_agent.py` yet — a later stage must decide whether the cross-agent registries
   are validated by their own script or folded into `scripts/validate_claude.py`'s cross-checks.
4. **No live canary evidence yet.** Unlike Session 06, this ADR does not yet commit to a specific live
   Claude Code canary fixture project; `skill-evidence-and-reconciliation.yaml`'s exit gate deliberately
   states fixtures are the minimum requirement and live evidence is recorded separately when available,
   reflecting that Claude's exact support label is not yet backed by any produced evidence.
5. **`claude.user_hook` has no real filesystem writer yet.** The installer target is declared in
   `contracts/claude/hooks-and-otel.yaml`, but — consistent with every prior session's installer
   scope — no code in this stage performs a real write to a user's Claude Code configuration; that
   remains gated behind `internal/installer`'s existing preview/consent/simulate-only machinery until a
   session explicitly promotes real writes, matching ADR 0002's support-governance gate.
6. **Detailed-telemetry unconditional-strip guarantee has no Go implementation to verify yet.** The rule
   is fully specified in `contracts/claude/hooks-and-otel.yaml`, but a later stage's tests must include a
   prohibited-content canary with detailed upstream telemetry settings enabled in the fixture, proving
   the guarantee holds in code, not only in the contract text.
7. **Gemini CLI and Cursor probe remain entirely out of scope.** No `contracts/gemini/`,
   `contracts/cursor/`, or corresponding lock files exist after this stage or any stage of this session;
   they are deferred to Session 07b in full, including their own research/fixture/ADR cycle. An earlier,
   now-abandoned attempt at this session briefly created Gemini/Cursor-flavored contract and ADR
   material before the scope was narrowed; that material was cleanly reverted before this session's
   current run began, and none of it is reintroduced here.
8. **`internal/adaptersdk`'s zero-agent-name-branch invariant is asserted here only as a contract
   requirement, not yet as verified code.** A later stage's Go implementation and tests must confirm
   that registering Claude and Wayfinder required no new `if agentID == ...` branch inside
   `internal/adaptersdk`'s own core files, the same way Session 06's implementation stage confirmed it
   for Codex.
