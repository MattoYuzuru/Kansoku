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
11. [Real-agent gap closure](11-real-agent-gap-closure.md)
12. [Incident workbench and safe quarantine](12-incident-workbench-and-safe-quarantine.md)
13. [Agent evidence bridge and model observatory](13-agent-evidence-and-model-observatory.md)
14. [Skill observatory](14-skill-observatory.md)
15. [MCP observatory](15-mcp-observatory.md)
16. [Plugin observatory](16-plugin-observatory.md)
17. [System self-observability](17-system-self-observability.md)
18. [Design system and content access](18-design-system-and-content-access.md)
19. [Local control plane and assisted remediation](19-local-control-plane-and-assisted-remediation.md)
20. [Opportunity evaluation lab](20-opportunity-evaluation-lab.md)

## Shared contracts

- [Canonical event contract](canonical-event-contract.md)
- [Adapter compatibility matrix](adapter-compatibility-matrix.md)
- [Engineering metrics catalog](../Engineering%20Proposal/metrics-catalog.md)

## Document convention

Normative language uses MUST/SHOULD/MAY. Every TDD includes boundaries, interfaces, persistence,
failure behavior, tests and an exit gate. When implementation differs, add an ADR and update the
document in the same change.
