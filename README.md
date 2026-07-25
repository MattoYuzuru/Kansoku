# Kansoku

Kansoku (観測, «наблюдение») — локальная платформа наблюдаемости для AI coding агентов
(Codex, Claude Code и других). Она отвечает на простой вопрос: **чем твои агенты реально
пользуются** — какие skills, plugins и MCP-инструменты активируются, как часто, с каким
успехом и в каких сессиях, — не сохраняя ни одного prompt'а, ответа модели или фрагмента кода.

Всё работает на устройстве пользователя: один Docker Compose стек, один порт для дашборда,
PostgreSQL для хранения. Ничего не уходит во внешние сервисы.

## Зачем

Агенты вроде Codex и Claude Code уже умеют экспортировать телеметрию (OTel) и слать хуки, но
у этих данных нет единого места, где видно фактическое использование агента — с учётом
ненадёжных источников, обрывов данных и разной степени доказательности события. Kansoku
собирает эти источники, честно помечает то, что не удалось подтвердить (`unknown ≠ zero`), и
показывает результат в локальном дашборде.

## Принципы

1. **Local-first.** Runtime и данные — на устройстве пользователя; UI слушает только loopback.
2. **Metadata by default.** Raw prompts, ответы моделей, tool input/output и код не сохраняются.
3. **No silent loss.** Каждый источник данных измеряется на покрытие; разрывы помечаются явно,
   а не заполняются нулями.
4. **Adapter-first.** Codex и Claude — обычные адаптеры, а не частные случаи в модели данных.
5. **Lineage everywhere.** У каждой метрики есть источник, версия адаптера, schema fingerprint
   и время наблюдения.
6. **Unknown is not zero.** Отсутствие данных, неподдерживаемый источник и настоящий ноль —
   три разных состояния, и они никогда не смешиваются.
7. **Useful over performative.** Никакого сводного «productivity score» — только объяснимые
   метрики с явным числителем, знаменателем и ограничениями.
8. **Cheap to keep running.** Один `docker compose up`, `restart: always`, ограниченное
   хранение и предсказуемое потребление ресурсов.

## Быстрый старт

Нужны Docker (Desktop или Engine) и Docker Compose v2. Все команды ниже выполняются из
каталога `deploy/`.

```bash
cd deploy

# 1. Собрать образ (контекст сборки — корень репозитория, поэтому Dockerfile лежит в deploy/,
#    а сама сборка идёт из deploy/ на уровень выше)
docker build -f Dockerfile -t kansoku:local ..

# 2. Сгенерировать 7 обязательных секретов (каждый — отдельный файл, ≥32 байт, без \n внутри)
mkdir -p secrets
for name in ingress_bearer read_bearer mutation_bearer csrf identity_hmac audit_hmac database_password; do
  openssl rand -base64 32 > "secrets/$name"
  chmod 600 "secrets/$name"
done

# 3. Зафиксировать тег образа в .env — без этого файла переменная не переживёт
#    следующий отдельный вызов docker compose (интерполируется заново при каждом запуске)
echo "KANSOKU_IMAGE=kansoku:local" > .env

# 4. Поднять стек
docker compose -f compose.yaml up -d

# 5. Дождаться healthy и проверить
docker compose -f compose.yaml ps
curl -s http://127.0.0.1:43100/ -o /dev/null -w '%{http_code}\n'
```

Дашборд — `http://127.0.0.1:43100`. OTLP-приём (для агентов) — `http://127.0.0.1:4318`
(HTTP) и `127.0.0.1:4317` (gRPC), оба требуют `ingress_bearer` из `secrets/ingress_bearer`.

`secrets/` и `.env` — локальные и в git не попадают (см. `.gitignore`). Оба сервиса
(`kansoku`, `postgres`) должны стать `Up ... (healthy)`; первый healthcheck может занять
до 30 секунд после старта.

## Подключение реальных агентов

Возьмите ingress bearer:

```bash
cat deploy/secrets/ingress_bearer
```

**Codex** — добавьте в `config.toml` профиля (`$CODEX_HOME/config.toml`; если вы используете
несколько профилей через разные `CODEX_HOME`, повторите для каждого):

```toml
[otel]
log_user_prompt = false

[otel.exporter.otlp-http]
endpoint = "http://127.0.0.1:4318/v1/logs"
protocol = "binary"
headers = { "Authorization" = "Bearer <ingress_bearer>" }
```

**Claude Code** — добавьте в блок `"env"` файла `~/.claude/settings.json`:

```json
{
  "env": {
    "CLAUDE_CODE_ENABLE_TELEMETRY": "1",
    "OTEL_METRICS_EXPORTER": "otlp",
    "OTEL_LOGS_EXPORTER": "otlp",
    "OTEL_EXPORTER_OTLP_PROTOCOL": "http/protobuf",
    "OTEL_EXPORTER_OTLP_ENDPOINT": "http://127.0.0.1:4318",
    "OTEL_EXPORTER_OTLP_HEADERS": "Authorization=Bearer <ingress_bearer>"
  }
}
```

Конфиг агенты читают один раз при старте — после правки нужна новая сессия/процесс агента.
Проверка: откройте дашборд → Activity/Agents, там должны появиться события за последние
минуты.

## Подключение через hooks

Session 11 (ADR 0014, Gap B) добавила hook-installer таргеты `codex.user_hook` /
`claude.user_hook` в `contracts/privacy/installer.yaml` и реальные builders
(`installer.BuildCodexHookPlan` / `installer.BuildClaudeHookPlan`) в `internal/installer`, вместе
с новым закрытым capability id `configuration.hook_install`
(`contracts/adapter-sdk/capabilities.yaml`). `PlanConfiguration` в `internal/codexadapter` и
`internal/claudeadapter` строит для этого capability реальный `ChangePlan`, проходящий через ту
же `Plan`/`Approval`/`SimulateApply`/`SimulateRollback`/`SimulateRemove`/`PlanSHA256` машину, что
и OTel-таргеты — без второго apply-механизма.

Ownership-изоляция (обязательное требование ADR 0014, п. 4): Codex-hook и Codex-OTel живут в
одном `config.toml` (разные таблицы), Claude-hook и Claude-OTel — в одном `settings.json`
(разные ключи). Каждый план владеет только своим набором ключей — `buildTargetPlan` в
`internal/installer/protocol.go` читает/пишет исключительно `targetSpecs[id].required` этого
таргета и сканирует только его собственный `forbidden`-список, поэтому применение или откат
hook-плана никогда не трогает уже применённые OTel-ключи (и наоборот) и не трогает никакой
другой существующий пользовательский контент файла. Это проверено round-trip тестом
(`TestHookPlanOwnershipIsolationRoundTrip` в `internal/installer/protocol_test.go`): применяем
OTel-план, применяем hook-план, откатываем hook-план — OTel-ключи и весь несвязанный контент
побайтово совпадают с состоянием до применения hook-плана.

Что именно пишет hook-план (только preview/simulate, см. ниже):

**Codex** (`config.toml`, таблица `notify`):

```toml
[notify]
command = "kansoku-codex-hook"
role = "collection_only"
```

**Claude Code** (`settings.json`, ключи `hooks.*` для всех 7 задокументированных событий):

```json
{
  "hooks": {
    "SessionStart": "kansoku-claude-hook",
    "UserPromptSubmit": "kansoku-claude-hook",
    "PreToolUse": "kansoku-claude-hook",
    "PostToolUse": "kansoku-claude-hook",
    "SubagentStart": "kansoku-claude-hook",
    "SubagentStop": "kansoku-claude-hook",
    "Stop": "kansoku-claude-hook"
  }
}
```

Оба hook-помощника (`kansoku-codex-hook` / `kansoku-claude-hook`) по контракту
(`contracts/codex/hooks-and-otel.yaml` / `contracts/claude/hooks-and-otel.yaml`) читают только
bounded stdin JSON, вычисляют prompt-фичи в памяти, никогда не пишут и не пересылают сырой
prompt/ответ/tool payload, и шлют уже санитизированное событие на тот же общий
`/v1/hooks/{adapter}/{event}` route, что и OTel-приёмник — `codexHookHandler` /
`claudeHookHandler` в `internal/observability/routes.go` (проверено
`TestRealAdapterHookStdinPayloadsReachCodexAndClaudeHookRoutes` синтетическим stdin-пейлоадом,
доходящим до реального handler'а).

**Важно — это остаётся simulate-only, как и весь `internal/installer` (ADR 0002).** В отличие от
раздела «Подключение реальных агентов» выше, здесь нет ручных шагов «впишите это в файл» — ADR
0008's известный пробел (нет `kansoku doctor`/`configure` CLI) означает, что `PlanConfiguration`
и `SimulateApply`/`SimulateRollback` для `configuration.hook_install` сегодня доступны только из
Go-кода (тесты и будущий CLI), а не из README-инструкции для реального пользователя.
`AuthorizeRealWrite` по-прежнему возвращает "not implemented" для любого таргета — ни один hook
или OTel план не пишет в реальный файл пользователя без будущей CLI-обвязки и явного
human-approval шага.

## Если тебя как агента попросили поднять Kansoku

Если ты — AI-агент и тебе передали этот репозиторий с задачей «подними Kansoku и подключи
себя к нему», порядок действий:

1. Спроси явное подтверждение пользователя перед выполнением — дальнейшие шаги создают
   локальные секреты и меняют файлы конфигурации самого пользователя (`config.toml` /
   `~/.claude/settings.json`).
2. Выполни шаги из «Быстрый старт» выше (сборка образа, секреты, `.env`, `docker compose up`,
   проверка healthy).
3. Прочитай `deploy/secrets/ingress_bearer` и пропиши его в конфиг своего собственного
   рантайма по разделу «Подключение реальных агентов» — определи, Codex ты или Claude Code,
   и какой у тебя `CODEX_HOME` / где твой `settings.json`.
4. Сообщи пользователю, что нужно начать новую сессию/перезапустить процесс, чтобы конфиг
   применился, и укажи, где смотреть результат (`http://127.0.0.1:43100`).

## Документы

- [ROADMAP.md](ROADMAP.md) — история разработки и зависимости между этапами.
- [Engineering Proposal](Engineering%20Proposal/README.md) — цели, альтернативы, продуктовые
  решения.
- [Technical Design Document](Technical%20Design%20Document/README.md) — контракты, схемы,
  алгоритмы, эксплуатационные требования.
- [SOURCES.md](SOURCES.md) — официальные интерфейсы агентов и инфраструктуры, на которых
  основан дизайн.
- `adr/` — architecture decision records; `reports/` — verification/reconciliation отчёты по
  каждому этапу разработки.
- [AGENTS.md](AGENTS.md) — правила работы над этим репозиторием для будущих сессий разработки.
