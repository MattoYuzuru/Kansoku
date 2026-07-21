import React, { useEffect, useRef, useState } from "react";
import * as echarts from "echarts/core";
import { FunnelChart, LineChart } from "echarts/charts";
import { DatasetComponent, GridComponent, LegendComponent, TooltipComponent } from "echarts/components";
import { SVGRenderer } from "echarts/renderers";
import AccessibleTable from "./AccessibleTable.jsx";
import { funnel, timeline } from "./data.js";

echarts.use([DatasetComponent, FunnelChart, GridComponent, LegendComponent, LineChart, SVGRenderer, TooltipComponent]);

export default function AppECharts() {
  const timelineRef = useRef(null);
  const funnelRef = useRef(null);
  const exportRef = useRef(null);
  const [selected, setSelected] = useState("invoked");

  useEffect(() => {
    const line = echarts.init(timelineRef.current, undefined, { renderer: "svg" });
    const stages = echarts.init(funnelRef.current, undefined, { renderer: "svg" });
    line.group = "kansoku-spike";
    stages.group = "kansoku-spike";
    echarts.connect("kansoku-spike");
    line.setOption({
      animation: false,
      tooltip: { trigger: "axis" },
      legend: { data: ["events", "p95 bytes"] },
      grid: { left: 52, right: 28, top: 42, bottom: 42 },
      xAxis: { type: "category", data: timeline.map((point) => point.minute), name: "minute" },
      yAxis: [{ type: "value", name: "events" }, { type: "value", name: "bytes" }],
      series: [
        { name: "events", type: "line", showSymbol: false, data: timeline.map((point) => point.events) },
        { name: "p95 bytes", type: "line", showSymbol: false, yAxisIndex: 1, data: timeline.map((point) => point.p95Bytes) },
      ],
    });
    stages.setOption({
      animation: false,
      tooltip: { trigger: "item" },
      series: [{ type: "funnel", data: funnel.map(([name, value]) => ({ name, value })), label: { formatter: "{b}: {c}" } }],
    });
    stages.on("click", (event) => setSelected(event.name));
    exportRef.current = () => line.getDataURL({ type: "svg" });
    const resize = () => { line.resize(); stages.resize(); };
    window.addEventListener("resize", resize);
    return () => { window.removeEventListener("resize", resize); echarts.disconnect("kansoku-spike"); line.dispose(); stages.dispose(); };
  }, []);

  return (
    <main>
      <h1>Dense analytics spike: ECharts</h1>
      <p role="status">Selected lifecycle stage: {selected}. Coverage: 310 / 500 complete.</p>
      <button type="button" onClick={() => exportRef.current?.()} aria-label="Export timeline as SVG data URL">Export SVG</button>
      <section aria-labelledby="timeline-heading"><h2 id="timeline-heading">Activity timeline</h2><div className="chart" ref={timelineRef} role="img" aria-label="Events and prompt byte p95 over 180 minutes" /></section>
      <section aria-labelledby="funnel-heading"><h2 id="funnel-heading">Component lifecycle</h2><div className="chart" ref={funnelRef} role="img" aria-label="Installed through succeeded component funnel" /></section>
      <AccessibleTable rows={funnel} selected={selected} />
    </main>
  );
}
