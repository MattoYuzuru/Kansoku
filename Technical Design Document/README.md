# Technical Design Documents

These documents turn each proposal into implementable contracts. They are intentionally detailed
enough that a future session can derive tests before code, while leaving benchmark-dependent ADRs
open until measured.

## Sessions

1. [Product contracts and SLO harness](01-product-contract-and-success.md)
2. [Privacy and security architecture](02-privacy-security-and-trust.md)
3. [Canonical ingestion architecture](03-observability-architecture.md)
4. [PostgreSQL data and analytics design](04-data-platform-and-metrics.md)
5. [Adapter SDK and inventory graph](05-adapter-sdk-and-inventory.md)
6. [Codex adapter](06-codex-integration.md)
7. [Claude, Gemini and future adapters](07-claude-gemini-and-next-agents.md)
8. [Daily integrity and drift engine](08-integrity-drift-and-daily-audit.md)
9. [Backend, API and local operations](09-local-runtime-and-operations.md)
10. [Frontend, hardening and release](10-dashboard-hardening-and-evolution.md)

## Shared contracts

- [Canonical event contract](canonical-event-contract.md)
- [Adapter compatibility matrix](adapter-compatibility-matrix.md)
- [Engineering metrics catalog](../Engineering%20Proposal/metrics-catalog.md)

## Document convention

Normative language uses MUST/SHOULD/MAY. Every TDD includes boundaries, interfaces, persistence,
failure behavior, tests and an exit gate. When implementation differs, add an ADR and update the
document in the same change.

