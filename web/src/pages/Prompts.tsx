/*
 * Prompts ("/prompts") — count timeline; byte percentile band; calendar and
 * hour/weekday heatmaps (contracts/dashboard.yaml panelId: prompt-shape).
 *
 * Calendar/hour/weekday heatmaps from the wireframe are not buildable:
 * /api/v1/prompts/shape is a daily-only series with no hour-of-day or
 * day-of-week dimension — noted as a gap, not fabricated.
 */
import { useMemo } from "react";
import { KpiCard } from "../components/KpiCard";
import { ChartContainer } from "../components/ChartContainer";
import { GapNote, Panel } from "../components/Panel";
import { RangeControl } from "../components/RangeControl";
import { deriveViewState } from "../api/client";
import { usePromptShape } from "../api/queries";
import { useRange } from "../hooks/useRange";
import { dayLabel, sum } from "../lib/format";
import { timeSeriesOption } from "../components/chartOptions";

export function Prompts() {
  const range = useRange();
  const rangeParams = useMemo(() => ({ from: range.from, to: range.to }), [range.from, range.to]);
  const shape = usePromptShape(rangeParams);
  const rows = shape.data?.data?.data ?? [];
  const state = deriveViewState(shape.data, { isLoading: shape.isLoading });
  const promptsTotal = sum(rows.map((r) => r.prompt_count));
  const percentileRows = rows.filter((r) => r.percentiles?.p50 != null);

  return (
    <section className="k-page">
      <header className="k-page__head">
        <h1 className="t-page-title">Prompt metadata</h1>
        <p className="k-page__wire t-caption">
          Prompt count timeline and byte-length percentile band (byte-length metadata
          only — raw prompt text is never read or returned).
        </p>
      </header>

      <Panel title="Prompt shape" actions={<RangeControl range={range} />}>
        <div className="k-grid k-grid--kpis">
          <KpiCard label="Prompts" value={promptsTotal} state={state} />
          <KpiCard
            label="Median size (p50, last day)"
            value={rows.length > 0 ? (rows[rows.length - 1]?.percentiles?.p50 ?? null) : null}
            unit="bytes"
            state={
              rows.length > 0 && rows[rows.length - 1]?.percentiles?.p50 == null
                ? "not_observed"
                : state
            }
          />
        </div>
        {rows.length > 0 ? (
          <ChartContainer
            ariaLabel="Prompt count per day"
            option={timeSeriesOption(
              rows.map((r) => dayLabel(r.day)),
              [{ name: "Prompts", data: rows.map((r) => r.prompt_count) }],
            )}
          />
        ) : (
          <p className="t-body" style={{ color: "var(--text-muted)" }}>
            {shape.isLoading ? "Loading…" : "No prompts observed in this range."}
          </p>
        )}
        {percentileRows.length > 0 ? (
          <ChartContainer
            ariaLabel="Prompt byte-length percentile band (p50/p90/p95/p99)"
            option={timeSeriesOption(
              percentileRows.map((r) => dayLabel(r.day)),
              [
                { name: "p50", data: percentileRows.map((r) => r.percentiles?.p50 ?? null) },
                { name: "p90", data: percentileRows.map((r) => r.percentiles?.p90 ?? null) },
                { name: "p95", data: percentileRows.map((r) => r.percentiles?.p95 ?? null) },
                { name: "p99", data: percentileRows.map((r) => r.percentiles?.p99 ?? null) },
              ],
            )}
          />
        ) : (
          rows.length > 0 && (
            <p className="t-body" style={{ color: "var(--text-muted)" }}>
              No days in range have a byte-length percentile (prompt_size_bytes was null
              for every prompt observed).
            </p>
          )
        )}
        <GapNote>
          A calendar heatmap and hour/weekday heatmaps are not shown: the backend's
          prompt-shape series is bucketed per calendar day only, with no hour-of-day or
          day-of-week dimension available to build either honestly.
        </GapNote>
      </Panel>
    </section>
  );
}
