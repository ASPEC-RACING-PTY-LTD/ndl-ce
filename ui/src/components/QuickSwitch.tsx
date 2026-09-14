import { useEffect, useMemo, useState } from "react";
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

type Row = { key: string; heading?: string; target?: TermTarget };

export function QuickSwitch({
  open,
  recents,
  hasCurrent,
  currentLive,
  onClose,
  onOpenNew,
  onReplace,
}: {
  open: boolean;
  recents: TermTarget[];
  hasCurrent: boolean;
  currentLive: boolean;
  onClose: () => void;
  onOpenNew: (target: TermTarget) => void;
  onReplace: (target: TermTarget) => void;
}) {
  const session = useSession();
  const roles = session.status === "ready" ? session.user?.roles : undefined;
  const mutate = canMutate(roles);
  const [query, setQuery] = useState("");
  const [active, setActive] = useState(0);
  const [catalog, setCatalog] = useState<TermTarget[]>([]);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [startTarget, setStartTarget] = useState<TermTarget | null>(null);
  const [replaceTarget, setReplaceTarget] = useState<TermTarget | null>(null);
  const [busy, setBusy] = useState(false);

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
          listWorkloads().catch(() => ({ items: [] })),
          listStacks().catch(() => ({ items: [] })),
        ]);
        if (cancelled) {
          return;
        }
        setCatalog(
          buildTermCatalog({
            roles,
            nodes,
            workloads: workloads.items ?? [],
            stacks: stacks.items ?? [],
          }),
        );
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
    const recentHits = recents
      .map((r) => catalog.find((t) => t.kind === r.kind && t.id === r.id) ?? r)
      .filter((t) => filterTermTargets([t], "").length >= 0);
    if (recentHits.length) {
      out.push({ key: "h-recent", heading: "Recent" });
      for (const target of recentHits) {
        out.push({ key: `recent:${target.kind}:${target.id}`, target });
      }
    }
    for (const group of GROUP_ORDER) {
      const items = catalog.filter((t) => t.group === group);
      if (items.length === 0) {
        continue;
      }
      out.push({ key: `h-${group}`, heading: groupHeading(group) });
      for (const target of items) {
        out.push({ key: `${target.kind}:${target.id}`, target });
      }
    }
    return out;
  }, [catalog, filtered, recents, searching]);

  const targetRows = rows.map((r, i) => ({ row: r, index: i })).filter((x) => x.row.target);
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
          if (event.key === "ArrowDown") {
            event.preventDefault();
            setActive((n) => {
              const next = targetRows.find((x) => x.index > n);
              return next?.index ?? n;
            });
          }
          if (event.key === "ArrowUp") {
            event.preventDefault();
            setActive((n) => {
              const prev = [...targetRows].reverse().find((x) => x.index < n);
              return prev?.index ?? n;
            });
          }
          if (event.key === "Enter" && activeTarget) {
            event.preventDefault();
            choose(activeTarget);
          }
        }}
      >
        <h2 id="qs-heading" className="palette-heading">
          Quick Switch
        </h2>
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
          <p className="banner banner-error" role="alert">
            {loadError}
          </p>
        ) : null}
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
