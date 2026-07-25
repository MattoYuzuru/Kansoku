/*
 * MCP ("/components/mcp") — server tree; connection timeline; calls/errors/
 * latency; support gaps (contracts/dashboard.yaml panelId: mcp-health).
 *
 * Topology from /api/v1/components/mcp/topology (server/tool tree),
 * per-server uptime from /api/v1/components/mcp/uptime (uptime_ratio is null
 * until observable_seconds>0; observable_seconds is the real observed
 * window, never the full requested range — surfaced explicitly per row), and
 * calls/success/latency from /api/v1/tools/analytics filtered to the
 * selected MCP server component_id (kept simple: a single-select dropdown
 * over the topology's server ids, per the task's scope boundary against a
 * general filter system).
 */
import { useMemo, useState } from "react";
import { KpiCard } from "../components/KpiCard";
import { ChartContainer } from "../components/ChartContainer";
import { DataTable, type Column } from "../components/DataTable";
import { Dropdown } from "../components/Dropdown";
import { GapNote, Panel } from "../components/Panel";
import { RangeControl } from "../components/RangeControl";
import { StatusBadge } from "../components/StatusBadge";
import { deriveViewState } from "../api/client";
import { useMCPTopology, useMCPUptime, useToolAnalytics } from "../api/queries";
import { useRange } from "../hooks/useRange";
import { dayLabel, sum } from "../lib/format";
import { timeSeriesOption } from "../components/chartOptions";
import type { ComponentTreeNode, MCPUptimeRow } from "../api/types";

export function MCP() {
  const range = useRange();
  const rangeParams = useMemo(() => ({ from: range.from, to: range.to }), [range.from, range.to]);
  const topology = useMCPTopology(rangeParams);
  const uptime = useMCPUptime(rangeParams);

  const servers = topology.data?.data?.data ?? [];
  const [selected, setSelected] = useState<string>("");
  const effectiveComponentId = selected || undefined;
  const toolAnalytics = useToolAnalytics(rangeParams, effectiveComponentId);

  const topologyState = deriveViewState(topology.data, { isLoading: topology.isLoading });
  const uptimeRows = uptime.data?.data?.data ?? [];

  const serverColumns: Column<ComponentTreeNode>[] = [
    { key: "component_id", header: "Server", render: (r) => r.component_id },
    {
      key: "children",
      header: "Child components",
      render: (r) => r.child_component_ids.length.toLocaleString(),
    },
    {
      key: "state",
      header: "Latest connection state",
      render: (r) =>
        r.latest_connection_state ? (
          r.latest_connection_state
        ) : (
          <StatusBadge state="not_observed" glyphOnly />
        ),
    },
  ];

  const uptimeColumns: Column<MCPUptimeRow>[] = [
    { key: "component_id", header: "Server", render: (r) => r.component_id },
    {
      key: "uptime_ratio",
      header: "Uptime",
      render: (r) =>
        r.uptime_ratio == null ? (
          <StatusBadge state="not_observed" glyphOnly />
        ) : (
          `${(r.uptime_ratio * 100).toFixed(1)}%`
        ),
    },
    {
      key: "observable_seconds",
      header: "Observable window (s)",
      align: "right",
      render: (r) => r.observable_seconds.toLocaleString(),
    },
  ];

  const toolRows = toolAnalytics.data?.data?.data ?? [];
  const toolState = deriveViewState(toolAnalytics.data, { isLoading: toolAnalytics.isLoading });

  return (
    <section className="k-page">
      <header className="k-page__head">
        <h1 className="t-page-title">MCP</h1>
        <p className="k-page__wire t-caption">
          Server tree; connection timeline; calls/errors/latency; support gaps.
        </p>
      </header>

      <Panel title="Server topology" actions={<RangeControl range={range} />}>
        <DataTable
          columns={serverColumns}
          rows={servers}
          rowKey={(r) => r.component_id}
          emptyMessage={topology.isLoading ? "Loading…" : "No MCP servers observed in this range."}
        />
        {topologyState !== "complete" && topologyState !== "loading" && servers.length === 0 && (
          <p className="t-caption" style={{ color: "var(--text-faint)" }}>
            Coverage state: {topologyState}
          </p>
        )}
      </Panel>

      <Panel title="Connection uptime">
        <DataTable
          columns={uptimeColumns}
          rows={uptimeRows}
          rowKey={(r) => r.component_id}
          emptyMessage={uptime.isLoading ? "Loading…" : "No MCP connection observations in this range."}
        />
        <p className="t-caption" style={{ color: "var(--text-faint)" }}>
          Observable window is the span between a server's first and last observed
          connection-state change in range — never assumed to cover the full requested
          range.
        </p>
      </Panel>

      <Panel
        title="Calls, success and latency"
        actions={
          <Dropdown
            caption="SERVER"
            options={[
              { value: "", label: "All components" },
              ...servers.map((s) => ({ value: s.component_id, label: s.component_id })),
            ]}
            value={selected}
            onChange={setSelected}
          />
        }
      >
        <div className="k-grid k-grid--kpis">
          <KpiCard label="Calls" value={sum(toolRows.map((r) => r.call_count))} state={toolState} />
          <KpiCard
            label="Succeeded"
            value={sum(toolRows.map((r) => r.success_count))}
            state={toolState}
          />
          <KpiCard label="Failed" value={sum(toolRows.map((r) => r.failure_count))} state={toolState} />
        </div>
        {toolRows.length > 0 ? (
          <ChartContainer
            ariaLabel="MCP tool calls per day"
            option={timeSeriesOption(
              toolRows.map((r) => dayLabel(r.day)),
              [
                { name: "Calls", data: toolRows.map((r) => r.call_count) },
                { name: "Succeeded", data: toolRows.map((r) => r.success_count) },
                { name: "Failed", data: toolRows.map((r) => r.failure_count), color: "var(--status-degraded)" },
              ],
            )}
          />
        ) : (
          <p className="t-body" style={{ color: "var(--text-muted)" }}>
            {toolAnalytics.isLoading ? "Loading…" : "No tool calls observed in this range."}
          </p>
        )}
        <GapNote>
          Support gaps (which capability/component kinds this server declares but never
          exercises) are not shown as a distinct visualization here: the lifecycle
          funnel on Skills/Plugins is the closest honest signal, but no query joins MCP
          topology to lifecycle-stage gaps directly yet.
        </GapNote>
      </Panel>
    </section>
  );
}
