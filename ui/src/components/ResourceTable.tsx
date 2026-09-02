import type { ReactNode } from "react";

export function ResourceTable({
  headers,
  rows,
  empty,
}: {
  headers: ReactNode[];
  rows: ReactNode[][];
  empty?: ReactNode;
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
              <th key={i}>{header}</th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((row, i) => (
            <tr key={i}>
              {row.map((cell, j) => (
                <td key={j}>{cell}</td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
