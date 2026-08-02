/*
 * Models ("/models") — request/token share; latency/errors; fallback
 * markers; estimated cost (contracts/dashboard.yaml panelIds: model-usage,
 * model-cost).
 *
 * model-usage + model-cost both come from /api/v1/models/usage
 * (ModelUsageResponse); cost = estimated_cost_micros / 1_000_000. Percentiles
 * and error_ratio are null on days with no native request/response evidence
 * carrying those measurements — rendered not_observed for those days rather
 * than zero. A per-model leaderboard table is added from
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
import { microsToUsd, sum } from "../lib/format";
import { bucketedTimeSeriesOption } from "../components/chartOptions";
import type { EntityRow } from "../api/types";

export function Models() {
  const range = useRange("models");
  const rangeParams = useMemo(
    () => ({ from: range.from, to: range.to, granularity: range.granularity, timezone: range.timezone }),
    [range.from, range.to, range.granularity, range.timezone],
  );
  const usage = useModelUsage(rangeParams);
  const breakdown = useModelBreakdown(rangeParams);

  const rows = usage.data?.data?.data ?? [];
  const state = deriveViewState(usage.data, { isLoading: usage.isLoading });
  const requestsTotal = sum(rows.map((r) => r.request_count));
  const tokensTotal = sum(rows.map((r) => r.total_tokens));
  const costTotalUsd = microsToUsd(sum(rows.map((r) => r.estimated_cost_micros)));
  const costedTotal = sum(rows.map((r) => r.costed_request_count));
  const providerCostTotal = sum(rows.map((r) => r.provider_cost_count));
  const upperBoundCostTotal = sum(rows.map((r) => r.upper_bound_cost_count));
  const costRows = rows.filter((r) => r.costed_request_count > 0);
  const costState =
    requestsTotal > 0 && costedTotal === 0
      ? "not_observed"
      : costedTotal < requestsTotal
        ? "partial"
        : state;
  const errorMetric = usage.data?.data?.error_ratio_metric;
  const latencyRows = rows.filter((r) => r.percentiles?.p95 != null);

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
    {
      key: "estimated_cost",
      header: "API-equivalent cost",
      align: "right",
      render: (r) =>
        (r.costed_count ?? 0) > 0
          ? `$${microsToUsd(r.estimated_cost_micros ?? 0).toFixed(2)}`
          : "Not observed",
    },
    {
      key: "cost_coverage",
      header: "Cost coverage",
      align: "right",
      render: (r) => `${(r.costed_count ?? 0).toLocaleString()} / ${r.event_count.toLocaleString()}`,
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
            label="Error ratio"
            value={
              errorMetric?.value != null
                ? errorMetric.value * 100
                : null
            }
            unit="%"
            precision={2}
            state={errorMetric?.completeness.status ?? (rows.length > 0 ? "not_observed" : state)}
            stateReason={
              errorMetric
                ? `${errorMetric.population.numerator} failed / ${errorMetric.population.denominator} terminal; ${errorMetric.exclusions.non_terminal_or_unknown_outcome ?? 0} unknown or non-terminal excluded; ${errorMetric.formula_version}.`
                : undefined
            }
          />
        </div>
        {rows.length > 0 ? (
          <ChartContainer
            ariaLabel="Model requests and tokens per selected time bucket"
            option={bucketedTimeSeriesOption(
              range,
              rows,
              [
                { name: "Requests", value: (r) => r.request_count },
                { name: "Tokens", value: (r) => r.total_tokens },
              ],
            )}
          />
        ) : (
          <p className="t-body" style={{ color: "var(--text-muted)" }}>
            {usage.isLoading ? "Loading…" : "No model usage observed in this range."}
          </p>
        )}
        {latencyRows.length > 0 && (
          <ChartContainer
            ariaLabel="Model p95 request latency in milliseconds, for buckets with native duration observations"
            option={bucketedTimeSeriesOption(
              range,
              latencyRows,
              [
                {
                  name: "p95 latency (ms)",
                  value: (r) => r.percentiles?.p95,
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

      <Panel title="API-equivalent estimated cost">
        <div className="k-grid k-grid--kpis">
          <KpiCard
            label="Estimated cost"
            value={costedTotal > 0 ? costTotalUsd : null}
            unit="USD"
            precision={2}
            state={costState}
            stateReason={
              costedTotal === 0
                ? "No provider-reported cost or matching public API price was available."
                : `${costedTotal} of ${requestsTotal} responses have cost evidence.`
            }
          />
          <KpiCard label="Costed responses" value={costedTotal} state={costState} />
        </div>
        {costRows.length > 0 && (
          <ChartContainer
            ariaLabel="API-equivalent estimated cost per selected time bucket, in USD"
            option={bucketedTimeSeriesOption(
              range,
              costRows,
              [
                {
                  name: "Cost (USD)",
                  value: (r) => microsToUsd(r.estimated_cost_micros),
                  color: "var(--accent-gold)",
                  type: "bar",
                },
              ],
            )}
          />
        )}
        <GapNote>
          This is not a ChatGPT or Codex subscription invoice. Provider-reported cost
          is used when present; otherwise Kansoku applies the versioned public API token
          price. {upperBoundCostTotal} response{upperBoundCostTotal === 1 ? "" : "s"} use
          an uncached-input upper bound because cached-token metadata was not observed;
          {` ${providerCostTotal}`} use provider-reported cost.
        </GapNote>
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
