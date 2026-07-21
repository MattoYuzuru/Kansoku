# Session 02 — Privacy, security and trust

## Purpose

Make local-first meaningful. A localhost dashboard is not automatically private: hooks may expose
prompts, OTLP attributes can contain tool arguments, transcripts contain full conversations, and a
container with broad host mounts can become the largest privacy risk in the system.

## Threat model

Protect against:

- accidental persistence of prompt/response/code/tool content;
- credentials in environment, commands, MCP payloads or telemetry headers;
- malicious or malformed transcript/OTLP/hook input;
- dashboard access from LAN, browser extensions or DNS rebinding;
- compromised third-party adapter/plugin;
- over-broad host filesystem mounts;
- data recovery from backups, quarantine or application logs;
- supply-chain compromise in images/frontend assets;
- silent enabling of agent telemetry settings with wider disclosure;
- misleading analytics used for human performance evaluation.

The initial model does not defend against an administrator/root user already controlling the host.

## Data classification

| Class | Examples | Default treatment |
|---|---|---|
| Prohibited content | Raw prompts, responses, source, tool input/output, secrets | Process transiently only; never persist |
| Sensitive identifiers | Raw paths, repo names, email/account IDs | HMAC pseudonym or opt-in alias |
| Operational metadata | timestamps, versions, token counts, latency, result class | Persist with retention |
| Derived metadata | word count, percentiles, opportunity confidence | Persist with lineage |
| Public catalog data | agent versions, model price snapshot, docs URL | Persist/version normally |

## Prompt feature boundary

The hook/adapter may see prompt content because the agent exposes it. It computes only approved
features in memory: byte/character/word/line counts, coarse language/script, code-fence/attachment
counts and explicit component mention metadata. The content buffer is overwritten/released and
never enters logs, traces, errors or retry queues.

Stable prompt hashes and embeddings are excluded by default. Even a hash can reveal repeated or
dictionary-guessable prompts; an optional rotating keyed HMAC may support short-window duplicate
rates after a separate privacy review.

## Local deployment boundary

- Bind UI and ingestion ports to `127.0.0.1`/`::1` only.
- No CDN, remote fonts, analytics pixels or automatic crash upload.
- No Docker socket mount.
- Agent config and transcript roots mounted read-only and only when an importer needs them.
- Separate writable volume for Kansoku data.
- Containers run non-root, read-only root filesystem where practical, dropped capabilities and
  `no-new-privileges`.
- Authenticated local session or CSRF-resistant loopback token for mutating dashboard actions.
- Strict CSP and protection against DNS rebinding/Host-header abuse.

## Installation and consent

Kansoku must not silently edit global Codex/Claude/Gemini/Cursor settings. The installer produces a
versioned plan, exact diff, disclosed fields, backup path and rollback command. The user approves
each agent target. Read-only inventory can run without configuration changes.

Telemetry detail flags are minimized. For example, if an agent requires a flag that also exposes
tool input in order to emit skill names, the collector must redact at ingress and the UI must show
that privacy trade-off before installation.

## Network policy

Runtime collection has no required internet egress. Optional allowlisted jobs may fetch public
version/changelog/price metadata without sending local identifiers or statistics. Egress status is
visible and auditable. Live agent canaries consume provider resources only after explicit opt-in and
have a daily budget.

## Retention proposal

- normalized metadata: 365 days by default;
- sanitized ingest envelopes: 7 days; metadata-only quarantine: 30 days;
- hourly/daily rollups: 1095 days by default;
- transient raw buffers: process lifetime only;
- audit/installer records: one year by default;
- operational SLO samples: 90 days;
- backups: 7 daily and 4 weekly by default, with restore tests and explicit deletion.

Users may preview a different bounded or indefinite policy. Applying or deleting data remains
explicit, audited and verifiable across partitions, quarantine, exports and backups where possible.
The accepted defaults and export formats live in `contracts/product.yaml`.

## Security UX

The dashboard includes a privacy center: active data classes, configured sources, host mounts,
retention, recent redactions, egress attempts, raw-canary status and one-click export of the current
privacy manifest. It never shows content samples to “prove” redaction.

## Deliverables

- Threat model and abuse-case tests.
- Data-classification registry enforced in schemas.
- Ingress redaction library and prohibited-field denylist/allowlist strategy.
- Privacy-safe logging standard.
- Installer consent/rollback protocol.
- Network and container hardening baseline.
- Raw-content canary corpus and end-to-end negative tests.

## Exit gate

Synthetic secrets, prompts, code and tool payloads cannot be found in database, logs, traces,
quarantine, error responses, dashboard network traffic, export or backup. All required host/config
access is enumerated and justified; the user can disable and remove Kansoku without damaging agent
configuration.

## Implemented contract (2026-07-21)

Session 02 implements this proposal through the machine-readable registries in
`contracts/privacy/`, the review-controlled `contracts/privacy-policy-locks.yaml`, ADRs
`adr/0004-session-02-privacy-boundary.md` and
`adr/0005-privacy-policy-lock-and-trust-root.md`, and the reconciliation report
`reports/session-02-reconciliation.md`.

The embedded aggregate registry SHA-256 is only a runtime drift check. Versioned semantic policy
locks and exact validator invariants independently reject coherent registry/runtime/checksum
weakenings. Archive/bootstrap validation uses the checked-out lock deterministically; after its first
trusted commit, old entries are append-only against a protected trusted ref. Protected review/CI is
the external root: no repository-local check can resist simultaneous malicious replacement of its
validator, lock, tests and Git history.

The accepted sink population is stricter than the original eight named outputs: durable and retry
queues are separate mandatory scopes, for ten total sinks. The synthetic canary contains prompt,
response, source, tool input/output, command, path, environment, credential, opaque high-entropy,
exception and attachment families. Both the Go scanner and an independent Python scanner find zero
matches across accepted and rejected materialized paths, including casefold, Unicode normalization/
confusable, base64, hexadecimal, URL encoding and fragmentation variants, while approved prompt
counts, versioned source lineage, confidence and idempotency survive.

The installer is deliberately a protocol implementation, not a live config writer in this session.
It renders an exact transient path/diff/backup/rollback preview, binds consent to the exact plan and
agent target and both revisions, rejects concurrent revision changes, models rollback/removal, and
persists only a keyed path pseudonym. Typed per-agent plans verify effective managed/environment
precedence and require a target-bound runtime canary before any future real write; the Session 02
real-write entry point always fails closed. No Codex, Claude, Gemini or Cursor configuration was
read or changed.

The Compose artifact is a hardening policy template for Session 09 rather than a released runtime:
it requires a caller-supplied digest-only application image, pins PostgreSQL, uses an internal
network and named volumes, and applies non-root/read-only/capability-drop/no-new-privileges controls.
It intentionally publishes no ports: a process bound to container loopback cannot be reached via
ordinary Docker publishing, and Session 09 owns the tested secure topology. Production image, SBOM,
vulnerability, reachability and soak proof remain release gates rather than claims of this proposal.
