import { useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { listNodes, listWorkloads } from "../api/client";
import { FilesBrowser } from "../components/FilesBrowser";
import { Icon } from "../components/Icon";
import { StatusBadge } from "../components/StatusBadge";
import { ErrorNotice } from "../components/ErrorNotice";
import { useTabDrag } from "../components/useTabDrag";
import { displayPath } from "../files/paths";
import { kindLabel } from "../labels";
import { navigate } from "../router";
import { isAdmin } from "../rbac";
import { useSession } from "../session";
import { storageGet, storageSet } from "../storage";

const STORE_KEY = "ndl-files-workspace";

export type FilesTarget = {
  kind: "node" | "workload";
  id: string;
  name: string;
  typeLabel: string;
  status?: string;
};

type FilesTab = {
  tabId: string;
  target: FilesTarget;
  path: string;
  edit: string;
  title?: string;
};

type Stored = { userId?: string; tabs?: FilesTab[]; activeId?: string | null };

function newId(): string {
  if (typeof crypto !== "undefined" && "randomUUID" in crypto) {
    return crypto.randomUUID();
  }
  return `files-${Date.now()}-${Math.random().toString(16).slice(2)}`;
}

function loadStore(userId: string): Stored {
  try {
    const raw = storageGet(STORE_KEY);
    const parsed = raw ? (JSON.parse(raw) as Stored) : {};
    if (parsed.userId && parsed.userId !== userId) {
      return {};
    }
    return parsed;
  } catch {
    return {};
  }
}

function tabLabel(tab: FilesTab): string {
  if (tab.title) {
    return tab.title;
  }
  const base = displayPath(tab.path).split("/").filter(Boolean).pop();
  return base ? `${tab.target.name}: ${base}` : tab.target.name;
}

function TargetPicker({ open, onClose, onPick }: { open: boolean; onClose: () => void; onPick: (t: FilesTarget) => void }) {
  const session = useSession();
  const roles = session.status === "ready" ? session.user?.roles : undefined;
  const [items, setItems] = useState<FilesTarget[]>([]);
  const [query, setQuery] = useState("");
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!open) {
      return;
    }
    setQuery("");
    setError(null);
    let cancelled = false;
    void Promise.all([isAdmin(roles) ? listNodes().catch(() => []) : Promise.resolve([]), listWorkloads()])
      .then(([nodes, workloads]) => {
        if (cancelled) {
          return;
        }
        const out: FilesTarget[] = nodes.map((n) => ({
          kind: "node" as const,
          id: n.id,
          name: n.name || n.id,
          typeLabel: "Host",
          status: n.status,
        }));
        for (const w of workloads.items ?? []) {
          if (w.kind !== "system-container" && w.kind !== "vm") {
            continue;
          }
          out.push({ kind: "workload", id: w.id, name: w.name || w.id, typeLabel: kindLabel(w.kind), status: w.status });
        }
        setItems(out);
      })
      .catch((err) => {
        if (!cancelled) {
          setError(err instanceof Error ? err.message : "Could not load targets");
        }
      });
    return () => {
      cancelled = true;
    };
  }, [open, roles]);

  const shown = useMemo(() => {
    const needle = query.trim().toLowerCase();
    return needle ? items.filter((t) => `${t.name} ${t.typeLabel} ${t.status ?? ""}`.toLowerCase().includes(needle)) : items;
  }, [items, query]);

  if (!open) {
    return null;
  }
  return (
    <div className="palette-backdrop" onClick={onClose}>
      <div
        className="palette qs-palette"
        role="dialog"
        aria-modal="true"
        aria-labelledby="files-pick-heading"
        onClick={(event) => event.stopPropagation()}
        onKeyDown={(event) => {
          if (event.key === "Escape") {
            event.preventDefault();
            onClose();
          }
          if (event.key === "Enter" && shown[0]) {
            event.preventDefault();
            onPick(shown[0]);
          }
        }}
      >
        <h2 id="files-pick-heading" className="palette-heading">
          Open files
        </h2>
        <label className="field-label" htmlFor="files-pick-search">
          Search
        </label>
        <input
          id="files-pick-search"
          className="field-input"
          autoFocus
          value={query}
          placeholder="Name or type"
          onChange={(event) => setQuery(event.target.value)}
        />
        {error ? <ErrorNotice error={error} /> : null}
        <ul className="palette-list qs-list">
          {shown.length === 0 ? (
            <li className="muted">No matching hosts, containers or VMs.</li>
          ) : (
            shown.map((t) => (
              <li key={`${t.kind}:${t.id}`}>
                <div className="qs-row">
                  <button type="button" className="qs-main" onClick={() => onPick(t)}>
                    <span className="qs-name">{t.name}</span>
                    <span className="qs-meta">{t.typeLabel}</span>
                    {t.status ? <StatusBadge status={t.status} /> : null}
                  </button>
                </div>
              </li>
            ))
          )}
        </ul>
      </div>
    </div>
  );
}

/** FilesWorkspacePage keeps several file browsers open as tabs, like the Terminal workspace. */
export function FilesWorkspacePage() {
  const session = useSession();
  const userId = session.status === "ready" ? session.user?.user_id ?? "" : "";
  const [tabs, setTabs] = useState<FilesTab[]>([]);
  const [activeId, setActiveId] = useState<string | null>(null);
  const [hydrated, setHydrated] = useState(false);
  const [picker, setPicker] = useState(false);
  const [menu, setMenu] = useState<{ tabId: string; x: number; y: number } | null>(null);
  const menuRef = useRef<HTMLDivElement>(null);

  function moveTab(tabId: string, toIndex: number) {
    setTabs((cur) => {
      const from = cur.findIndex((t) => t.tabId === tabId);
      const to = Math.max(0, Math.min(cur.length - 1, toIndex));
      if (from < 0 || from === to) {
        return cur;
      }
      const out = [...cur];
      const [moved] = out.splice(from, 1);
      out.splice(to, 0, moved);
      return out;
    });
  }
  const drag = useTabDrag(
    tabs.map((t) => t.tabId),
    moveTab,
  );

  // Restore saved tabs, then open the target named in the link, if any.
  useEffect(() => {
    if (!userId || hydrated) {
      return;
    }
    const stored = loadStore(userId);
    let list = (stored.tabs ?? []).filter((t) => t?.tabId && t.target?.id);
    let active = stored.activeId && list.some((t) => t.tabId === stored.activeId) ? stored.activeId : list[0]?.tabId ?? null;
    const q = new URLSearchParams(window.location.search);
    const kind = q.get("kind");
    const id = q.get("id");
    if ((kind === "node" || kind === "workload") && id) {
      const path = q.get("path") || "/";
      const same = list.find((t) => t.target.kind === kind && t.target.id === id && t.path === path);
      if (same) {
        active = same.tabId;
      } else {
        const known = list.find((t) => t.target.kind === kind && t.target.id === id)?.target;
        const tab: FilesTab = {
          tabId: newId(),
          target: known ?? { kind, id, name: q.get("name") || id, typeLabel: kind === "node" ? "Host" : "Workload" },
          path,
          edit: "",
        };
        list = [...list, tab];
        active = tab.tabId;
      }
      window.history.replaceState({}, "", "/files");
    }
    setTabs(list);
    setActiveId(active);
    setHydrated(true);
  }, [userId, hydrated]);

  useEffect(() => {
    if (!userId || !hydrated) {
      return;
    }
    storageSet(STORE_KEY, JSON.stringify({ userId, tabs, activeId }));
  }, [userId, hydrated, tabs, activeId]);

  useEffect(() => {
    if (!menu) {
      return;
    }
    function onDoc() {
      setMenu(null);
    }
    document.addEventListener("mousedown", onDoc);
    return () => document.removeEventListener("mousedown", onDoc);
  }, [menu]);

  useLayoutEffect(() => {
    const el = menuRef.current;
    if (!menu || !el) {
      return;
    }
    const margin = 8;
    const rect = el.getBoundingClientRect();
    el.style.left = `${Math.max(margin, Math.min(menu.x, window.innerWidth - rect.width - margin))}px`;
    el.style.top = `${Math.max(margin, Math.min(menu.y, window.innerHeight - rect.height - margin))}px`;
  }, [menu]);

  function open(target: FilesTarget, path = "/", after?: string) {
    const tab: FilesTab = { tabId: newId(), target, path, edit: "" };
    setTabs((cur) => {
      const at = after ? cur.findIndex((t) => t.tabId === after) : -1;
      if (at < 0) {
        return [...cur, tab];
      }
      const out = [...cur];
      out.splice(at + 1, 0, tab);
      return out;
    });
    setActiveId(tab.tabId);
  }

  function closeTabs(gone: Set<string>) {
    setTabs((cur) => {
      const next = cur.filter((t) => !gone.has(t.tabId));
      setActiveId((id) => {
        if (id && !gone.has(id)) {
          return id;
        }
        const idx = cur.findIndex((t) => t.tabId === id);
        const after = cur.slice(idx + 1).find((t) => !gone.has(t.tabId));
        const before = [...cur.slice(0, Math.max(idx, 0))].reverse().find((t) => !gone.has(t.tabId));
        return after?.tabId ?? before?.tabId ?? next[0]?.tabId ?? null;
      });
      return next;
    });
  }

  function setLocation(tabId: string, path: string, edit: string) {
    setTabs((cur) => cur.map((t) => (t.tabId === tabId ? { ...t, path, edit } : t)));
  }

  function renameTab(tab: FilesTab) {
    const next = window.prompt("Tab name", tabLabel(tab));
    if (next != null) {
      setTabs((cur) => cur.map((t) => (t.tabId === tab.tabId ? { ...t, title: next.trim() || undefined } : t)));
    }
  }

  const menuTab = menu ? tabs.find((t) => t.tabId === menu.tabId) ?? null : null;
  const menuIndex = menuTab ? tabs.indexOf(menuTab) : -1;

  function menuAction(action: () => void) {
    action();
    setMenu(null);
  }

  return (
    <section className="page page-wide page-files" aria-labelledby="files-ws-heading">
      <div className="term-toolbar">
        <h1 id="files-ws-heading" className="term-page-title">
          Files
        </h1>
        <div className="term-tabs" role="tablist" aria-label="Open file browsers" ref={drag.listRef}>
          {tabs.map((tab) => (
            <button
              key={tab.tabId}
              type="button"
              role="tab"
              aria-selected={tab.tabId === activeId}
              className={
                "term-tab" +
                (tab.tabId === activeId ? " is-active" : "") +
                (tab.target.kind === "node" ? " is-host" : "") +
                (drag.draggingId === tab.tabId ? " is-dragging" : "")
              }
              data-tab-id={tab.tabId}
              title={`${tab.target.name} · ${tab.target.typeLabel} · ${displayPath(tab.path)}`}
              onPointerDown={(event) => drag.onPointerDown(event, tab.tabId)}
              onClick={() => {
                if (drag.consumeClick()) {
                  return;
                }
                setActiveId(tab.tabId);
              }}
              onMouseDown={(event) => {
                if (event.button === 1) {
                  event.preventDefault();
                }
              }}
              onAuxClick={(event) => {
                if (event.button === 1) {
                  event.preventDefault();
                  closeTabs(new Set([tab.tabId]));
                }
              }}
              onContextMenu={(event) => {
                event.preventDefault();
                setMenu({ tabId: tab.tabId, x: event.clientX, y: event.clientY });
              }}
              onDoubleClick={() => renameTab(tab)}
            >
              {tab.target.kind === "node" ? <span className="term-host-dot">H</span> : null}
              <span className="term-tab-title">{tabLabel(tab)}</span>
            </button>
          ))}
          <button
            className="btn btn-sm btn-secondary term-add"
            type="button"
            aria-label="Open files"
            title="Open files"
            onClick={() => setPicker(true)}
          >
            <Icon name="create" size={14} />+
          </button>
        </div>
      </div>
      {tabs.length === 0 ? (
        <div className="term-empty">
          <p>No files open.</p>
          <p className="muted">Use + to browse a host, system container or VM.</p>
        </div>
      ) : null}
      {tabs.map((tab) => (
        <div key={tab.tabId} className="files-ws-slot" hidden={tab.tabId !== activeId}>
          <FilesBrowser
            kind={tab.target.kind}
            id={tab.target.id}
            initialPath={tab.path}
            initialEdit={tab.edit}
            embedded
            active={tab.tabId === activeId}
            onLocation={(path, edit) => setLocation(tab.tabId, path, edit)}
            onTerminalHere={(path) => {
              const base = tab.target.kind === "node" ? `/nodes/${tab.target.id}/terminal` : `/workloads/${tab.target.id}/terminal`;
              navigate(`${base}?cwd=${encodeURIComponent(path)}`);
            }}
          />
        </div>
      ))}
      {menu && menuTab ? (
        <div
          ref={menuRef}
          className="menu-panel term-ctx"
          role="menu"
          aria-label={`${tabLabel(menuTab)} actions`}
          style={{ left: menu.x, top: menu.y }}
          onMouseDown={(event) => event.stopPropagation()}
        >
          <button type="button" role="menuitem" onClick={() => menuAction(() => renameTab(menuTab))}>
            Rename
          </button>
          <button type="button" role="menuitem" onClick={() => menuAction(() => open(menuTab.target, menuTab.path, menuTab.tabId))}>
            Duplicate
          </button>
          <button type="button" role="menuitem" onClick={() => menuAction(() => open(menuTab.target, "/", menuTab.tabId))}>
            New Tab Here
          </button>
          <hr />
          <button type="button" role="menuitem" disabled={menuIndex <= 0} onClick={() => menuAction(() => moveTab(menuTab.tabId, menuIndex - 1))}>
            Move Left
          </button>
          <button
            type="button"
            role="menuitem"
            disabled={menuIndex < 0 || menuIndex >= tabs.length - 1}
            onClick={() => menuAction(() => moveTab(menuTab.tabId, menuIndex + 1))}
          >
            Move Right
          </button>
          <hr />
          <button type="button" role="menuitem" className="is-danger" onClick={() => menuAction(() => closeTabs(new Set([menuTab.tabId])))}>
            Close Tab
          </button>
          <button
            type="button"
            role="menuitem"
            className="is-danger"
            disabled={tabs.length < 2}
            onClick={() => menuAction(() => closeTabs(new Set(tabs.filter((t) => t.tabId !== menuTab.tabId).map((t) => t.tabId))))}
          >
            Close Other Tabs
          </button>
          <button
            type="button"
            role="menuitem"
            className="is-danger"
            disabled={menuIndex <= 0}
            onClick={() => menuAction(() => closeTabs(new Set(tabs.slice(0, menuIndex).map((t) => t.tabId))))}
          >
            Close Tabs to the Left
          </button>
          <button
            type="button"
            role="menuitem"
            className="is-danger"
            disabled={menuIndex < 0 || menuIndex >= tabs.length - 1}
            onClick={() => menuAction(() => closeTabs(new Set(tabs.slice(menuIndex + 1).map((t) => t.tabId))))}
          >
            Close Tabs to the Right
          </button>
        </div>
      ) : null}
      <TargetPicker
        open={picker}
        onClose={() => setPicker(false)}
        onPick={(t) => {
          open(t);
          setPicker(false);
        }}
      />
    </section>
  );
}
