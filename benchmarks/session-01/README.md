# Session 01 technology spikes

These spikes answer the bounded baseline questions in TDD 01. They are not production collectors,
database schemas, or UI components.

Run all measurements from the repository root:

```sh
python3 benchmarks/session-01/run.py
```

The runner builds local containers, starts loopback-only or internal temporary services, writes
generated results under this directory, and removes its named temporary containers. It does not
read agent configuration or transcripts. Backend payloads are synthetic; the durable spike sink
contains only receive time, route, record/byte counts, and a fixed schema fingerprint.

Every external container input is recorded as an immutable `@sha256` digest, the frontend requires
the committed package lock, and a successful invocation retains a `full_run` manifest with one run
record per spike plus hashes of final sanitized artifacts. The helper image used only to set the
temporary volume owner is pinned too. Cleanup is limited to `kansoku-s01-*` containers and volumes.

The backend comparison intentionally isolates the runtime by using only each language's standard
library and the same sanitized append sink. Database behavior is measured separately. PostgreSQL
and SQLite receive deterministic equivalent rows for the personal (10k) and enthusiast (100k)
one-day samples. The full multi-year million-event fixture remains Session 04's exit gate; Session
01 records an explicitly labeled linear capacity projection only.

The frontend comparison implements the same 180-point dense time series, component funnel, linked
selection, export control, ARIA summary, and table equivalent in React with ECharts and uPlot.
Generated `node_modules/` and `dist/` are ignored; the exact resolved package lock and raw bundle
measurements are retained.

Known limitations are embedded in each `raw-results.json` and must be carried into the ADR rather
than hidden by the preferred baseline.
