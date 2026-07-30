/*
 * Overview ("/") — health strip; KPI row; activity/model trend; independent
 * skill evidence planes; incident list (contracts/dashboard.yaml panelIds:
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
import { deriveViewState } from "../api/client";
import {
  useActivityTimeline,
  useCompletenessSummary,
  useIncidents,
  useModelUsage,
  useReliabilityCounts,
  useSkills,
} from "../api/queries";
import { useRange } from "../hooks/useRange";
import { sum } from "../lib/format";
import { bucketedTimeSeriesOption } from "../components/chartOptions";
import type { Incident } from "../api/types";

export function Overview() {
  const range = useRange("overview");
  const rangeParams = useMemo(
    () => ({ from: range.from, to: range.to, granularity: range.granularity, timezone: range.timezone }),
    [range.from, range.to, range.granularity, range.timezone],
  );

  const activity = useActivityTimeline(rangeParams);
  const modelUsage = useModelUsage(rangeParams);
  const skills = useSkills(rangeParams);
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

  const skillState = deriveViewState(skills.data, { isLoading: skills.isLoading });
  const skillCounts = skills.data?.data?.counts;

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
          Health strip, activity/model trend, skill evidence planes, incidents.
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

      <Panel title="Skill evidence planes">
        <div className="k-grid k-grid--kpis">
          <KpiCard label="Installed" value={skillCounts?.installed ?? null} state={skillState} />
          <KpiCard label="Enabled" value={skillCounts?.enabled ?? null} state={skillState} />
          <KpiCard label="Exposed" value={skillCounts?.exposed ?? null} state={skillState} />
          <KpiCard label="Invoked" value={skillCounts?.invoked ?? null} state={skillState} />
          <KpiCard label="Loaded" value={skillCounts?.loaded ?? null} state={skillState} />
          <KpiCard label="Cold" value={skillCounts?.cold ?? null} state={skillState} />
        </div>
        <GapNote>
          Availability and runtime are independent populations. Cold requires a
          complete exposure window. “Executed” is not a universal skill state, and
          outcome remains unsupported without a registered terminal contract.
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
              {incidents.data?.data?.data?.length ?? 0}
            </span>
          </div>
        </div>
        <DataTable
          columns={incidentColumns}
          rows={(incidents.data?.data?.data ?? []).slice(0, 10)}
          rowKey={(r) => r.incident_id}
          emptyMessage={incidents.isLoading ? "Loading…" : "No open incidents."}
        />
      </Panel>
    </section>
  );
}
