# Исследование дефектов и приоритетный план — 2026-07-30

## Статус

Исследование выполнено против живого локального appliance на ветке
`fix/skill-observatory-cold-state-2026-07-26`. Этот документ не реализует перечисленные
исправления: он фиксирует воспроизведение, причины, границы достоверности, порядок работы и
acceptance gates для следующей сессии.

Базовый P0/P1-набор, на котором проводилось исследование, зафиксирован коммитом `d35019d`.
Историческая телеметрия, пользовательские настройки агентов и incident/quarantine записи не
изменялись и не удалялись.

Полный машинно-читаемый реестр находится в
[`2026-07-30-defect-inventory.json`](./2026-07-30-defect-inventory.json), а безопасные агрегированные
runtime-доказательства — в
[`artifacts/2026-07-30/live-evidence.json`](./artifacts/2026-07-30/live-evidence.json).

## Главное

Пять проблем имеют приоритет P0:

1. Профиль скилла действительно ломает весь React-root из-за `file_tree:null` и
   `profile.file_tree.map(...)`; error boundary отсутствует.
2. Профили всех видимых агентов отвечают HTTP 503 и маскируются интерфейсом как вечные
   `Unknown agent / Loading`.
3. Трекинг скиллов и plugin child activity сейчас нельзя считать достоверным: rollout-source
   находится в `degraded`, а App Server не производит текущих наблюдений.
4. Причина деградации rollout-source найдена: 10 JSONL-строк больше лимита scanner в 1 MiB,
   максимум 7 756 804 байта. Одна такая строка останавливает обработку файла и всей source lane.
5. Метрика `Ingest latency p95` смешивает live latency, возраст backfill/replay и clock skew.
   Текущее значение около 4 000 000 ms математически соответствует формуле, но не заявленному
   смыслу live ingest latency.

Дополнительно найден trust-дефект: rollout regex считает любое `$identifier` в пользовательском
тексте запросом скилла. В базе уже 32 unresolved reconstructed request, среди которых есть
нескилловые идентификаторы. Их примеры намеренно не сохранены в артефактах.

## Приоритеты

Приоритет — это риск, а не обязательно буквальный порядок коммитов. По просьбе владельца первая
delivery-wave может быть декоративной, после неё нужно сразу перейти к P0.

| ID | Приоритет | Статус | Краткий вывод |
|---|---:|---|---|
| R02 | P0 | confirmed | Skill profile вызывает `null.map` и оставляет пустой root |
| R03 | P0 | confirmed | Plugin child use не теряется графиком — точные runtime-факты до него не доходят |
| R05 semantic | P0 | confirmed | Ingest latency имеет неверно смешанную популяцию |
| R06 | P0 | partial | Сейчас 3, не 4 Codex; все Agent profile API отвечают 503 |
| R08 | P0 | confirmed | Rollout scanner degraded, oversized lines, false `$` markers |
| R10 | P0 | partial | Outcome counts есть, но terminal-canary корректность не доказана |
| R01 | P1 | confirmed | Range живёт только в state и сбрасывается |
| R04 | P1 | partial | 9 808 unknown-schema occurrences нельзя честно приписать Claude |
| R11 | P1 | confirmed | Full reload, native selects, Next page, overflow и плотная таблица |
| R14 | P1 | confirmed | Один high npm audit для Vite, доступен non-major fix |
| R07 | P2 | design | Agents и Models можно связать вложенностью, но это разные измерения |
| R09 | P2 | confirmed | Glossary имеет только статический `:target` |
| R12 | P2 | confirmed | Database size выводится сырыми bytes и переполняет KPI |
| R13 | P2 | confirmed | Sidebar row tokens статичны и не выводятся из выбранных accent colors |

## Рекомендуемый порядок поставки

### Wave A — небольшой декоративный разогрев

Делать отдельными проверяемыми коммитами:

- R01: versioned per-page range preference;
- R05-format: единый formatter с максимумом 2 знака после запятой, без изменения формул;
- R09: deterministic glossary pulse + reduced-motion fallback;
- R12: readable MiB/GiB и общий KPI containment;
- R13: derived hover/selected colors и проверка контраста.

Не смешивать в этот набор изменение latency/error-ratio формул.

### Wave B — ограничение frontend-failure

- R02: пустые массивы на backend, defensive normalization на API boundary;
- route-level и root-level error boundary;
- явное состояние query error вместо вечного Loading;
- R11 navigation: внутренние ссылки через Wouter, без полной перезагрузки.

### Wave C — восстановление профилей данных

- R06: устранить 503 Agent profile в пределах query contract;
- исправить projection `agent_id -> adapter_id`;
- ввести явную классификацию real/canary/fixture/imported/unknown;
- только после восстановления существующего контракта добавлять графики.

### Wave D — доверие к телеметрии

- R08: безопасно продолжать после oversized rollout lines;
- исключить ложные `$identifier` requests;
- R03: exact/reconstructed plugin-child attribution;
- R10: terminal outcome matrix и replay-idempotency;
- провести контролируемый canary pool.

### Wave E — Reliability и reconciliation

- R04: восстановить безопасную source/version attribution для новых unknown schemas;
- объяснить все четыре уровня счётчиков: metadata rows, manifest records, occurrences, incidents;
- R05 semantic: разделить live receive-to-commit, observation age, backfill и clock skew;
- R11 workbench: custom filters, keyset infinite loading, bounded DOM, accessible fallback.

### Wave F — информационная архитектура и аналитика

- R07: ADR и prototype Fleet → Installation → Model;
- R06 visualization: временные ряды, сравнение моделей и cost coverage;
- сохранить глобальный cross-agent model comparison и совместимость `/models`.

## Подробные результаты

### R01 — range не сохраняется

`useRange(initial)` создаёт только `useState(initial)`. Query keys уже включают диапазон, поэтому
кэш данных не является проблемой; отсутствует именно сохранение пользовательского предпочтения.

Живое воспроизведение:

1. Activity → `Last 7 days`;
2. переход в Models;
3. возврат в Activity;
4. значение снова `Last 30 days`.

Рекомендуемый ключ: одна versioned запись, например `kansoku.range.v1`, со словарём по стабильному
logical page id. Хранить preset, а не вычисленные `from/to`. Detail pages должны либо наследовать
list-range, либо получить отдельный ключ — это нужно решить явно и покрыть browser test.

### R02 — белый экран профиля скилла

Live browser получил:

```text
TypeError: Cannot read properties of null (reading 'map')
```

`SkillProfileResponse.FileTree` остаётся nil, если metadata rows отсутствуют, и JSON становится
`null`. TypeScript объявляет массив, а `mergeSkillProfiles` без проверки вызывает `.map`.
Render exception не локализован, поэтому `#root` остаётся без дочерних узлов. Именно поэтому после
ошибки белыми остаются и остальные страницы до полной перезагрузки.

Исправление должно иметь три слоя:

1. API всегда возвращает `[]` для коллекций;
2. клиент нормализует legacy/null ответы;
3. route/root error boundary сохраняет shell и предлагает retry/back.

Нужно одновременно проверить nullable `children`, `versions`, `assertions`, `sources` в Plugin
profile, иначе тот же класс дефекта останется рядом.

### R03/R08 — почему SRE skills не появились

Текущие агрегаты:

- `search-workflow`: 10 exact invoked и 10 exact loaded;
- SRE plugin family: inventory/requested facts, но 0 exact loaded и 0 exact child activity;
- SRE child skills: installed/enabled, но invocation facts отсутствуют;
- skill outcome assertions: 0.

Frontend не отбросил уже существующие child facts: их нет в durable evidence. Причины:

- `codex.app_server` configured, но `not_observed`;
- `codex.rollout` degraded / `rollout_scan_failed`;
- scanner ограничен 1 MiB, тогда как в текущем rollout есть 10 больших строк до 7,8 MiB;
- Run игнорирует возвращённую ошибку и сохраняет только общий safe error class;
- `$` marker regex слишком широк и создаёт false-positive requested assertions;
- обычное implicit skill activation в Codex не имеет отдельного стабильного upstream lifecycle
  notification.

Из текущей официальной документации:

- App Server позволяет точно наблюдать explicit request, когда клиент передаёт `$skill` и typed
  `skill` input item;
- `skills/list` и `skills/changed` относятся к inventory/invalidation;
- Codex также умеет implicit activation, но официальной terminal activation notification для
  обычного CLI не определено.

Следовательно, Kansoku не должен обещать «абсолютно все вызовы точно» без указания источника и
coverage. Правильная модель:

- typed App Server input — exact requested/instruction injection;
- подтверждённый read/load в pinned rollout schema — reconstructed/corroborated;
- implicit invocation без наблюдаемой границы — `not_observed`, а не numeric zero;
- plugin child use — отдельный факт; он не означает plugin invocation.

### R04 — incidents и Claude

Сейчас открыто:

- 48 `unknown_schema`, 9 808 occurrences;
- 49 `component_identity_unresolved`, 135 occurrences;
- один ambiguous identity incident;
- ещё два ingestion incident.

Все 48 unknown-schema incidents имеют не наблюдённую source attribution. Поэтому присвоить им
приоритет «Claude» по текущим данным невозможно. Любая такая классификация была бы выдумкой.

Quarantine:

- OTLP log: 28 manifests / 6 066 occurrences / 6 082 aggregate records;
- OTLP metric: 24 / 3 754 / 3 753;
- evidence bridge: 2 / 5 / 5.

При этом retained `schema_quarantine_metadata.record_count` суммируется только в 44, 23 и 2.
Возможно, это корректные разные уровни агрегации, но UI и runbook их не объясняют. До reconciliation
нельзя «расчистить» очередь удалением или массовым resolve.

Текущие Claude docs всё ещё описывают `skill_activated`, `plugin_loaded`, `tool_result.success`,
а также `skill.name/plugin.name/agent.name` на cost/token telemetry. Но локальная версия — 2.1.197,
и текущая документация может описывать более новую схему. Сначала нужны sanitized version-pinned
fixtures. Запуск Claude на машине в этой сессии не выполнялся.

### R05 — округление и реальная математика

Сами percentile queries в основных model/tool/prompt paths используют PostgreSQL
`percentile_cont` над raw normalized facts. Тесты проверяют интерполяцию и запрещают усреднение
bucket percentiles. Это хорошая база.

Проблемы:

- форматирование расходится от 0 до 2 знаков на разных страницах;
- Agent p95 принудительно округляется до целого;
- Reliability p95 — до одного знака;
- `KpiCard` по умолчанию использует precision 0;
- Models KPI берёт невзвешенное среднее дневных error ratios.

Критичнее всего `Ingest latency`:

```sql
ingested_at - observed_at
```

Эта величина включает время между событием у агента и импортом, то есть replay/backfill и clock
skew. В 30-дневной популяции:

- OTLP log p95 ≈ 4 002 084 ms;
- Codex rollout p95 ≈ 3 092 033 ms;
- evidence bridge p95 ≈ 83 788 268 ms;
- 212 строк имеют отрицательную разницу.

Нужно отдельно хранить/показывать:

- receive-to-commit live latency;
- source observation age;
- replay/backfill population;
- clock-skew/invalid timestamp exclusions.

Формула и population обязаны иметь новую version и reconciliation test.

### R06 — Agents и данные по моделям/стоимости

Точный тезис «четыре Codex» сейчас не воспроизводится. В базе три Codex installations:

- одна с 128k+ events;
- две ручные canary installations с 15 events суммарно.

Эта классификация пока выведена из существующих test identifiers, а не из доменного поля. Это
нужно исправить: пользователь не должен угадывать, почему видит несколько одинаковых Codex.
Исторические строки удалять нельзя.

Все пять открытых из списка agent profiles:

- показывают `Unknown agent`;
- остаются Loading;
- получили HTTP 503 от `/api/v1/agents/{installation}`.

Root cause endpoint:

- контракт `agent_profile_range` ограничен 200 ms;
- запросы выполняются последовательно;
- только пять основных частей заняли около 174 ms;
- затем идут identity/incidents/freshness/прочее;
- после завершения endpoint сам отклоняет результат за превышение total wall budget.

Есть отдельный projection bug: второй SELECT-column `ai.agent_id` сканируется в поле
`Identity.AdapterID`.

Данные для будущих графиков уже существуют. Например, exact-attribution содержит Sol, Terra,
Luna, gpt-5.4-mini и две Claude models с token/outcome facts. Provider-reported cost присутствует
у Claude, но отсутствует у Codex. Поэтому:

- Claude можно показывать с provider cost coverage;
- Codex — только API-equivalent estimate/upper bound, если price catalog покрывает строку;
- это не «реально списанные деньги» подписки ChatGPT/Codex.

Рекомендуемые визуализации:

- line/stacked area: input, cached input, output tokens по датам;
- line: cost по датам и модели с переключателем provider/API-equivalent;
- grouped bars: Sol/Luna/Terra requests, tokens, cost, success/unknown;
- horizontal bars: model share с абсолютным числом и denominator;
- latency line или box/quantile band: p50/p95/p99 при достаточной population;
- coverage strip: costed/uncosted и terminal/unknown;
- comparison table как обязательная точная форма.

Pie chart допустим только для небольшой фиксированной доли 3–5 моделей; при длинном хвосте лучше
bars + table.

### R07 — объединять ли Agents и Models

Полностью сливать сущности в домене не стоит:

- Agent row — installation/fleet identity;
- Model row — операция модели, потенциально через несколько installations.

В UI естественнее сделать flow:

```text
Fleet
└── Installation
    ├── Overview
    ├── Models
    │   └── Model drill-down
    ├── Sources
    ├── Components
    └── Incidents
```

При этом нужен global Models comparison для вопросов «сколько суммарно потратили Sol и Terra на
всех installations». Удалять `/models` можно только после prototype, ADR, redirects и проверки
deep links/history.

### R09 — glossary pulse

Сейчас у target только статический border. Random seed не нужен: он усложняет тесты и не улучшает
ориентацию. Достаточно 2–3 мягких переливаний background/border за ~5 секунд:

- без layout properties;
- с повторным запуском на `hashchange`;
- класс снимается после окончания;
- `prefers-reduced-motion` получает статический highlight.

### R10 — success/failure

В БД есть:

- Claude tools: 1 616 succeeded / 48 failed;
- exact Codex tools: 3 986 / 52;
- unattributed tools: 1 715 / 24;
- exact Codex model operations: 2 960 succeeded / 570 unknown;
- skill outcomes: отсутствуют.

Эти числа синтаксически reconciled, но их семантическая правильность не доказана terminal canary.
Нужна матрица для:

- success;
- execution/protocol failure;
- denied;
- cancelled/interrupted;
- timed out;
- missing completion;
- duplicate completion;
- contradictory terminal states.

Показывать «какой именно call» можно только через ограниченную safe identity: tool category/name,
native call pseudonym и source lineage. Arguments, results и errors сохранять нельзя.

### R11 — Reliability UX

Подтверждено:

- navigation использует обычные `<a href>` и полностью перезагружает document;
- две формы используют native `<select>`, хотя общий Dropdown запрещает это;
- `Next page` — ручная keyset pagination;
- p95 переполняет KPI на 1440, 1024 и 390 px;
- incident table слишком плотная и визуально обрезает правые колонки.

Infinite scroll совместим с производительностью, если:

- backend оставляет signed keyset cursor;
- frontend использует `useInfiniteQuery`;
- IntersectionObserver загружает следующую страницу один раз;
- старые pages ограничиваются/виртуализуются;
- есть keyboard-accessible `Load more` fallback;
- URL хранит filters, а back восстанавливает scroll/cursor.

### R12 — Database size

KPI получает raw `263550655`, хотя `bytesToReadable` уже используется в detail text. Нужен единый
formatter: например MiB/GiB с максимумом 2 знака, exact bytes в tooltip/detail. Одновременно
`KpiCard` должен получить `min-width:0` и безопасную политику длинных value/unit.

### R13 — цвета sidebar

`applyAppearance` изменяет только `--accent-purple` и `--accent-gold`. `--row-hover` и
`--row-selected` остаются фиксированными значениями tokens.css. Browser evidence:

- marker поменялся на новый accent;
- active background не изменился.

Derived colors должны вычисляться одинаково в inline pre-paint script и ThemeProvider, иначе будет
flash старой палитры. После изменения обязательна WCAG AA проверка dark/light/presets/custom.

### R14 — дополнительный dependency finding

`npm audit` нашёл один high finding у direct dependency Vite 6.3.6. Доступно исправление 6.4.3 без
major bump. Production appliance отдаёт prebuilt assets, поэтому exposure dev-server в production
не доказан; это не повод игнорировать обновление, но и не основание заявлять production compromise.

## Canary plan для skills/plugins/outcomes

Canary должен выполняться только после восстановления observer health.

| Canary | Ожидание | Ограничение |
|---|---|---|
| Standalone explicit skill ×2 | invoked=2, replay остаётся 2 | typed App Server input предпочтителен |
| Plugin-owned skill ×2 | child invoked=2, plugin child_activity=2 | не создавать plugin invoked |
| `search-workflow` | точное наблюдение | не считать inventory invocation |
| Kotlin skill | один файл только в `mktemp -d` | low effort, удалить temp после evidence |
| `central-university-lms` | expected safe failure до внешнего действия | не использовать auth и не обращаться к LMS без отдельной авторизации |
| SRE plugin skill | exact owner attribution или visible not_observed | не угадывать owner при collision |
| Implicit/proactive | exact только при native evidence | иначе reconstructed/not_observed |
| Duplicate payload | счётчики не меняются | idempotency key обязателен |
| Oversized JSONL then valid | source продолжает producing | raw line не сохранять |
| `$variable`/код | 0 skill requests | privacy-safe fixture |
| Timezone Moscow + DST zone | правильные bucket и display | UTC storage остаётся неизменным |
| Tool terminals | каждый terminal state ровно один раз | unknown не превращать в failed |

## Общие acceptance gates

Перед закрытием каждой wave:

1. Unit, contract, replay, migration и privacy tests.
2. PostgreSQL reconciliation между raw normalized facts, API numerator/denominator/exclusions и UI.
3. Browser matrix: desktop/tablet/mobile, light/dark, 200%, reduced motion, keyboard.
4. Нет runtime exceptions, пустого root, скрытых 5xx и overflow.
5. Повторный ingestion/replay не увеличивает exact counts.
6. Unknown schemas остаются в quarantine и не coerced/dropped.
7. Изменение формулы получает новую version и обновление paired proposal/TDD.
8. Material architecture/IA decisions получают ADR.
9. Никакие тесты не переписывают historical telemetry или пользовательскую agent config.

## Артефакты

- [`browser-evidence.json`](./artifacts/2026-07-30/browser-evidence.json) — DOM, navigation,
  overflow, theme, glossary, HTTP statuses и runtime exception.
- [`browser-research.mjs`](./artifacts/2026-07-30/browser-research.mjs) — воспроизводимый CDP
  сценарий с ephemeral browser profile.
- [`live-evidence.json`](./artifacts/2026-07-30/live-evidence.json) — безопасные агрегаты API/БД.
- [`skill-profile-after-spa-click.png`](./artifacts/2026-07-30/skill-profile-after-spa-click.png)
  — подтверждение пустого document после crash.
- [`agent-profile.png`](./artifacts/2026-07-30/agent-profile.png) — Unknown agent/Loading.
- [`reliability-incidents.png`](./artifacts/2026-07-30/reliability-incidents.png) — текущий
  incident workbench.
- [`system-tablet.png`](./artifacts/2026-07-30/system-tablet.png) и
  [`system-mobile.png`](./artifacts/2026-07-30/system-mobile.png) — responsive System evidence.
- [`2026-07-30-defect-inventory.json`](./2026-07-30-defect-inventory.json) — backlog для автоматики.
- [`2026-07-30-next-agent-prompt.md`](./2026-07-30-next-agent-prompt.md) — готовый handoff prompt.

Артефакты не содержат raw prompts, model responses, source code payloads, tool input/output,
credentials, environment values, user-specific host paths или telemetry paths. Browser IDs
остаются локальными opaque pseudonyms и не используются как пользовательские labels.

## Остаточные риски

- Claude runtime недоступен для live canary; текущие Claude findings опираются на безопасные
  агрегаты, локальную версию и официальную документацию.
- Exact correctness существующих failed calls пока не доказана source-terminal fixtures.
- Unknown-schema backlog нельзя распределить по агентам задним числом без source attribution.
- Canary/fixture rows уже смешаны с production catalog; до появления explicit classification UI
  будет продолжать выглядеть дублированным.
- Query timings сняты на текущем объёме и железе; оптимизация должна иметь repeatable budget test,
  а не разовое измерение.
- Исправление oversized lines должно оставаться memory-bounded и не сохранять raw content.
- Информационная архитектура Agents/Models требует product validation; это не безопасный
  «cleanup плохого кода» без совместимости маршрутов.

## Reconciliation result

Все 13 пользовательских пунктов классифицированы. Двенадцать имеют подтверждённую техническую
часть; точное число «4 Codex» сейчас не воспроизведено, но причина дубликатов установлена как
ручные canary installations без явной классификации. Claude attribution и outcome correctness
оставлены честно `not_observed/unproven`, а не угаданы. Дополнительно зафиксированы rollout
oversized-line failure, false-positive skill requests и high Vite audit finding.
