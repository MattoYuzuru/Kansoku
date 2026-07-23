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

## Progress

- **01 — complete (2026-07-21):** registries, SLO harness, reproducible spikes, ADR 0001 and
  reconciliation report exist; automated contract gates pass. ADR 0002 separates that sequencing
  gate from the still-blocked public adapter Supported/Beta gate, which requires two independent
  approved human reviews plus bounded privacy/replay/audit/canary evidence.
- **02 — complete (2026-07-21):** typed bounded sanitizer, data/threat/host/retention registries,
  local HTTP and container policy, virtual installer consent/rollback, and the independent
  ten-sink raw-content canary pass. Versioned privacy-policy locks and independent exact invariants
  reject coherent registry/runtime/checksum weakening; protected review/CI remains the external
  trust root. This synthetic privacy proof does not satisfy adapter-specific public support evidence.
- **03 — complete (2026-07-21):** closed canonical envelope/lifecycle/ingress/reconciliation
  contracts and a typed Go spike converge one synthetic fact across authenticated hook, OTLP
  HTTP/gRPC protobuf and checkpointed transcript lanes. Replay/reorder/crash/source-loss/unknown-
  schema/privacy gates pass. ADR 0006 keeps the bounded file writer explicitly pre-PostgreSQL and
  records unsupported OTLP gzip as a conformance gap.
- **04 — complete (2026-07-22):** closed schema/rollups/query-contract/retention registries and a
  real PostgreSQL 18 `internal/dataplatform` package replace the Session 03 file-durability spike.
  Monthly-partitioned facts, exact `percentile_cont` rollups with a never-average late-data repair
  path, two-sided query-budget enforcement, partition-drop retention and lineage-verified
  backup/restore all pass against an ephemeral, pinned-digest Postgres harness. ADR 0007 records
  million-event query-budget evidence, mergeable percentile sketches, time-range preset resolution
  and cost-formula computation as explicit downstream gaps.
- **05 — complete (2026-07-22):** closed manifest/capabilities/inventory-graph/discovery-and-plans
  registries and a typed `internal/adaptersdk` package deliver the `Adapter` interface, a
  permission-checked `HostView`, an immutable inventory entity graph and a reversible `ChangePlan`
  that reuses `internal/installer`'s existing `Plan`/`Approval`/`SimulateApply`/`SimulateRollback`/
  `SimulateRemove`/`PlanSHA256` machinery instead of a second mechanism. A fake external-vocabulary
  conformance adapter ("Loomwright") passes the full discovery/inventory/normalization/
  reconciliation/audit suite through the same `Registry`/`CapabilityMatrix`/`HostView` APIs any
  built-in adapter would use, with zero agent-name branch in core code. ADR 0008 records
  external-process/Wasm adapter execution, compatibility-registry persistence and the
  `kansoku doctor`/`configure`/`adapter verify` CLI as explicit downstream gaps.
- **06 — complete (2026-07-22):** closed manifest/discovery, hooks-and-otel, rollout-and-inventory
  and skill-evidence-and-reconciliation registries and a typed `internal/codexadapter` package
  deliver the first real `internal/adaptersdk` registration: Codex. Four independently-degrading
  sources (`codex.hook`, `codex.otel`, `codex.rollout`, `codex.inventory`) reconcile into one session
  view without ever fabricating a whole-session zero, and the five-tier skill-evidence model never
  promotes inferred or reconstructed evidence to a native exact activation. The adapter reuses the
  existing `codex.user_otel` installer target and the generic `/v1/hooks/{adapter}/{event}` ingress
  route verbatim, adding only a new `codex.user_hook` target. ADR 0009 records the sequential
  checkpointed build order and lists live-CLI canary evidence and CLI surface as explicit
  downstream gaps.
- **07 — next:** Claude, Gemini and next agents (dual-adapter portability without agent-specific
  core schema).

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
