/*
 * Overview ("/") — health strip; KPI row; activity/model trend; component
 * funnel; incident list (contracts/dashboard.yaml panelIds:
 * overview-collection-health, overview-activity, overview-component-funnel,
 * overview-incidents).
 *
 * Collection health leads with durable ingestion facts: accepted events,
 * source-supplied values, and quarantined schema observations. It does not
 * relabel event-value completeness as source coverage.
 */
import { useMemo } from "react";
import { KpiCard } from "../components/KpiCard";
import { ChartContainer } from "../components/ChartContainer";
import { DataTable, type Column } from "../components/DataTable";
import { GapNote, Panel } from "../components/Panel";
import { PercentageDisplay } from "../components/PercentageDisplay";
import { RangeControl } from "../components/RangeControl";
import { StatusBadge } from "../components/StatusBadge";
import { deriveViewState, type ViewState } from "../api/client";
import {
  useActivityTimeline,
  useCompletenessSummary,
  useComponentLifecycleFunnel,
  useIncidents,
  useModelUsage,
  useReliabilityCounts,
} from "../api/queries";
import { useRange } from "../hooks/useRange";
import { sum } from "../lib/format";
import { bucketedTimeSeriesOption, funnelBarOption } from "../components/chartOptions";
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
  const rangeParams = useMemo(
    () => ({ from: range.from, to: range.to, granularity: range.granularity, timezone: range.timezone }),
    [range.from, range.to, range.granularity, range.timezone],
  );

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
  const completenessState = deriveViewState(completeness.data, { isLoading: completeness.isLoading });
  const knownEvents = completeness.data?.data?.numerator ?? 0;
  const acceptedEvents = completeness.data?.data?.denominator ?? 0;
  const unknownSchemaTotal = sum(
    (reliabilityCounts.data?.data?.data ?? []).map((r) => r.unknown_schema_count),
  );

  const funnelRows = funnel.data?.data?.data ?? [];
  const funnelState = deriveViewState(funnel.data, { isLoading: funnel.isLoading });
  const installedRow = funnelRows.find((r) => r.stage === "installed");
  const enabledRow = funnelRows.find((r) => r.stage === "enabled");
  const executedRow = funnelRows.find((r) => r.stage === "executed");
  const succeededRow = funnelRows.find((r) => r.stage === "succeeded");
  const installed = installedRow?.component_count ?? 0;
  const enabled = enabledRow?.component_count ?? 0;
  const executed = executedRow?.component_count ?? 0;
  const succeeded = succeededRow?.component_count ?? 0;
  const funnelRowState = (valueState: string | undefined): ViewState =>
    funnel.isLoading ? "loading" : ((valueState as ViewState | undefined) ?? funnelState);

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

      <Panel
        title="Collection health"
        caption="Durably accepted telemetry and schema evidence. Raw prompts, responses, tool payloads, and environment values are excluded."
      >
        <div className="k-grid k-grid--kpis">
          <KpiCard label="Accepted events" value={acceptedEvents} state={completenessState} />
          <KpiCard label="Source-supplied values" value={knownEvents} state={completenessState} />
          <KpiCard
            label="Unknown schema (range)"
            value={unknownSchemaTotal}
            state={deriveViewState(reliabilityCounts.data, { isLoading: reliabilityCounts.isLoading })}
          />
        </div>
        {completeness.data?.data && (
          <PercentageDisplay
            numerator={knownEvents}
            denominator={acceptedEvents}
            completeness={completenessState as never}
          />
        )}
        <GapNote>
          Event completeness is the share of accepted events with a source-supplied value state. Source
          coverage needs an independently observed expected-event population and is not inferred from this
          ratio.
        </GapNote>
      </Panel>

      <Panel title="Activity & model usage" actions={<RangeControl range={range} />}>
        <div className="k-grid k-grid--kpis">
          <KpiCard label="Sessions" value={sessionsTotal} state={activityState} />
          <KpiCard label="Prompts" value={promptsTotal} state={activityState} />
          <KpiCard label="Tokens" value={tokensTotal} state={modelState} precision={0} />
        </div>
        {activityData.length > 0 ? (
          <ChartContainer
            ariaLabel="Sessions and prompts over time"
            option={bucketedTimeSeriesOption(
              range,
              activityData,
              [
                { name: "Sessions", value: (r) => r.session_count },
                { name: "Prompts", value: (r) => r.prompt_count },
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
          <KpiCard
            label="Installed"
            value={installed}
            state={funnelRowState(installedRow?.value_state)}
          />
          <KpiCard
            label="Enablement ratio"
            value={installed > 0 ? Math.round((100 * enabled) / installed) : null}
            unit="%"
            state={installed > 0 ? funnelRowState(enabledRow?.value_state) : "not_observed"}
          />
          <KpiCard
            label="Success ratio"
            value={executed > 0 ? Math.round((100 * succeeded) / executed) : null}
            unit="%"
            state={executed > 0 ? funnelRowState(succeededRow?.value_state) : "not_observed"}
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
        <GapNote>
          Installed and enabled are current inventory observations. Later stages
          require runtime lifecycle evidence and remain “Not observed” when the
          active agent interface emits no qualifying signal.
        </GapNote>
      </Panel>

      <Panel title="Incidents & drift">
        <div className="k-grid k-grid--kpis">
          <KpiCard
            label="Unknown schema (range)"
            value={unknownSchemaTotal}
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
