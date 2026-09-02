import type { KeyboardEvent } from "react";
import { useEffect, useMemo, useState } from "react";
import { listNodes, listStacks, listWorkloads } from "../api/client";
import { Icon } from "../components/Icon";
import { Link } from "../components/Link";
import { canMutate } from "../rbac";
import { navigate, usePath } from "../router";
import { useSession } from "../session";
import { useQuery } from "../query";
import { ContextSidebar } from "./ContextSidebar";
import { hrefForTarget, isCreatePath, isManagePath, selectedTargetFromPath, viewFromPath } from "./match";
import { loadGroupState, loadLastView, saveGroupState } from "./prefs";
import { buildNavTargets, filterNavTargets, groupedTargets } from "./targets";
import { targetIsLive, type NavGroup, type NavTarget } from "./types";

const RENDER_CAP = 200;

function itemLabel(target: NavTarget): string {
  const state = targetIsLive(target) ? "running" : target.status || "stopped";
  const node = target.nodeName && target.kind !== "node" ? `, ${target.nodeName}` : "";
  return `${target.name}, ${target.typeLabel}, ${state}${node}`;
}

export function WorkloadsNavigator() {
  const path = usePath();
  const session = useSession();
  const roles = session.status === "ready" ? session.user?.roles : undefined;
  const mutate = canMutate(roles);
  const [query, setQuery] = useState("");
  const [active, setActive] = useState(0);
  const [open, setOpen] = useState<Record<string, boolean>>(() => loadGroupState());
  const selected = selectedTargetFromPath(path);
  const requested = selected ? viewFromPath(path) : loadLastView();

  const nodesQ = useQuery("ctx-nodes", () => listNodes(), 10000);
  const workloadsQ = useQuery("ctx-workloads", () => listWorkloads(), 10000);
  const stacksQ = useQuery("ctx-stacks", () => listStacks().catch(() => ({ items: [] })), 15000);

  const catalog = useMemo(
    () =>
      buildNavTargets({
        nodes: nodesQ.data ?? [],
        workloads: workloadsQ.data?.items ?? [],
        stacks: stacksQ.data?.items ?? [],
      }),
    [nodesQ.data, workloadsQ.data, stacksQ.data],
  );
  const filtered = useMemo(() => filterNavTargets(catalog, query), [catalog, query]);
  const groups = useMemo(() => groupedTargets(filtered), [filtered]);
  const visible = useMemo(
    () => groups.flatMap((group) => (open[group.group] === false ? [] : group.items)),
    [groups, open],
  );
  const flat = useMemo(() => visible.slice(0, RENDER_CAP), [visible]);
  const multiNode = catalog.filter((t) => t.kind === "node").length > 1;

  useEffect(() => {
    setActive(0);
  }, [query]);

  function toggleGroup(group: NavGroup) {
    setOpen((cur) => {
      const next = { ...cur, [group]: cur[group] === false };
      saveGroupState(next);
      return next;
    });
  }

  function onTreeKey(event: KeyboardEvent) {
    if (event.target instanceof HTMLElement && event.target.closest(".xterm, .term-wrap")) {
      return;
    }
    if (event.target instanceof HTMLInputElement) {
      if (event.key !== "ArrowDown" && event.key !== "ArrowUp" && event.key !== "Enter") {
        return;
      }
    }
    if (flat.length === 0) {
      return;
    }
    if (event.key === "ArrowDown") {
      event.preventDefault();
      setActive((n) => Math.min(n + 1, flat.length - 1));
    }
    if (event.key === "ArrowUp") {
      event.preventDefault();
      setActive((n) => Math.max(n - 1, 0));
    }
    if (event.key === "Enter") {
      const hit = flat[active];
      if (hit) {
        event.preventDefault();
        navigate(hrefForTarget(hit, requested));
      }
    }
  }

  return (
    <ContextSidebar title="Workloads">
      <div className="ctx-actions">
        <Link
          href="/workloads"
          className="nav-link"
          aria-label="Manage"
          title="Filter, sort, and bulk-manage workloads"
          aria-current={isManagePath(path) && !selected ? "page" : undefined}
        >
          <Icon name="workloads" />
          <span className="nav-link-label">Manage</span>
        </Link>
        {mutate ? (
          <details className="ctx-create" open={isCreatePath(path) || undefined}>
            <summary className="nav-link" aria-label="Create">
              <Icon name="create" />
              <span className="nav-link-label">Create</span>
            </summary>
            <Link
              href="/workloads/new/system-container"
              className="nav-link"
              aria-current={path.includes("/new/system-container") ? "page" : undefined}
            >
              <span className="nav-link-label">System container</span>
            </Link>
            <Link href="/workloads/new/vm" className="nav-link" aria-current={path.includes("/new/vm") ? "page" : undefined}>
              <span className="nav-link-label">VM</span>
            </Link>
            <Link href="/workloads/new/oci" className="nav-link" aria-current={path.includes("/new/oci") ? "page" : undefined}>
              <span className="nav-link-label">OCI</span>
            </Link>
          </details>
        ) : null}
      </div>
      <label className="ctx-search search-field">
        <Icon name="search" size={14} />
        <input
          className="field-input"
          type="search"
          value={query}
          placeholder="Search"
          aria-label="Search targets"
          onChange={(event) => setQuery(event.target.value)}
          onKeyDown={onTreeKey}
        />
      </label>
      <div className="ctx-tree" role="tree" aria-label="Infrastructure" tabIndex={0} onKeyDown={onTreeKey}>
        {groups.length === 0 ? <p className="muted ctx-empty">No matching targets.</p> : null}
        {groups.map((group) => {
          const expanded = open[group.group] !== false;
          const shown = expanded ? group.items.filter((item) => flat.some((t) => t.kind === item.kind && t.id === item.id)) : [];
          return (
            <div className="ctx-group" key={group.group}>
              <button
                className="ctx-group-toggle"
                type="button"
                aria-expanded={expanded}
                onClick={() => toggleGroup(group.group)}
              >
                {group.heading}
              </button>
              {expanded
                ? shown.map((target) => {
                    const idx = flat.findIndex((t) => t.kind === target.kind && t.id === target.id);
                    const current = selected?.kind === target.kind && selected.id === target.id;
                    const live = targetIsLive(target);
                    return (
                      <Link
                        key={`${target.kind}:${target.id}`}
                        href={hrefForTarget(target, requested)}
                        className={
                          "ctx-item" +
                          (current ? " is-current" : "") +
                          (idx === active ? " is-active" : "") +
                          (target.kind === "node" ? " is-host" : "")
                        }
                        role="treeitem"
                        aria-current={current ? "page" : undefined}
                        aria-label={itemLabel(target)}
                        title={`${target.name} · ${target.typeLabel} · ${target.status}${
                          target.nodeName && target.kind !== "node" ? ` · ${target.nodeName}` : ""
                        }`}
                        data-nav-id={`${target.kind}:${target.id}`}
                      >
                        <span className={"ctx-dot" + (live ? " is-live" : "")} aria-hidden="true">
                          {live ? "●" : "○"}
                        </span>
                        <span className="ctx-item-label">{target.name}</span>
                        {target.kind === "node" ? <span className="ctx-host-mark">Host</span> : null}
                        {multiNode && target.kind !== "node" && target.nodeName ? (
                          <span className="ctx-node">{target.nodeName}</span>
                        ) : null}
                      </Link>
                    );
                  })
                : null}
            </div>
          );
        })}
        {visible.length > RENDER_CAP ? (
          <p className="muted ctx-empty">
            Showing {RENDER_CAP} of {visible.length}. Narrow search to see the rest.
          </p>
        ) : null}
      </div>
    </ContextSidebar>
  );
}
