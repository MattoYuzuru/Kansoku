# Kansoku

Kansoku (観測, «наблюдение», «измерение явлений») — локальная платформа наблюдаемости
для AI-агентов разработчика. Она инвентаризирует установленные агенты, skills, plugins и MCP,
собирает metadata об их фактическом использовании, сверяет независимые источники событий и
показывает статистику в локальном dashboard без сохранения текстов пользовательских prompts.

## Статус

Session 04 / фаза 004 выполнена 2026-07-22: закрытые schema/rollups/query-contract/retention
registries и реальный PostgreSQL 18 `internal/dataplatform` заменяют Session 03 file-durability
spike — monthly partitioning, точные `percentile_cont` rollups, never-average late-data repair,
enforced query budgets и lineage-verified backup/restore находятся в репозитории.

Session 05 / фаза 005 выполнена 2026-07-22: закрытые manifest/capabilities/inventory-graph/
discovery-and-plans registries под `contracts/adapter-sdk/` и Go-пакет `internal/adaptersdk`
формализуют typed `Adapter` interface, permission-checked `HostView`, immutable inventory
entity-graph и reversible `ChangePlan`. Fake external-vocabulary adapter ("Loomwright") проходит
полный conformance suite (manifest/schema validation, discovery/inventory/normalization/
reconciliation fixtures, prohibited-content canary, unknown-version degrade, idempotent replay,
permission-bound tests) через те же `Registry`/`CapabilityMatrix`/`HostView` API, что и любой
будущий built-in адаптер — без единого agent-name branch в core коде за пределами регистрации
самого адаптера. `ChangePlan` не изобретает второй apply/rollback механизм: он переиспользует
`internal/installer`'s `Plan`/`Approval`/`SimulateApply`/`SimulateRollback`/`SimulateRemove`/
`PlanSHA256` через `PlanSHA256`-binding. Реализация использует только synthetic fixture
agent и не повышает ни один реальный адаптер выше Experimental: для публичного Supported/Beta
по-прежнему нужны version-bounded agent fixtures/evidence и два независимых human review.

Session 06 / фаза 006 выполнена 2026-07-22: закрытые manifest/discovery, hooks-and-otel,
rollout-and-inventory и skill-evidence-and-reconciliation registries под `contracts/codex/` и
Go-пакет `internal/codexadapter` формализуют первый реальный `internal/adaptersdk`-адаптер — Codex.
Четыре независимо деградирующих источника (`codex.hook`, `codex.otel`, `codex.rollout`,
`codex.inventory`) сверяются в едином reconciliation без единого silent-zero для всей сессии;
пятиуровневая skill-evidence модель никогда не повышает inferred/reconstructed доказательство до
native exact activation. Hook ingress переиспользует общий `/v1/hooks/{adapter}/{event}` route из
Session 03 без параллельного механизма; OTel план переиспользует существующий `codex.user_otel`
installer target из Session 02 verbatim, добавляя только новый `codex.user_hook` target. Canary
fixture project (`kansoku-canary-skill` + local echo MCP) проходит ожидаемую session/prompt/tool/MCP
цепочку на materialized fixture, а не на живом Codex CLI. Automated Session 01–06 gates проходят;
следующая реализационная сессия — Session 07 (Claude, Gemini и next agents). Codex остаётся
Experimental: публичный Supported/Beta по-прежнему требует version-bounded live evidence и два
независимых human review.

Session 07 / фаза 007 выполнена 2026-07-23 (**Claude-only scope** — Gemini CLI и Cursor probe из
исходного объединённого TDD/proposal 07 вынесены в отдельную **Session 07b** и в этой сессии не
затрагиваются): закрытые manifest/hooks-and-otel/transcript-and-inventory/skill-evidence-and-
reconciliation registries под `contracts/claude/` и Go-пакет `internal/claudeadapter` формализуют
второй реальный `internal/adaptersdk`-адаптер — Claude Code — с нулевым новым agent-name branch
внутри самого `internal/adaptersdk`. `claude.hook` реализует закрытый семикомпонентный event
vocabulary с path-псевдонимизацией и unconditional-strip правилом; `claude.otel` переиспользует
существующий `claude.user_otel` installer target из Session 02 verbatim и безусловно отбрасывает
`log.body`/`tool_payload`/`prompt_text`/`assistant_response_text`/`raw_api_body` независимо от
значений `OTEL_LOG_*` переменных, документированных как пользовательские переключатели. Hook ingress
использует тот же общий `/v1/hooks/{adapter}/{event}` route из Session 03, добавляя только новый
`claude` dispatch case рядом с уже существующими `fixture-agent`/`codex`. Семиуровневая
skill-evidence модель никогда не повышает `semantic_opportunity_classifier` выше `inferred` и не
разрешает ambiguous ownership повышать до `component.invoked`. Второй, по-другому устроенный
fictional fixture-agent ("Wayfinder", `contracts/cross-agent/`) и cross-agent invariant test
(`internal/crossagent`, единый логический сценарий `session -> prompt metadata -> skill activation
-> MCP tool call -> model tokens -> success` для Codex и Claude) доказывают, что SDK не требует
core-изменений ни для второго реального адаптера, ни для второго synthetic агента. Automated
Session 01–07 gates проходят; Claude остаётся Experimental — публичный Supported/Beta по-прежнему
требует version-bounded live evidence и два независимых human review. Известные пробелы (нет Go
JSONL importer для `claude.transcript`, нет live-CLI canary, `Audit` возвращает nil, `claude.user_hook`
без filesystem writer) зафиксированы в `reports/session-07-reconciliation.md`.

Session 08 / фаза 008 собрана и проверена 2026-07-24 на pinned PostgreSQL 18:
`internal/integrity` добавляет первый durable daily
audit из 11 timeout-bounded стадий поверх PostgreSQL session advisory lock, crash recovery,
source-aware idempotent check rows, structural-only drift fingerprints и targeted revalidation.
Пассивные endpoint/hook probes не меняют agent config; unknown schemas остаются видимыми и
metadata-only quarantined. Health API возвращает девять независимых `green/yellow/red/gray`
измерений без numeric score: gray — честный default, green требует свежего runtime pass, а
открытый incident не может быть скрыт более поздним pass другого source. Live-canary machinery
проверена только в bounded simulation: argv, disposable namespace, credentials+explicit consent,
turn/token/cost/duration budgets, cooldown, exact DAG и deterministic cleanup; реальный agent
process/аккаунт не запускался. Catalog содержит ровно 21 fault case с тремя явно разными уровнями
доказательств: 17 component classifiers без end-to-end SLO claim, 2 PostgreSQL-tagged measured
mutation integration (`corrupt_spool` и production synthetic handoff failure), и 2 runtime-required
сценария (`db_restart` и `failed_restore`). Обе mutation-интеграции прошли на pinned PostgreSQL 18
и измерили границу по фактическому durable `Incident.OpenedAt`; полный PostgreSQL-tagged suite,
data-platform runtime validator и standalone privacy canary также прошли. `db_restart`,
`failed_restore` и реальный provider canary по-прежнему не запускались, поэтому aggregate
runtime-claim для всех 21 fault case не делается. Финальный audit report
versioned и HMAC-signed внутри одной атомарной Stage 11
транзакции. Session 07b
по-прежнему остаётся отдельным backlog и не была возобновлена; следующая основная сессия — 09.

Проект по-прежнему строится десятью последовательными сессиями (плюс Session 07b), каждая из которых
имеет продуктовый
proposal и парный technical design document. Переход к следующей сессии допускается только после
выполнения exit gate предыдущей.

## Неподвижные принципы

1. **Local-first.** Runtime и данные находятся на устройстве пользователя; UI слушает loopback.
2. **Metadata by default.** Raw prompts, ответы, tool input/output и исходный код не сохраняются.
3. **No silent loss.** Kansoku не обещает недоказуемые «100%». Он измеряет покрытие каждого
   источника, обнаруживает разрывы и явно помечает неполные периоды.
4. **Adapter-first.** Codex и Claude — первые адаптеры, а не специальные случаи в доменной модели.
5. **Lineage everywhere.** Каждая метрика знает источник, версию адаптера, schema fingerprint,
   confidence и время наблюдения/приёма.
6. **Unknown is not zero.** Отсутствующие, неподдерживаемые и реальные нулевые значения различаются.
7. **Useful over performative.** Никакого сводного «developer productivity score»; только
   объяснимые показатели с явными числителем, знаменателем и ограничениями.
8. **Cheap to keep running.** Один `docker compose`, `restart: always`, bounded retention и
   предсказуемое потребление ресурсов.

## Документы

- [ROADMAP.md](ROADMAP.md) — десять последовательных сессий и зависимости.
- [Engineering Proposal](Engineering%20Proposal/README.md) — цели, варианты, trade-offs и
  продуктовые решения.
- [Technical Design Document](Technical%20Design%20Document/README.md) — контракты, схемы,
  алгоритмы, тесты и эксплуатационные требования.
- [SOURCES.md](SOURCES.md) — официальные интерфейсы агентов и инфраструктуры, на которых основан
  дизайн.
- [AGENTS.md](AGENTS.md) — правила работы будущих сессий над проектом.
- [Session 02 reconciliation](reports/session-02-reconciliation.md) — точный privacy/security exit
  gate, десять проверенных sink и остаточные риски.
- [Session 03 reconciliation](reports/session-03-reconciliation.md) — canonical ingestion,
  multi-lane replay, protocol/durability evidence и явные ограничения spike.
- [Privacy policy trust ADR](adr/0005-privacy-policy-lock-and-trust-root.md) — versioned semantic
  locks, bootstrap/trusted-history model и внешний review/CI root of trust.
- [Ingestion durability ADR](adr/0006-session-03-ingestion-and-durability-boundary.md) — bounded
  pre-PostgreSQL writer, pinned OTLP protobuf dependencies и gzip conformance gap.
- [Session 04 reconciliation](reports/session-04-reconciliation.md) — PostgreSQL schema/rollup/query-
  budget/retention/backup exit gate и явные downstream-gaps.
- [Data platform ADR](adr/0007-session-04-data-platform-and-metrics.md) — partitioning/rollup/query-
  budget/retention decisions и rejected alternatives поверх ADR 0001 baseline.
- [Session 05 reconciliation](reports/session-05-reconciliation.md) — adapter SDK/inventory exit gate,
  fake "Loomwright" conformance evidence и явные downstream-gaps.
- [Adapter SDK ADR](adr/0008-session-05-adapter-sdk-and-inventory.md) — Adapter interface/HostView/
  inventory-graph/ChangePlan decisions, `internal/installer` reuse и rejected alternatives.
- [Session 06 reconciliation](reports/session-06-reconciliation.md) — Codex adapter exit gate,
  four-source reconciliation/skill-evidence evidence, canary fixture chain и явные downstream-gaps.
- [Codex adapter ADR](adr/0009-session-06-codex-adapter.md) — sequential checkpointed build order,
  installer/ingress/sanitizer reuse decisions и rejected alternatives.
- [Session 07 reconciliation](reports/session-07-reconciliation.md) — Claude adapter (Claude-only
  scope) exit gate, second fixture-agent ("Wayfinder") и Codex+Claude cross-agent invariant evidence,
  явные downstream-gaps и explicit Gemini/Cursor scope exclusion (Session 07b).
- [Claude adapter and portability proof ADR](adr/0010-session-07-claude-adapter-and-portability-proof.md)
  — sequential checkpointed build order, Claude-only scope narrowing rationale, installer/ingress/
  sanitizer reuse decisions и rejected alternatives.
- [Session 08 reconciliation](reports/session-08-reconciliation.md) — 11-stage scheduler,
  PostgreSQL 18 lifecycle/mutation evidence, 17 component classifiers, 2 measured mutations,
  health/drift/live-canary simulation и 2 явно незакрытых runtime fault case.
- [Integrity and drift ADR](adr/0011-session-08-integrity-and-drift.md) — PostgreSQL advisory lock,
  structural fingerprints, incident/health composition и disabled-by-default live canary.

## Принятый технологический baseline

ADR 0001 принял измеренный baseline после Session 01 spikes:

- **collector/backend:** Go, один процесс и один статический UI bundle;
- **database:** PostgreSQL, без обязательной time-series extension на старте;
- **frontend:** TypeScript + React + Apache ECharts, без CDN и внешней аналитики;
- **protocols:** OTLP HTTP/gRPC, hook HTTP/Unix-friendly CLI ingestion, append-only JSONL import;
- **deployment:** Docker Compose с loopback-only ports, healthchecks и persistent volumes;
- **scheduling:** встроенный durable scheduler для daily integrity audit плюс запуск после старта;
- **distribution:** локальный CLI installer/configurator и versioned adapter packages.

Точные dependency/image ranges принадлежат реализационным сессиям и должны оставаться pinned;
замена baseline требует нового ADR и сопоставимых измерений.

## Что должно считаться использованием

Kansoku хранит разные стадии жизненного цикла компонента, а не один неоднозначный счётчик:

```text
installed -> enabled -> exposed -> invoked -> loaded -> executed -> succeeded
```

Для неявных активаций возможна дополнительная стадия `opportunity_detected`; она всегда имеет
вероятностный confidence и не смешивается с нативным событием агента.
