/*
 * Overview ("/") — health strip; KPI row; activity/model trend; component
 * funnel; incident list (contracts/dashboard.yaml panelIds:
 * overview-collection-health, overview-activity, overview-component-funnel,
 * overview-incidents).
 *
 * overview-collection-health's 3 declared metrics (collection.coverage_ratio,
 * collection.completeness_duration_seconds, collection.acknowledged_durability_ratio)
 * have no durable backing table anywhere in the backend — rendered
 * unsupported. /api/v1/completeness's real known/unknown event ratio is
 * surfaced alongside it as a distinct, clearly-labeled figure (never
 * conflated with collection.coverage_ratio).
 */
import { useMemo } from "react";
import { KpiCard } from "../components/KpiCard";
import { ChartContainer } from "../components/ChartContainer";
import { DataTable, type Column } from "../components/DataTable";
import { Panel, UnsupportedPanel } from "../components/Panel";
import { PercentageDisplay } from "../components/PercentageDisplay";
import { RangeControl } from "../components/RangeControl";
import { StatusBadge } from "../components/StatusBadge";
import { deriveViewState } from "../api/client";
import {
  useActivityTimeline,
  useCompletenessSummary,
  useComponentLifecycleFunnel,
  useIncidents,
  useModelUsage,
  useReliabilityCounts,
} from "../api/queries";
import { useRange } from "../hooks/useRange";
import { dayLabel, sum } from "../lib/format";
import { funnelBarOption, timeSeriesOption } from "../components/chartOptions";
import type { Incident } from "../api/types";

const STAGE_LABELS: Record<string, string> = {
  opportunity_detected: "Opportunity detected",
  installed: "Installed",
  enabled: "Enabled",
  exposed: "Exposed",
  invoked: "Invoked",
  loaded: "Loaded",
  executed: "Executed",
  succeeded: "Succeeded",
};

export function Overview() {
  const range = useRange();
  const rangeParams = useMemo(() => ({ from: range.from, to: range.to }), [range.from, range.to]);

  const activity = useActivityTimeline(rangeParams);
  const modelUsage = useModelUsage(rangeParams);
  const funnel = useComponentLifecycleFunnel(rangeParams, "");
  const reliabilityCounts = useReliabilityCounts(rangeParams);
  const incidents = useIncidents();
  const completeness = useCompletenessSummary();

  const activityData = activity.data?.data?.data ?? [];
  const sessionsTotal = sum(activityData.map((r) => r.session_count));
  const promptsTotal = sum(activityData.map((r) => r.prompt_count));
  const tokensTotal = sum((modelUsage.data?.data?.data ?? []).map((r) => r.total_tokens));

  const activityState = deriveViewState(activity.data, {
    isLoading: activity.isLoading,
    isEmptyMeasuredZero: activityData.length > 0 && sessionsTotal === 0,
  });
  const modelState = deriveViewState(modelUsage.data, { isLoading: modelUsage.isLoading });

  const funnelRows = funnel.data?.data?.data ?? [];
  const funnelState = deriveViewState(funnel.data, { isLoading: funnel.isLoading });
  const installed = funnelRows.find((r) => r.stage === "installed")?.component_count ?? 0;
  const enabled = funnelRows.find((r) => r.stage === "enabled")?.component_count ?? 0;
  const invoked = funnelRows.find((r) => r.stage === "invoked")?.component_count ?? 0;
  const succeeded = funnelRows.find((r) => r.stage === "succeeded")?.component_count ?? 0;

  const incidentColumns: Column<Incident>[] = [
    { key: "incident_id", header: "Incident", render: (r) => r.incident_id },
    { key: "failure_class", header: "Failure class", render: (r) => r.failure_class },
    { key: "capability_id", header: "Capability", render: (r) => r.capability_id },
    {
      key: "first_seen_at",
      header: "First seen",
      render: (r) => new Date(r.first_seen_at).toLocaleString(),
    },
  ];

  return (
    <section className="k-page">
      <header className="k-page__head">
        <h1 className="t-page-title">Overview</h1>
        <p className="k-page__wire t-caption">
          Health strip, activity/model trend, component lifecycle funnel, incidents.
        </p>
      </header>

      <UnsupportedPanel
        title="Collection health"
        reason={
          <>
            <code>collection.coverage_ratio</code>, <code>collection.completeness_duration_seconds</code> and{" "}
            <code>collection.acknowledged_durability_ratio</code> have no durable backing table yet — see{" "}
            <code>internal/runtime/diagnostics.go</code>. The figure below is a related but distinct real
            signal: the share of ingested events with a known (non-<code>unknown</code>/
            <code>not_observed</code>) value state, from <code>/api/v1/completeness</code>.
          </>
        }
      />
      {completeness.data?.data && (
        <Panel title="Event completeness (distinct from collection.coverage_ratio)">
          <PercentageDisplay
            numerator={completeness.data.data.numerator}
            denominator={completeness.data.data.denominator}
            completeness={deriveViewState(completeness.data, { isLoading: completeness.isLoading }) as never}
          />
        </Panel>
      )}

      <Panel title="Activity & model usage" actions={<RangeControl range={range} />}>
        <div className="k-grid k-grid--kpis">
          <KpiCard label="Sessions" value={sessionsTotal} state={activityState} />
          <KpiCard label="Prompts" value={promptsTotal} state={activityState} />
          <KpiCard label="Tokens" value={tokensTotal} state={modelState} precision={0} />
        </div>
        {activityData.length > 0 ? (
          <ChartContainer
            ariaLabel="Sessions and prompts over time"
            option={timeSeriesOption(
              activityData.map((r) => dayLabel(r.day)),
              [
                { name: "Sessions", data: activityData.map((r) => r.session_count) },
                { name: "Prompts", data: activityData.map((r) => r.prompt_count) },
              ],
            )}
          />
        ) : (
          <p className="t-body" style={{ color: "var(--text-muted)" }}>
            {activity.isLoading ? "Loading…" : "No activity observed in this range."}
          </p>
        )}
      </Panel>

      <Panel title="Component lifecycle funnel (all kinds)">
        <div className="k-grid k-grid--kpis">
          <KpiCard label="Installed" value={installed} state={funnelState} />
          <KpiCard
            label="Activation ratio"
            value={installed > 0 ? Math.round((100 * enabled) / installed) : null}
            unit="%"
            state={funnelState}
          />
          <KpiCard
            label="Success ratio"
            value={invoked > 0 ? Math.round((100 * succeeded) / invoked) : null}
            unit="%"
            state={funnelState}
          />
        </div>
        {funnelRows.length > 0 ? (
          <ChartContainer
            ariaLabel="Component lifecycle funnel by stage"
            option={funnelBarOption(
              funnelRows.map((r) => STAGE_LABELS[r.stage] ?? r.stage),
              funnelRows.map((r) => r.component_count),
            )}
          />
        ) : (
          <p className="t-body" style={{ color: "var(--text-muted)" }}>
            {funnel.isLoading ? "Loading…" : "No component lifecycle events observed in this range."}
          </p>
        )}
      </Panel>

      <Panel title="Incidents & drift">
        <div className="k-grid k-grid--kpis">
          <KpiCard
            label="Unknown schema (range)"
            value={sum((reliabilityCounts.data?.data?.data ?? []).map((r) => r.unknown_schema_count))}
            state={deriveViewState(reliabilityCounts.data, { isLoading: reliabilityCounts.isLoading })}
          />
          <KpiCard
            label="Reconciliation mismatches (range)"
            value={sum(
              (reliabilityCounts.data?.data?.data ?? []).map((r) => r.reconciliation_mismatch_count),
            )}
            state={deriveViewState(reliabilityCounts.data, { isLoading: reliabilityCounts.isLoading })}
          />
          <div>
            <div className="t-section-header" style={{ color: "var(--text-muted)", marginBottom: 8 }}>
              Open incidents
            </div>
            <StatusBadge state={incidents.isLoading ? "unknown" : "complete"} glyphOnly />
            <span className="t-body" style={{ marginLeft: 8 }}>
              {incidents.data?.data?.length ?? 0}
            </span>
          </div>
        </div>
        <DataTable
          columns={incidentColumns}
          rows={(incidents.data?.data ?? []).slice(0, 10)}
          rowKey={(r) => r.incident_id}
          emptyMessage={incidents.isLoading ? "Loading…" : "No open incidents."}
        />
      </Panel>
    </section>
  );
}
