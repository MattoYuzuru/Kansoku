# Session 14 reconciliation — Skill observatory

Date: 2026-07-26

Status: **live exit gate green**

## Acceptance result

Session 14 replaces the false universal component funnel with independent availability and runtime
evidence. Migration 0009 stores installed, enabled, exposed, invoked, loaded, child and
terminal-contract assertions; complete observation windows; exact/unresolved/ambiguous identity;
and metadata-only file-tree summaries. Historical lifecycle facts are not rewritten.

`/api/v1/skills` and `/api/v1/skills/:id` publish numerator, denominator, exclusions,
completeness, freshness and formula version. There is no file-content route. Outcome is
`unsupported` unless an assertion references a registered terminal contract.

## Live Codex canary and exact reconciliation

The canary used the external namespaced `kansoku-noop-skill`, Codex CLI 0.145.0 and
`gpt-5.6-luna` with medium reasoning in an ephemeral run. The task selected the skill and returned
its deterministic harmless marker. User agent configuration was not changed; App Server
`extraRoots` made the canary visible only to that bounded process.

The live executable established the exact bridge mapping:

```text
skills/list       -> exposed + complete model-visible observation window
item/started      -> invoked(explicit) + loaded
turn/started      -> correlation only; items were empty in 0.145.0
timestamp         -> top-level emittedAtMs
```

The bridge accepted the actual JSONL frames through stdin, projected safe fields immediately and
discarded prompt/message content and paths. Production reconciliation for the no-op skill is:

```text
installed exact = 1
enabled exact   = 1
exposed exact   = 4 assertions / 4 complete windows
invoked exact   = 1
loaded exact    = 1
unique sessions = 1
mode            = explicit
outcome         = unsupported
```

Repeated exposure assertions have distinct source event identities and do not inflate the
installed/enabled/invoked/loaded populations. The PostgreSQL fixture separately proves exact,
zero-candidate and multi-candidate resolution, idempotency, ambiguous child exclusion, incomplete
exposure behavior and terminal-contract supported/unsupported cases.

## Production API and browser proof

For the live 24-hour range, the API and database reconcile:

```text
installed=15 enabled=15 exposed=15 invoked=1 loaded=1 cold=14
population=14/15 exclusions=0 completeness=complete
formula=skill.cold_count/1
```

Headless Chrome rendered the production Skills list and no-op detail route. Summary cards, linked
row, assertion timeline and source matrix match the API. The browser pass exposed two defects that
were fixed before acceptance: asynchronous KPIs animated from a fabricated zero, and the client
recognized payload `status` but not the API envelope's `completeness` field. The final image shows
the reconciled values with complete state.

Production image
`sha256:4dbe013d5fe912a3a1876a23d9b335db977b3598ab238fe637711b02bc820601`
started at `2026-07-26T15:58:45Z` and is healthy. Migration 0009 was applied at
`2026-07-26T15:30:32Z`. The measured database size was 28,595,903 bytes; Session 14 retains only
bounded assertions/windows and metadata summaries under the existing retention/backup boundary.

## Privacy and recovery proof

The native privacy canary scanned ten accepted and ten rejection sinks:

```text
content canary matches = 0
secret-format matches  = 0
backup exact bytes     = true
```

Native backup `backup_89a43ea2327a358fb40510b8d54d6eeb` captured 92 component assertions,
58 observation windows, 17 component installations, 5,806 facts and 5,806 evidence rows with
schema migration 0009. Two independent restore-verification runs returned `status=pass` with exact
table counts and cleaned their temporary databases.

The first restore attempt found a pre-existing verifier error: it rejected a valid rollup whenever
`unknown_count > event_count`, although these fields are unknown and known counts and the formal
denominator is their sum. The verifier now rejects empty formula lineage or negative counts
instead. A PostgreSQL regression fixture with `event_count=0, unknown_count=4` proves that an
unknown-dominant but valid population restores successfully.

## Verification

```text
python3 scripts/validate_component_evidence.py
python3 scripts/validate_codex.py
python3 scripts/validate_contracts.py
python3 scripts/validate_data_platform.py --runtime-only --json
python3 -m unittest discover -s tests -p 'test_*.py'  # 162 pass
python3 scripts/run_privacy_canary.py
go vet ./...
go test ./...
npm --prefix web run typecheck
web/scripts/build-and-embed.sh
```

All listed gates pass, including the PostgreSQL-tagged runtime suite, live production API/browser
checks, native backup and repeated restore.

## Residual risks and explicit unsupported states

- Codex App Server is experimental. Only the reviewed 0.145.0 schema subset is native exact
  evidence; future/unknown frames quarantine visibly.
- Claude Code's documented native skill event was not live-exercised in this gate. Missing source
  coverage changes completeness and never fabricates zero usage.
- Child activity is counted only with unique ownership; ambiguous relations remain excluded.
- Skill terminal outcome, optimization eligibility and missed opportunity are unsupported without
  their later versioned contracts.
- File content remains unavailable by design until Session 18.
- `npm audit` reports one high-severity development dependency advisory in the locked frontend
  tree. No forced dependency rewrite was made inside Session 14; it remains a release dependency
  maintenance item.
