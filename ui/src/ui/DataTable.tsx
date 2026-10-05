import { useMemo, useState, type ReactNode } from "react";
import { Icon } from "../components/Icon";

export type Column<T> = {
  id: string;
  header: ReactNode;
  cell: (row: T) => ReactNode;
  /** Sort key; columns without one are not sortable. */
  sortValue?: (row: T) => string | number;
  align?: "left" | "right";
  width?: string;
  className?: string;
};

export type SortState = { id: string; dir: "asc" | "desc" };

/**
 * Sortable data table with optional clickable rows and client-side paging.
 * Rows are keyboard operable when onRowClick is set.
 */
export function DataTable<T>({
  columns,
  rows,
  rowKey,
  initialSort,
  onRowClick,
  selectedKey,
  pageSize,
  empty,
  label,
}: {
  columns: Column<T>[];
  rows: T[];
  rowKey: (row: T) => string;
  initialSort?: SortState;
  onRowClick?: (row: T) => void;
  selectedKey?: string | null;
  pageSize?: number;
  empty?: ReactNode;
  label?: string;
}) {
  const [sort, setSort] = useState<SortState | null>(initialSort ?? null);
  const [page, setPage] = useState(0);

  const sorted = useMemo(() => {
    const col = sort ? columns.find((c) => c.id === sort.id) : undefined;
    if (!col?.sortValue || !sort) {
      return rows;
    }
    const get = col.sortValue;
    const dir = sort.dir === "asc" ? 1 : -1;
    return [...rows].sort((a, b) => {
      const va = get(a);
      const vb = get(b);
      if (typeof va === "number" && typeof vb === "number") {
        return (va - vb) * dir;
      }
      return String(va).localeCompare(String(vb), undefined, { numeric: true, sensitivity: "base" }) * dir;
    });
  }, [rows, columns, sort]);

  const pages = pageSize ? Math.max(1, Math.ceil(sorted.length / pageSize)) : 1;
  const current = Math.min(page, pages - 1);
  const visible = pageSize ? sorted.slice(current * pageSize, current * pageSize + pageSize) : sorted;

  function toggle(id: string) {
    setPage(0);
    setSort((cur) => (cur?.id === id ? { id, dir: cur.dir === "asc" ? "desc" : "asc" } : { id, dir: "asc" }));
  }

  if (rows.length === 0) {
    return <>{empty ?? null}</>;
  }

  return (
    <div className="stack">
      <div className="table-wrap">
        <table aria-label={label}>
          <thead>
            <tr>
              {columns.map((col) => {
                const active = sort?.id === col.id;
                return (
                  <th
                    key={col.id}
                    className={[col.align === "right" ? "num" : "", col.className ?? ""].filter(Boolean).join(" ") || undefined}
                    style={col.width ? { width: col.width } : undefined}
                    aria-sort={active ? (sort?.dir === "asc" ? "ascending" : "descending") : undefined}
                  >
                    {col.sortValue ? (
                      <button type="button" className={"th-sort" + (active ? " is-active" : "")} onClick={() => toggle(col.id)}>
                        {col.header}
                        <Icon name={active ? (sort?.dir === "asc" ? "sort-up" : "sort-down") : "sort"} size={12} />
                      </button>
                    ) : (
                      col.header
                    )}
                  </th>
                );
              })}
            </tr>
          </thead>
          <tbody>
            {visible.map((row) => {
              const key = rowKey(row);
              return (
                <tr
                  key={key}
                  className={[onRowClick ? "is-clickable" : "", selectedKey === key ? "is-selected" : ""].filter(Boolean).join(" ") || undefined}
                  tabIndex={onRowClick ? 0 : undefined}
                  onClick={onRowClick ? () => onRowClick(row) : undefined}
                  onKeyDown={
                    onRowClick
                      ? (event) => {
                          if (event.key === "Enter" || event.key === " ") {
                            event.preventDefault();
                            onRowClick(row);
                          }
                        }
                      : undefined
                  }
                >
                  {columns.map((col) => (
                    <td key={col.id} className={[col.align === "right" ? "num" : "", col.className ?? ""].filter(Boolean).join(" ") || undefined}>
                      {col.cell(row)}
                    </td>
                  ))}
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
      {pageSize && sorted.length > pageSize ? (
        <div className="table-foot">
          <span>
            {current * pageSize + 1} to {Math.min(sorted.length, (current + 1) * pageSize)} of {sorted.length}
          </span>
          <div className="btn-row">
            <button className="btn btn-sm btn-secondary" type="button" disabled={current === 0} onClick={() => setPage(current - 1)}>
              Previous
            </button>
            <button
              className="btn btn-sm btn-secondary"
              type="button"
              disabled={current >= pages - 1}
              onClick={() => setPage(current + 1)}
            >
              Next
            </button>
          </div>
        </div>
      ) : null}
    </div>
  );
}
