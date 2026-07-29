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
import { GlossaryTerm } from "../components/GlossaryTerm";
import { GapNote, Panel } from "../components/Panel";
import { StatusBadge } from "../components/StatusBadge";
import type { ViewState } from "../api/client";
import { deriveViewState } from "../api/client";
import { useRuntimeHealth, useSystemSnapshot } from "../api/queries";
import { bytesToReadable, secondsToReadable } from "../lib/format";

function healthViewState(state: string | undefined): Exclude<ViewState, "loading"> {
  switch (state) {
    case "pass":
      return "complete";
    case "warning":
      return "partial";
    case "degraded":
    case "critical":
      return "degraded";
    default:
      return "unknown";
  }
}

function timestamp(value: string | null | undefined): string {
  if (!value) return "—";
  const parsed = new Date(value);
  return Number.isNaN(parsed.getTime()) ? value : parsed.toLocaleString();
}

export function System() {
  const snapshot = useSystemSnapshot();
  const runtimeHealth = useRuntimeHealth();
  const snap = snapshot.data?.data;
  const health = runtimeHealth.data?.data;
  const state = deriveViewState(snapshot.data, { isLoading: snapshot.isLoading });
  const healthState = runtimeHealth.isLoading
    ? "loading"
    : healthViewState(health?.status);
  const spoolBytes = Object.values(health?.spool_bytes ?? {}).reduce(
    (total, value) => total + value,
    0,
  );
  const spoolBudget = Object.values(health?.spool_budget_bytes ?? {}).reduce(
    (total, value) => total + value,
    0,
  );

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

      <Panel
        title="Durability and capacity"
        actions={
          health ? (
            <StatusBadge
              state={healthViewState(health.status)}
              reason={`Runtime health is ${health.status}.`}
            />
          ) : undefined
        }
      >
        <div className="k-grid k-grid--kpis">
          <KpiCard
            label={<GlossaryTerm id="database_budget">Database budget used</GlossaryTerm>}
            value={health?.database_budget.percentage ?? null}
            unit="%"
            precision={2}
            state={
              runtimeHealth.isLoading
                ? "loading"
                : healthViewState(health?.database_budget.state)
            }
          />
          <KpiCard
            label={<GlossaryTerm id="checkpoint_budget">Checkpoint budget used</GlossaryTerm>}
            value={health?.checkpoint_usage.percentage ?? null}
            unit="%"
            precision={2}
            state={
              runtimeHealth.isLoading
                ? "loading"
                : healthViewState(health?.checkpoint_usage.state)
            }
          />
          <KpiCard
            label={<GlossaryTerm id="docker_filesystem">Docker filesystem free</GlossaryTerm>}
            value={health?.storage_components.filesystem.free_percentage ?? null}
            unit="%"
            precision={2}
            state={
              runtimeHealth.isLoading
                ? "loading"
                : healthViewState(health?.storage_components.filesystem.state)
            }
          />
          <KpiCard
            label="Emergency spool"
            value={health ? spoolBytes : null}
            unit="bytes"
            state={healthState}
          />
          <KpiCard
            label={<GlossaryTerm id="backpressure_rejection">Backpressure rejections</GlossaryTerm>}
            value={health?.backpressure_rejected_total ?? null}
            state={healthState}
          />
          <KpiCard
            label="Durability unavailable"
            value={health?.durability_unavailable_total ?? null}
            state={healthState}
          />
        </div>

        {health && (
          <>
            <dl className="k-kv">
              <div className="k-kv__row">
                <dt className="t-caption">
                  <GlossaryTerm id="database_budget">Database current / soft budget</GlossaryTerm>
                </dt>
                <dd className="t-body">
                  {bytesToReadable(health.database_budget.current_bytes)} /{" "}
                  {bytesToReadable(health.database_budget.budget_bytes)}
                </dd>
              </div>
              <div className="k-kv__row">
                <dt className="t-caption">
                  <GlossaryTerm id="estimated_exhaustion">
                    Database growth / estimated exhaustion
                  </GlossaryTerm>
                </dt>
                <dd className="t-body">
                  {health.database_budget.growth_bytes_per_day == null
                    ? "Not observed"
                    : `${bytesToReadable(health.database_budget.growth_bytes_per_day)}/day`}
                  {" · "}
                  {timestamp(health.database_budget.estimated_exhaustion_at)}
                </dd>
              </div>
              <div className="k-kv__row">
                <dt className="t-caption">
                  <GlossaryTerm id="checkpoint_budget">Checkpoint current / budget</GlossaryTerm>
                </dt>
                <dd className="t-body">
                  {bytesToReadable(health.checkpoint_usage.current_bytes)} /{" "}
                  {bytesToReadable(health.checkpoint_usage.budget_bytes)}
                </dd>
              </div>
              <div className="k-kv__row">
                <dt className="t-caption">Emergency spool current / aggregate lane budget</dt>
                <dd className="t-body">
                  {bytesToReadable(spoolBytes)} / {bytesToReadable(spoolBudget)}
                </dd>
              </div>
              <div className="k-kv__row">
                <dt className="t-caption">Filesystem available / recommendation</dt>
                <dd className="t-body">
                  {bytesToReadable(health.storage_components.filesystem.available_bytes)} /{" "}
                  {bytesToReadable(
                    health.storage_components.filesystem.minimum_recommended_free_bytes,
                  )}
                </dd>
              </div>
              <div className="k-kv__row">
                <dt className="t-caption">Heap / indexes / backups</dt>
                <dd className="t-body">
                  {bytesToReadable(health.storage_components.table_heap.bytes)} /{" "}
                  {bytesToReadable(health.storage_components.indexes.bytes)} /{" "}
                  {bytesToReadable(health.storage_components.backups.bytes)}
                </dd>
              </div>
              <div className="k-kv__row">
                <dt className="t-caption">WAL headroom</dt>
                <dd className="t-body">
                  <StatusBadge
                    state="not_observed"
                    reason={health.storage_components.wal_headroom.notes}
                  />
                </dd>
              </div>
              <div className="k-kv__row">
                <dt className="t-caption">Last successful / rejected ingest</dt>
                <dd className="t-body">
                  {timestamp(health.last_successful_ingest_at)} /{" "}
                  {timestamp(health.last_rejected_ingest_at)}
                </dd>
              </div>
              <div className="k-kv__row">
                <dt className="t-caption">Projection repair pending</dt>
                <dd className="t-body">{health.pending_projection_count.toLocaleString()}</dd>
              </div>
              <div className="k-kv__row">
                <dt className="t-caption">Counter scope</dt>
                <dd className="t-body">{health.counter_scope.replaceAll("_", " ")}</dd>
              </div>
            </dl>

            <div>
              <h3 className="t-section-header">Emergency spool lanes</h3>
              <dl className="k-kv">
                {Object.keys(health.spool_budget_bytes)
                  .sort()
                  .map((lane) => (
                    <div className="k-kv__row" key={lane}>
                      <dt className="t-caption">{lane}</dt>
                      <dd className="t-body">
                        {bytesToReadable(health.spool_bytes[lane] ?? 0)} /{" "}
                        {bytesToReadable(health.spool_budget_bytes[lane])}
                        {" · queue "}
                        {(health.queue_depth[lane] ?? 0).toLocaleString()}
                      </dd>
                    </div>
                  ))}
              </dl>
            </div>

            <div>
              <h3 className="t-section-header">Source freshness</h3>
              <dl className="k-kv">
                {health.source_freshness.map((source, index) => (
                  <div
                    className="k-kv__row"
                    key={`${source.source_id ?? source.source_kind ?? "source"}-${index}`}
                  >
                    <dt className="t-caption">
                      {source.source_id ?? source.source_kind ?? "unknown source"}
                    </dt>
                    <dd className="t-body">
                      {source.value_state.replaceAll("_", " ")}
                      {" · "}
                      {timestamp(
                        source.last_successful_at ??
                          source.last_committed_at ??
                          source.last_observed_at ??
                          source.last_attempted_at,
                      )}
                    </dd>
                  </div>
                ))}
              </dl>
            </div>

            {(runtimeHealth.data?.completeness?.exclusions?.length ?? 0) > 0 && (
              <GapNote>
                Completeness {runtimeHealth.data?.completeness?.numerator ?? 0}/
                {runtimeHealth.data?.completeness?.denominator ?? 0}:{" "}
                {runtimeHealth.data?.completeness?.exclusions?.join("; ")}
              </GapNote>
            )}
          </>
        )}
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
