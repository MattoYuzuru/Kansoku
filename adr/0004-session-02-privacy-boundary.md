# ADR 0004 — Session 02 privacy boundary and proof scope

- Status: accepted
- Date: 2026-07-21
- Owners: Kansoku core
- Supersedes: none

## Context

Agent hooks, OTLP and transcripts may contain prompts, responses, source, tool bodies, paths,
environment values and credentials even when provider-side detail flags are disabled. A localhost
dashboard and a denylist applied at storage time do not prevent raw values from entering logs,
traces, retries, quarantine, errors, exports or backups. Installation also needs to enable narrowly
scoped telemetry without silently rewriting global agent configuration.

## Decision

1. Raw agent data exists only inside a bounded decoder/feature-extractor/redactor. The executable
   boundary is Go `privacy.Sanitizer`; only exact typed `SafeRecord` or `SafeError` values may leave.
   Source schema and catalog maps must equal a registered contract and cannot be widened by the
   caller. Unknown schema/field/enum/catalog input fails visibly into metadata-only quarantine.
2. The initial identity mechanism is a 32-byte Linux rootless secret file outside database/export/
   backup, reached through an fd-relative no-symlink directory walk, created once at mode `0600`,
   restricted to one link and verified by inode/path rebinding. Other platforms fail closed.
   Versioned HMAC domains
   pseudonymize source IDs, record IDs, idempotency keys and persisted installer paths. Prompt hashes,
   embeddings and prompt HMAC remain disabled.
3. The raw-content SLO is a closed ten-sink population: database, application logs, internal traces,
   durable queue, retry queue, quarantine, error response, dashboard network traffic, export and
   backup. A synthetic corpus must independently search all ten while proving approved aggregates,
   states, lineage, confidence and idempotency remain.
4. Installer authorization is per exact plan and target. Exact path/diff/backup/rollback data is
   transient preview material; the durable receipt contains only plan/target/result/revisions and a
   keyed path pseudonym. Apply and rollback refuse concurrent revision changes. Session 02 ships a
   virtual state model only and performs no live agent configuration write.
5. Local HTTP uses explicit UI-stream, hook/OTLP and UI-mutation modes over one guard: canonical
   loopback peer/Host/origin, bearer on every route, CSRF for UI mutation, method/body/rate limits,
   forwarded-header rejection and strict response headers. The Session 02 container template
   publishes no ports because its process is loopback-bound; Session 09 owns a tested reachable
   topology. The database remains unpublished on an internal network with named volumes and one
   exact database secret.
6. `deploy/compose.security-baseline.yaml` is a policy template, not a released runnable stack. The
   application image must be supplied as repository plus exact SHA-256 digest; Session 09 owns the
   production image, container reachability, database role enforcement, health behavior and soak.

## Rejected alternatives

- Persist then redact asynchronously: raw content would already have crossed durable/logging paths.
- Generic `map[string]any` canonical events: schema changes could silently introduce raw fields.
- Stable prompt hash: repeated or dictionary-guessable prompts remain linkable.
- One global installer approval: it cannot express different content exposure and rollback scopes.
- Persist exact configuration paths in audit: the audit becomes a sensitive path inventory.
- Treat a zero database search as the privacy gate: it omits queues, observability, UI, exports and
  backups.
- Claim production Compose security from a static template: image/runtime/network behavior requires
  the later integration and soak gates.

## Consequences

- Session 03 parsers must adapt raw source formats into the registered sanitizer rather than adding
  payload maps to the canonical core.
- A newly supported sink makes the privacy SLO incomplete until the sink registry, serializer,
  independent scanner and backup/delete coverage all expand.
- A new agent installation target requires an exact target/format contract and separate consent.
- The Session 02 canary is synthetic evidence only. It does not satisfy the bounded adapter privacy,
  replay, passive-audit, live-canary or two-human-review requirements for Supported/Beta.
- OS keychain integration, real atomic config writers, production database roles, signed SBOM/
  provenance and physical erasure behavior remain explicit residual gates rather than hidden claims.
