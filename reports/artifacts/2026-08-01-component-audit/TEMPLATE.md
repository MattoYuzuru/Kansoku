# Component audit — <COMPONENT NAME>

- **Lane id**: `<NN-slug>`
- **Owner surface(s)**: routes, API endpoints, packages, contracts this lane owns
- **Auditor**: sre-agent lane subagent
- **Date**: 2026-08-01
- **Test bed**: `/tmp/kansoku-testbed` (native PostgreSQL 18 + host-run `kansoku serve`, no Docker in this VM)
- **Verdict**: `broken` | `partially-correct` | `correct-with-risks` | `correct`

## 1. What this component must do

Reconstructed from `contracts/*`, the paired Engineering Proposal / TDD, and `AGENTS.md`.
State the requirement before judging behaviour. Cite file:line.

## 2. Evidence chain executed

One row per executed test. `Invoked` = the entity that was made to happen;
`Expected surface` = where it must show up; `Observed` = what actually showed up.

| # | Invoked entity | How it was driven | Expected surface | Observed | Result |
|---|---|---|---|---|---|

Raw evidence files: `evidence/<lane>/...` (commands, HTTP responses, SQL output, logs).

## 3. Findings

Repeat this block per finding. Ordered most severe first.

### F-<NN>-<n> — <one-line defect statement>

- **Class**: confirmed defect | probable defect | design risk | missing evidence | documentation inconsistency | intentional behaviour | unverified hypothesis
- **Severity**: P0 | P1 | P2 | P3
- **Location**: `path/to/file.go:LINE` (+ contract / route / endpoint)
- **Requirement violated**: contract id or doc reference
- **Preconditions**: exact state needed to reproduce
- **Reproduction**: exact commands, copy-pasteable, against the test bed
- **Expected**: …
- **Actual**: …
- **Evidence**: file/line of captured output
- **Why it happens (root cause)**: the exact broken point, not a symptom
- **How to fix**: concrete change, files to touch, invariants to preserve
- **Regression test to add**: exact test name + package + what it must assert
- **Confidence**: high | medium | low

## 4. Not-reproduced / rejected claims

Claims investigated and rejected, with the evidence that rejected them.
Prevents the fix agent from re-chasing them.

## 5. Docs to actualise

| Doc | Statement that is now wrong | Correct statement |
|---|---|---|

## 6. Dead weight / optimisation candidates in this lane

Code, fixtures, deps, generated files that can be removed or collapsed with
identical behaviour. Include the evidence that they are unused.

## 7. Residual risk / not covered

What this lane could not test in this environment and what would be needed.

## 8. Fix order for this lane

Ordered checklist a single fix agent can execute top-to-bottom.

1. …
