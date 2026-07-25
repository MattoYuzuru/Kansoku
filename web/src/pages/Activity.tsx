/*
 * Activity ("/activity") — timeline; active duration; session distribution;
 * weekday/hour heatmap (contracts/dashboard.yaml panelId: activity-timeline).
 *
 * The weekday/hour heatmap and "session distribution" from the wireframe are
 * not buildable: /api/v1/activity returns one row per calendar day, never an
 * hour-of-day or day-of-week breakdown — building either would mean
 * fabricating a distribution the backend doesn't provide. Both are called
 * out explicitly below rather than invented.
 */
import { useMemo } from "react";
import { KpiCard } from "../components/KpiCard";
import { ChartContainer } from "../components/ChartContainer";
import { GapNote, Panel } from "../components/Panel";
import { RangeControl } from "../components/RangeControl";
import { deriveViewState } from "../api/client";
import { useActivityTimeline } from "../api/queries";
import { useRange } from "../hooks/useRange";
import { dayLabel, secondsToReadable, sum } from "../lib/format";
import { timeSeriesOption } from "../components/chartOptions";

export function Activity() {
  const range = useRange();
  const rangeParams = useMemo(() => ({ from: range.from, to: range.to }), [range.from, range.to]);
  const activity = useActivityTimeline(rangeParams);
  const rows = activity.data?.data?.data ?? [];
  const state = deriveViewState(activity.data, { isLoading: activity.isLoading });

  const sessionsTotal = sum(rows.map((r) => r.session_count));
  const promptsTotal = sum(rows.map((r) => r.prompt_count));
  const durationRows = rows.filter((r) => r.active_duration_seconds !== null);
  const durationTotal = sum(durationRows.map((r) => r.active_duration_seconds));

  return (
    <section className="k-page">
      <header className="k-page__head">
        <h1 className="t-page-title">Activity</h1>
        <p className="k-page__wire t-caption">
          Timeline of sessions, prompts and reconstructed active duration.
        </p>
      </header>

      <Panel title="Activity timeline" actions={<RangeControl range={range} />}>
        <div className="k-grid k-grid--kpis">
          <KpiCard label="Sessions" value={sessionsTotal} state={state} />
          <KpiCard label="Prompts" value={promptsTotal} state={state} />
          <KpiCard
            label="Active duration"
            value={durationRows.length > 0 ? durationTotal : null}
            unit={durationRows.length > 0 ? secondsToReadable(durationTotal) : undefined}
            state={durationRows.length === 0 && rows.length > 0 ? "not_observed" : state}
          />
        </div>
        {rows.length > 0 ? (
          <ChartContainer
            ariaLabel="Sessions and prompts per day"
            option={timeSeriesOption(
              rows.map((r) => dayLabel(r.day)),
              [
                { name: "Sessions", data: rows.map((r) => r.session_count) },
                { name: "Prompts", data: rows.map((r) => r.prompt_count) },
              ],
            )}
          />
        ) : (
          <p className="t-body" style={{ color: "var(--text-muted)" }}>
            {activity.isLoading ? "Loading…" : "No activity observed in this range."}
          </p>
        )}
        {durationRows.length > 0 && (
          <ChartContainer
            ariaLabel="Reconstructed active duration per day, in seconds"
            option={timeSeriesOption(
              durationRows.map((r) => dayLabel(r.day)),
              [
                {
                  name: "Active duration (s)",
                  data: durationRows.map((r) => r.active_duration_seconds),
                  color: "var(--status-complete)",
                },
              ],
            )}
          />
        )}
        <GapNote>
          Session distribution and a weekday/hour heatmap are not shown: the backend's
          activity timeline is bucketed per calendar day only, with no hour-of-day or
          day-of-week breakdown available to build either honestly.
        </GapNote>
      </Panel>
    </section>
  );
}
