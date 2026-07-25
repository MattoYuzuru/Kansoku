/*
 * Reliability ("/reliability") — coverage timeline; source gaps/watermarks;
 * drift/mismatch tables; incident list (contracts/dashboard.yaml panelIds:
 * reliability-coverage, reliability-drift).
 *
 * reliability-coverage's declared metrics (collection.coverage_ratio,
 * collection.completeness_duration_seconds, collection.active_source_gap_seconds,
 * collection.ingest_latency_seconds, collection.rollup_freshness_seconds,
 * collection.acknowledged_durability_ratio) are ALL on the no-backend-support
 * list — none has a durable backing table (see internal/runtime/diagnostics.go
 * and the collector's doc comments: this appliance does not persist
 * ingest-latency or per-source watermark histories). The whole panel renders
 * as UnsupportedPanel rather than fabricating any of these numbers. In its
 * place, the real reliability_coverage_timeline analytics budget (per source
 * instance/day/status, from completeness_intervals) is shown as an honest
 * substitute for "source gaps" — it shows *which* source/day/status
 * combinations were observed, not a gap-duration metric.
 *
 * reliability-drift mixes one real pair (reliability.unknown_schema_count,
 * reliability.reconciliation_mismatch_count, from /api/v1/reliability/counts)
 * with one unsupported metric (reliability.drift_detection_seconds, no
 * durable timer anywhere) — the KPI for that metric renders explicitly
 * unsupported rather than omitting it silently.
 */
import { useMemo } from "react";
import { KpiCard } from "../components/KpiCard";
import { ChartContainer } from "../components/ChartContainer";
import { DataTable, type Column } from "../components/DataTable";
import { GapNote, Panel, UnsupportedPanel } from "../components/Panel";
import { RangeControl } from "../components/RangeControl";
import { StatusBadge } from "../components/StatusBadge";
import { deriveViewState } from "../api/client";
import {
  useIncidents,
  useReliabilityCounts,
  useReliabilityCoverageTimeline,
} from "../api/queries";
import { useRange } from "../hooks/useRange";
import { dayLabel, sum } from "../lib/format";
import { stackedBarOption } from "../components/chartOptions";
import type { Incident, ReliabilityDayRow } from "../api/types";

export function Reliability() {
  const range = useRange();
  const rangeParams = useMemo(() => ({ from: range.from, to: range.to }), [range.from, range.to]);
  const coverage = useReliabilityCoverageTimeline(rangeParams);
  const counts = useReliabilityCounts(rangeParams);
  const incidents = useIncidents();

  const coverageRows = coverage.data?.data?.data ?? [];
  const coverageState = deriveViewState(coverage.data, { isLoading: coverage.isLoading });

  const countsRows = counts.data?.data?.data ?? [];
  const countsState = deriveViewState(counts.data, { isLoading: counts.isLoading });
  const unknownSchemaTotal = sum(countsRows.map((r) => r.unknown_schema_count));
  const mismatchTotal = sum(countsRows.map((r) => r.reconciliation_mismatch_count));

  const incidentRows: Incident[] = incidents.data?.data ?? [];
  const incidentsState = deriveViewState(incidents.data, { isLoading: incidents.isLoading });

  const coverageColumns: Column<ReliabilityDayRow>[] = [
    { key: "day", header: "Day", render: (r) => dayLabel(r.day) },
    { key: "source", header: "Source instance", render: (r) => r.source_instance_id },
    { key: "status", header: "Status", render: (r) => r.status },
    {
      key: "intervals",
      header: "Intervals",
      align: "right",
      render: (r) => r.interval_count.toLocaleString(),
    },
  ];

  const incidentColumns: Column<Incident>[] = [
    { key: "incident_id", header: "Incident", render: (r) => r.incident_id },
    { key: "installation_id", header: "Installation", render: (r) => r.installation_id },
    { key: "source_id", header: "Source", render: (r) => r.source_id },
    { key: "capability_id", header: "Capability", render: (r) => r.capability_id },
    { key: "failure_class", header: "Failure class", render: (r) => r.failure_class },
    { key: "first_seen_at", header: "First seen", render: (r) => dayLabel(r.first_seen_at) },
    { key: "recovery_criteria", header: "Recovery criteria", render: (r) => r.recovery_criteria },
  ];

  return (
    <section className="k-page">
      <header className="k-page__head">
        <h1 className="t-page-title">Reliability</h1>
        <p className="k-page__wire t-caption">
          Coverage timeline; source gaps/watermarks; drift/mismatch tables; incident list.
        </p>
      </header>

      <UnsupportedPanel
        title="Collection health"
        reason="Coverage ratio, completeness duration, active-source gap, ingest latency,
        rollup freshness and acknowledged-durability ratio have no durable backing table in
        this appliance (see internal/runtime/diagnostics.go) — none of these timers or
        ratios are persisted anywhere, so no honest number can be shown here."
      />

      <Panel title="Source coverage timeline" actions={<RangeControl range={range} />}>
        <DataTable
          columns={coverageColumns}
          rows={coverageRows}
          rowKey={(r) => `${r.day}-${r.source_instance_id}-${r.status}`}
          emptyMessage={coverage.isLoading ? "Loading…" : "No coverage intervals observed in this range."}
        />
        {coverageState !== "complete" && coverageState !== "loading" && coverageRows.length === 0 && (
          <p className="t-caption" style={{ color: "var(--text-faint)" }}>
            Coverage state: {coverageState}
          </p>
        )}
        <GapNote>
          This table shows which source/day/status combinations were actually observed —
          it is not a gap-duration metric. A dedicated "gap seconds since last
          acknowledged event" figure is not buildable (see the Collection health panel
          above).
        </GapNote>
      </Panel>

      <Panel title="Drift and mismatch">
        <div className="k-grid k-grid--kpis">
          <KpiCard label="Unknown schema events" value={unknownSchemaTotal} state={countsState} />
          <KpiCard label="Reconciliation mismatches" value={mismatchTotal} state={countsState} />
          <KpiCard label="Drift detection time" value={null} unit="s" state="unsupported" />
        </div>
        {countsRows.length > 0 ? (
          <ChartContainer
            ariaLabel="Unknown schema and reconciliation mismatch counts per day"
            option={stackedBarOption(
              countsRows.map((r) => dayLabel(r.day)),
              [
                { name: "Unknown schema", data: countsRows.map((r) => r.unknown_schema_count) },
                {
                  name: "Reconciliation mismatch",
                  data: countsRows.map((r) => r.reconciliation_mismatch_count),
                  color: "var(--status-degraded)",
                },
              ],
            )}
          />
        ) : (
          <p className="t-body" style={{ color: "var(--text-muted)" }}>
            {counts.isLoading ? "Loading…" : "No drift or mismatch events observed in this range."}
          </p>
        )}
        <GapNote>
          Drift detection time has no durable timer anywhere in the schema — shown as{" "}
          <StatusBadge state="unsupported" glyphOnly /> rather than estimated.
        </GapNote>
      </Panel>

      <Panel title="Incidents">
        <DataTable
          columns={incidentColumns}
          rows={incidentRows}
          rowKey={(r) => r.incident_id}
          emptyMessage={incidents.isLoading ? "Loading…" : "No open incidents."}
        />
        {incidentsState !== "complete" && incidentsState !== "loading" && incidentRows.length === 0 && (
          <p className="t-caption" style={{ color: "var(--text-faint)" }}>
            Coverage state: {incidentsState}
          </p>
        )}
      </Panel>
    </section>
  );
}
