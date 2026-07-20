# Kansoku: ten-session roadmap

Каждая сессия рассчитана на отдельный рабочий цикл: исследование, утверждение решений,
реализацию, тесты, review и обновление документации. Это порядок зависимостей, а не обещание
одинаковой длительности.

| Session | Тема | Главный результат | Exit gate |
|---:|---|---|---|
| 01 | Product contract and SLOs | Глоссарий, user journeys, scope, измеримые SLO и ADR baseline | Нет неоднозначных терминов или скрытых product decisions |
| 02 | Privacy, security and trust | Threat model, data classes, retention, consent и безопасная установка | Raw-content canary не попадает ни в один storage/log path |
| 03 | Core observability architecture | Canonical event envelope, lifecycle, ingestion paths и lineage | Один fixture проходит hook, OTLP и transcript routes идемпотентно |
| 04 | Data platform and analytics | PostgreSQL schema, partitions, rollups, quantiles и query budgets | Replay даёт точные агрегаты и укладывается в performance budget |
| 05 | Adapter SDK and inventory | Capability model, discovery, versioning, plugin API и inventory graph | Dummy third-party adapter работает без изменения core |
| 06 | Codex adapter | Hooks, OTel, sessions, skills/plugins/MCP inventory и reconciliation | Поддерживаемый Codex canary даёт полную ожидаемую цепочку или incident |
| 07 | Claude, Gemini and next agents | Claude + Gemini adapters, Cursor probe и portability validation | Два разных telemetry models нормализуются без agent-specific core schema |
| 08 | Integrity and drift detection | Daily audit, schema drift, watermarks, canaries и health scoring | Намеренная поломка каждого source обнаруживается в пределах SLO |
| 09 | Local runtime and backend | API, Docker Compose, scheduler, backup/restore и resource controls | 7-day soak переживает restarts без silent loss и duplicate inflation |
| 10 | Dashboard, hardening and release | Полный UX, accessibility, review, packaging и evolution loop | Privacy/reliability/performance gates зелёные; fresh install воспроизводим |

## Dependency graph

```text
01 -> 02 -> 03 -> 04 -> 05 -> 06 -> 07 -> 08 -> 09 -> 10
                   \-----------------------> analytics/UI fixtures
```

Sessions 06 и 07 могут делить parser fixtures, но не должны идти параллельно до стабилизации
Adapter SDK в Session 05. Frontend spikes допустимы раньше, однако production UI строится только
после фиксации semantics и completeness states.

## Cross-session deliverables

Все сессии поддерживают общие артефакты:

- ADR log с rejected alternatives;
- versioned canonical event schemas;
- sanitized fixture corpus по agent/version/source;
- adapter capability and compatibility matrix;
- metrics catalog и formula registry;
- privacy regression corpus;
- reconciliation and coverage reports;
- release notes с известными gaps.

