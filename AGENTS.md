# Working on Kansoku

Kansoku is built over multiple deliberate sessions. Treat the documents in this repository as the
acceptance contract, not as disposable planning notes.

## Before changing anything

1. Read `ARCHITECTURE.md` first — it maps every module (`internal/*`, `cmd/*`, `web/`,
   `contracts/*`) to its purpose and governing contracts, and tells you which of the documents
   below to open for the specific area you're touching, so you don't have to re-read all of them.
   Then read `README.md`, `ROADMAP.md`, the selected Engineering Proposal, and its paired Technical
   Design Document for that area.
2. Inspect the current repository state, branch, recent decisions, migrations, fixtures, and tests.
3. Re-check official documentation for every agent interface being changed. Record the retrieval
   date and relevant version in `SOURCES.md`.
4. Preserve unrelated work and never rewrite user data, agent configuration, or historical
   telemetry to make a test pass.

## Core contracts

- Never persist raw prompts, model responses, source code, tool input/output, environment values,
  credentials, or unredacted filesystem paths by default.
- Every normalized event carries source lineage, adapter/schema versions, confidence, and an
  idempotency key.
- A parser may quarantine unknown data; it may not silently drop or coerce it.
- `unsupported`, `not_observed`, `redacted`, `unknown`, and numeric zero are separate states.
- Installation/configuration writes require an explicit preview and confirmation. Runtime
  collection must remain read-only toward agents.
- External network egress is disabled by default. Metadata/changelog refresh is allowlisted and
  must never include local telemetry.
- Dashboard percentages always expose numerator, denominator, exclusions, and completeness.
- New adapters implement the shared capability contract; do not add agent names to core domain
  branching when a capability check is sufficient.

## Session workflow

1. Confirm the session exit gate and convert it into tests.
2. Build deterministic collectors/parsers before agent-facing setup prose.
3. Add sanitized fixtures for every supported source/version.
4. Run unit, contract, replay, migration, privacy, and relevant live-canary tests.
5. Review resource overhead and data retention impact.
6. Update the paired proposal/TDD when reality changes; add an ADR for material decisions.
7. End with a reconciliation report and explicit residual risks.

## Definition of done

A feature is not complete when the UI renders. It is complete when ingestion is idempotent,
unknown schemas fail visibly, privacy tests pass, metrics reconcile, gaps are shown in the UI,
backup/restore behavior is known, and the daily audit can verify the feature.

## Debug skills

`SKILLS/` holds local, repo-specific debugging playbooks — see `SKILLS/README.md` for the index.
Check there before re-deriving connection details, ports, or credential locations from scratch.
In particular, `SKILLS/db-quick-connect/SKILL.md` covers fast, credential-safe access to the
local Postgres instance (schemas, tables, ad-hoc queries).

