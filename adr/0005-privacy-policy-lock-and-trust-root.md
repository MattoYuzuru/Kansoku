# ADR 0005 — Privacy policy lock and external trust root

- Status: accepted
- Date: 2026-07-21
- Owners: Kansoku core
- Supersedes: none

## Context

The canonical aggregate SHA-256 embedded in the Go boundary detects accidental registry/runtime
drift, but it is not an independent policy authority. A coherent change can weaken a privacy
registry, update the Go implementation and replace that aggregate in the same patch. Recursive
closure has the same limitation when the validator treats the current registry as its expected
value.

Session 02 needs a review-controlled identity for every security-significant privacy policy and
direct invariants that do not derive their expected values from the registry being checked. It also
needs deterministic behavior in an exported archive, where Git history may not be available,
without pretending that repository-local code is a cryptographic root of trust.

## Decision

1. `contracts/privacy-policy-locks.yaml` binds a monotonically versioned policy identity to the
   canonical semantic SHA-256 of each of the eight privacy registries. Canonicalization is sorted,
   compact UTF-8 JSON over the parsed JSON-subset-YAML value, not source bytes.
2. Before the first reviewed commit containing the lock, or in an archive without Git history, the
   checked-out lock is the deterministic bootstrap source of truth. After bootstrap, validation
   compares every prior entry with an explicit trusted ref (HEAD by default when present). Existing
   version entries are append-only and byte-for-byte immutable at the parsed semantic level.
3. A reviewed registry semantic change appends a new ordinal for that registry and its new digest;
   it never changes or removes an earlier ordinal. The highest ordinal must bind the current
   registry semantics.
4. The validator also carries independent exact invariants for source/catalog allowlists, durable
   and nested Go schemas, safe logs, installer values, host accesses, loopback HTTP routes and
   nonempty control sets. Go field/type schemas are checked directly rather than inferred from the
   current ingress registry.
5. The old aggregate registry/runtime SHA-256 remains as a useful drift/reconciliation check. It is
   explicitly not the policy trust anchor.
6. Protected review or CI supplies the external trusted revision. Repository-local validation
   cannot resist a simultaneous malicious rewrite of the validator, registries, runtime, locks,
   tests and Git history. This limitation is part of the contract, not an omitted threat claim.

## Rejected alternatives

- Treat the self-updated aggregate as a security lock: a coherent policy/runtime edit passes it.
- Pin source-file bytes: formatting-only changes would require a policy version and obscure the
  semantic boundary.
- Require Git history in all environments: source archives and bootstrap checkouts would become
  nondeterministic or unverifiable.
- Claim local tamper resistance: an attacker allowed to replace every local authority can replace
  the result of any repository-local check.

## Consequences

- Review/CI must pass a protected trusted ref and require history once the bootstrap lock is in the
  protected branch.
- Intentional policy evolution carries an explicit new policy version and preserves all old locks.
- Nine coherent weakening cases remain rejected even when their mutable registry/runtime aggregate
  is recomputed.
- A source archive can still validate deterministically, but its bootstrap lock is only as trusted
  as the archive provenance.
