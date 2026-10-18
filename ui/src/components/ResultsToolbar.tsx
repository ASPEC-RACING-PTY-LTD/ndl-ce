import { useState } from "react";
import { storageGet, storageSet } from "../storage";

export type ResultsView = "grid" | "table";

export type ResultsOption = { value: string; label: string };

/**
 * useResultsView keeps a page's Grid/Table choice per browser, like the
 * Docker page's Groups/Table switch.
 */
export function useResultsView(key: string, fallback: ResultsView = "grid"): [ResultsView, (v: ResultsView) => void] {
  const [view, setView] = useState<ResultsView>(() => {
    const saved = storageGet(key);
    return saved === "grid" || saved === "table" ? saved : fallback;
  });
  return [
    view,
    (next) => {
      setView(next);
      storageSet(key, next);
    },
  ];
}

/**
 * ResultsToolbar is the Docker page's search, filter and view bar, shared by
 * lists that group results the same way (Uptime, Deployments).
 */
export function ResultsToolbar({
  label,
  query,
  onQuery,
  placeholder,
  filter,
  filters,
  onFilter,
  filterLabel = "Filter",
  sort,
  sorts,
  onSort,
  view,
  onView,
}: {
  label: string;
  query: string;
  onQuery: (q: string) => void;
  placeholder?: string;
  filter?: string;
  filters?: ResultsOption[];
  onFilter?: (v: string) => void;
  filterLabel?: string;
  sort: string;
  sorts: ResultsOption[];
  onSort: (v: string) => void;
  view: ResultsView;
  onView: (v: ResultsView) => void;
}) {
  return (
    <div className="docker-toolbar results-toolbar" role="group" aria-label={label}>
      <input
        className="field-input"
        type="search"
        aria-label={`Search ${label.toLowerCase()}`}
        value={query}
        onChange={(e) => onQuery(e.target.value)}
        placeholder={placeholder}
      />
      {filters && onFilter ? (
        <select className="field-input" aria-label={filterLabel} value={filter} onChange={(e) => onFilter(e.target.value)}>
          {filters.map((o) => (
            <option key={o.value} value={o.value}>
              {o.label}
            </option>
          ))}
        </select>
      ) : null}
      <select className="field-input" aria-label="Sort by" value={sort} onChange={(e) => onSort(e.target.value)}>
        {sorts.map((o) => (
          <option key={o.value} value={o.value}>
            Sort: {o.label}
          </option>
        ))}
      </select>
      <div className="segmented" role="group" aria-label="View">
        <button type="button" aria-pressed={view === "grid"} onClick={() => onView("grid")}>
          Grid
        </button>
        <button type="button" aria-pressed={view === "table"} onClick={() => onView("table")}>
          Table
        </button>
      </div>
    </div>
  );
}
