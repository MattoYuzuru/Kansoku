# Wave D reconciliation — telemetry trust

Date: 2026-07-30  
Scope: R03, R08, R10  
Implementation commits: `0881fb6`, `8b46d79`, `37030f2`, `22abc90`, `3ca291f`,
`ab24166`, `51d15b4`, `60be47f`

## Exit-gate result

- The ordinary Codex rollout reader retains at most 1 MiB of one record plus a 64 KiB reader
  buffer. An oversized newline-terminated record is drained, reduced to keyed metadata, quarantined,
  checkpointed and followed by later valid records in the same scan.
- Raw rollout JSONL has no durable slot. PostgreSQL integration tests find one metadata-only
  quarantine occurrence, the following three corroborated skill assertions and no raw canary
  marker in durable tables.
- `$PATH`, `$HOME`, an ordinary identifier and currency remain ephemeral without a matching
  `SKILL.md` read and produce zero durable skill assertions. A corroborated marker produces
  reconstructed requested, loaded and invoked evidence bound to the explicit installation.
- A uniquely owned plugin child produces one child invocation and one plugin `child_activity`.
  It does not fabricate plugin invoked, loaded or success evidence. Duplicate replay preserves
  exact logical counts.
- The shared terminal matrix maps success, failure, cancel, deny, timeout and interruption.
  Missing and contradictory completion remain `unknown` with metadata-only rejection evidence;
  an identical duplicate terminal remains one logical call.
- Skills renders source lifecycle/health separately from formula completeness. Europe/Moscow and
  America/New_York DST bucket/display canaries pass.

## Controlled canaries

- `search-workflow` completed its local setup check with all declared local tools available.
- Two authenticated typed App Server selections of `search-workflow` produced two native invoked
  and two native loaded assertions. Two selections of plugin-owned
  `sre-agent:finding-validation` produced the same child assertions with owner identity plus
  exactly two plugin `child_activity` summaries. Reposting the identical stream retained those
  logical counts and incremented only evidence replay counts.
- The Kotlin canary used a mode-0700 `mktemp -d` outside the repository and removed it after the
  check. `kotlinc` was unavailable, so compilation is honestly `not_observed`; no package was
  installed and no user repository was touched.
- `central-university-lms` stopped with `confirmation_required` before Playwright setup, browser
  launch, authentication, network or LMS activity, as required by its guardrail.
- The oversized-then-valid, dollar-variable, duplicate-replay, plugin-child and complete terminal
  matrices are deterministic fixture/integration canaries.
- Claude 2.1.197 was not launched. Its mapping is covered only by sanitized, version-pinned
  fixtures, preserving the explicit operator boundary.

## Validation evidence

- `python3 scripts/validate_runtime.py --runtime-only`
- `python3 scripts/validate_adapter_sdk.py`
- `python3 scripts/validate_codex.py`
- `python3 scripts/validate_data_platform.py`
- `python3 scripts/validate_plugins.py`
- targeted Go tests for adapter SDK, Codex bridge, Claude mapping, observability, runtime and
  PostgreSQL plugin/snapshot/timezone reconciliation
- race tests for `internal/dataplatform` and `internal/runtime`
- frontend component-catalog tests, TypeScript typecheck, production build and embed/dist parity
- production image `kansoku:wave-d6-20260730`, live authenticated App Server replay and saved
  Chrome 150 harness
- a 330,000-row isolated PostgreSQL skew fixture reconciled exact event/evidence totals inside the
  reviewed profile budget; the saved live harness returned 200 for all five agent profiles with
  the primary installation at 277.3 ms

## Resource, privacy and retention review

- Per watched file, retained parse memory is bounded by 1 MiB plus the 64 KiB reader buffer and
  small correlation maps. The App Server stream already limits frames to 10,000 and concurrently
  demultiplexed request IDs to 128.
- Oversized content is digested in-stream and discarded. Prompts, responses, source code, tool
  input/output, raw errors, environment values, credentials and unredacted paths gain no durable
  destination.
- Checkpoint and quarantine metadata are additive. No historical telemetry, incidents, agent
  configuration or retention class is rewritten.
- A live first-pass canary exposed loaded-derived plugin child summaries. They remain immutable;
  `plugin.active_share/2` and `plugin_profile/2` exclude them through the origin event. A fresh
  post-fix canary reconciled two plugin-owned selections to two child summaries and zero
  loaded-derived summaries. Synthetic path matches across event/evidence/assertion sinks were zero.
- The primary agent reached 328,653 exact events while Wave D collectors recovered. At that shape,
  one exact events aggregate measured 203.937 ms alone and the evidence contour measured
  289.723 ms under concurrent shared-buffer pressure, disproving the earlier end-to-end 200 ms
  claim. `agent_profile_range/1` now uses one bounded `GROUPING SETS` pass, selected-only price
  lookup, 16 MiB `work_mem` and at most one gather worker. Query-contract 1.8.0 records an explicit
  500 ms ceiling. Five post-deploy requests returned 200 in 405.53, 195.97, 168.36, 179.99 and
  183.04 ms; no result population or historical row was dropped to meet it.

## Residual risks

- Kotlin compilation remains `not_observed` on this host because `kotlinc` is absent.
- The Claude mapping has fixture evidence only; local Claude execution remains outside the
  authorized scope.
- The appliance health command remains sensitive to unrelated source lanes that are configured but
  not currently observed. The two Wave D lanes themselves reconcile as
  `codex.rollout=producing/observed` and `codex.app_server=producing/observed` with no safe error
  class.
- The final Wave D browser pass observed transient first-attempt 503 responses from Activity,
  Reliability Counts and Collection Health while every agent profile returned 200. Those routes
  are retained as explicit Wave E performance/formula work; the UI recovered visibly on retry and
  the harness recorded zero runtime exceptions or failed network requests.
