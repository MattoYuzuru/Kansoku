/*
 * Privacy ("/privacy") — data classes; retention; redactions/canary; egress
 * and host-access status (contracts/dashboard.yaml panelIds: privacy-canary,
 * privacy-retention).
 *
 * privacy-canary declares privacy.raw_content_persisted_count as a metric,
 * but an *exact count* of persisted raw content is explicitly on the
 * no-backend-support list (there is no query that counts raw-content rows —
 * doing so would require scanning payloads the appliance is designed not to
 * retain). What IS real and durable is /api/v1/privacy/canary-history: a
 * daily pass/fail count from a redaction canary check. That is a materially
 * different signal (a check-history, not a content count) and is labeled as
 * such rather than substituted silently. reliability.unknown_schema_count is
 * shown alongside it, from /api/v1/reliability/counts, as another real signal
 * declared on this panel.
 *
 * privacy-retention's two metrics (system.database_size_bytes,
 * system.backup_age_seconds) are both real, from /api/v1/system/snapshot.
 * "Egress and host-access status" from the wireframe has no backend signal
 * anywhere (no egress-tracking or host-access table) — noted as a gap.
 */
import { useMemo } from "react";
import { KpiCard } from "../components/KpiCard";
import { ChartContainer } from "../components/ChartContainer";
import { GapNote, Panel } from "../components/Panel";
import { RangeControl } from "../components/RangeControl";
import { StatusBadge } from "../components/StatusBadge";
import { deriveViewState } from "../api/client";
import {
  usePrivacyCanaryHistory,
  useReliabilityCounts,
  useSystemSnapshot,
} from "../api/queries";
import { useRange } from "../hooks/useRange";
import { bytesToReadable, secondsToReadable, sum } from "../lib/format";
import { bucketedStackedBarOption } from "../components/chartOptions";

export function Privacy() {
  const range = useRange();
  const rangeParams = useMemo(
    () => ({ from: range.from, to: range.to, granularity: range.granularity, timezone: range.timezone }),
    [range.from, range.to, range.granularity, range.timezone],
  );
  const canary = usePrivacyCanaryHistory(rangeParams);
  const counts = useReliabilityCounts(rangeParams);
  const snapshot = useSystemSnapshot();

  const canaryRows = canary.data?.data?.data ?? [];
  const canaryState = deriveViewState(canary.data, { isLoading: canary.isLoading });
  const passTotal = sum(canaryRows.map((r) => r.pass_count));
  const failTotal = sum(canaryRows.map((r) => r.fail_count));

  const countsRows = counts.data?.data?.data ?? [];
  const countsState = deriveViewState(counts.data, { isLoading: counts.isLoading });
  const unknownSchemaTotal = sum(countsRows.map((r) => r.unknown_schema_count));

  const snap = snapshot.data?.data;
  const snapshotState = deriveViewState(snapshot.data, { isLoading: snapshot.isLoading });

  return (
    <section className="k-page">
      <header className="k-page__head">
        <h1 className="t-page-title">Privacy</h1>
        <p className="k-page__wire t-caption">
          Data classes; retention; redactions/canary; egress and host-access status.
        </p>
      </header>

      <Panel title="Redaction canary" actions={<RangeControl range={range} />}>
        <div className="k-grid k-grid--kpis">
          <KpiCard label="Canary passes" value={passTotal} state={canaryState} />
          <KpiCard label="Canary failures" value={failTotal} state={canaryState} />
          <KpiCard label="Raw content persisted (exact count)" value={null} state="unsupported" />
          <KpiCard label="Unknown schema events" value={unknownSchemaTotal} state={countsState} />
        </div>
        {canaryRows.length > 0 ? (
          <ChartContainer
            ariaLabel="Redaction canary pass and fail counts per selected time bucket"
            option={bucketedStackedBarOption(
              range,
              canaryRows,
              [
                { name: "Pass", value: (r) => r.pass_count, color: "var(--status-complete)" },
                { name: "Fail", value: (r) => r.fail_count, color: "var(--status-degraded)" },
              ],
            )}
          />
        ) : (
          <p className="t-body" style={{ color: "var(--text-muted)" }}>
            {canary.isLoading ? "Loading…" : "No canary checks recorded in this range."}
          </p>
        )}
        <GapNote>
          An exact count of persisted raw content (<code>privacy.raw_content_persisted_count</code>)
          has no backing query — computing it would require scanning payloads this
          appliance is designed not to retain, so it renders as{" "}
          <StatusBadge state="unsupported" glyphOnly />. The pass/fail canary history above
          is a related but distinct signal: a redaction check history, not a content count.
        </GapNote>
      </Panel>

      <Panel title="Retention snapshot">
        <div className="k-grid k-grid--kpis">
          <KpiCard
            label="Database size"
            value={snap ? snap.database_size_bytes : null}
            state={snapshotState}
          />
          <KpiCard
            label="Backup age"
            value={snap?.backup_age_seconds ?? null}
            unit="s"
            state={snap?.backup_age_seconds == null ? "not_observed" : snapshotState}
          />
        </div>
        {snap && (
          <dl className="k-kv">
            <div className="k-kv__row">
              <dt className="t-caption">Database size (readable)</dt>
              <dd className="t-body">{bytesToReadable(snap.database_size_bytes)}</dd>
            </div>
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
              <dt className="t-caption">Restore test age</dt>
              <dd className="t-body">{secondsToReadable(snap.restore_test_age_seconds)}</dd>
            </div>
          </dl>
        )}
        <GapNote>
          Data-class inventory and egress/host-access status are not shown: there is no
          data-classification table or egress/host-access log anywhere in the backend
          schema to build either honestly.
        </GapNote>
      </Panel>
    </section>
  );
}
