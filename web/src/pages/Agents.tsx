/*
 * Agents ("/agents") — installation/version table; surface activity;
 * capability support matrix (contracts/dashboard.yaml panelId: agent-fleet).
 *
 * AgentBreakdown groups internal/dataplatform's raw `events` table by
 * agent_installation_id (see entity_breakdown.go): event_count/success_count/
 * failure_count are *event* counts, not session or prompt counts. The table
 * below labels the column "Events" (not "Sessions") to stay honest about
 * what's actually measured; sessions/prompts are inventory-level only
 * (agent_installations total, not per-agent) from /api/v1/inventory.
 *
 * collection.coverage_ratio has no durable backing table — rendered as an
 * explicit unsupported badge per row rather than a fabricated percentage.
 *
 * "Version table" and "capability support matrix" from the wireframe are not
 * buildable: there's no per-agent version/capability breakdown endpoint
 * (adapter_versions is a flat inventory count, not joined to installations
 * here) — noted as a gap.
 */
import { useMemo } from "react";
import { Link } from "wouter";
import { KpiCard } from "../components/KpiCard";
import { DataTable, type Column } from "../components/DataTable";
import { GapNote, Panel } from "../components/Panel";
import { RangeControl } from "../components/RangeControl";
import { StatusBadge } from "../components/StatusBadge";
import { deriveViewState } from "../api/client";
import { useAgentBreakdown, useInventory } from "../api/queries";
import { useRange } from "../hooks/useRange";
import type { EntityRow } from "../api/types";

function agentLabel(agentID?: string): string {
  if (!agentID) return "Unknown agent";
  return agentID
    .split(/[-_]/)
    .filter(Boolean)
    .map((part) => part.charAt(0).toUpperCase() + part.slice(1))
    .join(" ");
}

export function Agents() {
  const range = useRange();
  const rangeParams = useMemo(
    () => ({ from: range.from, to: range.to, granularity: range.granularity, timezone: range.timezone }),
    [range.from, range.to, range.granularity, range.timezone],
  );
  const inventory = useInventory();
  const breakdown = useAgentBreakdown(rangeParams);

  const rows = breakdown.data?.data?.data ?? [];
  const state = deriveViewState(breakdown.data, { isLoading: breakdown.isLoading });

  const columns: Column<EntityRow>[] = [
    {
      key: "agent_id",
      header: "Agent",
      render: (r) => (
        <Link href={`/agents/${encodeURIComponent(r.entity_id)}`} className="t-table-cell">
          {r.display_alias || r.display_name || agentLabel(r.agent_id)}
        </Link>
      ),
    },
    { key: "provider", header: "Provider", render: (r) => r.provider_id || r.agent_id || "Unknown" },
    { key: "surface", header: "Surface", render: (r) => r.surface_kind || "Unknown" },
    {
      key: "version",
      header: "Version",
      render: (r) => r.agent_version || r.adapter_version || "Not observed",
    },
    {
      key: "entity_id",
      header: "Installation",
      render: (r) => (
        <span className="t-caption" title={r.entity_id}>
          {r.entity_id.length > 18 ? `${r.entity_id.slice(0, 14)}…` : r.entity_id}
        </span>
      ),
    },
    { key: "event_count", header: "Events", align: "right", render: (r) => r.event_count.toLocaleString() },
    {
      key: "success_count",
      header: "Succeeded",
      align: "right",
      render: (r) => r.success_count.toLocaleString(),
    },
    {
      key: "failure_count",
      header: "Failed",
      align: "right",
      render: (r) => r.failure_count.toLocaleString(),
    },
    {
      key: "coverage",
      header: "Coverage",
      render: () => (
        <StatusBadge
          state="unknown"
          glyphOnly
          reason="No per-installation expected-event population has been persisted yet."
        />
      ),
    },
  ];

  return (
    <section className="k-page">
      <header className="k-page__head">
        <h1 className="t-page-title">Agents</h1>
        <p className="k-page__wire t-caption">
          Fleet-wide agent installations and per-agent event activity.
        </p>
      </header>

      <Panel title="Fleet inventory">
        <div className="k-grid k-grid--kpis">
          <KpiCard
            label="Agent installations"
            value={inventory.data?.data?.agent_installations ?? null}
            state={deriveViewState(inventory.data, { isLoading: inventory.isLoading })}
          />
          <KpiCard
            label="Agent surfaces"
            value={inventory.data?.data?.agent_surfaces ?? null}
            state={deriveViewState(inventory.data, { isLoading: inventory.isLoading })}
          />
          <KpiCard
            label="Adapter versions"
            value={inventory.data?.data?.adapter_versions ?? null}
            state={deriveViewState(inventory.data, { isLoading: inventory.isLoading })}
          />
        </div>
      </Panel>

      <Panel title="Per-agent event activity" actions={<RangeControl range={range} />}>
        <DataTable
          columns={columns}
          rows={rows}
          rowKey={(r) => r.entity_id}
          emptyMessage={breakdown.isLoading ? "Loading…" : "No agent activity observed in this range."}
        />
        {state !== "loading" && rows.length === 0 && (
          <p className="t-caption" style={{ color: "var(--text-faint)" }}>
            Coverage state: <StatusBadge state={state} glyphOnly /> {state}
          </p>
        )}
        <GapNote>
          The agent name comes from the adapter identity stored with the installation.
          The shortened <code>ain_…</code> value is its privacy-safe technical key, not
          the model or provider name. Per-installation capability coverage still needs
          durable expected-event populations.
        </GapNote>
      </Panel>
    </section>
  );
}
