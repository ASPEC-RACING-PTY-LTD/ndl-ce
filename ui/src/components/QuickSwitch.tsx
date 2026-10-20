import { useEffect, useMemo, useRef, useState } from "react";
import { listNodes, listStacks, listWorkloads, workloadAction } from "../api/client";
import { canMutate } from "../rbac";
import { useSession } from "../session";
import {
  buildTermCatalog,
  canOpenTermTarget,
  filterTermTargets,
  GROUP_ORDER,
  groupHeading,
} from "../terminal/catalog";
import type { TermTarget } from "../terminal/types";
import { ConfirmDialog } from "./ConfirmDialog";
import { StatusBadge } from "./StatusBadge";
import { ErrorNotice } from "./ErrorNotice";
import { storageGet, storageSet } from "../storage";

const LAYOUT_KEY = "ndl-qs-layout";

/** statusDot maps a target status to the grid's coloured dot. */
function statusDot(status: string): "ok" | "busy" | "off" | "bad" {
  const s = (status || "").toLowerCase();
  if (/(running|active|online|ready|healthy|available|up)/.test(s)) {
    return "ok";
  }
  if (/(restart|start|creat|pending|degraded|crash|migrat|provision)/.test(s)) {
    return "busy";
  }
  if (/(error|fail|critical|dead)/.test(s)) {
    return "bad";
  }
  return "off";
}

type Row = { key: string; heading?: string; target?: TermTarget };

/** gridSections splits the flat row list at its headings for the grid layout. */
function gridSections(rows: Row[]): { key: string; heading?: string; first: boolean; items: { row: Row; index: number }[] }[] {
  const out: { key: string; heading?: string; first: boolean; items: { row: Row; index: number }[] }[] = [];
  rows.forEach((row, index) => {
    if (row.heading) {
      out.push({ key: row.key, heading: row.heading, first: false, items: [] });
      return;
    }
    if (!row.target) {
      return;
    }
    if (out.length === 0) {
      out.push({ key: "results", first: false, items: [] });
    }
    out[out.length - 1].items.push({ row, index });
  });
  const firstFull = out.find((s) => s.items.length > 1) ?? out[0];
  if (firstFull) {
    firstFull.first = true;
  }
  return out.filter((s) => s.items.length > 0);
}

export function QuickSwitch({
  open,
  recents,
  hasCurrent,
  currentLive,
  onClose,
  onOpenNew,
  onReplace,
  onCatalog,
}: {
  open: boolean;
  recents: TermTarget[];
  hasCurrent: boolean;
  currentLive: boolean;
  onClose: () => void;
  onOpenNew: (target: TermTarget) => void;
  onReplace: (target: TermTarget) => void;
  /** Called with the live targets once they load, so stale recents can be pruned. */
  onCatalog?: (live: TermTarget[]) => void;
}) {
  const session = useSession();
  const roles = session.status === "ready" ? session.user?.roles : undefined;
  const mutate = canMutate(roles);
  const [query, setQuery] = useState("");
  const [active, setActive] = useState(0);
  const [catalog, setCatalog] = useState<TermTarget[]>([]);
  const [catalogLoaded, setCatalogLoaded] = useState(false);
  const [localNodeId, setLocalNodeId] = useState<string | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [startTarget, setStartTarget] = useState<TermTarget | null>(null);
  const [replaceTarget, setReplaceTarget] = useState<TermTarget | null>(null);
  const [busy, setBusy] = useState(false);
  const [layout, setLayoutState] = useState<"grid" | "table">(() => (storageGet(LAYOUT_KEY) === "table" ? "table" : "grid"));
  function setLayout(next: "grid" | "table") {
    setLayoutState(next);
    storageSet(LAYOUT_KEY, next);
  }

  // The latest callback, so a new function from the parent does not reload the list.
  const onCatalogRef = useRef(onCatalog);
  useEffect(() => {
    onCatalogRef.current = onCatalog;
  }, [onCatalog]);

  useEffect(() => {
    if (!open) {
      return;
    }
    setQuery("");
    setActive(0);
    setLoadError(null);
    let cancelled = false;
    void (async () => {
      try {
        const [nodes, workloads, stacks] = await Promise.all([
          listNodes().catch(() => []),
          listWorkloads().catch(() => null),
          listStacks().catch(() => ({ items: [] })),
        ]);
        if (cancelled) {
          return;
        }
        setLocalNodeId((nodes.find((n) => n.local) ?? (nodes.length === 1 ? nodes[0] : undefined))?.id ?? null);
        const live = buildTermCatalog({
          roles,
          nodes,
          workloads: workloads?.items ?? [],
          stacks: stacks.items ?? [],
        });
        setCatalog(live);
        // Only a successful workload list can prove a workload is gone.
        if (workloads) {
          setCatalogLoaded(true);
          onCatalogRef.current?.(live);
        }
      } catch (err) {
        if (!cancelled) {
          setLoadError(err instanceof Error ? err.message : "Could not load targets");
        }
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [open, roles]);

  const filtered = useMemo(() => filterTermTargets(catalog, query), [catalog, query]);
  const searching = query.trim().length > 0;

  const rows = useMemo<Row[]>(() => {
    if (searching) {
      return filtered.map((target) => ({ key: `${target.kind}:${target.id}`, target }));
    }
    const out: Row[] = [];
    // The local host always sits at the top, then recents, then everything else.
    const isLocal = (t: TermTarget) => t.kind === "node" && t.id === localNodeId;
    const local = catalog.find(isLocal);
    if (local) {
      out.push({ key: "h-local", heading: "This host" });
      out.push({ key: `local:${local.kind}:${local.id}`, target: local });
    }
    const recentHits = recents
      .map((r) => catalog.find((t) => t.kind === r.kind && t.id === r.id) ?? (catalogLoaded && r.kind === "workload" ? null : r))
      .filter((t): t is TermTarget => t != null && !isLocal(t));
    if (recentHits.length) {
      out.push({ key: "h-recent", heading: "Recent" });
      for (const target of recentHits) {
        out.push({ key: `recent:${target.kind}:${target.id}`, target });
      }
    }
    for (const group of GROUP_ORDER) {
      const items = catalog.filter((t) => t.group === group && !isLocal(t));
      if (items.length === 0) {
        continue;
      }
      out.push({ key: `h-${group}`, heading: groupHeading(group) });
      for (const target of items) {
        out.push({ key: `${target.kind}:${target.id}`, target });
      }
    }
    return out;
  }, [catalog, catalogLoaded, filtered, recents, searching, localNodeId]);

  const targetRows = rows.map((r, i) => ({ row: r, index: i })).filter((x) => x.row.target);
  const gridRef = useRef<HTMLDivElement>(null);
  function gridColumns(): number {
    const el = gridRef.current;
    if (!el) {
      return 1;
    }
    return Math.max(1, getComputedStyle(el).gridTemplateColumns.split(" ").filter(Boolean).length);
  }
  const activeTarget = rows[active]?.target;

  useEffect(() => {
    const first = rows.findIndex((r) => r.target);
    setActive(first < 0 ? 0 : first);
  }, [query, catalog, rows]);

  if (!open) {
    return null;
  }

  function choose(target: TermTarget) {
    if (!canOpenTermTarget(target, roles)) {
      if (target.kind === "workload" && !target.terminalReady && mutate) {
        setStartTarget(target);
      }
      return;
    }
    onOpenNew(target);
    onClose();
  }

  function confirmReplace(target: TermTarget) {
    if (!canOpenTermTarget(target, roles)) {
      return;
    }
    if (currentLive) {
      setReplaceTarget(target);
      return;
    }
    onReplace(target);
    onClose();
  }

  return (
    <div className="palette-backdrop" onClick={onClose}>
      <div
        className="palette qs-palette"
        role="dialog"
        aria-modal="true"
        aria-labelledby="qs-heading"
        onClick={(event) => event.stopPropagation()}
        onKeyDown={(event) => {
          if (event.key === "Escape") {
            event.preventDefault();
            onClose();
          }
          const step = (dir: 1 | -1, count: number) =>
            setActive((n) => {
              const at = targetRows.findIndex((x) => x.index === n);
              const to = targetRows[Math.max(0, Math.min(targetRows.length - 1, at + dir * count))];
              return to?.index ?? n;
            });
          const cols = layout === "grid" ? gridColumns() : 1;
          if (event.key === "ArrowDown") {
            event.preventDefault();
            step(1, cols);
          }
          if (event.key === "ArrowUp") {
            event.preventDefault();
            step(-1, cols);
          }
          if (layout === "grid" && event.key === "ArrowRight" && (event.target as HTMLElement).id !== "qs-search") {
            event.preventDefault();
            step(1, 1);
          }
          if (layout === "grid" && event.key === "ArrowLeft" && (event.target as HTMLElement).id !== "qs-search") {
            event.preventDefault();
            step(-1, 1);
          }
          if (event.key === "Enter" && activeTarget) {
            event.preventDefault();
            choose(activeTarget);
          }
        }}
      >
        <div className="qs-head">
          <h2 id="qs-heading" className="palette-heading">
            New terminal
          </h2>
          <div className="segmented" role="group" aria-label="Layout">
            <button type="button" aria-pressed={layout === "grid"} onClick={() => setLayout("grid")}>
              Grid
            </button>
            <button type="button" aria-pressed={layout === "table"} onClick={() => setLayout("table")}>
              Table
            </button>
          </div>
        </div>
        <p className="field-hint">
          Enter opens a new terminal session. Existing tabs stay attached. Replace the current session only with the
          explicit action.
        </p>
        <label className="field-label" htmlFor="qs-search">
          Search
        </label>
        <input
          id="qs-search"
          className="field-input"
          autoFocus
          value={query}
          placeholder="Name, type, or node"
          onChange={(event) => setQuery(event.target.value)}
        />
        {loadError ? (
          <ErrorNotice error={loadError} />
        ) : null}
        {layout === "grid" ? (
          <div className="qs-grid-wrap">
            {rows.length === 0 ? <p className="muted">No matching targets.</p> : null}
            {gridSections(rows).map((sec) => (
              <section key={sec.key} className="qs-grid-section">
                {sec.heading ? <h3 className="qs-heading">{sec.heading}</h3> : null}
                <div className="qs-grid" ref={sec.first ? gridRef : undefined}>
                  {sec.items.map(({ row, index }) => {
                    const t = row.target!;
                    const openable = canOpenTermTarget(t, roles);
                    const startable = !t.terminalReady && t.kind === "workload" && mutate;
                    return (
                      <div key={row.key} className={"qs-tile" + (index === active ? " is-active" : "")}>
                        <button
                          type="button"
                          className="qs-tile-main"
                          data-target={`${t.kind}:${t.id}`}
                          title={`${t.name} · ${t.typeLabel} · ${t.status}`}
                          aria-label={`${t.name}, ${t.status}`}
                          onMouseEnter={() => setActive(index)}
                          onClick={() => choose(t)}
                          disabled={!openable && !startable}
                        >
                          <span className={"qs-dot is-" + statusDot(t.status)} aria-hidden="true" />
                          <span className="qs-tile-name">{t.name}</span>
                        </button>
                        {openable && hasCurrent ? (
                          <button type="button" className="qs-tile-alt" title="Replace current session" aria-label={`Replace current session with ${t.name}`} onClick={() => confirmReplace(t)}>
                            ⇄
                          </button>
                        ) : null}
                      </div>
                    );
                  })}
                </div>
              </section>
            ))}
          </div>
        ) : (
        <ul className="palette-list qs-list">
          {rows.length === 0 ? (
            <li className="muted">No matching targets.</li>
          ) : (
            rows.map((row, index) =>
              row.heading ? (
                <li key={row.key} className="qs-heading">
                  {row.heading}
                </li>
              ) : row.target ? (
                <li key={row.key}>
                  <div className={index === active ? "qs-row is-active" : "qs-row"}>
                    <button
                      type="button"
                      className="qs-main"
                      data-target={`${row.target.kind}:${row.target.id}`}
                      onMouseEnter={() => setActive(index)}
                      onClick={() => choose(row.target!)}
                      disabled={!canOpenTermTarget(row.target, roles) && !(row.target.kind === "workload" && !row.target.terminalReady && mutate)}
                    >
                      <span className="qs-name">{row.target.name}</span>
                      <span className="qs-meta">
                        {row.target.typeLabel}
                        {row.target.nodeName && row.target.kind !== "node" ? ` · ${row.target.nodeName}` : ""}
                      </span>
                      <StatusBadge status={row.target.status} />
                    </button>
                    {canOpenTermTarget(row.target, roles) && hasCurrent ? (
                      <button
                        type="button"
                        className="btn btn-ghost btn-sm"
                        onClick={() => confirmReplace(row.target!)}
                      >
                        Replace current
                      </button>
                    ) : null}
                    {!row.target.terminalReady && row.target.kind === "workload" && mutate ? (
                      <button
                        type="button"
                        className="btn btn-ghost btn-sm"
                        onClick={() => setStartTarget(row.target!)}
                      >
                        Start and open terminal
                      </button>
                    ) : null}
                  </div>
                </li>
              ) : null,
            )
          )}
        </ul>
        )}
        <p className="field-hint">Only authorized, terminal-capable targets can be opened. Stopped workloads are listed but not connected.</p>
      </div>
      <div onClick={(event) => event.stopPropagation()}>
      <ConfirmDialog
        open={Boolean(startTarget)}
        title="Start workload?"
        confirmLabel="Start and open terminal"
        onClose={() => setStartTarget(null)}
        onConfirm={() => {
          const target = startTarget;
          if (!target) {
            return;
          }
          setBusy(true);
          void workloadAction(target.id, "start")
            .then(() => {
              onOpenNew({ ...target, status: "running", terminalReady: true });
              setStartTarget(null);
              onClose();
            })
            .catch((err: unknown) => {
              setLoadError(err instanceof Error ? err.message : "Start failed");
              setStartTarget(null);
            })
            .finally(() => setBusy(false));
        }}
      >
        <p>
          {startTarget?.name} is stopped. Start it, then open a new terminal session? This does not happen unless you
          confirm.
        </p>
        {busy ? <p className="muted">Starting...</p> : null}
      </ConfirmDialog>
      <ConfirmDialog
        open={Boolean(replaceTarget)}
        title="Replace current session?"
        confirmLabel="Replace and close current"
        danger
        onClose={() => setReplaceTarget(null)}
        onConfirm={() => {
          if (replaceTarget) {
            onReplace(replaceTarget);
          }
          setReplaceTarget(null);
          onClose();
        }}
      >
        <p>
          This closes the current terminal session, including any foreground process attached to that PTY. Other tabs
          stay open. Prefer opening a new tab unless you intend to replace this session.
        </p>
      </ConfirmDialog>
      </div>
    </div>
  );
}
