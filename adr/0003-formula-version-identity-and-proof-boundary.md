# ADR 0003 — Formula version identity and Session 01 proof boundary

- Status: Accepted
- Date: 2026-07-21
- Scope: Formula-version changes and the normalized-record fixture harness

## Context

A digest stored beside the formula it signs is not an independent version identity: registry,
fixture and digest can be rewritten coherently while retaining `/1`. Session 01 fixtures also carry
only a preclassified `in_interval` flag, not timestamps at the exact `from` and `to` boundaries.

## Decision

`contracts/formula-version-locks.yaml` is the review-controlled version-to-semantic-digest source of
truth. The validator recomputes each metric semantic digest from its population, expression, exact
typed evaluator, fixture policy and ratio operands, then requires the digest to match both the
formula fixture and its independent lock entry.

Existing lock entries are append-only after their first trusted commit. A semantic change requires
a new formula version, population/evaluator version IDs and lock entry; the old lock remains. The
validator compares current locks with `HEAD` when that file exists there, and CI/review may provide
an explicit trusted merge-base through `--formula-history-ref`. Archive and pre-first-commit runs use
`--formula-history-ref none`; in that deterministic bootstrap mode the current lock file is trusted
through review rather than Git history.

Evaluator schemas have exact fields, implementations, parameter names, parameter values and types.
Record access fails as an explicit validation result instead of raising `KeyError`. Ratio fields and
declared numerator/denominator semantics must be distinct, and normalized ratio records require
`0 <= numerator <= denominator`.

The Session 01 harness proves aggregation after an upstream producer has classified the boolean
`in_interval`. It does not prove timestamp classification or exact `[from,to)` boundary behavior;
those tests remain Sessions 03–04 gates.

## Trust boundary

The append-only check detects a coherent rewrite of registry, fixture and current lock when a
trusted historical lock is supplied or available in Git. No repository-local validator can defend
against a simultaneous malicious rewrite of validator, registries, locks, tests and Git history.
That case requires an external trusted revision, protected review/CI and reviewer judgment. The
bootstrap lock is therefore not described as immutable until its first reviewed commit.

## Consequences

- Reusing `/1` for changed semantics fails against the independent current lock.
- Rewriting every current contract file still fails against a trusted historical lock.
- Clean archive validation is repeatable and does not require `.git`.
- Session 01 makes no timestamp-boundary proof claim.
