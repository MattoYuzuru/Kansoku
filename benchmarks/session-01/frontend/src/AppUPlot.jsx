import React, { useEffect, useRef, useState } from "react";
import uPlot from "uplot";
import "uplot/dist/uPlot.min.css";
import AccessibleTable from "./AccessibleTable.jsx";
import { funnel, timeline } from "./data.js";

export default function AppUPlot() {
  const timelineRef = useRef(null);
  const plotRef = useRef(null);
  const [selected, setSelected] = useState("invoked");

  useEffect(() => {
    const plot = new uPlot(
      {
        width: Math.max(640, timelineRef.current.clientWidth), height: 320,
        scales: { x: { time: false } },
        series: [{ label: "minute" }, { label: "events", stroke: "#5dd6c0" }, { label: "p95 bytes", stroke: "#f5a65b", scale: "bytes" }],
        axes: [{ label: "minute" }, { label: "events" }, { scale: "bytes", side: 1, label: "bytes" }],
      },
      [timeline.map((point) => point.minute), timeline.map((point) => point.events), timeline.map((point) => point.p95Bytes)],
      timelineRef.current,
    );
    plotRef.current = plot;
    return () => plot.destroy();
  }, []);

  const exportPng = () => plotRef.current?.ctx?.canvas?.toDataURL("image/png");
  return (
    <main>
      <h1>Dense analytics spike: uPlot</h1>
      <p role="status">Selected lifecycle stage: {selected}. Coverage: 310 / 500 complete.</p>
      <button type="button" onClick={exportPng} aria-label="Export timeline canvas as PNG data URL">Export PNG</button>
      <section aria-labelledby="timeline-heading"><h2 id="timeline-heading">Activity timeline</h2><div className="chart uplot-host" ref={timelineRef} role="img" aria-label="Events and prompt byte p95 over 180 minutes" /></section>
      <section aria-labelledby="funnel-heading">
        <h2 id="funnel-heading">Component lifecycle (custom DOM)</h2>
        <div className="dom-funnel" aria-label="Installed through succeeded component funnel">
          {funnel.map(([stage, count], index) => <button key={stage} type="button" style={{ width: `${100 - index * 9}%` }} onClick={() => setSelected(stage)} aria-pressed={selected === stage}>{stage}: {count}</button>)}
        </div>
      </section>
      <AccessibleTable rows={funnel} selected={selected} />
    </main>
  );
}
