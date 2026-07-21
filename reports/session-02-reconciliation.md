# Session 02 reconciliation report

- Session: 02 — Privacy, security and trust
- Date: 2026-07-21
- Result: automated exit gate passed; Session 03 must use the typed privacy boundary
- Public support claims: blocked; evidence covers only the synthetic fixture agent
- Agent configuration reads/writes: none

## Delivered contract

| Acceptance item | Evidence | Result |
|---|---|---:|
| Threat model and data classes | recursively closed `contracts/privacy/` registries and mutation tests | pass |
| Registry/runtime binding | canonical aggregate registry SHA-256 embedded in privacy, installer and HTTP packages; drift check only | pass |
| Policy lock/trust boundary | eight versioned semantic locks, append-only trusted history and nine coherent-mutation tests | pass; protected review/CI is external root |
| Strict ingress | duplicate-safe token decoder; Unicode/numeric/container/bounds/schema/catalog adversarial tests | pass |
| Typed persistence boundary | exact `SafeRecord`, `SafeError`, catalog, feature, redaction, lineage and log schemas | pass |
| Lineage and states | complete known fingerprint; keyed unknown structural fingerprint; typed absence; idempotent replay | pass |
| Identity secret | Linux fd-relative walk; `0600`, nlink 1, owner/type/inode binding, file+directory fsync | pass |
| Local HTTP | canonical loopback, explicit route modes, auth/origin/CSRF/method/header/body/rate tests | pass |
| Installer protocol | four typed builders; exact diff; revision consent; effective-setting/canary gate; virtual restore/remove | pass |
| Host/config access | seven justified accesses and a closed forbidden-mount population | pass |
| Container template | immutable images, hardening, internal network and no published placeholder ports | pass as unreachable static policy |
| Retention/deletion | all live/derived/export/backup surfaces and physical-erasure limitations | pass as contract |
| Raw-content canary | ten accepted plus ten rejection-path materialized artifacts, independently scanned | pass |
| Toolchain | immutable Go 1.26.5 bookworm image, GCC, vet, race and fuzz | pass |
| Current interface review | official Codex/Claude/Gemini/Cursor configuration and telemetry docs in `SOURCES.md` | pass |

The eight privacy registries are JSON-subset YAML and require no third-party Python package. Every
nested registry is closed. Their canonical aggregate hash is
`219e7f4c72ffc67c2ea764ee85da56dda68c2d0ff25afeb99d3f57c4d37cf3d2`; validators require the
same value in all three executable boundary packages. This aggregate detects accidental
registry/runtime drift, but a coherently updated registry, runtime and aggregate could satisfy it;
it is not the privacy-policy authority.

`contracts/privacy-policy-locks.yaml` separately binds a versioned canonical semantic digest for
each registry. Current digests must match the highest policy version, and every entry in a trusted
prior revision remains append-only. Direct review-controlled invariants—independent of the mutated
registries—fix the exact source/catalog allowlists, durable and nested Go schemas, prohibited raw/
content/free-text features, safe log schema, installer values, host accesses, loopback HTTP routes
and nonempty controls. Tests recompute the mutable aggregate for nine coherent weakening cases and
still reject all nine. An intentional semantic change appends a new policy version and digest while
preserving every old entry.

Before the first reviewed lock commit, or in a source archive without history, the checked-out lock
is the deterministic bootstrap authority. After bootstrap, validation compares against an explicit
trusted merge-base or HEAD revision when available, and protected review/CI must require that
history. Repository-local validation cannot resist simultaneous malicious replacement of the
validator, registries, runtime, locks, tests and Git history; protected review/CI is the external
root of trust. Archive trust is therefore only as strong as archive provenance.

## Ingress, lineage and state behavior

`privacy.IngressSanitizer` reads at most one MiB before any output path. Its token decoder rejects
duplicate names after JSON unescape and before map materialization, invalid UTF-8, unpaired UTF-16
escapes, non-finite/extreme numbers, empty/non-record containers, trailing/polyglot JSON and known
compressed frames. Depth, string, object, array and record bounds are then evaluated with sorted
field traversal so multi-failure classification is deterministic. The default record limit is 128.

The only admitted schema remains `fixture.agent-hook/1`. Its full registered source identity,
versions, fields, types, enums, catalogs, nested safe types and sanitizer contract contribute to the
known SHA-256 fingerprint. Unknown schemas receive device-keyed HMAC structural fingerprints over
source/version/schema shape and decoded field/type shape; distinct drift is visible without exposing
untrusted identifiers or values. Unknown fields hide their key as `$.[unknown]` and enter typed
metadata-only quarantine.

`unsupported`, `not_observed`, `redacted`, `unknown` and `numeric_zero` remain distinct typed values.
Missing `value_state` becomes `unknown`, not silently `not_observed`. Missing model/tool observations
carry a typed `not_observed` state and JSON null ID rather than a catalog sentinel string. Attachment
redaction is counted independently. Lineage contains adapter/schema versions, source pseudonym,
schema and contract hashes, sanitizer version, confidence and replay-stable idempotency identity.

## Raw-content exit gate

The sole raw artifact is `tests/fixtures/session-02/raw-canary-input.json`; it is synthetic and
contains no real user path, account, prompt or credential. It covers prompt, response, source,
tool input/output, command, path, environment, credential, high-entropy, exception and attachment
families plus declared casefold, NFC/NFD, Unicode-confusable, base64, hex, URL-encoded and fragmented
variants. Two rejection records exercise unknown-field and wrong-type paths.

`scripts/run_privacy_canary.py` runs the Go producer with network disabled, a read-only repository
mount and the pinned toolchain. It independently decodes and scans actual materialized bytes from all
ten accepted and ten rejection-path outputs, recomputes every hash, checks exact safe record fields,
and restores the export bytes from backup byte-for-byte. The closed report binds the fixture,
generator, source revision and toolchain in `reports/session-02-canary-results.json`.

| Accepted sink | Bytes | SHA-256 |
|---|---:|---|
| application logs | 336 | `f778280b121506fc8230ea72145082e672cc6acb42c8a696740894926f0247c4` |
| backup | 2,432 | `b76a2cdaaeae5bcaa67bb7482c6381a4963e48d146a77495c46f9f51455148a4` |
| dashboard network | 1,745 | `d2f1f3266cab984c0c79ba4bdee366b81a0c812e34b07259676f897c5684726b` |
| database | 1,670 | `6b514b5352a0adede913fea862b9d2d1c6a79b3108fe844f51c7a1f9ac6c4cee` |
| durable queue | 1,670 | `6b514b5352a0adede913fea862b9d2d1c6a79b3108fe844f51c7a1f9ac6c4cee` |
| error response | 39 | `99e0562472988acbdcd413387f0fa23cebfaf6e1032f9cac5e9bce6be7672498` |
| export | 1,677 | `8d83a3673b8788222623db8f174f92db9a99ffb49824c2cd32b80acc590caec4` |
| internal traces | 384 | `e274be522d212238a226aadef6152df45a17aa8f14ced669755ea16249ac82b5` |
| quarantine | 3 | `37517e5f3dc66819f61f5a7bb8ace1921282415f10551d2defa5c3eb0985b570` |
| retry queue | 42 | `9c655d4fc06910d5363039ceaa70f9a0baebff314cd588ebdbdf4993d7f131e9` |

The report contains a second exact hash table for rejection-path artifacts. Both internal and
independent scanners report zero canary and zero known secret-format matches. Backup stores exact
safe export bytes as base64 plus SHA-256, rather than a reserialized JSON approximation.

## Key file, HTTP and installer boundaries

The Linux key backend opens every path component fd-relatively with `O_DIRECTORY|O_NOFOLLOW`, checks
ancestor ownership/write safety and requires an owner-only `0700` leaf. Key create/load requires a
regular owner-owned `0600` inode with nlink 1, verifies name and directory inode bindings and fsyncs
file plus directory. A partial failed create is deliberately retained and rejected; automatic
pathname cleanup cannot unlink an attacker replacement. Non-Linux builds fail closed until an
equivalent syscall or OS-keychain backend is reviewed.

`localhttp.Guard` accepts only the exact loopback host/origin population and distinct secrets. It
stores fixed-size SHA-256 secret digests and compares them in constant time. UI/stream, hook/OTLP and
UI-mutation routes have explicit method/auth/origin/CSRF rules; every mode requires bearer auth,
mutation additionally requires same-origin and CSRF, and ingestion rejects browser Origin. Unknown,
CONNECT, TRACE and custom methods fail. Forwarded identity headers, noncanonical IPv4/IPv6 Host,
mapped loopback, remote peers, oversized bodies and rate excess all fail with safe responses.

Codex, Claude, Gemini and Cursor plans are built by separate typed functions. Required and forbidden
settings, locator kind, format, ownership and precedence checks come from the exact registry;
operations are derived from original-to-planned state. Consent binds plan, target, original/planned
hashes and nonce. Apply, rollback and removal reject revision races. Effective managed/environment
overrides, prompt/tool/raw logging, credentials, remote exporters, Gemini GCP/outfile/useCliAuth and
Cursor-hook-as-privacy-boundary misuse fail closed. A target/revision/source-bound runtime canary is
required before any future real write. Session 02's real-write authorizer always returns not
implemented, and no agent configuration or environment was read or changed.

## Container, SLO and supply chain

The Compose file is an intentionally unreachable Session 02 static policy template. It publishes no
ports because an application bound to container loopback is not reachable through ordinary Docker
port publishing. Session 09 owns a live tested topology. The template otherwise requires immutable
application/PostgreSQL images, non-root users, read-only roots, all capabilities dropped,
no-new-privileges, named volumes, one exact secret and one internal network; database ports, host
binds and Docker socket remain forbidden.

`contracts/privacy/sinks.yaml` maps every privacy sink ID one-to-one and in order to the ten
`raw-content-persisted-count` SLO evidence scopes. A rename, omission, duplication or new sink makes
the validator and SLO gate fail rather than producing numeric zero.

The Go module has no third-party dependencies. Tests use the Docker Official Image
`golang:1.26.5-bookworm` at
`sha256:1ecb7edf62a0408027bd5729dfd6b1b8766e578e8df93995b225dfd0944eb651`, which reports Go
1.26.5 and GCC 12.2.0. `reports/session-02-sbom.json` is an exact source/toolchain inventory, not a
signed production SPDX/CycloneDX SBOM. Production image vulnerability scan and provenance remain
release-blocking Session 09/10 work.

## Verification

- `python3 scripts/validate_contracts.py --json` — pass.
- `python3 scripts/validate_privacy.py --json` — pass.
- `python3 -m unittest discover -s tests -v` — 42/42 pass: 24 Session 01, 18 Session 02.
- `python3 scripts/run_go_tests.py` — 20 Go tests pass; `go vet ./...` passes;
  `CGO_ENABLED=1 go test -race ./internal/...` passes.
- bounded Go fuzz — pass, 11.0 seconds, 682 executions, two workers, five corpus items.
- `python3 scripts/run_privacy_canary.py --verify-report` — 20 materialized artifacts, zero leaks,
  exact backup bytes/checksum and report/source/toolchain bindings pass.
- `docker compose -f deploy/compose.security-baseline.yaml config --quiet` with deterministic
  placeholders — pass; no service, volume or network started.
- `git diff --check` and repository privacy scans — pass.

The synthetic fixture is 3,315 bytes; its closed result report is 3,618 bytes and source inventory
is 1,924 bytes. Test/build caches live in container tmpfs and disappear under `--rm`. No collector or
database was started, so production idle CPU/RSS, disk rate, database roles and retention execution
are not claimed. Retention defaults were not widened; the HMAC key is excluded from database,
export and backup.

## Residual risks

1. Only `fixture.agent-hook/1` is admitted. Real Codex/Claude/Gemini/Cursor fixtures, bounded runtime
   versions and live canaries do not exist; no public adapter support receipt is closed.
2. JSON is implemented. JSONL/compression fail closed and protobuf has only its pre-allocation frame
   gate; production hook/OTLP/transcript parsers and corpora belong to Sessions 03, 06 and 07.
3. The Compose artifact is unreachable by design. Production listener topology, database roles,
   health, restart and soak behavior need Session 09 integration evidence.
4. Secure rootless key storage is Linux-only; macOS, Windows, other Unix, OS keychain and rotation/
   re-key migration remain open.
5. The installer has no real atomic TOML/JSON writer. An explicit user-confirmed future session must
   implement filesystem apply/verify/rollback without weakening the current canary gate.
6. HTTP evidence is unit-level, not a live browser/container WebSocket/SSE/DNS test.
7. Production SBOM, signed provenance and vulnerability scanning remain open.
8. Two independent human classification reviews remain absent, so Supported/Beta is blocked.
9. The privacy-policy lock is review-controlled, not locally tamper-proof. Protected branch review/
   CI and trusted source/archive provenance remain the external root of trust.

## Exit decision

Session 03 may build canonical ingestion routes only when all raw inputs cross this strict typed
sanitizer before any queue, log, trace, quarantine or response and every new sink expands the closed
canary/SLO population. This decision does not authorize live agent config writes or publish support.
