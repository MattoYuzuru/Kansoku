# Session 03 reconciliation report

- Session: 03 — Core observability architecture
- Date: 2026-07-21
- Result: automated exit gate passed; Session 04 may begin from the committed/reviewed result
- Public support claims: blocked; all source schemas and evidence are synthetic fixture-agent only
- Agent configuration reads/writes: none
- Network telemetry/export: none; protocol tests use process-local or loopback test traffic only

## Delivered contract

| Acceptance item | Evidence | Result |
|---|---|---:|
| Canonical event/evidence envelope | closed registry, typed Go allowlists and semantic lock | pass |
| Source and event lifecycles | exact primary/branch states and lifecycle-stage tests | pass |
| Confidence/completeness | tier ceilings, three-lane completeness and source-loss regression | pass |
| Idempotency and replay | lane-independent fact plus lane-specific evidence IDs; replay counters | pass |
| Correlation | exact, candidate, ambiguous and unmatched cases retain candidates | pass |
| Hook ingress | Session 02 loopback bearer guard, bounded JSON and post-durable acknowledgement | pass |
| OTLP/HTTP protobuf | logs/metrics/traces standard paths and binary protobuf responses | pass with gzip gap |
| OTLP/gRPC protobuf | real loopback unary services for logs/metrics/traces with bearer interceptor | pass with gzip gap |
| Transcript import | read-only JSONL, keyed path identity and atomic append-safe checkpoint | pass |
| Durable writer | bounded typed `0600` file, file fsync, atomic rename, directory fsync | pass as Session 03 spike |
| Crash/restart | before temp sync, before rename and after durable rename recovery | pass |
| Unknown schema | keyed metadata-only fingerprint/quarantine plus durable degraded incident | pass |
| Contradiction | distinct evidence retained, first fact preserved and incident opened | pass |
| Watermarks/gaps | monotonic sequence, gap count, clock skew, eligible stall vs inactivity | pass |
| Poison/backpressure/spool | bounded per-source failure, retryable capacity and typed `0600` spool | pass |
| Privacy | Session 02 sanitizer before every durable path and ten-sink canary regression | pass |
| Supply chain | exact modules, `go.sum`, offline vendor tree and CycloneDX 1.6 inventory | pass |

## Canonical and reconciliation behavior

The authority is the four JSON-subset YAML registries under `contracts/observability/`, with current
digests bound by `contracts/observability-policy-locks.yaml`. Independent validator invariants still
reject a coherent registry/lock edit that removes a prohibited field, ambiguity, gRPC signal,
durable acknowledgement, metadata-only quarantine, three-lane expectation or the explicit
pre-PostgreSQL boundary.

`tests/fixtures/session-03/shared-scenario.json` is the single synthetic logical event. Hook and OTLP
are native-tier evidence; transcript is reconstructed at the 0.95 ceiling. All three deliveries
produce one fact and three evidence records with complete status. Replay of an evidence identity
increments `replay_count`, while different lanes retain separate lineage. Reorder and late sequence
values never change fact identity or reduce the source high-water sequence. A disabled/degraded/error
lane is excluded from current completeness, lowering the fact to partial without deleting either the
fact or historical evidence.

The Session 02 privacy boundary now emits a keyed `Lineage.session_pseudonym` under appended policy
lock `privacy.ingress/2`. Native session text never crosses the boundary, while two different events
in the same session receive the same canonical session scope. This replaced an invalid draft mapping
that derived session scope from an event-specific record identity. The aggregate privacy registry
binding is `57af85c5fe779b6833d15bc9d62e2a9ec5550c58b7be3941bcbc152093c2cce7`; the Session 02 canary
report and reconciliation hashes were regenerated after the append-only transition.

Known OTLP resources require the exact synthetic adapter, version and schema tuple. The extractor
admits only eight Kansoku-owned safe attributes and discards log bodies, span names/events/links,
metric descriptions and unknown attributes before constructing a typed event. Unknown tuples use a
device-keyed HMAC over bounded source identity and protobuf message type; raw bytes, source strings
and unkeyed content hashes are absent from quarantine. Conflicting outcome/value/type evidence opens
`evidence_contradiction` and does not overwrite the established fact.

## Durability, restart and protocol boundary

`FileStore` is deliberately a pre-Session-04 correctness spike. Each mutation clones only typed
state, enforces the caller's byte ceiling, writes a `0600` temporary snapshot, fsyncs the file,
renames atomically and fsyncs the containing directory. The in-memory revision advances only after
that sequence. Event/evidence/idempotency outcome, source watermark, correlation and importer
checkpoint share one commit. A crash before file sync or rename reloads the previous revision; the
post-rename crash injection occurs after directory fsync and reloads the complete next revision.

The transcript importer opens a regular file read-only. Its durable locator is a device-keyed HMAC
of importer identity and canonical path, not the path itself; appending retains identity and resumes
exactly at the committed byte offset. Truncation below the checkpoint fails. Replacing a file at the
same canonical path with another file at least as long is not detected by this Session 03 spike and
remains a Session 04/05 file-revision identity requirement.

OTLP 1.10.0 binary protobuf is decoded using the official generated request/response types. HTTP
uses `application/x-protobuf` and the standard signal paths; gRPC registers the three official unary
Export services. Both routes require loopback and bearer authentication, enforce a one MiB message
ceiling, return safe permanent errors, and return retryable overload without payload logging. A
multi-record OTLP request commits record-by-record; failure after an earlier record can cause the
client to replay the whole request, which is safe through idempotency but is not an atomic batch.

OTLP 1.10.0 requires both no compression and gzip. The reviewed Session 02 contract still rejects
compressed ingress until bounded streaming decompression and bomb defenses exist. Therefore gzip is
rejected, JSON is absent, and this is explicitly an Experimental, non-fully-conformant OTLP spike.
No real adapter or public Supported/Beta evidence may cite it as full OTLP support.

## Supply chain and resource evidence

The Go protocol build pins `go.opentelemetry.io/proto/otlp v1.10.0`, `google.golang.org/grpc
v1.82.1` and `google.golang.org/protobuf v1.36.11`; six resolved runtime transitives make nine
vendored modules total. `vendor/` is 16 MiB. `reports/session-03-sbom.json` is deterministic
CycloneDX 1.6 source/module evidence with report SHA-256
`5293846a02917f6666f53e0244ad21e621e9c2c3f79227a85ad4b2981acddb7e`. Verification uses the
immutable Go 1.26.5 bookworm image at
`sha256:1ecb7edf62a0408027bd5729dfd6b1b8766e578e8df93995b225dfd0944eb651`, network disabled and
`-mod=vendor`.

The deterministic property load delivers 80 unique logical events three times (240 durable
deliveries) in 0.82 seconds on the local validation host and finishes with exactly 80 facts, 80
evidence records and replay count two for every evidence. This is correctness/resource evidence,
not a production throughput SLO. Snapshot serialization rewrites growing state and is expected to
scale quadratically; Session 04 PostgreSQL replaces it. The spool and store are bounded by explicit
call-site limits; tests/caches are tmpfs and containers are removed after every command.

## Verification

- `python3 scripts/validate_contracts.py --json` — pass.
- `python3 scripts/validate_privacy.py --json` — pass.
- `python3 scripts/validate_observability.py --json` — pass.
- `python3 -m unittest discover -s tests -v` — 49/49 pass: 24 Session 01, 18 Session 02,
  7 Session 03 independent contract/mutation tests.
- `python3 scripts/run_go_tests.py` — 39 Go tests pass, including 18 Session 03 tests;
  `go vet -mod=vendor ./...` and `CGO_ENABLED=1 go test -mod=vendor -race ./internal/...` pass.
- bounded Session 03 fuzz — pass, 8.2 seconds, 98,977 executions, two workers and 99 corpus items.
- deterministic load/property test — pass, 240 deliveries in 0.82 seconds.
- real loopback protocol conformance tests — pass for hook HTTP, OTLP/HTTP binary protobuf and
  OTLP/gRPC unary binary protobuf logs, metrics and traces.
- `python3 scripts/run_privacy_canary.py --verify-report` — twenty materialized accepted/rejection
  artifacts across ten sinks, zero exact canaries and zero secret-format matches; exact backup bytes
  and checksum pass.
- `python3 scripts/session03_supply_chain.py --verify` — nine components and all module/vendor/source
  hashes match.
- `git diff --check`, Go format check and scoped repository privacy scans — pass.

## Residual risks and downstream gates

1. OTLP gzip is a known mandatory-protocol gap; bounded streaming decompression needs a reviewed
   privacy policy transition before implementation.
2. `FileStore` is single-process, rewrite-based and lacks PostgreSQL constraints, partitions,
   migrations, production backup/restore and multi-writer locking. Session 04 owns replacement.
3. OTLP batches are record-atomic rather than batch-atomic; idempotent replay prevents inflation but
   can add replay evidence and work after a partial request.
4. Transcript identity does not detect same-path replacement when the replacement remains longer
   than the checkpoint. Session 04/05 needs a safe file-revision identity without raw path/content.
5. Late events mark watermarks and facts correctly, but persistent rollup invalidation/recomputation
   starts only after Session 04 defines rollups.
6. No real agent fixture, runtime canary or agent configuration was observed or changed. All real
   adapters remain Experimental and public Supported/Beta governance remains blocked.
7. CycloneDX evidence is unsigned and no production application image exists; vulnerability scan,
   signed provenance and production resource/soak/backup evidence remain Session 09/10 gates.
