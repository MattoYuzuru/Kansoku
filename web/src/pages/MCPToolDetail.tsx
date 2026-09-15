import { useMemo } from "react";
import { Link } from "wouter";
import { useMCPToolProfile } from "../api/queries";
import { KpiCard } from "../components/KpiCard";
import { GapNote, Panel } from "../components/Panel";
import { RangeControl } from "../components/RangeControl";
import { useRange } from "../hooks/useRange";
import { formatMetric } from "../lib/format";

export function MCPToolDetail({ serverID, toolID }: { serverID: string; toolID: string }) {
  const range = useRange("mcp");
  const params = useMemo(
    () => ({ from: range.from, to: range.to, granularity: range.granularity, timezone: range.timezone }),
    [range.from, range.to, range.granularity, range.timezone],
  );
  const query = useMCPToolProfile(serverID, toolID, params);
  const profile = query.data?.data;
  const identity = profile?.identity;
  const outcomes = profile?.outcomes;
  return (
    <section className="k-page">
      <header className="k-page__head">
        <h1 className="t-page-title">{identity?.declared_name ?? "MCP tool profile"}</h1>
        <p className="k-page__wire t-caption">
          {profile ? `${profile.parent.declared_name} · ${identity?.tool_component_id}` : toolID}
        </p>
      </header>
      <Panel
        title="Independent tool evidence"
        actions={<><Link href={`/components/mcp/${encodeURIComponent(serverID)}`}>Back to server</Link><RangeControl range={range} /></>}
      >
        <div className="k-grid k-grid--kpis">
          <KpiCard label="Starts" value={outcomes?.started ?? null} />
          <KpiCard label="Completed (protocol)" value={outcomes?.completed ?? null} />
          <KpiCard label="Execution error" value={outcomes?.execution_error ?? null} />
          <KpiCard label="Protocol error" value={outcomes?.protocol_error ?? null} />
          <KpiCard label="Timeout" value={outcomes?.timed_out ?? null} />
          <KpiCard label="Cancelled" value={outcomes?.cancelled ?? null} />
          <KpiCard label="Denied" value={outcomes?.denied ?? null} />
          <KpiCard label="Call p95" value={profile?.call_p95_ms ?? null} unit="ms" precision={2} formatValue={formatMetric} />
        </div>
        <GapNote>
          Population {profile?.population.numerator ?? 0}/{profile?.population.denominator ?? 0};
          inventory {profile?.inventory.completeness ?? "unknown"}; calls {profile?.calls.completeness ?? "unknown"}.
          No observed calls never implies that a tool is unused unless exposure is complete.
        </GapNote>
      </Panel>
      <Panel title="Privacy-safe inventory metadata">
        <dl className="k-kv">
          <div className="k-kv__row"><dt>Kind</dt><dd>{identity?.kind ?? "not observed"}</dd></div>
          <div className="k-kv__row"><dt>Enumeration</dt><dd>{identity?.enumeration_completeness ?? "unknown"}</dd></div>
          <div className="k-kv__row"><dt>Schema fingerprint</dt><dd>{identity?.schema_fingerprint ?? "not observed"}</dd></div>
          <div className="k-kv__row"><dt>Structural bytes</dt><dd>{identity ? (identity.description_byte_count ?? 0) + (identity.schema_byte_count ?? 0) : "not observed"}</dd></div>
        </dl>
      </Panel>
    </section>
  );
}
