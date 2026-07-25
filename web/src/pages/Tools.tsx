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
import { GapNote, Panel } from "../components/Panel";
import { RangeControl } from "../components/RangeControl";
import { deriveViewState } from "../api/client";
import { useToolAnalytics } from "../api/queries";
import { useRange } from "../hooks/useRange";
import { dayLabel, ratio, sum } from "../lib/format";
import { timeSeriesOption } from "../components/chartOptions";
import type { ToolAnalyticsDayRow } from "../api/types";

export function Tools() {
  const range = useRange();
  const rangeParams = useMemo(() => ({ from: range.from, to: range.to }), [range.from, range.to]);
  const analytics = useToolAnalytics(rangeParams);

  const rows = analytics.data?.data?.data ?? [];
  const state = deriveViewState(analytics.data, { isLoading: analytics.isLoading });
  const calls = sum(rows.map((r) => r.call_count));
  const succeeded = sum(rows.map((r) => r.success_count));
  const failed = sum(rows.map((r) => r.failure_count));
  const latencyRows = rows.filter((r) => r.percentiles?.p95 != null);

  const columns: Column<ToolAnalyticsDayRow>[] = [
    { key: "day", header: "Day", render: (r) => dayLabel(r.day) },
    { key: "calls", header: "Calls", align: "right", render: (r) => r.call_count.toLocaleString() },
    { key: "success", header: "Succeeded", align: "right", render: (r) => r.success_count.toLocaleString() },
    { key: "failure", header: "Failed", align: "right", render: (r) => r.failure_count.toLocaleString() },
    {
      key: "success_ratio",
      header: "Success ratio",
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
      render: (r) => (r.percentiles?.p95 != null ? r.percentiles.p95.toLocaleString() : "—"),
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
          <KpiCard label="Calls" value={calls} state={state} />
          <KpiCard label="Succeeded" value={succeeded} state={state} />
          <KpiCard label="Failed" value={failed} state={state} />
          <KpiCard
            label="Success ratio"
            value={calls > 0 ? Math.round(100 * (ratio(succeeded, calls) ?? 0)) : null}
            unit="%"
            state={calls > 0 ? state : "not_observed"}
          />
        </div>
        {rows.length > 0 ? (
          <ChartContainer
            ariaLabel="Tool calls, successes and failures per day"
            option={timeSeriesOption(
              rows.map((r) => dayLabel(r.day)),
              [
                { name: "Calls", data: rows.map((r) => r.call_count) },
                { name: "Succeeded", data: rows.map((r) => r.success_count) },
                { name: "Failed", data: rows.map((r) => r.failure_count), color: "var(--status-degraded)" },
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
            ariaLabel="Tool call p95 latency in milliseconds, for days with observed calls"
            option={timeSeriesOption(
              latencyRows.map((r) => dayLabel(r.day)),
              [
                {
                  name: "p95 latency (ms)",
                  data: latencyRows.map((r) => r.percentiles?.p95 ?? null),
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
