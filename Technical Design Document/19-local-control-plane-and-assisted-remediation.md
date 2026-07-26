# TDD 19 — Local control plane and assisted remediation

## Prerequisite

Session 19 cannot begin until Sessions 12–18 reconciliation reports are accepted and a new threat
model approves process launch, filesystem mutation, restart and content handoff.

## Change execution

All component configuration writes reuse `ChangePlan`, exact preview, precondition hash, backup,
approval, narrow ownership, apply, verification and rollback. Adapters provide plans; core
orchestrates capabilities without brand branching. Collector workers remain read-only.

## Assisted incident run

Persist only metadata lifecycle: proposed, approved, running, awaiting_change_approval,
verifying, completed/failed/cancelled. Work occurs in an isolated workspace/branch with explicit
agent/model, token/time budget and permission set. Raw prompts, responses and tool content are not
ingested as Kansoku telemetry.

Implementation, commit and service restart are distinct approvals. Incident recovery still requires
fresh detector evidence.

## Exit gate

Canary configuration changes and rolls back without altering unrelated bytes; every partial failure
is recoverable; and no agent action, commit, restart or resolution bypasses its approval/evidence
gate.
