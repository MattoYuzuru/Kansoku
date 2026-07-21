# Kansoku

Kansoku (観測, «наблюдение», «измерение явлений») — локальная платформа наблюдаемости
для AI-агентов разработчика. Она инвентаризирует установленные агенты, skills, plugins и MCP,
собирает metadata об их фактическом использовании, сверяет независимые источники событий и
показывает статистику в локальном dashboard без сохранения текстов пользовательских prompts.

## Статус

Session 01 / фаза 001 выполнена 2026-07-21: product contract, registries, SLO harness, измеряемые
technology spikes и ADR baseline находятся в репозитории, а автоматизированный exit gate проходит.
Следующая реализационная сессия — Session 02 (privacy, security and trust). До появления bounded
fixtures, privacy tests, audit/canary evidence и двух независимых human reviews ни один адаптер не
имеет публичного статуса Supported/Beta.

Проект по-прежнему строится десятью последовательными сессиями, каждая из которых имеет продуктовый
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
