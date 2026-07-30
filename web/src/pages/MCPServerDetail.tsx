import { useMemo } from "react";
import { Link } from "wouter";
import { useMCPServerProfile } from "../api/queries";
import type { MCPPrimitiveRow } from "../api/types";
import { DataTable, type Column } from "../components/DataTable";
import { KpiCard } from "../components/KpiCard";
import { GapNote, Panel } from "../components/Panel";
import { RangeControl } from "../components/RangeControl";
import { useRange } from "../hooks/useRange";

export function MCPServerDetail({id}:{id:string}) {
  const range=useRange("mcp");
  const params=useMemo(()=>({from:range.from,to:range.to,granularity:range.granularity,timezone:range.timezone}),[range.from,range.to,range.granularity,range.timezone]);
  const query=useMCPServerProfile(id,params); const p=query.data?.data; const i=p?.identity; const o=p?.outcomes;
  const columns:Column<MCPPrimitiveRow>[]=[
    {key:"tool",header:"Advertised primitive",render:(r)=>r.kind==="tool"
      ? <Link href={`/components/mcp/${encodeURIComponent(id)}/tools/${encodeURIComponent(r.tool_component_id)}`}>{r.declared_name}</Link>
      : r.declared_name},
    {key:"kind",header:"Kind",render:(r)=>r.kind},
    {key:"bytes",header:"Structural bytes",align:"right",render:(r)=>(r.description_byte_count??0)+(r.schema_byte_count??0)},
    {key:"coverage",header:"Enumeration",render:(r)=>r.enumeration_completeness},
  ];
  return <section className="k-page">
    <header className="k-page__head"><h1 className="t-page-title">{i?.declared_name??"MCP server profile"}</h1>
      <p className="k-page__wire t-caption">{i?`${i.transport} · ${i.locality} · ${i.server_component_id}`:id}</p></header>
    <Panel title="Protocol and call lifecycle" actions={<RangeControl range={range}/>}>
      <div className="k-grid k-grid--kpis">
        <KpiCard label="Starts" value={o?.started??null}/>
        <KpiCard label="Completed (protocol)" value={o?.completed??null}/>
        <KpiCard label="Execution error" value={o?.execution_error??null}/>
        <KpiCard label="Protocol error" value={o?.protocol_error??null}/>
        <KpiCard label="Timeout" value={o?.timed_out??null}/>
        <KpiCard label="Cancelled" value={o?.cancelled??null}/>
        <KpiCard label="Denied" value={o?.denied??null}/>
        <KpiCard label="Call p95" value={p?.call_p95_ms??null} unit="ms"/>
      </div>
      <GapNote>Completed means MCP result with isError=false only. Error text, arguments, results, URLs, commands, environment and resource URIs are never available here.</GapNote>
    </Panel>
    <Panel title="Advertised tools and primitives"><DataTable columns={columns} rows={p?.primitives??[]} rowKey={(r)=>r.tool_component_id}/></Panel>
  </section>;
}
