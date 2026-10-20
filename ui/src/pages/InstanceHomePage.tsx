import { useMemo, useState } from "react";
import { listWorkloads } from "../api/client";
import type { Workload } from "../api/phase5";
import { EmptyState, LoadingState } from "../components/EmptyState";
import { ErrorNotice } from "../components/ErrorNotice";
import { Icon } from "../components/Icon";
import { Link } from "../components/Link";
import { PageHeader } from "../components/PageHeader";
import { StatusBadge } from "../components/StatusBadge";
import { formatBytes } from "../format";
import { kindLabel } from "../labels";
import { usePref } from "../prefs";
import { useQuery } from "../query";

export type WidgetId = "types" | "running" | "resources" | "attention";

export const WIDGETS: { id: WidgetId; label: string }[] = [
  { id: "types", label: "By type" },
  { id: "running", label: "Running" },
  { id: "resources", label: "Resources" },
  { id: "attention", label: "Needs attention" },
];

const WIDGETS_KEY = "instance.widgets";

/** parseWidgets keeps known widget ids, in the saved order, without repeats. */
export function parseWidgets(raw: unknown): WidgetId[] {
  if (!Array.isArray(raw)) {
    return WIDGETS.map((w) => w.id);
  }
  const known = new Set<string>(WIDGETS.map((w) => w.id));
  return [...new Set(raw.filter((x): x is WidgetId => typeof x === "string" && known.has(x)))];
}

function isRunning(w: Workload): boolean {
  return w.status === "running";
}

function needsAttention(w: Workload): boolean {
  return w.status === "warning" || w.status === "failed" || w.status === "error";
}

/** filterInstances applies the table's search, type and status filters. */
export function filterInstances(items: Workload[], q: string, kind: string, status: string): Workload[] {
  const needle = q.trim().toLowerCase();
  return items.filter((w) => {
    if (kind !== "all" && w.kind !== kind) {
      return false;
    }
    if (status === "running" && !isRunning(w)) {
      return false;
    }
    if (status === "stopped" && isRunning(w)) {
      return false;
    }
    if (status === "attention" && !needsAttention(w)) {
      return false;
    }
    if (!needle) {
      return true;
    }
    return [w.name, w.kind, kindLabel(w.kind), w.nics?.[0]?.ipv4 ?? ""].some((v) => (v ?? "").toLowerCase().includes(needle));
  });
}

/**
 * InstanceHomePage is the Instance overview: widgets the person picks and
 * orders, and one table of every instance with filters.
 */
export function InstanceHomePage() {
  const q = useQuery("workloads", () => listWorkloads(), 10000);
  const items = useMemo(() => q.data?.items ?? [], [q.data]);
  const [rawWidgets, setWidgets] = usePref<unknown>(WIDGETS_KEY, null);
  const widgets = parseWidgets(rawWidgets);
  const [editing, setEditing] = useState(false);
  const [search, setSearch] = useState("");
  const [kind, setKind] = useState("all");
  const [status, setStatus] = useState("all");

  const kinds = useMemo(() => [...new Set(items.map((w) => w.kind))].sort(), [items]);
  const rows = filterInstances(items, search, kind, status);
  const running = items.filter(isRunning).length;
  const attention = items.filter(needsAttention);
  const cpus = items.reduce((n, w) => n + (w.cpus ?? 0), 0);
  const mem = items.reduce((n, w) => n + (w.memory_bytes ?? 0), 0);

  function move(id: WidgetId, delta: number) {
    const i = widgets.indexOf(id);
    const j = i + delta;
    if (i < 0 || j < 0 || j >= widgets.length) {
      return;
    }
    const next = [...widgets];
    [next[i], next[j]] = [next[j], next[i]];
    setWidgets(next);
  }

  function widget(id: WidgetId) {
    switch (id) {
      case "types":
        return (
          <ul className="widget-list">
            {kinds.length === 0 ? <li className="muted">None yet</li> : null}
            {kinds.map((k) => (
              <li key={k}>
                <button type="button" className="link-btn" onClick={() => setKind(k)}>
                  {kindLabel(k)}
                </button>
                <strong>{items.filter((w) => w.kind === k).length}</strong>
              </li>
            ))}
          </ul>
        );
      case "running":
        return (
          <p className="widget-big">
            <strong>{running}</strong>
            <span className="muted"> of {items.length} running</span>
          </p>
        );
      case "resources":
        return (
          <dl className="kv-compact">
            <dt>vCPUs</dt>
            <dd>{cpus}</dd>
            <dt>Memory</dt>
            <dd>{formatBytes(mem)}</dd>
          </dl>
        );
      case "attention":
        return attention.length === 0 ? (
          <p className="muted">All clear.</p>
        ) : (
          <ul className="widget-list">
            {attention.slice(0, 6).map((w) => (
              <li key={w.id}>
                <Link href={`/workloads/${w.id}`}>{w.name}</Link>
                <StatusBadge status={w.status} />
              </li>
            ))}
          </ul>
        );
    }
  }

  return (
    <section className="page page-wide" aria-labelledby="instance-heading">
      <PageHeader
        id="instance-heading"
        icon="workloads"
        title="Instance"
        kicker={items.length ? `${items.length} instances · ${running} running` : "Every container, VM and application on this appliance."}
        actions={
          <button className="btn btn-ghost" type="button" aria-pressed={editing} onClick={() => setEditing((v) => !v)}>
            {editing ? "Done" : "Customise"}
          </button>
        }
      />
      {q.error && !q.data ? <ErrorNotice error={q.error} /> : null}
      {editing ? (
        <div className="panel widget-editor" aria-label="Widgets">
          {WIDGETS.map((w) => {
            const on = widgets.includes(w.id);
            return (
              <div key={w.id} className="widget-editor-row">
                <label className="check-row">
                  <input
                    type="checkbox"
                    checked={on}
                    onChange={(e) => setWidgets(e.target.checked ? [...widgets, w.id] : widgets.filter((x) => x !== w.id))}
                  />
                  <span>{w.label}</span>
                </label>
                {on ? (
                  <span className="btn-row is-flush">
                    <button className="btn btn-ghost btn-sm" type="button" aria-label={`Move ${w.label} earlier`} onClick={() => move(w.id, -1)}>
                      ↑
                    </button>
                    <button className="btn btn-ghost btn-sm" type="button" aria-label={`Move ${w.label} later`} onClick={() => move(w.id, 1)}>
                      ↓
                    </button>
                  </span>
                ) : null}
              </div>
            );
          })}
          <button className="btn btn-ghost btn-sm" type="button" onClick={() => setWidgets(null)}>
            Reset
          </button>
        </div>
      ) : null}
      {widgets.length ? (
        <div className="fluid-grid instance-widgets">
          {widgets.map((id) => (
            <article key={id} className="panel instance-widget">
              <h2>{WIDGETS.find((w) => w.id === id)?.label}</h2>
              {widget(id)}
            </article>
          ))}
        </div>
      ) : null}
      <div className="toolbar">
        <div className="toolbar-group">
          <label className="search-field">
            <Icon name="search" size={14} />
            <input className="field-input" type="search" placeholder="Filter by name, type or address" value={search} onChange={(e) => setSearch(e.target.value)} aria-label="Filter instances" />
          </label>
          <select className="field-input" aria-label="Type" value={kind} onChange={(e) => setKind(e.target.value)}>
            <option value="all">All types</option>
            {kinds.map((k) => (
              <option key={k} value={k}>
                {kindLabel(k)}
              </option>
            ))}
          </select>
          <select className="field-input" aria-label="Status" value={status} onChange={(e) => setStatus(e.target.value)}>
            <option value="all">Any status</option>
            <option value="running">Running</option>
            <option value="stopped">Not running</option>
            <option value="attention">Needs attention</option>
          </select>
        </div>
      </div>
      {q.loading && !q.data ? (
        <LoadingState label="Loading instances" />
      ) : rows.length === 0 ? (
        <EmptyState icon="workloads" title={items.length ? "Nothing matches" : "No instances yet"}>
          {items.length ? "Change the filters to see more." : "Use Create above to make the first one."}
        </EmptyState>
      ) : (
        <div className="table-wrap">
          <table className="wl-table" aria-label="Instances">
            <thead>
              <tr>
                <th>Name</th>
                <th>Type</th>
                <th>Status</th>
                <th>Address</th>
                <th className="num">vCPU</th>
                <th className="num">Memory</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((w) => (
                <tr key={w.id}>
                  <td>
                    <Link href={`/workloads/${w.id}`}>{w.name}</Link>
                  </td>
                  <td>{kindLabel(w.kind)}</td>
                  <td>
                    <StatusBadge status={w.status} />
                  </td>
                  <td className="cell-mono">{w.nics?.[0]?.ipv4 || ""}</td>
                  <td className="num">{w.cpus ?? ""}</td>
                  <td className="num">{formatBytes(w.memory_bytes)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}
