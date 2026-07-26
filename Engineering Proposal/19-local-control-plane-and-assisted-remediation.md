# Session 19 — Local control plane and assisted remediation

## Status

Deferred until read-only observability is trusted. Further threat-model review is required.

## Purpose

Add explicit, reversible local changes for component configuration and a safe agent-assisted
incident workflow without allowing collection to mutate agents implicitly.

## Scope

- enable/disable skill, plugin and MCP configuration through the existing ChangePlan protocol;
- exact preview, precondition hash, backup, restart impact, verification and rollback;
- metadata-only incident bundle passed to a user-selected agent;
- isolated workspace/branch, budget, timeout and permissions;
- diagnosis and plan before any edit;
- separate confirmation for implementation, commit and restart;
- recovery only from later evidence/audit, never from the agent declaring success.

The normal collector remains read-only. No unattended retries or writes occur.

## Deliverables

- mutation authorization and CSRF/session design;
- adapter-owned configuration plans with generic core orchestration;
- component control UI;
- assisted incident run lifecycle and audit trail;
- rollback, dirty-worktree, timeout, partial-failure and restart tests.

## Exit gate

A canary component can be disabled and re-enabled with exact preview and rollback while unrelated
configuration remains byte-identical. A synthetic incident can produce an isolated agent diagnosis,
but no file, commit, restart or incident resolution occurs without its required confirmation and
fresh verification evidence.
