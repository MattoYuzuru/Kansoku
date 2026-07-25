/*
 * DataTable — dense mono-typed table. Any table wider than its container gets
 * a horizontal scroll (§5 scrollbar) with the identity (first) column
 * position:sticky left:0 (§10 responsive rule). Header uses the Table-header
 * type role; cells use the Table-cell role. Generic over a row type.
 */
import type { ReactNode } from "react";
import { ScrollArea } from "./ScrollArea";
import "./DataTable.css";

export interface Column<Row> {
  key: string;
  header: ReactNode;
  render: (row: Row) => ReactNode;
  /** Right-align numeric columns. */
  align?: "left" | "right";
}

export interface DataTableProps<Row> {
  columns: readonly Column<Row>[];
  rows: readonly Row[];
  rowKey: (row: Row) => string;
  /** Make the first column sticky (identity column). Default true. */
  stickyFirst?: boolean;
  emptyMessage?: ReactNode;
  className?: string;
}

export function DataTable<Row>({
  columns,
  rows,
  rowKey,
  stickyFirst = true,
  emptyMessage = "No rows",
  className,
}: DataTableProps<Row>) {
  return (
    <ScrollArea axis="x" className={`k-table-wrap${className ? " " + className : ""}`}>
      <table className={`k-table${stickyFirst ? " k-table--sticky" : ""}`}>
        <thead>
          <tr>
            {columns.map((c) => (
              <th
                key={c.key}
                className={`t-table-header${c.align === "right" ? " k-align-right" : ""}`}
                scope="col"
              >
                {c.header}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.length === 0 ? (
            <tr>
              <td className="k-table__empty t-body" colSpan={columns.length}>
                {emptyMessage}
              </td>
            </tr>
          ) : (
            rows.map((row) => (
              <tr key={rowKey(row)}>
                {columns.map((c, i) => {
                  const Cell = i === 0 && stickyFirst ? "th" : "td";
                  return (
                    <Cell
                      key={c.key}
                      className={`t-table-cell${c.align === "right" ? " k-align-right" : ""}`}
                      {...(Cell === "th" ? { scope: "row" as const } : {})}
                    >
                      {c.render(row)}
                    </Cell>
                  );
                })}
              </tr>
            ))
          )}
        </tbody>
      </table>
    </ScrollArea>
  );
}
