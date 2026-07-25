/*
 * Agent detail ("/agents/:id") — version markers; activity/model mix;
 * capability coverage; evidence sources (contracts/dashboard.yaml panelIds:
 * agent-detail-usage, agent-detail-coverage).
 *
 * KNOWN GAP: no backend endpoint filters any breakdown/timeline to a single
 * agent. This page fetches agent_breakdown_range for the whole selected
 * range and finds the row whose entity_id matches the route's opaque :id
 * (never anything but the opaque id appears in the URL, per
 * safe_url_policy). That row only distinguishes event/success/failure
 * counts — activity.sessions/activity.prompts/model.requests/model.tokens
 * cannot be honestly derived from it, so each is rendered `unsupported`
 * rather than approximated from the event count. agent-detail-coverage's
 * two declared metrics (collection.coverage_ratio,
 * collection.active_source_gap_seconds) have no durable backing table at
 * all and are rendered unsupported outright.
 *
 * A real per-agent time-series/filter endpoint is a reasonable Session 11+
 * follow-up; this page does not fabricate one now.
 */
import { useMemo } from "react";
import { KpiCard } from "../components/KpiCard";
import { Panel, UnsupportedPanel } from "../components/Panel";
import { RangeControl } from "../components/RangeControl";
import { StatusBadge } from "../components/StatusBadge";
import { deriveViewState } from "../api/client";
import { useAgentBreakdown } from "../api/queries";
import { useRange } from "../hooks/useRange";

export interface AgentDetailProps {
  /** Opaque alias from the route, safe_url_policy compliant. */
  alias: string;
}

export function AgentDetail({ alias }: AgentDetailProps) {
  const range = useRange();
  const rangeParams = useMemo(() => ({ from: range.from, to: range.to }), [range.from, range.to]);
  const breakdown = useAgentBreakdown(rangeParams);

  const rows = breakdown.data?.data?.data ?? [];
  const row = rows.find((r) => r.entity_id === alias);
  const state = deriveViewState(breakdown.data, { isLoading: breakdown.isLoading });
  const rowFoundState = breakdown.isLoading ? "loading" : row ? state : "not_observed";

  return (
    <section className="k-page">
      <header className="k-page__head">
        <h1 className="t-page-title">Agent {alias}</h1>
        <p className="k-page__wire t-caption">
          Version markers; activity/model mix; capability coverage; evidence sources.
        </p>
      </header>

      <Panel title="Event activity" actions={<RangeControl range={range} />}>
        {!breakdown.isLoading && !row && (
          <p className="t-body" style={{ color: "var(--text-muted)" }}>
            No event activity found for this agent installation in the selected range.
          </p>
        )}
        <div className="k-grid k-grid--kpis">
          <KpiCard label="Events" value={row?.event_count ?? null} state={rowFoundState} />
          <KpiCard label="Succeeded" value={row?.success_count ?? null} state={rowFoundState} />
          <KpiCard label="Failed" value={row?.failure_count ?? null} state={rowFoundState} />
        </div>
        <div className="k-grid k-grid--kpis">
          <KpiCard label="Sessions" value={null} state="unsupported" />
          <KpiCard label="Prompts" value={null} state="unsupported" />
          <KpiCard label="Model requests" value={null} state="unsupported" />
          <KpiCard label="Model tokens" value={null} state="unsupported" />
        </div>
        <p className="t-caption" style={{ color: "var(--text-faint)" }}>
          <StatusBadge state="unsupported" glyphOnly /> Sessions, prompts and model
          request/token counts are not shown per-agent: the only per-agent breakdown
          endpoint (<code>agent_breakdown_range</code>) groups the raw <code>events</code>{" "}
          table and distinguishes only event/success/failure counts, not session,
          prompt or model-operation identity. A dedicated per-agent endpoint is a
          reasonable follow-up, not something this page approximates from the event
          count.
        </p>
      </Panel>

      <UnsupportedPanel
        title="Collection coverage"
        reason={
          <>
            <code>collection.coverage_ratio</code> and{" "}
            <code>collection.active_source_gap_seconds</code> have no durable backing
            table anywhere in the schema — see <code>internal/runtime/diagnostics.go</code>.
          </>
        }
      />

      <UnsupportedPanel
        title="Version markers & capability coverage"
        reason="No backend endpoint reports per-agent adapter-version history or per-agent capability coverage today; version and capability data are only available as fleet-wide inventory counts on /agents."
      />
    </section>
  );
}
