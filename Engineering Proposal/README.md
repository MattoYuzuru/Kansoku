# Engineering Proposals

Этот каталог отвечает на вопросы «какую проблему решаем», «почему именно так», «какие варианты
отклонены» и «какой пользовательский результат обязан появиться». Реализационные контракты живут
в парных документах каталога `Technical Design Document`.

## Sessions

1. [Product contract and success](01-product-contract-and-success.md)
2. [Privacy, security and trust](02-privacy-security-and-trust.md)
3. [Observability architecture](03-observability-architecture.md)
4. [Data platform and metrics](04-data-platform-and-metrics.md)
5. [Adapter SDK and inventory](05-adapter-sdk-and-inventory.md)
6. [Codex integration](06-codex-integration.md)
7. [Claude, Gemini and next agents](07-claude-gemini-and-next-agents.md)
8. [Integrity, drift and daily audit](08-integrity-drift-and-daily-audit.md)
9. [Local runtime and operations](09-local-runtime-and-operations.md)
10. [Dashboard, hardening and evolution](10-dashboard-hardening-and-evolution.md)

## Shared product artifacts

- [Metrics catalog](metrics-catalog.md) defines user-facing questions and formula semantics.
- [Root roadmap](../ROADMAP.md) defines ordering and exit gates.
- [Source registry](../SOURCES.md) records external contracts and retrieval dates.

## Proposal discipline

Every implementation session begins by reviewing its proposal. If empirical evidence invalidates a
decision, update the proposal and add an ADR; do not allow code to become the undocumented source
of product truth.

