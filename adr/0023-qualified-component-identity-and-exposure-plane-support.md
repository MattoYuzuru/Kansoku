# ADR 0023 — Qualified component identity and adapter-declared exposure plane support

## Status

Proposed on 2026-08-01. Documentation and contract direction only; no code has changed yet. The
implementation handoff is `reports/2026-08-01-claude-skill-observatory-reconciliation.md`.

## Context

Claude Code skill telemetry reached the appliance and was persisted, but no Claude skill ever
appeared as invoked in the Skill observatory. Measured against the running local stack on
2026-08-01 with Claude Code `2.1.220`:

- `component_assertions` held 22 Claude `skill`/`invoked` rows, the most recent minutes old.
- Every one of them carried `identity_resolution='unresolved'` and `candidate_count=0`.
- `GET /api/v1/skills` returned 140 Claude rows, all `cold_state='not_observed'`, all
  `invoked_count=0`, with `exclusions.unresolved_identity=39`.

So the failure was never ingestion. It was identity resolution followed by exposure eligibility.
Four independent defects were isolated; each one alone is sufficient to produce a zero.

### A — the owner-plugin namespace was applied twice

`internal/observability/ingest.go` unconditionally prepends `plugin.name` to `skill.name` for
`component_kind == "skill"`. The live wire capture shows Claude Code already sends `skill.name`
fully qualified:

```text
event.name=skill_activated  skill.name="sre-agent:verification-strategy"
plugin.name="sre-agent"     skill.source="plugin"
invocation_trigger="claude-proactive"  marketplace.name="yuzuru-engineering"
```

The stored identities were therefore `sre-agent:sre-agent:verification-strategy`,
`t-skills-kotlin:t-skills-kotlin:kotlin-jpa-entity`, and so on. The resolver in
`internal/dataplatform/observability_handoff.go` compares against
`owner.declared_name || ':' || c.declared_name`, which such a value can never equal.

### A-bis — a non-vocabulary `skill.source` silently narrowed the resolver to zero

The resolver's final predicate is `AND ($5 = '' OR node.source_scope = $5)`, where `$5` is the
value carried through from `skill.source`. Only the attribute key is remapped; the value passes
through raw. Claude sends `skill.source="plugin"`, which is not a member of
`adaptersdk.SourceScope` (`system|user|repository|admin|marketplace|plugin_cache|transient_session`).
Live inventory stores every plugin-bundled Claude skill at `plugin_cache`.

Running the production CTE verbatim against the live database for
`t-skills-dev-workflow:jira-workflow` under installation `ain_c2e4a67af4327907cd7a66172c685713`:

| `$5` | candidates |
|---|---:|
| `''` | 1 |
| `'plugin'` | 0 |
| `'plugin_cache'` | 1 |

Fixing A without A-bis resolves nothing. The two are one change.

`plugin.scope="user-local"` and `enabled_via="user-install"` are non-vocabulary in the same way.
`enabled_via` only escapes the failure because it lands in the unconstrained `identity_source`
column, which no filter reads.

### B — Claude has no exposure surface, and its absence rendered as zero

`skill.cold_count/1` requires `enabled AND complete_exposure AND exposed_count > 0`. Exposure
windows are written to `component_observation_windows` with `plane='availability'`. Codex populates
them from the App Server `skills/list` response. Claude Code documents no equivalent: no event and
no snapshot reports the model-visible skill set. Live counts confirm it — 60 `exposed` assertions,
all Codex, none Claude.

Consequently every Claude skill would remain `not_observed` even with identity resolution fully
repaired. The appliance was reporting "we looked and saw nothing" where the truth was "there is no
surface to look at" — a distinction `AGENTS.md` requires to be preserved.

### C — dangling symlinks silently truncated the user-scope inventory

`~/.claude/skills` on the local host holds eight skills, seven of which are absolute symlinks into
a separate library directory. The compose bind maps only the link directory, so inside the
container the targets do not exist. Of eight user skills, one was inventoried.

`claudeadapter.Reconcile` nevertheless marked the snapshot `complete`, because it treats the
presence of any component node as completeness. A mis-mounted host therefore produced a confident,
silently truncated inventory — the exact failure mode `AGENTS.md` prohibits.

### D — permanently unresolvable classes

Built-in Claude Code skills (`dataviz`, `simplify`, `run`, `loop`, `claude-api`, `review`,
`security-review`, and the rest) are compiled into the executable. `find` over the installed npm
package returns no `SKILL.md`. No filesystem scan can ever inventory them, so their invocations
cannot reach `exact`.

Repository-scope skills are bound one project at a time, and Claude's OTel carries no project or
working-directory attribute, so several bound projects exposing the same skill name are
indistinguishable in principle.

## Decision

1. **The owner-plugin namespace is applied at most once.** Qualification becomes idempotent:
   `f(owner, f(owner, identity)) == f(owner, identity)`. This is safe because an inventory
   `declared_name` can never contain `:` — both adapters' `SKILL.md` frontmatter parsers restrict
   the name to `[A-Za-z0-9][A-Za-z0-9._-]{0,127}` — so an identity already carrying the owner
   prefix is always an upstream-qualified identity, never a bare name. Both the bare marketplace
   name and the `name@marketplace` form are recognised as an existing prefix.

2. **A source scope outside the closed vocabulary never narrows identity resolution.** Such a value
   is recorded as advisory evidence with state `unknown`, and opens one idempotent `info` incident
   keyed on `(adapter, component_kind, raw value)`. It is not coerced into a vocabulary member:
   mapping `"plugin"` to `plugin_cache` would recreate the same silent zero in a new place, because
   a plugin-bundled skill does not always live in the plugin cache.

3. **Exposure is an adapter-declared support state, not an inference.** The adapter manifest
   declares, per component kind, whether the exposed plane is `native`, `reconstructed` or
   `unsupported`, with a bounded reason. Claude declares `unsupported`; Codex declares `native`,
   which pins existing behaviour by contract rather than by absence. The declaration is persisted
   per installation and read by the data platform as data, so no agent name enters core branching.
   An undeclared plane is treated as supported, preserving today's behaviour for every other
   adapter.

4. **Cold eligibility gains a second path, and `unsupported` is never rendered as zero.**
   `skill.cold_count/1` becomes `/2`:

   ```text
   eligible = enabled AND (
        exposure_plane <> 'unsupported' AND exposed_count > 0 AND complete_exposure
     OR exposure_plane  = 'unsupported' AND inventory_snapshot.completeness = 'complete'
   )
   numerator = eligible AND invoked_count = 0
   ```

   A new per-row `exposure_state ∈ {observed, not_observed, unsupported}` carries the distinction to
   the UI. This mirrors `plugin.active_share/2`, which already gates eligibility on inventory
   completeness with no exposure window at all; skills were the outlier. The existing prohibition on
   treating a global enabled list as exposure is retained unchanged — the enabled list still does not
   become an exposure assertion; it only becomes an eligibility precondition when no exposure surface
   exists.

5. **An unreadable or unresolvable skill entry is a visible coverage gap.** The scanners classify
   each skipped entry into a closed vocabulary (`unresolvable_symlink`,
   `unreadable_component_manifest`, `truncated_component_manifest`,
   `unparseable_component_manifest`), carry the tally on the snapshot, and downgrade snapshot
   completeness to `partial`. This coupling is deliberate: it is what keeps decision 4 honest, since
   a mis-mounted host then fails the inventory-completeness path instead of silently reporting a
   confident cold count.

6. **Built-in skills remain unresolved with a typed, visible exclusion.** No curated catalogue is
   introduced. A hand-maintained list would drift with every Claude Code release and would assert
   installation state that the appliance cannot observe.

7. **`OTEL_EXPORTER_OTLP_HEADERS` is user-owned, never written by Kansoku, and no longer a preview
   hard failure.** The loopback OTLP ingress requires a bearer, and this variable is Claude Code's
   only header mechanism, so the operator must set it. The installer's forbidden-key list is split:
   keys Kansoku must never write, versus keys that additionally must not pre-exist. This one moves
   to the former and is surfaced as a disclosure. `OTEL_LOGS_EXPORTER_FILE` and `remote_endpoint`
   stay hard-forbidden.

## Consequences

- Claude plugin-bundled skill invocations reach `identity_resolution='exact'` and become countable.
  Decisions 1 and 2 must ship together; either alone still yields zero.
- `skill.cold_count/1 → /2` is a formula version change and a user-visible semantic change: a Claude
  skill can now be `cold` on inventory completeness alone. Neither `skill.cold_count` nor
  `skill_profile` is currently registered in `contracts/metrics.yaml`, which is a pre-existing
  governance gap recorded here as residual risk rather than closed in this change.
- Exclusions split so nothing is double counted: `partial_or_missing_exposure_window` applies only
  to supported planes, and a new `exposure_plane_unsupported_without_complete_inventory` covers the
  rest. `unresolved_identity` and `ambiguous_identity` are unchanged.
- The Codex path is unchanged by construction. Qualification is reached only from the OTLP and hook
  ingress boundaries; Codex OTel has no skill event, the Codex hook field builder sets no
  `component_*` key, and Codex skill evidence from the App Server bridge, the rollout watcher and
  inventory sets its qualified identity directly. Decision 3 additionally pins Codex exposure as
  `native` so a regression has something to assert against.
- Operators with symlinked skill libraries must bind the library root at its identical absolute path.
  Until they do, the affected installation reports `partial` inventory rather than a false complete.
- Two undeclared Claude events, `hook_registered` and `assistant_response`, currently quarantine on
  every session start. Declaring them removes standing incident noise.
- Per-skill cost and token attribution remains impossible for third-party skills: Claude stamps the
  sentinel `skill.name="third-party"` on `api_request` and on cost/token metrics. This is harmless
  today but contradicts the optimistic tone of the skill evidence contract and is recorded.

## Alternatives rejected

- **Derive exposure from `installed AND enabled`.** Contradicts the standing rule that a global
  enabled list is not exposure when the agent applies context limits or filtering, and converts
  `cold` — a user-facing claim — into a partly inferred number.
- **Implement the `kansoku-claude-hook` SessionStart helper as the exposure source.** Claude's
  SessionStart payload carries `session_id`, `transcript_path`, `cwd`, `hook_event_name` and
  `source` — no skill list. A helper enumerating the filesystem would be the rejected inference
  executed inside the agent process, and would additionally require the entire unimplemented helper
  binary plus a real installer writer.
- **Coerce `skill.source="plugin"` to `plugin_cache`.** Recreates the silent-zero failure for
  plugin-bundled skills that do not live in the plugin cache.
- **Curate a built-in skill catalogue.** Permanent drift obligation against a compiled skill set,
  asserting availability the appliance cannot observe.
- **Add a project or working-directory disambiguator for repository scope.** Claude's OTel carries
  no such attribute; `cwd` is deliberately dropped at the privacy boundary.
- **Branch on the agent name in the data platform.** Prohibited by the capability contract, and
  would not generalise to Gemini or Cursor, which have the same missing exposure surface.

## Residual risks

- Repository-scope skills sharing a declared name across several bound projects resolve to
  `ambiguous`, not `exact`. Unresolvable in principle with the attributes Claude emits.
- Built-in skills stay permanently unresolved by decision 6.
- `skill.cold_count/2` and `skill_profile/2` remain unregistered in `contracts/metrics.yaml`.
- Third-party skills carry no per-skill cost or token attribution.
- The identity-path bind required for symlinked libraries places a read-only path outside
  `/agent-state`. It must be the narrowest directory containing the targets, never `$HOME` or `/`.
