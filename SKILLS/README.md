# Debug skills

Local, repo-specific playbooks for the agent working on Kansoku. These are not the product's
own agent-adapter skills (see `contracts/claude/`, `contracts/codex/`) — they're notes-to-self
for whichever coding agent (Claude, Codex, ...) is debugging this repo, so it doesn't have to
re-derive the same connection details, ports, and file locations every session.

Each skill lives in its own folder with a `SKILL.md`. Referenced from `AGENTS.md`.

## Available skills

- [`db-quick-connect/`](db-quick-connect/SKILL.md) — fast, credential-safe access to the local
  Postgres instance (psql one-liners + a small Python wrapper). Points at where secrets and
  connection config already live instead of guessing or inventing new credential storage.

## Conventions for adding a new skill

- One folder per skill, `SKILLS/<name>/SKILL.md`.
- Never hardcode secrets, tokens, or passwords in a skill file or script. Point at the file/env
  var/command that already holds them (`deploy/secrets/*`, `KANSOKU_TEST_POSTGRES_DSN`, etc.),
  the same way `db-quick-connect` does.
- Prefer wrapping existing project commands (`docker compose exec`, `scripts/validate_*.py`)
  over introducing a new dependency or credential path.
