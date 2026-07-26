# Handoff prompt — implement Kansoku Sessions 12–15

Ты работаешь в корне checkout Kansoku. Реализуй **последовательно и полностью** Sessions 12, 13,
14 и 15. Не превращай их в один огромный diff: каждая сессия должна дать
самостоятельный end-to-end результат, пройти свой exit gate, получить reconciliation report и
логические conventional commits до начала следующей.

## Обязательное начало

1. Полностью прочитай:

   - `AGENTS.md`;
   - `ARCHITECTURE.md`;
   - `README.md`;
   - `ROADMAP.md`;
   - Engineering Proposal и TDD текущей сессии;
   - связанные ранние EP/TDD, на которые указывает `ARCHITECTURE.md`;
   - `SOURCES.md`;
   - `SKILLS/README.md` и `SKILLS/db-quick-connect/SKILL.md`.

2. Проверь branch, `git status`, последние commits, migrations, fixtures и live Compose state.
   Существующие изменения и untracked-файлы принадлежат пользователю. Не удаляй, не переписывай и
   не включай их в commit без прямой связи с текущей сессией.
3. До изменений сформулируй exit gate как исполняемые тесты и зафиксируй baseline live-БД
   read-only запросами.
4. Перед изменением любого Codex/Claude/MCP/OTel interface заново проверь официальную документацию,
   локальную версию агента и обнови `SOURCES.md` с датой, версией и границей доказательности. Не
   программируй по памяти и не используй вторичный блог вместо primary docs.

## Непереговорные архитектурные правила

- Raw prompts, ответы, reasoning, source code, tool/MCP input/output, raw errors, environment,
  credentials, URLs/resource URIs и unredacted paths не попадают в durable storage, logs, queues,
  quarantine, incidents, diagnostics, API, export или backup.
- Unknown schema остаётся metadata-only. Не добавляй raw JSONB quarantine.
- `unsupported`, `not_observed`, `redacted`, `unknown` и numeric zero различаются.
- Не переписывай историческую telemetry и user config ради теста.
- Не добавляй brand branching в canonical core. Agent-specific parsing/protocol живёт в adapter или
  его evidence bridge; core работает с capabilities и safe assertions.
- Один logical fact может иметь несколько evidence lanes. OTel, hook и bridge не должны
  дублировать session/model/component/MCP counts.
- Любое изменение agent configuration использует существующий ChangePlan с preview и explicit
  confirmation. Sessions 12–15 по умолчанию read-only к агентам.
- Процент имеет numerator, denominator, exclusions, formula version и completeness.
- Если точный сигнал недоступен, показывай честное состояние; не синтезируй правдоподобный ноль.

## Debug scaffolding

Разрешено и желательно добавлять временные строительные леса, если они ускоряют доказательное
расследование:

- safe structured debug logs с закрытыми полями;
- read-only SQL reconciliation scripts;
- fixture inspectors;
- source/bridge health dumps;
- trace/correlation counters;
- browser/network diagnostics;
- deterministic canary runners.

Не удаляй такую диагностику, пока конкретная гипотеза не подтверждена или опровергнута на всех
затронутых слоях. После решения:

- полезную общую диагностику преврати в документированный bounded developer tool/test;
- одноразовый шум удали;
- проверь, что debug code не логирует запрещённые поля и не остаётся включённым без bounds;
- опиши cleanup в reconciliation report.

Не маскируй проблему catch-all fallback, hardcoded ID, ручной правкой БД или UI-only подстановкой.

## Live canary assets

Пользователь разрешил создать namespaced тестовые assets в локальном sibling checkout
`personal/yuzuru-skills`. Разреши его путь из текущего workspace во время выполнения, но не
записывай абсолютный host path в contracts, telemetry или отчёты. Перед записью проверь
существующую структуру и сохрани пользовательские файлы. Используй официальные форматы и доступные
`skill-creator` / `plugin-creator` инструкции, если они применимы.

Нужны дешёвые, детерминированные и неинвазивные компоненты:

- `kansoku-noop-skill`: понятный точный trigger, без сети и пользовательских файлов;
- `kansoku-noop-plugin`: валидный bundle, связывающий canary skill и/или MCP согласно реальному
  plugin contract; plugin сам не должен имитировать invocation, если upstream этого не делает;
- `kansoku-noop-mcp`: локальный stdio server без egress и credentials с tools:
  `nothing.success`, `nothing.error` (`isError=true`) и bounded delay/cancel behavior.

Все outputs маленькие, статичные и не содержат host state. Не считай временную близость skill/plugin/
MCP доказательством ownership: relation должна идти из inventory или native causal identity.

Для живого запуска используй bounded недорогого тестового агента: предпочитай локально доступный
`gpt-5.6-luna` с reasoning effort `medium`. Сначала проверь CLI/help/config и доступность модели, не
угадывай синтаксис. Если Luna недоступна, зафиксируй это как evidence gap и выбери явно названный
самый дешёвый поддержанный fallback; не переименовывай fallback evidence в Luna. Canary выполняется
только в disposable workspace, с лимитом turns/tokens/time, без доступа к пользовательскому repo.

## Общий цикл каждой сессии

1. Baseline:

   - schema/table/row counts;
   - последние relevant events/evidence;
   - open incidents/quarantine;
   - source/capability health;
   - API response и production UI state.

2. Contracts/tests first:

   - versioned contract and formula changes;
   - migrations and rollback/upgrade behavior;
   - sanitized fixtures;
   - negative privacy and unknown-schema cases;
   - replay/idempotency/reconciliation tests.

3. Implementation:

   - deterministic collectors/parsers before prose;
   - durable lineage and explicit value states;
   - API with query budgets/pagination;
   - UI drill-down and all view states;
   - targeted audit/incidents.

4. Verification:

   - unit, contract, replay, migration, privacy and relevant integration suites;
   - read-only SQL before/after reconciliation;
   - source-loss and contradiction test;
   - live canary;
   - browser/API check against production embedded bundle.

5. Rebuild/restart:

   - run the repository's current frontend build-and-embed workflow;
   - verify `web/dist` and `internal/webui/dist` agree;
   - rebuild the exact Docker image tag used by `deploy/.env`;
   - recreate/restart the Compose service, not merely the Vite dev server;
   - wait for healthy state and prove the running container serves the new asset/build;
   - re-run live API, UI and DB checks after restart.

6. Close:

   - update EP/TDD if reality changed and write an ADR for material decisions;
   - add `reports/session-NN-reconciliation.md` with measured evidence and residual risks;
   - check resource/retention/backup impact;
   - cleanup/promote debug scaffolding;
   - use scoped conventional commits;
   - do not begin the next session while the current exit gate is red or only fixture-proven where
     live evidence is required.

## Session 12 — Incident workbench and safe quarantine

Authority:

- `Engineering Proposal/12-incident-workbench-and-safe-quarantine.md`
- `Technical Design Document/12-incident-workbench-and-safe-quarantine.md`

Implement the unified incident read model, occurrence history, metadata-only structural manifests,
keyset pagination, Reliability tabs, incident profile, safe debug bundle/prompt, triage separated
from detector state and fresh-evidence recovery. Preserve both legacy incident projections during
migration and never force missing installation/source values.

Live proof:

- ingest a known-safe unknown schema;
- verify exactly one incident/fingerprint;
- replay it without inflation;
- send independent repeats and verify occurrence count;
- exercise multiple cursor pages;
- add/fix the parser with a sanitized fixture;
- ingest a new supported occurrence;
- run the targeted audit and verify recovery;
- scan all sinks for secret-like canary markers.

Recommended commit groups:

- `docs/contracts`;
- `feat(incidents): durable model and queries`;
- `feat(reliability): incident workbench`;
- `test(incidents): replay privacy and live recovery`;
- reconciliation docs/build artifact commit when required.

## Session 13 — Agent evidence bridge and model observatory

Authority:

- `Engineering Proposal/13-agent-evidence-and-model-observatory.md`
- `Technical Design Document/13-agent-evidence-and-model-observatory.md`

Implement a generic adapter-owned EvidenceBridge contract and conformance fake. Codex App Server is
the first optional, version-pinned bridge; it is not a Codex branch in core. Drop messages,
reasoning, arguments/results/errors/resources/paths before any sink. Expand Claude mappings only
where current official docs plus local version fixtures prove them.

Fix installation/session/model dimensional attribution and build useful agent profiles:
provider/surface/version/alias, source matrix, activity, per-model requests/tokens/cost/latency/
errors, components/tools/MCP and incidents. Keep `ain_*` as secondary opaque ID.

Live proof:

- two Codex sessions with different models under one installation;
- Claude/fake bridge coexistence;
- bridge + OTel duplicate one logical fact with multiple evidence;
- bridge outage degrades only bridge-owned capabilities;
- DB, API and UI counts reconcile exactly after container restart.

## Session 14 — Skill observatory

Authority:

- `Engineering Proposal/14-skill-observatory.md`
- `Technical Design Document/14-skill-observatory.md`

Replace the universal component funnel with Availability and Runtime evidence planes. Reserve
Optimization for Session 20. Do not implement the content viewer here; show file-tree metadata only.
Do not infer universal executed/succeeded. Outcome exists only with a terminal-contract ID.

Implement exact inventory identity resolution, durable assertions, formulas, Skills list/profile,
source/evidence matrix, cold semantics over complete exposure windows and unresolved/ambiguous
incidents.

Live proof uses `kansoku-noop-skill` and verifies installed/enabled/exposed/invoked/loaded only where
the active source proves each state. Add ambiguous child activity and source-loss negative cases.
DB evidence and UI populations must reconcile with no duplicated lane counts.

## Session 15 — MCP observatory

Authority:

- `Engineering Proposal/15-mcp-observatory.md`
- `Technical Design Document/15-mcp-observatory.md`

Implement three independent contours: configuration/inventory, protocol connection/capability and
call lifecycle. Extend existing graph/connections/tool calls; do not create a parallel MCP analytics
store. Replace free-text states with closed versioned assertions without rewriting historical
facts.

Build MCP list, server profile and tool profile. Distinguish `isError=true`, protocol error,
timeout, cancellation, denial, transport loss and incomplete terminal. Uptime uses only observed
intervals. No-call means “no observed demand” only when exposure is complete.

Live proof uses `kansoku-noop-mcp` for paginated list, list change, connect/disconnect/reconnect,
success, execution error, protocol error, timeout, cancel and deny. Pair starts/terminals 1:1,
verify p95 and denominators from SQL, break each source independently and run the full raw-content
canary with secret-like arguments/results/errors/URL/env/resource URI.

## Required final response

Report by session:

- commits;
- contracts/migrations;
- tests and exact commands;
- Docker image ID and healthy Compose state;
- live agent/model/version and UTC interval;
- DB reconciliation numbers;
- UI routes verified;
- debug scaffolding kept/promoted/removed;
- residual risks and explicitly unsupported fields.

Do not say “done” because the UI renders. Sessions 12–15 are complete only when their individual
exit gates, live proofs, privacy scans and post-restart production checks all pass.
