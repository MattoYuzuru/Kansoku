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

