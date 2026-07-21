# TDD 02 — Privacy and security architecture

## Trust boundaries

```text
untrusted agent payload/transcript
        |
        v
[bounded decoder] -> [source allowlist] -> [feature extractor] -> [redactor]
        |                                                        |
        +---- reject safely / metadata-only incident <-----------+
                                                                 v
                                                    sanitized canonical event
                                                                 |
                                                   durable queue / PostgreSQL
```

The decoder/feature extractor is the only component allowed transient access to prohibited content.
It MUST run before logs, traces, retries and quarantine serialization.

## Ingress API

Each source parser implements:

```go
type Sanitizer interface {
    InspectMetadata(reader io.Reader, limit Limits) (Fingerprint, error)
    DecodeAndExtract(reader io.Reader, schema SourceSchema) ([]SafeRecord, SafeError)
}
```

`SafeError` contains source, schema fingerprint, field path, category and byte counts—never values.
Parsers operate streaming where possible, enforce depth/array/string/total limits and reject
compression bombs, invalid UTF-8 policy violations and oversized protobuf frames.

## Data policy enforcement

- Persistence structs are generated from an allowlisted schema and do not include raw-content fields.
- Generic `map[string]any` cannot be passed across the sanitization package boundary.
- Static analysis/lint rules forbid logging payload objects and `%+v` on source errors.
- OpenTelemetry for Kansoku itself uses a safe attribute registry.
- Database roles deny adapter processes direct access; adapters submit to ingress only.
- Metadata quarantine accepts only fingerprint/count/reason/sample-shape-without-values.

## Prompt feature extraction

Approved functions return integers/enums only. Word counting must be documented by Unicode rules;
language is coarse and optional. URL/file-reference metrics count patterns without retaining hosts or
paths. Explicit skill names may be retained only after matching against current inventory, so random
prompt text is not stored as a “name”.

Buffers are not copied unnecessarily. “Memory zeroization” cannot be guaranteed by every runtime,
so the stronger guarantee is no durable serialization, bounded lifetime and tests across all sinks.

## Pseudonymization and keys

- Generate device-scoped HMAC key on first run.
- Store key in host OS keychain or rootless secret file with mode `0600`, outside PostgreSQL volume.
- Canonicalize paths per OS before HMAC; include entity type and versioned salt domain.
- Rotating the identity key intentionally breaks cross-rotation linkage unless a local re-key
  migration is explicitly requested.
- User aliases are stored separately and escaped as untrusted display text.

## Local HTTP security

- Listen on loopback only; reject non-loopback peer and unexpected `Host`.
- Mutating endpoints require a local bearer/session secret and CSRF protection.
- Set CSP, `frame-ancestors 'none'`, no MIME sniffing and strict referrer policy.
- WebSocket/SSE connections use the same origin/auth checks.
- No CORS wildcard.
- Rate/payload limits apply even on localhost.

## Container baseline

- non-root UID/GID; read-only root filesystem;
- `cap_drop: [ALL]`, `security_opt: [no-new-privileges:true]`;
- only named data volume writable;
- exact read-only host mounts configured per adapter importer;
- no Docker socket, home root, SSH/GPG/keychain or provider credential mounts;
- separate internal database network; database port not published;
- pinned release images, SBOM and vulnerability scanning.

Hooks send events to loopback and do not receive database credentials.

## Retention/deletion

Retention policies are rows with version/effective time. A deletion plan reports partitions,
rollups, exports and backups affected. Execution is explicit, audited and verified with post-delete
queries. PostgreSQL vacuum semantics and backup immutability limitations are documented honestly.

## Tests

- raw canary strings across prompt, response, command, path, source, exception and compressed input;
- secret formats and high-entropy values;
- malicious JSON/protobuf/JSONL, path traversal, symlink and decompression cases;
- database/log/trace/quarantine/export/backup/UI/network capture searches for canary;
- loopback/Host/CORS/CSRF/DNS-rebinding checks;
- container permission/mount/egress assertions;
- installer preview/consent/rollback and config race tests;
- dependency/SBOM and reproducible build checks.

## Exit gate

The privacy test suite verifies every durable/output sink, fuzzers find no unbounded decoder path,
container policy matches the manifest, and configuration changes are reversible and separately
authorized.

## Implemented design (2026-07-21)

The executable boundary is the stdlib-only Go module under `internal/`:

The complete registry set is `contracts/privacy/threat-model.yaml`,
`contracts/privacy/data-classes.yaml`, `contracts/privacy/ingress.yaml`,
`contracts/privacy/sinks.yaml`, `contracts/privacy/installer.yaml`,
`contracts/privacy/host-access.yaml`, `contracts/privacy/deployment.yaml`, and
`contracts/privacy/retention.yaml`.

`contracts/privacy-policy-locks.yaml` is a separate review-controlled registry of versioned
canonical semantic digests for all eight policy registries. The highest version for each registry
must match current semantics; every entry present in a trusted prior revision remains append-only.
The validator also carries independent exact security invariants and hard-coded Go field/type
schemas, so a recomputed mutable aggregate cannot authorize a policy weakening. The aggregate
embedded in Go remains a drift check only. Archive/bootstrap mode deterministically trusts the
checked-out lock; protected review/CI supplies the trusted historical revision after bootstrap.
Simultaneous malicious replacement of validator, lock, tests and Git history is outside the power of
repository-local validation and remains an explicit external trust boundary. See
`adr/0005-privacy-policy-lock-and-trust-root.md`.

- `privacy.IngressSanitizer` enforces one MiB total/frame, depth 16, 1,024 array/object entries,
  64 KiB strings, 128-byte finite numbers and 128 records. A token decoder rejects duplicate names
  before map collapse, unpaired UTF-16 escapes, non-object/non-record containers, trailing/polyglot
  JSON and gzip/ZIP/bzip inputs; semantic traversal is sorted for deterministic multi-failure
  classification. Future protobuf routes must call the tested frame-length gate.
- The only admitted schema is the bounded synthetic `fixture.agent-hook/1`. Its field/catalog maps
  must equal the authoritative contract exactly; a caller cannot widen them. Unknown schema/field,
  enum or catalog values become typed metadata-only `SafeError`, never raw quarantine.
- `SafeRecord`, catalog observations, prompt features, redaction counts, lineage and safe log events
  have exact closed schemas in `contracts/privacy/ingress.yaml`. Missing model/tool IDs use typed
  states plus JSON null, never string sentinels; attachment redaction is counted separately. Known
  fingerprints cover the full registered contract and unknown schemas receive distinct device-keyed
  structural fingerprints. HMAC domains create source pseudonyms, record IDs and idempotency keys.
- The Linux rootless key backend walks every directory with fd-relative `openat`, `O_DIRECTORY` and
  `O_NOFOLLOW`, checks owner/mode/type/link count and inode/path rebinding, and fsyncs file plus
  directory. It never path-unlinks a possibly replaced failure artifact. Non-Linux backends fail
  closed until an equivalent syscall or OS-keychain implementation is reviewed.
- Prompt features count maximal Unicode letter/number runs, bytes/runes/lines, coarse script,
  fences, attachments and reference patterns. Only exact inventory IDs may survive as component
  mentions. Prompt hashes, embeddings and optional prompt HMAC remain disabled.
- `SerializeAllSinks` accepts only `[]SafeRecord` and `*SafeError`; it emits the ten closed scopes in
  `contracts/privacy/sinks.yaml`, mapped one-to-one to the raw-content SLO scopes. Backup stores the
  exact safe export bytes as base64 plus SHA-256 and is isolated-restored byte-for-byte.
- `localhttp.Guard` accepts only canonical loopback hosts/origins and distinct secrets, compares
  fixed-size SHA-256 digests in constant time, rejects forwarded headers and unknown/CONNECT/TRACE
  methods, and applies explicit UI-stream, hook/OTLP and UI-mutation modes. All modes require bearer
  auth; UI mutation additionally requires same-origin and CSRF. SSE/WebSocket use UI-stream mode.
- `installer` derives exact operations through four typed builders, binds consent to target/plan and
  both revisions, verifies effective settings plus a target-bound runtime canary, and models virtual
  apply/rollback/removal with path-pseudonymous audit. Real mutation is unavailable in Session 02.

Runnable evidence is `scripts/validate_privacy.py`, `scripts/run_go_tests.py`,
`scripts/run_privacy_canary.py`, `tests/test_privacy_contracts.py`, the Go package tests/fuzzer, and
`reports/session-02-canary-results.json`. The fuzzer proves bounded behavior for the implemented
JSON route over its run, not every future transcript/OTLP/protobuf parser. Database roles,
production queues, actual PostgreSQL deletion, browser networking, OS keychain integration, live
agent config writes, signed images and full restore/soak remain owned by Sessions 03–10. The pinned
Go 1.26.5 bookworm toolchain includes GCC and runs the race detector over every `internal/` package.
