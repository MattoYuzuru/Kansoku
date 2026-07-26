import { useMemo } from "react";
import { Link } from "wouter";
import { deriveViewState } from "../api/client";
import { useMCPObservatory } from "../api/queries";
import type { MCPServerRow } from "../api/types";
import { DataTable, type Column } from "../components/DataTable";
import { KpiCard } from "../components/KpiCard";
import { GapNote, Panel } from "../components/Panel";
import { RangeControl } from "../components/RangeControl";
import { useRange } from "../hooks/useRange";

export function MCP() {
  const range = useRange();
  const params = useMemo(() => ({ from: range.from, to: range.to, granularity: range.granularity, timezone: range.timezone }),
    [range.from, range.to, range.granularity, range.timezone]);
  const query = useMCPObservatory(params);
  const response = query.data?.data;
  const rows = response?.data ?? [];
  const state = deriveViewState(query.data, { isLoading: query.isLoading });
  const columns: Column<MCPServerRow>[] = [
    { key: "server", header: "Server", render: (r) => <Link href={`/components/mcp/${r.server_component_id}`}>{r.declared_name}</Link> },
    { key: "transport", header: "Transport", render: (r) => `${r.transport} · ${r.locality}` },
    { key: "inventory", header: "Inventory", render: (r) => `${r.tool_count} tools · ${r.enumeration_completeness}` },
    { key: "connection", header: "Connection", render: (r) => r.latest_connection_state },
    { key: "calls", header: "Calls", align: "right", render: (r) => r.call_count },
    { key: "terminals", header: "Terminals", align: "right", render: (r) => r.terminal_count },
    { key: "uptime", header: "Observed uptime", render: (r) => r.uptime_ratio == null ? "Not observed" : `${(r.uptime_ratio*100).toFixed(1)}% (${r.connected_seconds}/${r.observable_seconds}s)` },
  ];
  return <section className="k-page">
    <header className="k-page__head"><h1 className="t-page-title">MCP</h1>
      <p className="k-page__wire t-caption">Independent configuration, protocol connection and call evidence.</p></header>
    <Panel title="Evidence contours" actions={<RangeControl range={range}/>}>
      <div className="k-grid k-grid--kpis">
        <KpiCard label="Configured" value={response ? rows.filter((r)=>r.configured).length : null} state={state}/>
        <KpiCard label="Connected now" value={response ? rows.filter((r)=>r.latest_connection_state==="connected").length : null} state={state}/>
        <KpiCard label="Observed calls" value={response ? rows.reduce((n,r)=>n+r.call_count,0) : null} state={state}/>
      </div>
      <GapNote>Configured does not mean connected, connected does not mean advertised, and protocol completion is not user-task success. Uptime covers only real observed transitions.</GapNote>
    </Panel>
    <Panel title="Server inventory and evidence" caption={`Population ${response?.population.numerator ?? 0}/${response?.population.denominator ?? 0}; incomplete pagination ${response?.exclusions.incomplete_pagination ?? 0}.`}>
      <DataTable columns={columns} rows={rows} rowKey={(r)=>r.server_component_id} emptyMessage={query.isLoading?"Loading…":"No MCP server evidence observed."}/>
    </Panel>
  </section>;
}
