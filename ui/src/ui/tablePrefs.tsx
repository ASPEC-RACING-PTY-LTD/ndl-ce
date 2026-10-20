import { useCallback, useRef, useState, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { Icon } from "../components/Icon";
import { useFloatingPanel } from "./floating";
import { usePref } from "../prefs";

// Sortable tables with a column picker. Which columns show, the sort and any
// table options are saved per table to the user's profile (and cached in the
// browser, see prefs.ts).

export type TableColumn<T> = {
  id: string;
  label: string;
  /** sortValue makes the column sortable. */
  sortValue?: (row: T) => string | number;
  /** hidden columns start off; required columns cannot be hidden. */
  defaultHidden?: boolean;
  required?: boolean;
};

export type SortState = { id: string; dir: "asc" | "desc" } | null;

type Saved = { hidden?: string[]; shown?: string[]; sort?: SortState; options?: Record<string, boolean> };

export function useTablePrefs<T>(key: string, columns: TableColumn<T>[], defaultSort: SortState = null) {
  const [saved, setSaved] = usePref<Saved>(`table.${key}`, {});
  const hidden = new Set(saved.hidden ?? []);
  const shown = new Set(saved.shown ?? []);
  const isVisible = (c: TableColumn<T>) => c.required || (c.defaultHidden ? shown.has(c.id) : !hidden.has(c.id));
  const visible = columns.filter(isVisible);
  const sort = saved.sort === undefined ? defaultSort : saved.sort;

  function setVisible(id: string, on: boolean) {
    setSaved((cur) => {
      const h = new Set(cur?.hidden ?? []);
      const s = new Set(cur?.shown ?? []);
      const col = columns.find((c) => c.id === id);
      if (col?.defaultHidden) {
        if (on) s.add(id);
        else s.delete(id);
      } else if (on) {
        h.delete(id);
      } else {
        h.add(id);
      }
      return { ...cur, hidden: [...h], shown: [...s] };
    });
  }

  /** toggleSort cycles a column through ascending, descending and back to the default order. */
  function toggleSort(id: string) {
    setSaved((cur) => {
      const now = cur?.sort === undefined ? defaultSort : cur.sort;
      let next: SortState;
      if (!now || now.id !== id) {
        next = { id, dir: "asc" };
      } else if (now.dir === "asc") {
        next = { id, dir: "desc" };
      } else {
        next = null;
      }
      return { ...cur, sort: next };
    });
  }

  function setSort(next: SortState) {
    setSaved((cur) => ({ ...cur, sort: next }));
  }

  function option(name: string, fallback: boolean): boolean {
    const v = saved.options?.[name];
    return v === undefined ? fallback : v;
  }

  function setOption(name: string, value: boolean) {
    setSaved((cur) => ({ ...cur, options: { ...(cur?.options ?? {}), [name]: value } }));
  }

  function reset() {
    setSaved({});
  }

  function sortRows(rows: T[]): T[] {
    if (!sort) {
      return rows;
    }
    const col = columns.find((c) => c.id === sort.id);
    if (!col?.sortValue) {
      return rows;
    }
    const get = col.sortValue;
    const mul = sort.dir === "asc" ? 1 : -1;
    return rows
      .map((row, i) => ({ row, i, v: get(row) }))
      .sort((a, b) => {
        const c =
          typeof a.v === "number" && typeof b.v === "number"
            ? a.v - b.v
            : String(a.v).localeCompare(String(b.v), undefined, { numeric: true, sensitivity: "base" });
        return c !== 0 ? c * mul : a.i - b.i;
      })
      .map((x) => x.row);
  }

  return { visible, isVisible, setVisible, sort, toggleSort, setSort, sortRows, option, setOption, reset, columns };
}

export type TablePrefs<T> = ReturnType<typeof useTablePrefs<T>>;

/** SortHeader is a column heading that sorts the table when clicked. */
export function SortHeader<T>({ column, prefs, className }: { column: TableColumn<T>; prefs: TablePrefs<T>; className?: string }) {
  if (!column.sortValue) {
    return <th className={className}>{column.label}</th>;
  }
  const active = prefs.sort?.id === column.id ? prefs.sort.dir : null;
  return (
    <th className={className} aria-sort={active === "asc" ? "ascending" : active === "desc" ? "descending" : "none"}>
      <button
        type="button"
        className={"th-sort" + (active ? " is-active" : "")}
        title={active === "asc" ? "Sorted A to Z; click for Z to A" : active === "desc" ? "Sorted Z to A; click for the default order" : `Sort by ${column.label}`}
        onClick={() => prefs.toggleSort(column.id)}
      >
        {column.label}
        <Icon name={active === "asc" ? "sort-up" : active === "desc" ? "sort-down" : "sort"} size={13} />
      </button>
    </th>
  );
}

/** ColumnEditor is the pencil at the end of a table header for choosing columns. */
export function ColumnEditor<T>({ prefs, extra, label = "Edit columns" }: { prefs: TablePrefs<T>; extra?: ReactNode; label?: string }) {
  const [open, setOpen] = useState(false);
  const root = useRef<HTMLDivElement>(null);
  const panel = useRef<HTMLDivElement>(null);
  // The panel floats on document.body so the table's overflow never clips it.
  useFloatingPanel(open, root, panel, useCallback(() => setOpen(false), []));
  return (
    <div className="col-editor" ref={root}>
      <button
        type="button"
        className="btn btn-ghost btn-sm btn-icon"
        aria-label={label}
        title={label}
        aria-expanded={open}
        onClick={() => setOpen((v) => !v)}
      >
        <Icon name="edit" size={14} />
      </button>
      {open
        ? createPortal(
            <div ref={panel} className="menu-panel is-floating col-editor-panel" role="dialog" aria-label={label}>
              <p className="ctx-title">Columns</p>
              {prefs.columns
                .filter((c) => c.label)
                .map((c) => (
                  <label key={c.id} className="check-row">
                    <input type="checkbox" checked={prefs.isVisible(c)} disabled={c.required} onChange={(e) => prefs.setVisible(c.id, e.target.checked)} />
                    <span>{c.label}</span>
                  </label>
                ))}
              {extra ? (
                <>
                  <hr />
                  {extra}
                </>
              ) : null}
              <hr />
              <button type="button" className="btn btn-ghost btn-sm" onClick={() => prefs.reset()}>
                Reset to default
              </button>
            </div>,
            document.body,
          )
        : null}
    </div>
  );
}
