import React from "react";

export default function AccessibleTable({ rows, selected }) {
  return (
    <table>
      <caption>Component lifecycle values; chart-equivalent data table</caption>
      <thead><tr><th scope="col">Stage</th><th scope="col">Count</th><th scope="col">Completeness</th></tr></thead>
      <tbody>
        {rows.map(([stage, count]) => (
          <tr key={stage} aria-current={selected === stage ? "true" : undefined}>
            <th scope="row">{stage}</th><td>{count}</td><td>complete</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}
