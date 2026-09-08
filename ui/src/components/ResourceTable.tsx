import type { ReactNode } from "react";

export function ResourceTable({
  headers,
  rows,
  empty,
  numeric = [],
  selected,
  onRowClick,
}: {
  headers: ReactNode[];
  rows: ReactNode[][];
  empty?: ReactNode;
  numeric?: number[];
  selected?: number;
  onRowClick?: (index: number) => void;
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
            <tr
              key={i}
              className={[selected === i ? "is-selected" : "", onRowClick ? "is-clickable" : ""].filter(Boolean).join(" ") || undefined}
              onClick={onRowClick ? () => onRowClick(i) : undefined}
              onKeyDown={
                onRowClick
                  ? (event) => {
                      if (event.key === "Enter" || event.key === " ") {
                        event.preventDefault();
                        onRowClick(i);
                      }
                    }
                  : undefined
              }
              tabIndex={onRowClick ? 0 : undefined}
              role={onRowClick ? "button" : undefined}
            >
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
