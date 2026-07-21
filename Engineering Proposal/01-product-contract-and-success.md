# Session 01 — Product contract and success

## Purpose

Turn the idea into a bounded product contract before choosing implementation details. Kansoku must
answer what is installed, what was observable, what was used, whether collection is healthy, and
which components help or remain undiscovered.

## Primary users

1. A solo developer with several local agents and personal skills.
2. An agent/tool author validating discoverability and adoption.
3. An enthusiast comparing models, plugins and workflows over time.
4. Later, a small team using one workstation per developer; multi-user management is not MVP.

## Core user journeys

- Open one local URL and see whether collection is complete today.
- Understand activity across day/week/sprint/month/half-year/year/all-time/custom ranges.
- Drill from an agent into model, session, skill, plugin, MCP server/tool and evidence source.
- Find installed components that are never exposed, never invoked, failing, duplicated or stale.
- Compare current period to a previous equivalent period without confusing unknown data with zero.
- See prompt-size and activity distributions without storing prompt text.
- Explain why a metric changed: agent update, adapter drift, missing source, model switch or behavior.
- Export a privacy-safe report and restore the local database after failure.

## Questions Kansoku must answer

- Which agents/surfaces are active on this device and which versions produced the data?
- How much of each agent's observable lifecycle is covered?
- When do I work with agents, and how large/complex are my requests?
- Which models consume tokens and time, and where are fallbacks/errors concentrated?
- Which skills/plugins/MCP tools are installed, exposed, invoked and successful?
- Was a skill unused because there was no opportunity, poor discovery, failure or missing telemetry?
- Did an update break collection, or did I simply not use that agent?
- How much overhead does Kansoku itself add?

## Scope

### MVP scope

- One device and one local user.
- Codex and Claude complete adapters; Gemini validates portability.
- Inventory, sessions, prompts metadata, tokens/models, skills/plugins/MCP/tools, reliability.
- Local Docker Compose, PostgreSQL and loopback dashboard.
- Historical import where local transcripts exist.
- Daily integrity audit and visible completeness intervals.

### Explicit non-goals

- Capturing or searching raw conversations/code.
- Employee ranking, surveillance or a universal productivity score.
- Pretending inferred skill opportunities are facts.
- Modifying agent behavior automatically to improve dashboard numbers.
- Cloud synchronization, SaaS accounts, public endpoints or mobile apps in MVP.
- Guaranteed observation of proprietary cloud surfaces without an official export/API.

## Product SLO candidates

- **No silent loss:** known source breakage appears as degraded within 24 hours; within 5 minutes
  when the source has active heartbeats/events.
- **Ingest durability:** acknowledged local events survive container restart; target 99.99% for
  supported event paths under the tested fault model.
- **Freshness:** live events visible within 10 seconds p95; rollups within 2 minutes p95.
- **Dashboard:** common 30-day panels under 500 ms p95 on reference hardware.
- **Overhead:** collector under 1% average CPU when idle and bounded memory/disk budgets.
- **Privacy:** zero raw-content canary persistence across DB, logs, quarantine, backups and UI.
- **Explainability:** every chart exposes formula, source coverage and last refresh.

Targets become binding only after Session 01 benchmarks and are versioned thereafter.

## Brainstormed differentiators

- Coverage timeline shown alongside usage, preventing false conclusions during telemetry gaps.
- Component lifecycle funnel rather than a single usage count.
- Opportunity/activation analysis using skill eval examples, performed ephemerally.
- Version markers on charts to correlate regressions with agent/plugin/model updates.
- Cross-source reconciliation as a first-class feature, not an operations afterthought.
- Local-only comparative experiments for skill descriptions and workflows.
- Adapter health score decomposed into evidence, not one opaque percentage.

## Decisions to make in this session

1. Approve vocabulary and lifecycle state machine.
2. Approve MVP agents and what “complete support” means per capability.
3. Choose reference hardware/data volume for performance budgets.
4. Confirm sprint semantics: configurable duration/start day rather than hard-coded two weeks.
5. Decide whether cost estimation is MVP or post-MVP.
6. Approve initial retention defaults and export formats at product level.

## Deliverables

- Approved glossary and user stories.
- Metrics catalog review with priorities: must/should/could.
- Product SLO document and reference load profile.
- First ADR for Go/PostgreSQL/React baseline after spikes.
- Wireframe inventory showing every planned dashboard route.
- Explicit unsupported matrix so marketing cannot outrun evidence.

## Exit gate

The Session 01 automated contract gate requires every MVP chart to map to a user question and data
source; SLOs, privacy defaults, non-goals, lifecycle classifications and support-claim evidence
requirements must be machine-readable and mutation-tested before Session 02 begins. Two independent
human reviewers must still classify the same bounded fixture before any adapter capability may be
published as Supported or Beta. The automation does not fabricate those sign-offs. ADR 0002 records
this separation of the implementation-sequencing gate from the public-support governance gate.

## Session outcome — 2026-07-21

The product decisions are now explicit and machine-readable in `contracts/product.yaml`:

- sprint ranges use a configurable local anchor and duration (14 days by default);
- cost estimation is in MVP scope but is not a release blocker when exact tokens or a matching
  versioned price snapshot are unavailable; it renders `unknown`, never a synthetic zero;
- bounded default retention is 365 days for normalized metadata, 7 days for sanitized envelopes,
  30 days for metadata-only quarantine, and 1095 days for rollups;
- MVP exports are privacy-safe NDJSON and a versioned privacy manifest; Parquet is a `should`;
- support is published per capability and bounded agent version, never for an agent brand as a
  whole;
- the reference machine is an Apple M2 Pro with 16 GiB RAM; cross-architecture release gates remain
  required.

The six approved registries are `contracts/glossary.yaml`, `contracts/capabilities.yaml`,
`contracts/metrics.yaml`, `contracts/formula-version-locks.yaml`, `contracts/slo.yaml`, and
`contracts/dashboard.yaml`. Deterministic review fixtures prove that the rubric rejects inferred
promotion, hidden or ineligible SLO evidence scopes,
false passes through authorized exclusions and unauthorized exclusions while keeping unsupported,
not-observed, redacted, unknown and numeric zero distinct. All 34 metric formula versions bind a
stable population ID, expression, typed evaluator and fixture policy with a semantic SHA-256. Their
review-controlled version locks make existing `/1` identities append-only after the first trusted
commit. Normalized-record fixtures exercise population/filter/dedupe, selection by a preclassified
`in_interval` flag, exclusion semantics and ordering; they do not prove timestamp boundary
classification. p95 uses PostgreSQL continuous interpolation. Raw-event derivation, exact
`[from,to)` boundary tests and production SQL remain Sessions 03–04 gates. Two independent, exactly
bound human review receipts are still required before any public Supported/Beta claim; automation
does not fabricate them.
