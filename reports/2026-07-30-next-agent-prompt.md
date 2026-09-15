# Промпт для следующего агента

Продолжай работу над Kansoku из корня уже открытого репозитория.

Пользователь уже провёл со мной research-сессию по 13 проблемам. Не начинай исследование заново и
не проси пересказать жалобы. Сначала прочитай:

1. `AGENTS.md`;
2. `ARCHITECTURE.md`, затем `README.md` и `ROADMAP.md`;
3. `reports/2026-07-30-defect-research-and-priority-plan.md`;
4. `reports/2026-07-30-defect-inventory.json`;
5. `reports/artifacts/2026-07-30/live-evidence.json`;
6. `reports/artifacts/2026-07-30/browser-evidence.json`;
7. выбранные paired документы:
   - `Engineering Proposal/10-dashboard-hardening-and-evolution.md` и TDD 10;
   - EP/TDD 12 incident workbench;
   - EP/TDD 13 agent/model observatory;
   - EP/TDD 14 skill observatory;
   - EP/TDD 16 plugin observatory;
   - EP/TDD 17 system self-observability;
   - EP/TDD 18 design system/content access.

Базовый P0/P1-набор уже зафиксирован коммитом
`d35019d feat(observability): make telemetry durable and identity-aware`. Research-пакет находится
в следующем за ним docs-коммите. Не переписывай и не squash эти коммиты. Не push без отдельной
просьбы пользователя.

## Цель

Реализовать backlog из research-пакета небольшими проверяемыми коммитами. Пользователь попросил
начать с декоративных исправлений, затем чередовать критические и обычные. Приоритет P0 из отчёта
остаётся приоритетом риска; декоративная Wave A — только порядок входа в работу.

## Порядок

### Wave A — декоративные исправления

Делай отдельными логическими коммитами, не смешивая формулы со стилями:

1. R01 — versioned per-page range preference:
   - хранить preset, не stale from/to;
   - stable logical page keys;
   - корректный fallback при corrupt/disabled localStorage;
   - browser test: Activity=7d, Models=1y, route change + reload.
2. R05-format — общий formatter для p50/p90/p95/p99 с максимумом 2 знака:
   - не менять percentile/error/latency формулы в этом коммите;
   - сохранить raw value в tooltip/export.
3. R09 — glossary target pulse:
   - 2–3 мягких deterministic pulse за ~5 секунд;
   - replay на hashchange;
   - no layout shift;
   - reduced-motion = static highlight.
4. R12 — database size:
   - readable MiB/GiB, максимум 2 знака;
   - exact bytes остаются доступны;
   - общий KpiCard containment для длинных значений.
5. R13 — theme-derived sidebar hover/selected:
   - вычислять одинаково в pre-paint script и ThemeProvider;
   - проверить dark/light/presets/custom и WCAG AA.

После Wave A прогони frontend/browser matrix и сделай reconciliation update. Не останавливайся,
если безопасно перейти к следующей wave.

### Wave B — P0 frontend containment

R02 и navigation-часть R11:

- backend API коллекции всегда `[]`, не `null`;
- defensive normalization nullable legacy response;
- аудит Skill/Plugin profile collections;
- route/root error boundary с сохранением shell, retry и back;
- query error должен быть видимым, а не вечным Loading;
- внутренние Reliability ссылки через Wouter, без document reload;
- regression browser tests для SPA/direct/back/refresh и malformed-null fixture.

### Wave C — P0 Agent profiles

R06:

- воспроизвести 503 через сохранённый browser harness;
- сохранить контракт snapshot/consistency;
- оптимизировать/объединить запросы и доказать `agent_profile_range <= 200 ms` либо подготовить
  evidence-backed изменение контракта;
- исправить `agent_id`, ошибочно попадающий в `adapter_id`;
- explicit installation class/provenance: real/canary/fixture/imported/unknown;
- не удалять две canary installations и historical telemetry;
- visible API errors и initialized arrays;
- только после 200 response добавлять model/token/cost charts;
- provider cost и API-equivalent estimate показывать раздельно; для Codex не заявлять billed spend.

### Wave D — P0 telemetry trust

R03, R08, R10:

- rollout scanner сейчас падает на 10 lines >1 MiB, max ~7.8 MiB;
- замени fail-whole-source на memory-bounded skip/quarantine/advance/continue;
- raw JSONL нельзя сохранять;
- broad `$identifier` regex уже создал 32 false requests — обычные dollar variables должны давать
  zero skill assertions;
- typed App Server skill item может быть exact; ordinary implicit CLI invocation без native
  lifecycle остаётся reconstructed/not_observed;
- plugin child invocation создаёт child invocation + plugin child_activity, но не fabricated plugin
  invocation;
- exact counts обязаны быть replay-idempotent;
- source health показывать отдельно от metric completeness;
- реализовать terminal outcome matrix, не превращать missing/contradictory completion в failed.

После восстановления source health выполни контролируемые canaries из research report:

- standalone skill ×2;
- plugin-owned skill ×2;
- search-workflow;
- SRE plugin child;
- Kotlin skill пишет только в `mktemp -d` на low effort, затем temp очищается;
- central-university-lms — только expected failure до любого внешнего действия, без auth/LMS call;
- duplicate replay;
- oversized line followed by valid line;
- `$variable` false-positive fixture;
- Europe/Moscow + одна DST timezone;
- success/failure/cancel/deny/timeout/missing/duplicate/contradictory terminals.

Если central-university или другой canary требует новую внешнюю авторизацию, не расширяй scope:
зафиксируй blocked subcase и продолжи локальные canaries.

### Wave E — Reliability и формулы

R04, semantic часть R05, R11 workbench:

- не удалять и не массово resolve incidents;
- для новых unknown schemas сохранить safe source/version/parser/event-type attribution;
- reconciliate metadata rows, manifest records, occurrences и incidents;
- Claude priority только после frontend; локальный Claude не запускать без просьбы;
- использовать sanitized version-pinned fixtures, потому что docs могут быть новее Claude 2.1.197;
- разделить receive-to-commit live latency, observation age, replay/backfill и clock skew;
- formula version + numerator/denominator/exclusions/completeness;
- исправить Models error ratio: total failures / terminal population либо честно переименовать
  unweighted daily average;
- custom Dropdown вместо native select;
- signed-keyset `useInfiniteQuery` + IntersectionObserver;
- bounded/virtualized DOM, accessible Load more fallback, URL filters и scroll restore;
- responsive table/KPI без overflow.

### Wave F — IA и аналитика

R07 и visualization часть R06:

- подготовить prototype/ADR Fleet → Installation → Models/Sources/Components/Incidents;
- model row открывает filtered drill-down;
- сохранить global cross-agent Models comparison;
- не удалять `/models` без redirect/deep-link/history tests;
- предпочесть line/stacked bars/comparison table; donut только для 3–5 bounded categories;
- везде показывать cost/outcome coverage и unknown exclusions.

## Неприкосновенные контракты

- Не сохраняй raw prompts, responses, source code, tool input/output, environment, credentials или
  unredacted host paths.
- Не переписывай historical telemetry, incidents или пользовательскую agent config ради теста.
- `unsupported`, `not_observed`, `redacted`, `unknown` и numeric zero различны.
- Unknown schema можно quarantine, но нельзя silently drop/coerce.
- Installation/config writes требуют preview + confirmation; runtime collection read-only к агентам.
- External egress default deny.
- Проценты и ratios показывают numerator, denominator, exclusions, completeness.
- Не ветвись по agent name в core domain там, где подходит capability.

## Проверка каждого коммита

- tests до кода для воспроизведённого exit gate;
- unit + contract + replay + migration + privacy;
- targeted race tests для runtime/observability/dataplatform при изменении ingestion;
- `go test ./...` и `go vet ./...` для backend waves;
- web typecheck, component tests, a11y, build, embed/dist parity;
- живой Chrome harness из `reports/artifacts/2026-07-30/browser-research.mjs`;
- desktop/tablet/mobile, light/dark, 200%, reduced motion, keyboard;
- SQL/API/UI reconciliation;
- resource/retention review;
- обновить paired EP/TDD и `SOURCES.md`, если реальность интерфейса/формулы изменилась;
- ADR для material IA/architecture;
- завершить reconciliation report и перечислить residual risks.

## Как начать прямо сейчас

1. Покажи `git status`, ветку и последние три коммита.
2. Убедись, что live stack доступен; используй `SKILLS/README.md` и
   `SKILLS/db-quick-connect/SKILL.md`, не извлекай credentials в вывод.
3. Запусти сохранённый browser harness как baseline.
4. Преврати acceptance R01 в тест и реализуй первый небольшой коммит Wave A.
5. Продолжай по порядку, не возвращаясь к широкому research, пока новое доказательство не
   противоречит пакету.
