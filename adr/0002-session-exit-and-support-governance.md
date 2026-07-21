# ADR 0002 — Separate session sequencing from public support governance

- Status: Accepted
- Date: 2026-07-21
- Scope: Session 01 exit interpretation and every future adapter support claim

## Context

The original Session 01 proposal phrased its exit gate as two reviewers independently classifying
the same event. Session 01 now has deterministic lifecycle fixtures, mutation-tested registries and
an automated contract gate, but no two human classification sign-offs. Treating the entire session
as blocked would prevent Session 02 from implementing the privacy evidence needed by the same
support contract. Treating automation as human approval would fabricate governance evidence.

## Decision

Kansoku uses two non-interchangeable gates:

1. The **automated contract gate** controls implementation sequencing. It passes when product
   semantics, formulas, states, SLO evidence requirements, dashboard ownership and public-support
   governance are machine-readable and their negative/mutation tests pass. Passing it allows the
   next implementation session to begin.
2. The **public adapter support governance gate** controls Supported and Beta capability claims.
   It remains blocked until an ordered range under an explicit parser/comparator scheme has one
   passing typed receipt for each of capability contract, privacy test, sanitized fixture replay,
   passive audit and canary or equivalent end-to-end evidence. Every receipt binds the exact
   adapter, capability and version range to registered, typed, repo-bounded canonical JSON files.
   The validator rejects traversal/symlink escape, recomputes exact bytes and SHA-256, and requires
   each artifact ID to be that content address. Two distinct approved human review receipts must
   bind that same tuple and cite verified classification fixtures and the exact evidence receipt set.

Session progress is never evidence for the public-support gate. Experimental and Unsupported
records remain documentation/probe states and must not be promoted by roadmap status.

## Consequences

- Session 01 may be reconciled as automated-contract complete without a human sign-off claim.
- Session 02 is unblocked and owns the first privacy evidence required by support governance.
- `contracts/capabilities.yaml` strictly validates Supported and Beta mutations, exact SemVer-core
  syntax and ordering, canonical artifact bytes/paths/content addresses, typed evidence receipts
  and exactly bound distinct human review receipts.
- Reports and UI must show the two gate statuses separately.
- A later human review records a bounded ID, reviewer identity, adapter/capability/version tuple,
  fixture IDs, cited evidence receipt IDs and result; this ADR does not create or imply any such
  record.

## Rejected alternatives

- **Mark the two reviewers approved from automated replay:** rejected because software checks are
  not independent human judgment.
- **Block Session 02 until human review:** rejected because privacy/replay/canary evidence is built
  in later sessions and product semantics are already deterministic enough to implement safely.
- **Allow Beta without the full governance evidence:** rejected because Beta is public and can still
  induce users to trust incomplete or privacy-unsafe collection.
