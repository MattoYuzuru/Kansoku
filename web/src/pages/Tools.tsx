/*
 * Tools ("/tools") — unfiltered calls/success/failure and latency
 * (contracts/dashboard.yaml panelId: tool-analytics).
 *
 * /api/v1/tools/analytics with no component_id filter (the full picture
 * across every tool-shaped component). "Approvals" from the wireframe has no
 * backend signal: there is no approval/consent-decision column on the tool
 * analytics rollup, or anywhere else in the schema — noted as a gap rather
 * than invented.
 */
import { useMemo } from "react";
import { KpiCard } from "../components/KpiCard";
import { ChartContainer } from "../components/ChartContainer";
import { DataTable, type Column } from "../components/DataTable";
import { GlossaryTerm } from "../components/GlossaryTerm";
import { GapNote, Panel } from "../components/Panel";
import { RangeControl } from "../components/RangeControl";
import { deriveViewState } from "../api/client";
import { useToolAnalytics } from "../api/queries";
import { useRange } from "../hooks/useRange";
import { dayLabel, formatMetric, formatMetricWithRaw, ratio, sum } from "../lib/format";
import { bucketedTimeSeriesOption } from "../components/chartOptions";
import type { ToolAnalyticsDayRow } from "../api/types";

export function Tools() {
  const range = useRange("tools");
  const rangeParams = useMemo(
    () => ({ from: range.from, to: range.to, granularity: range.granularity, timezone: range.timezone }),
    [range.from, range.to, range.granularity, range.timezone],
  );
  const analytics = useToolAnalytics(rangeParams);

  const rows = analytics.data?.data?.data ?? [];
  const state = deriveViewState(analytics.data, { isLoading: analytics.isLoading });
  const calls = sum(rows.map((r) => r.call_count));
  const succeeded = sum(rows.map((r) => r.success_count));
  const failed = sum(rows.map((r) => r.failure_count));
  const latencyRows = rows.filter((r) => r.percentiles?.p95 != null);

  const columns: Column<ToolAnalyticsDayRow>[] = [
    { key: "day", header: "Day", render: (r) => dayLabel(r.day) },
    { key: "calls", header: <GlossaryTerm id="call">Calls</GlossaryTerm>, align: "right", render: (r) => r.call_count.toLocaleString() },
    { key: "success", header: <GlossaryTerm id="succeeded">Succeeded</GlossaryTerm>, align: "right", render: (r) => r.success_count.toLocaleString() },
    { key: "failure", header: "Failed", align: "right", render: (r) => r.failure_count.toLocaleString() },
    {
      key: "success_ratio",
      header: <GlossaryTerm id="succeeded">Success ratio</GlossaryTerm>,
      align: "right",
      render: (r) => {
        const pct = ratio(r.success_count, r.call_count);
        return pct != null ? `${Math.round(100 * pct)}%` : "—";
      },
    },
    {
      key: "p95",
      header: "p95 latency (ms)",
      align: "right",
      render: (r) =>
        r.percentiles?.p95 != null
          ? <span title={formatMetricWithRaw(r.percentiles.p95, "ms")}>{formatMetric(r.percentiles.p95)}</span>
          : "—",
    },
  ];

  return (
    <section className="k-page">
      <header className="k-page__head">
        <h1 className="t-page-title">Tools</h1>
        <p className="k-page__wire t-caption">
          Calls, success/failure and latency across every tool-shaped component, unfiltered.
        </p>
      </header>

      <Panel title="Tool call volume" actions={<RangeControl range={range} />}>
        <div className="k-grid k-grid--kpis">
          <KpiCard label={<GlossaryTerm id="call">Calls</GlossaryTerm>} value={calls} state={state} />
          <KpiCard label={<GlossaryTerm id="succeeded">Succeeded</GlossaryTerm>} value={succeeded} state={state} />
          <KpiCard label="Failed" value={failed} state={state} />
          <KpiCard
            label={<GlossaryTerm id="succeeded">Success ratio</GlossaryTerm>}
            value={calls > 0 ? Math.round(100 * (ratio(succeeded, calls) ?? 0)) : null}
            unit="%"
            state={calls > 0 ? state : "not_observed"}
          />
        </div>
        {rows.length > 0 ? (
          <ChartContainer
            ariaLabel="Tool calls, successes and failures per selected time bucket"
            option={bucketedTimeSeriesOption(
              range,
              rows,
              [
                { name: "Calls", value: (r) => r.call_count },
                { name: "Succeeded", value: (r) => r.success_count },
                { name: "Failed", value: (r) => r.failure_count, color: "var(--status-degraded)" },
              ],
            )}
          />
        ) : (
          <p className="t-body" style={{ color: "var(--text-muted)" }}>
            {analytics.isLoading ? "Loading…" : "No tool calls observed in this range."}
          </p>
        )}
        {latencyRows.length > 0 && (
          <ChartContainer
            ariaLabel="Tool call p95 latency in milliseconds, for buckets with observed calls"
            option={bucketedTimeSeriesOption(
              range,
              latencyRows,
              [
                {
                  name: "p95 latency (ms)",
                  value: (r) => r.percentiles?.p95,
                  color: "var(--accent-gold)",
                },
              ],
            )}
          />
        )}
        <GapNote>
          Approvals are not shown: there is no approval/consent-decision column on the tool
          analytics rollup, or anywhere else in the backend schema, to build this from.
        </GapNote>
      </Panel>

      <Panel title="Daily detail">
        <DataTable
          columns={columns}
          rows={rows}
          rowKey={(r) => r.day}
          emptyMessage={analytics.isLoading ? "Loading…" : "No tool calls observed in this range."}
        />
      </Panel>
    </section>
  );
}
