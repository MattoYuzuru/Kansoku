/*
 * Models ("/models") — request/token share; latency/errors; fallback
 * markers; estimated cost (contracts/dashboard.yaml panelIds: model-usage,
 * model-cost).
 *
 * model-usage + model-cost both come from /api/v1/models/usage
 * (ModelUsageResponse); cost = estimated_cost_micros / 1_000_000. Percentiles
 * and error_ratio are null on days with no model_operations row matched to
 * an events row (see ModelUsage's doc comment) — rendered not_observed for
 * those days rather than zero. A per-model leaderboard table is added from
 * model_breakdown_range. "Fallback markers" from the wireframe have no
 * backend signal (no fallback/retry-chain column anywhere) — noted as a gap.
 */
import { useMemo } from "react";
import { KpiCard } from "../components/KpiCard";
import { ChartContainer } from "../components/ChartContainer";
import { DataTable, type Column } from "../components/DataTable";
import { GapNote, Panel } from "../components/Panel";
import { RangeControl } from "../components/RangeControl";
import { deriveViewState } from "../api/client";
import { useModelBreakdown, useModelUsage } from "../api/queries";
import { useRange } from "../hooks/useRange";
import { dayLabel, microsToUsd, sum } from "../lib/format";
import { timeSeriesOption } from "../components/chartOptions";
import type { EntityRow } from "../api/types";

export function Models() {
  const range = useRange();
  const rangeParams = useMemo(() => ({ from: range.from, to: range.to }), [range.from, range.to]);
  const usage = useModelUsage(rangeParams);
  const breakdown = useModelBreakdown(rangeParams);

  const rows = usage.data?.data?.data ?? [];
  const state = deriveViewState(usage.data, { isLoading: usage.isLoading });
  const requestsTotal = sum(rows.map((r) => r.request_count));
  const tokensTotal = sum(rows.map((r) => r.total_tokens));
  const costTotalUsd = microsToUsd(sum(rows.map((r) => r.estimated_cost_micros)));
  const errorRows = rows.filter((r) => r.error_ratio != null);

  const breakdownRows = breakdown.data?.data?.data ?? [];
  const breakdownState = deriveViewState(breakdown.data, { isLoading: breakdown.isLoading });
  const modelColumns: Column<EntityRow>[] = [
    { key: "entity_id", header: "Model", render: (r) => r.entity_id },
    { key: "requests", header: "Requests", align: "right", render: (r) => r.event_count.toLocaleString() },
    {
      key: "tokens",
      header: "Tokens",
      align: "right",
      render: (r) => (r.value ?? 0).toLocaleString(),
    },
  ];

  return (
    <section className="k-page">
      <header className="k-page__head">
        <h1 className="t-page-title">Models</h1>
        <p className="k-page__wire t-caption">
          Request/token volume, latency, error ratio and estimated cost.
        </p>
      </header>

      <Panel title="Model usage" actions={<RangeControl range={range} />}>
        <div className="k-grid k-grid--kpis">
          <KpiCard label="Requests" value={requestsTotal} state={state} />
          <KpiCard label="Tokens" value={tokensTotal} state={state} />
          <KpiCard
            label="Error ratio (days observed)"
            value={
              errorRows.length > 0
                ? Math.round((100 * sum(errorRows.map((r) => r.error_ratio ?? 0))) / errorRows.length)
                : null
            }
            unit="%"
            state={errorRows.length === 0 && rows.length > 0 ? "not_observed" : state}
          />
        </div>
        {rows.length > 0 ? (
          <ChartContainer
            ariaLabel="Model requests and tokens per day"
            option={timeSeriesOption(
              rows.map((r) => dayLabel(r.day)),
              [
                { name: "Requests", data: rows.map((r) => r.request_count) },
                { name: "Tokens", data: rows.map((r) => r.total_tokens) },
              ],
            )}
          />
        ) : (
          <p className="t-body" style={{ color: "var(--text-muted)" }}>
            {usage.isLoading ? "Loading…" : "No model usage observed in this range."}
          </p>
        )}
        {errorRows.length > 0 && (
          <ChartContainer
            ariaLabel="Model p95 latency in milliseconds, for days with a matched event"
            option={timeSeriesOption(
              errorRows.map((r) => dayLabel(r.day)),
              [
                {
                  name: "p95 latency (ms)",
                  data: errorRows.map((r) => r.percentiles?.p95 ?? null),
                  color: "var(--status-degraded)",
                },
              ],
            )}
          />
        )}
        <GapNote>
          Fallback markers are not shown: there is no fallback/retry-chain column in the
          backend schema to build this from.
        </GapNote>
      </Panel>

      <Panel title="Estimated cost">
        <div className="k-grid k-grid--kpis">
          <KpiCard label="Estimated cost" value={costTotalUsd} unit="USD" precision={2} state={state} />
        </div>
        {rows.length > 0 && (
          <ChartContainer
            ariaLabel="Estimated cost per day, in USD"
            option={timeSeriesOption(
              rows.map((r) => dayLabel(r.day)),
              [
                {
                  name: "Cost (USD)",
                  data: rows.map((r) => microsToUsd(r.estimated_cost_micros)),
                  color: "var(--accent-gold)",
                  type: "bar",
                },
              ],
            )}
          />
        )}
      </Panel>

      <Panel title="Per-model leaderboard">
        <DataTable
          columns={modelColumns}
          rows={breakdownRows}
          rowKey={(r) => r.entity_id}
          emptyMessage={breakdown.isLoading ? "Loading…" : "No per-model activity in this range."}
        />
        {breakdownState !== "complete" && breakdownState !== "loading" && breakdownRows.length === 0 && (
          <p className="t-caption" style={{ color: "var(--text-faint)" }}>
            Coverage state: {breakdownState}
          </p>
        )}
      </Panel>
    </section>
  );
}
