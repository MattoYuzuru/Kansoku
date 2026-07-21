# ADR 0001 — Measured technology baseline

- Status: Accepted
- Date: 2026-07-21
- Scope: Sessions 02–10 baseline; later ADRs may replace a choice only with measured evidence
- Reference machine: Apple M2 Pro (10 cores), 16 GiB RAM, arm64, macOS 26.5.1, Docker Engine 29.5.2

## Context

Kansoku needs an always-on local collector, a concurrent relational/analytics store, and a dense
offline dashboard. The blueprint proposed Go, PostgreSQL, React and Apache ECharts, but TDD 01 made
that proposal contingent on bounded reproducible spikes. Privacy applies to spikes too: synthetic
OTLP bodies are processed transiently and only batch receive time, route, record/byte counts and a
fixed fingerprint reach the durable sink.

Raw measurements and exact dependency/image identities live under `benchmarks/session-01/`. The
tests assert final artifact hashes, idempotent database replay, explicit accessibility alternatives,
shutdown flush and absence of prohibited sink keys.

## Decision

1. Use **Go** for the collector/backend and static asset host. Keep source decoders behind bounded
   interfaces and prefer the standard library where it reduces dependency surface.
2. Use plain **PostgreSQL 18** as the primary system of record. Keep **SQLite** as a credible future
   lite/offline mode, not as a transparent substitute with different concurrency semantics.
3. Use **TypeScript + React** for the dashboard and **Apache ECharts** for the primary analytics
   chart layer. Load analytics routes/charts lazily, keep the ECharts route chunk at or below the
   provisional 250 KiB gzip spike budget, and always provide Kansoku-authored text/table
   equivalents. uPlot remains a possible specialized time-series optimization only after evidence
   shows that supporting two chart systems is cheaper than one.
4. Package the local runtime with Docker Compose. PostgreSQL 18 data volumes mount at
   `/var/lib/postgresql`, not the pre-18 `/var/lib/postgresql/data` path.

This ADR selects architecture, not production dependency ranges. Release images and packages must
be pinned to reviewed versions/digests by the owning implementation session.

## Evidence

### Backend language spike

Both implementations used standard-library-only OTLP/HTTP JSON structural decoding, a 1 MiB body
limit, a bounded queue, fsync-before-ack sanitized batch sink, health endpoint, eight request
workers, 400 measured requests and 20 warmups. Go resolved to 1.26.5; Python to 3.14.6.

| Measure | Go | Python |
|---|---:|---:|
| Final image | 2,371,589 B | 18,167,987 B |
| Cold start to health | 0.159 s | 0.450 s |
| Idle RSS point sample | 5.61 MiB | 15.43 MiB |
| Load RSS point sample | 8.15 MiB | 15.44 MiB |
| Throughput | 231 req/s | 233 req/s |
| Request latency p95 | 38.7 ms | 38.6 ms |
| Acknowledged batches present after stop | yes | yes |
| Prohibited durable sink keys | 0 | 0 |

The workload is intentionally fsync-bound, so its small throughput/latency differences are not a
decoder-throughput conclusion. Go wins the whole-product trade-off through a roughly 7.7× smaller
final image, roughly 2.7× lower idle RSS point sample and roughly 2.8× faster cold start without
losing durability in the tested path. Both rejected ten content-bearing aliases/keys and an
oversized body before persistence. All 420 retained rows per implementation matched the exact
five-field allowlist, and recursive scans of keys and values found none of the five unique canaries.
The CPU/RSS readings are single Docker samples and do not satisfy the binding 24-hour idle SLO.

### Database spike

SQLite 3.53.3 and PostgreSQL 18.4 received equivalent deterministic 10k and 100k one-day samples,
unique idempotency keys, duplicate replay, four representative queries and an additive migration.

| Engine/profile | Insert events/s | Slowest common query | Bytes/event | Replay exact |
|---|---:|---:|---:|---:|
| SQLite / 10k | 579,967 | 8.3 ms | 88.5 | yes |
| SQLite / 100k | 426,127 | 89.1 ms | 91.0 | yes |
| PostgreSQL / 10k | 129,016 | 2.5 ms | 195.8 | yes |
| PostgreSQL / 100k | 272,681 | 18.0 ms | 205.1 | yes |

SQLite also completed a two-second mixed four-reader/one-writer WAL run without errors (3,291
read ops/s and 5,685 committed writes/s at the 100k sample). PostgreSQL's separate four-client read
run completed 2,362 transactions/s with no failed transactions at 100k. These concurrency tests are
not equivalent and do not prove PostgreSQL faster. PostgreSQL is selected because the required
product includes independent ingest/rollup/dashboard/retention/backup workers, range partitions,
`percentile_cont`, explicit roles, mature backup/restore and predictable container operation. Its
higher storage and operational cost are accepted risks. Session 04 must still test equivalent
mixed-write workloads, partitions and the materialized million-event fixture.

The SQLite spike now implements the same continuous p95 interpolation as PostgreSQL
`percentile_cont(0.95)`: both engines produced `4749.05` for both deterministic profiles. This
alignment increases the measured SQLite quantile-query cost and prevents nearest-rank fixtures from
silently validating different production semantics.

The naive five-year stress projection is about 166 GB for SQLite and 375 GB for PostgreSQL before
partition policy, compression or rollups. It is a warning, not a capacity claim; it confirms that
bounded retention and disk forecasting are mandatory.

### Frontend spike

Both prototypes used React 19.2.8 and implemented the same 180-point dense series, component funnel,
linked selection, export control, ARIA summary, keyboard-native controls, reduced-motion CSS and
table equivalent. ECharts resolved to 6.1.0 and uPlot to 1.6.32.

| Measure | ECharts | uPlot |
|---|---:|---:|
| Production build | 1.80 s | 0.48 s |
| Total bundle | 725,335 B | 247,885 B |
| Total gzip | 238,198 B | 83,311 B |
| Dense time series | native | native |
| Funnel | native | custom DOM |
| Linked charts/filtering | native group/connect + app state | custom app state |
| Export | native SVG/PNG | custom canvas path |

uPlot is roughly 2.9× smaller after gzip. ECharts is accepted because the planned dashboard also
needs funnels, heatmaps, matrices, scatter plots, trees, timeline annotations, linked brushing and
export; rebuilding these correctly would move chart-library complexity into Kansoku. Static checks
only establish the accessibility surface. Browser, screen-reader, keyboard and visual-state gates
remain Session 10 work.

## Rejected alternatives

- **Python backend baseline:** implementation was concise and fast enough, but its measured resident
  memory, image and cold-start costs lose for an always-on single-binary local service.
- **SQLite primary store:** excellent measured speed/size and retained as a future lite mode, but it
  would require an application-owned single-writer/rollup/backup coordination design that does not
  simplify the complete product enough yet.
- **ClickHouse primary store:** operationally disproportionate to personal/enthusiast loads.
- **DuckDB primary store:** valuable for offline Parquet analysis/export, not the concurrent system
  of record.
- **uPlot as the only chart library:** compelling size and time-series performance, but insufficient
  native coverage for the accepted information architecture.

## Consequences and follow-up gates

- Session 02 privacy tests target Go persistence structs and PostgreSQL/log/export sinks, while
  remaining language-agnostic at the contract boundary.
- Session 03 must replace the JSON spike with bounded OTLP HTTP/gRPC protobuf decoding and unknown
  schema quarantine behavior.
- Session 04 may overturn PostgreSQL only after equivalent mixed concurrency, migration, replay,
  retention, backup/restore and million-event query evidence.
- Session 09 must pin the PostgreSQL image digest and test the version-specific volume layout,
  upgrade path and restore. The measured 117.8 MB image and roughly 205 B/event sample feed disk
  forecasts.
- Session 10 must split ECharts by route where effective and enforce the provisional 250 KiB gzip
  analytics-chunk budget plus accessible table/summary parity.
