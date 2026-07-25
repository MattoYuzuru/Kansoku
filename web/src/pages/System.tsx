/*
 * System ("/system") — CPU/RSS/disk; ingest/query latency; database growth;
 * backup/restore state (contracts/dashboard.yaml panelIds: system-overhead,
 * system-recovery).
 *
 * system-overhead declares 5 metrics; 4 of them (collector_cpu_ratio,
 * collector_rss_bytes, database_growth_bytes_per_day,
 * common_query_latency_seconds) are on the no-backend-support list — this
 * appliance does not run a resource-usage sampler for its own collector
 * process, and has no query-latency histogram table (see
 * internal/runtime/diagnostics.go). Each renders its own unsupported KPI
 * rather than the whole panel being suppressed, because the panel's 5th
 * metric (system.database_size_bytes) IS real, from /api/v1/system/snapshot,
 * and is shown alongside them.
 *
 * system-recovery's two metrics (system.backup_age_seconds,
 * system.restore_test_age_seconds) are both real, from the same endpoint.
 */
import { KpiCard } from "../components/KpiCard";
import { GapNote, Panel } from "../components/Panel";
import { StatusBadge } from "../components/StatusBadge";
import { deriveViewState } from "../api/client";
import { useSystemSnapshot } from "../api/queries";
import { bytesToReadable, secondsToReadable } from "../lib/format";

export function System() {
  const snapshot = useSystemSnapshot();
  const snap = snapshot.data?.data;
  const state = deriveViewState(snapshot.data, { isLoading: snapshot.isLoading });

  return (
    <section className="k-page">
      <header className="k-page__head">
        <h1 className="t-page-title">System</h1>
        <p className="k-page__wire t-caption">
          CPU/RSS/disk; ingest/query latency; database growth; backup/restore state.
        </p>
      </header>

      <Panel title="Collector overhead">
        <div className="k-grid k-grid--kpis">
          <KpiCard label="Collector CPU" value={null} unit="%" state="unsupported" />
          <KpiCard label="Collector RSS" value={null} unit="bytes" state="unsupported" />
          <KpiCard
            label="Database size"
            value={snap ? snap.database_size_bytes : null}
            state={state}
          />
          <KpiCard label="Database growth" value={null} unit="bytes/day" state="unsupported" />
          <KpiCard label="Common query latency" value={null} unit="s" state="unsupported" />
        </div>
        {snap && (
          <p className="t-body" style={{ color: "var(--text-muted)" }}>
            Database size (readable): {bytesToReadable(snap.database_size_bytes)}
          </p>
        )}
        <GapNote>
          Collector CPU/RSS, database growth rate and common-query latency are shown as{" "}
          <StatusBadge state="unsupported" glyphOnly /> — this appliance does not run a
          resource sampler on its own collector process, nor does it persist a
          query-latency histogram (see <code>internal/runtime/diagnostics.go</code>). Only
          the current database size is a real, durable figure.
        </GapNote>
      </Panel>

      <Panel title="Backup and restore state">
        <div className="k-grid k-grid--kpis">
          <KpiCard
            label="Backup age"
            value={snap?.backup_age_seconds ?? null}
            unit="s"
            state={snap?.backup_age_seconds == null ? "not_observed" : state}
          />
          <KpiCard
            label="Restore test age"
            value={snap?.restore_test_age_seconds ?? null}
            unit="s"
            state={snap?.restore_test_age_seconds == null ? "not_observed" : state}
          />
        </div>
        {snap && (
          <dl className="k-kv">
            <div className="k-kv__row">
              <dt className="t-caption">Backup age (readable)</dt>
              <dd className="t-body">{secondsToReadable(snap.backup_age_seconds)}</dd>
            </div>
            <div className="k-kv__row">
              <dt className="t-caption">Backup checksum</dt>
              <dd className="t-body">
                {snap.backup_checksum_ok === undefined ? (
                  <StatusBadge state="not_observed" glyphOnly />
                ) : snap.backup_checksum_ok ? (
                  "OK"
                ) : (
                  "Failed"
                )}
              </dd>
            </div>
            <div className="k-kv__row">
              <dt className="t-caption">Restore test age (readable)</dt>
              <dd className="t-body">{secondsToReadable(snap.restore_test_age_seconds)}</dd>
            </div>
            <div className="k-kv__row">
              <dt className="t-caption">Restore test result</dt>
              <dd className="t-body">
                {snap.restore_test_passed === undefined ? (
                  <StatusBadge state="not_observed" glyphOnly />
                ) : snap.restore_test_passed ? (
                  "Passed"
                ) : (
                  "Failed"
                )}
              </dd>
            </div>
          </dl>
        )}
      </Panel>
    </section>
  );
}
