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
| 07 | Claude adapter and next-agent portability proof | Claude adapter, второй fixture-agent, cross-agent conformance (Codex + Claude) | Claude данные сосуществуют с Codex в одной canonical model без agent-specific core schema |
| 07b | Gemini and Cursor | Gemini adapter, Cursor probe, обновлённая portability validation на три реальных источника | Gemini/Cursor нормализуются без agent-specific core schema; Cursor остаётся Experimental |
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
- **07 — complete (2026-07-23):** closed manifest/hooks-and-otel/transcript-and-inventory/
  skill-evidence-and-reconciliation registries under `contracts/claude/` and a typed
  `internal/claudeadapter` package deliver the second real `internal/adaptersdk` registration —
  Claude Code — with zero new agent-name branch inside `internal/adaptersdk` itself. `claude.hook`'s
  seven-event vocabulary and `claude.otel` (reusing the existing `claude.user_otel` installer target
  verbatim) unconditionally strip prompt/tool/response/raw-body content regardless of upstream
  `OTEL_LOG_*` settings, and the seven-tier skill-evidence model never promotes inferred or
  ambiguous-ownership evidence to a native exact activation. A second, differently-shaped fictional
  fixture-agent ("Wayfinder", `contracts/cross-agent/`) and a Codex+Claude cross-agent invariant test
  (`internal/crossagent`) prove the SDK needs no core change for either a second real adapter or a
  second synthetic agent. Session scope was narrowed from the original Claude+Gemini+Cursor grouping:
  Gemini and the Cursor probe are deferred to a new **Session 07b**, so Claude adapter evidence lands
  sooner without waiting on two more agents. ADR 0010 records the scope-narrowing rationale and lists
  the missing `claude.transcript` JSONL importer, live-CLI canary evidence and nil `Audit` as explicit
  downstream gaps.
- **07b — deferred/backlog (2026-07-23):** Gemini adapter and Cursor probe (deferred from the
  original Session 07 scope). 07b was originally slated as the immediate next session, but the
  sequencing changed on 2026-07-23: it is pulled out of the main dependency chain into backlog so
  Session 08 can start now. It keeps the original TDD/proposal 07 content for Gemini/Cursor and is
  not cancelled, just decoupled from the 08-10 sequence — whoever picks it up later should re-verify
  it against whatever Sessions 08-10 have changed in the meantime.
- **08 — implementation and bounded runtime validation complete; 2 fault claims pending (2026-07-24):** four locked integrity registries and `internal/integrity`
  implement the durable 11-stage daily audit over the existing PostgreSQL pool and a session-scoped
  advisory lock. Checks are source-aware and timeout-bounded; stale runs fail visibly, drift
  fingerprints are structural-only and trigger targeted stages, incidents require later fresh
  positive recovery, and the Health API exposes nine decomposed green/yellow/red/gray dimensions
  without a magic score. The 21-entry fault catalog is evidence-partitioned: 17 component
  classifiers (no end-to-end SLO claim), 2 measured PostgreSQL mutation integrations, and 2
  runtime-required scenarios (DB restart and failed restore). The mutation integrations measure
  scheduler/Stage-11/persistent-incident detection at durable `Incident.OpenedAt`; both passed in
  the full pinned PostgreSQL 18 tagged suite. DB restart and failed restore remain unexecuted, so
  there is no aggregate 21-fault runtime claim. Production assembly
  and report signing fail closed; the synthetic probe uses the shared public-ingress-to-PostgreSQL
  handoff and verifies rollups with exact cleanup. Live canary execution remains disabled and
  simulation-only pending credentials plus explicit consent; Session 07b remains untouched backlog.
- **09 — next:** Local runtime and backend.

## Dependency graph

```text
01 -> 02 -> 03 -> 04 -> 05 -> 06 -> 07 -> 08 -> 09 -> 10
                   \-----------------------> analytics/UI fixtures
                                 \-> 07b (Gemini/Cursor, backlog, non-blocking)
```

Sessions 06 and 07 могут делить parser fixtures, но не должны идти параллельно до стабилизации
Adapter SDK в Session 05. Frontend spikes допустимы раньше, однако production UI строится только
после фиксации semantics и completeness states. 07b остаётся валидной будущей сессией, но больше не
блокирует 08-10.

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
