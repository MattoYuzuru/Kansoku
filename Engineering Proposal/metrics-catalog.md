# Metrics catalog

This is the product-facing catalog. Exact tables and query contracts are defined in the paired TDDs.
Every metric must declare unit, dimensions, numerator, denominator, completeness, provenance and
whether it is exact, estimated, inferred or unavailable.

## Session 01 priority review

`contracts/metrics.yaml` is the authoritative MVP registry and assigns stable IDs plus `must`,
`should` or `could` priority. The review outcome is:

- **Must:** collection coverage/completeness/freshness/durability, agent inventory, sessions and
  prompt counts/size, provider tokens/model reliability, component lifecycle, tool/MCP reliability,
  unknown schemas/reconciliation/drift, raw-content canary, collector/database/query overhead and
  backup/restore age.
- **Should:** versioned cost estimates when exact provider token categories and a matching price
  snapshot exist; Parquet export and deeper distributions that do not change core semantics.
- **Could:** opportunity classifiers, causal workflow experiments, rework proxies, panel-usage
  telemetry, co-activation/sequence mining and advanced quality correlations.

Catalog entries without a registered metric ID remain backlog questions, not implicit UI or support
claims. Before a panel uses one, it must gain a versioned formula, population, lineage,
completeness behavior and deterministic complete/unknown/degraded fixtures.

## Universal dimensions

- time range, timezone and comparison period;
- device/installation, agent, surface and agent version;
- project alias, session, turn and subagent;
- model/provider and authentication mode when safely available;
- component kind: skill, plugin, MCP server, MCP tool, built-in tool, custom command, hook;
- component identity, version, source/marketplace and enabled state;
- evidence source, adapter version, confidence and completeness status;
- outcome, error class, permission mode and execution environment.

## Fleet and inventory

- installed/enabled/discovered agents by version and surface;
- first seen, last seen, update time, active days and session count;
- installed/enabled/exposed components and version/source collisions;
- skills description budget, duplicate names, shadowed paths and discoverability pressure;
- plugins by marketplace, version, bundled skills/MCP/hooks and update age;
- MCP configured/connected/degraded servers, advertised tools and authorization state;
- inventory churn: installs, upgrades, disables, removals and configuration drift;
- unsupported or partially supported capabilities by adapter.

## Sessions and activity

- sessions, turns and prompts per hour/day/week;
- active duration, wall duration, idle ratio and resume count;
- prompts/turns/tool calls per session: mean, p50, p75, p90, p95, p99;
- subagent count, depth, concurrency and parent/child duration;
- compactions, interruptions, cancellations and abandoned sessions;
- time to first model event, first tool, first edit and final response;
- hour-of-day × weekday heatmap and calendar activity heatmap;
- streaks and active-day consistency, without gamified productivity scoring.

## Prompt metadata without prompt retention

Computed in memory at the trusted boundary, then raw content is discarded:

- UTF-8 bytes, Unicode scalar count, grapheme count, words and lines;
- attachment count and aggregate bytes grouped by coarse media category;
- number of code fences, list items, URLs, file references and explicit component mentions;
- language/script classification and mixed-language flag;
- median and percentile distributions by hour, agent, project and model;
- follow-up chain length, prompt interval and correction/retry signals;
- prompt count timeline and payload-size percentile band;
- optional rotating-HMAC duplicate indicator; disabled by default because hashes can leak equality.

Never store raw prompt, normalized prompt, n-grams, embeddings or stable content hashes by default.

## Tokens, context and cost

- input, output, cached-read, cached-write and reasoning tokens when exposed separately;
- token rate, tokens/session, tokens/prompt and tokens/successful outcome;
- context-window utilization, compaction frequency and post-compaction growth;
- cache hit share and estimated savings;
- token and cost share by agent, model, project and component attribution;
- model fallback/switch rate and provider error rate;
- cost estimates bound to a versioned price catalog and confidence; never present estimates as bills;
- comparison against previous period with absolute and percentage change.

## Skills

- installed → enabled → exposed → invoked → loaded → executed → succeeded funnel;
- explicit vs implicit invocation share;
- unique sessions/users/projects and repeat-use rate;
- success, failure, fallback and median duration by skill/version;
- opportunity count, activation recall and false-positive estimate for opt-in classifier;
- unused, cold, newly adopted, declining and resurrected skills;
- co-activation matrix and common sequences;
- scripts/references actually used, if observable without content capture;
- trigger-eval coverage and description discoverability score;
- before/after experiment cohorts for token/time impact; no unsupported causal claim.

## Plugins

- installed/enabled/active versions and marketplace/source;
- share of sessions with any bundled component usage;
- bundled skill, MCP, hook and command usage as separate children;
- activation funnel, failures after upgrade and version adoption curve;
- permissions/trust state and stale configuration;
- plugin value view: used components / shipped components, with completeness caveat.

## MCP and tools

- server connection attempts, uptime, handshake latency and disconnects;
- advertised/allowed/disabled/called tools;
- calls, success, error, timeout, cancellation, retry and latency percentiles;
- approvals requested/granted/denied and policy blocks;
- request/response byte counts only when natively available and safe;
- top error classes with version markers;
- cold servers/tools and tools used without an associated skill;
- local vs remote server share and transport type;
- tool sequence funnels, read/edit/shell/test ratios and external-write count.

## Models and quality proxies

- model usage share, latency, errors, token efficiency and outcome correlation;
- tool-use rate, edit acceptance proxy, test-after-edit rate and retry loops;
- successful test/check/commit/PR signals when observable;
- changed files and line counts from agent-native metadata, never source contents;
- rework signals: immediate revert, repeated edit of same pseudonymous target, reopened task;
- human intervention/approval count;
- outcome states remain explicit and may be `unknown`.

These are workflow diagnostics, not measures of developer worth or individual performance.

## Reliability and collection quality

- eligible, observed, normalized, quarantined and reconciled events;
- source coverage ratio with exact numerator and denominator;
- ingest lag p50/p95/p99 and latest watermark;
- duplicates, out-of-order events, parser failures and unknown schemas;
- heartbeat and sequence gaps;
- cross-source disagreement by agent/version/capability;
- daily audit pass rate, canary age and mean time to detection/recovery;
- periods marked complete, partial, degraded or unknown;
- adapter version adoption and fixture coverage.

## Privacy and security

- redaction counts by category, without retaining matched secrets;
- raw-content canary violations (target must remain zero);
- denied actions, sandbox escalations, policy blocks and external writes;
- collector authentication failures and rejected non-loopback requests;
- retention deletions, backup age and restore-test age;
- configuration changes and installer consent records;
- network egress attempts by Kansoku components.

## Kansoku operational health

- ingest events/sec and queue depth;
- CPU, RSS, disk usage and database growth/day;
- transaction/lock latency, failed writes and retry queue;
- rollup freshness, query p50/p95 and slow dashboard panels;
- partition, vacuum/analyze, backup and migration status;
- container restarts and healthcheck failures;
- estimated days until configured disk budget is exhausted.

## Required percentage semantics

Every percentage is rendered with its formula. Examples:

```text
skill activation rate = invoked eligible skills / exposed eligible skills
activation recall      = invoked opportunities / detected opportunities
MCP success rate       = successful terminal calls / terminal calls
source coverage        = normalized eligible events / expected eligible events
active plugin share    = plugins with child usage / enabled plugins
```

If the denominator is unknown, Kansoku shows `unknown`, not `0%`. Small denominators show the raw
counts prominently and suppress misleading trend arrows.
