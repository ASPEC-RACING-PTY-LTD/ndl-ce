import type { ReactNode } from "react";

export function ResourceTable({
  headers,
  rows,
  empty,
  numeric = [],
  selected,
}: {
  headers: ReactNode[];
  rows: ReactNode[][];
  empty?: ReactNode;
  numeric?: number[];
  selected?: number;
}) {
  if (rows.length === 0) {
    return empty ? <>{empty}</> : <p>None yet.</p>;
  }
  return (
    <div className="table-wrap">
      <table>
        <thead>
          <tr>
            {headers.map((header, i) => (
              <th key={i} className={numeric.includes(i) ? "num" : undefined}>
                {header}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((row, i) => (
            <tr key={i} className={selected === i ? "is-selected" : undefined}>
              {row.map((cell, j) => (
                <td key={j} className={numeric.includes(j) ? "num" : undefined}>
                  {cell}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
