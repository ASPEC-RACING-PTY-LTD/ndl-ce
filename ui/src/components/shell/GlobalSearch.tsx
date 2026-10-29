import { useEffect, useMemo, useRef, useState } from "react";
import { searchAll, type SearchResults } from "../../api/client";
import type { SearchFilter, SearchResult } from "../../generated/openapi";
import { useNavDisclosure } from "../../nav/NavDisclosure";
import { filterPaletteActions, visiblePaletteActions } from "../../palette";
import { hasRoleAccess } from "../../rbac";
import { navigate } from "../../router";
import { useSession } from "../../session";
import { Icon } from "../Icon";
import { OPEN_PALETTE } from "../ContextMenu";

/** One row in the dropdown: a page or action from the app, or a server result. */
export type SearchRow = { key: string; title: string; subtitle?: string; href: string; group: string; status?: string };

const KIND_LABELS: Record<string, string> = {
  page: "Pages",
  user: "Users",
  group: "Groups",
  role: "Roles",
  workload: "Workloads",
  vm: "VMs",
  oci: "OCI containers",
  node: "Nodes",
  game_server: "Game servers",
  stack: "Container groups",
  storage: "Storage",
  network: "Networks",
  backup: "Backups",
  template: "Templates",
  registry: "Registries",
  flow: "Flows",
  alert: "Alerts",
  deploy: "Deployments",
  runner: "Runners",
  file: "Files",
  host_file: "Host files",
};

export function kindLabel(kind: string): string {
  return KIND_LABELS[kind] ?? kind.replace(/_/g, " ");
}

/** mergeRows puts pages first, then server results grouped by kind in the order they arrived. */
export function mergeRows(pages: SearchRow[], items: SearchResult[]): SearchRow[] {
  const out = [...pages];
  const order: string[] = [];
  const byKind = new Map<string, SearchRow[]>();
  for (const it of items) {
    if (!byKind.has(it.kind)) {
      byKind.set(it.kind, []);
      order.push(it.kind);
    }
    byKind.get(it.kind)!.push({
      key: `${it.kind}:${it.id}`,
      title: it.title,
      subtitle: it.subtitle,
      href: it.link,
      group: kindLabel(it.kind),
      status: it.status,
    });
  }
  for (const k of order) {
    out.push(...byKind.get(k)!);
  }
  return out;
}

/**
 * GlobalSearch is the header search box. Typing shows pages and actions at
 * once and asks the server for everything else the person may see. Results
 * are links, so middle-click or Ctrl+click opens them in a new tab.
 */
export function GlobalSearch() {
  const session = useSession();
  const user = session.status === "ready" ? session.user : null;
  const roles = user?.roles;
  const { featureEnabled } = useNavDisclosure();
  const [q, setQ] = useState("");
  const [open, setOpen] = useState(false);
  const [active, setActive] = useState(0);
  const [filter, setFilter] = useState<SearchFilter | null>(null);
  const [res, setRes] = useState<SearchResults | null>(null);
  const [loading, setLoading] = useState(false);
  const [failed, setFailed] = useState(false);
  const input = useRef<HTMLInputElement>(null);
  const root = useRef<HTMLDivElement>(null);

  const actions = useMemo(
    () => visiblePaletteActions(roles, user).filter((a) => !a.feature || featureEnabled[a.feature]),
    [roles, user, featureEnabled],
  );

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        input.current?.focus();
        input.current?.select();
        setOpen(true);
      }
    }
    const focus = () => {
      input.current?.focus();
      setOpen(true);
    };
    window.addEventListener("keydown", onKey);
    window.addEventListener(OPEN_PALETTE, focus);
    return () => {
      window.removeEventListener("keydown", onKey);
      window.removeEventListener(OPEN_PALETTE, focus);
    };
  }, []);

  useEffect(() => {
    if (!open) {
      return;
    }
    function onDoc(e: MouseEvent) {
      if (root.current && !root.current.contains(e.target as Node)) {
        setOpen(false);
      }
    }
    document.addEventListener("mousedown", onDoc);
    return () => document.removeEventListener("mousedown", onDoc);
  }, [open]);

  const term = [q.trim(), filter?.query ?? ""].filter(Boolean).join(" ");
  // Server search is closed to people without a role; they get page suggestions only.
  const serverSearch = hasRoleAccess(user);
  useEffect(() => {
    if (!open || !serverSearch || (term.length < 2 && !filter)) {
      setRes((cur) => (cur && !filter ? { ...cur, items: [] } : cur));
      return;
    }
    const ctl = new AbortController();
    const id = window.setTimeout(() => {
      setLoading(true);
      searchAll(term, { kinds: filter?.kinds, status: filter?.status, limit: 40, signal: ctl.signal })
        .then((r) => {
          setRes(r);
          setFailed(false);
        })
        .catch((err: unknown) => {
          if (!(err instanceof DOMException && err.name === "AbortError")) {
            setFailed(true);
          }
        })
        .finally(() => setLoading(false));
    }, 180);
    return () => {
      ctl.abort();
      window.clearTimeout(id);
    };
  }, [term, filter, open, serverSearch]);

  // The saved filters come with any result; fetch them once on first open.
  useEffect(() => {
    if (open && !res && serverSearch) {
      searchAll("", { limit: 1 })
        .then((r) => setRes((cur) => cur ?? { ...r, items: [] }))
        .catch(() => undefined);
    }
  }, [open, res, serverSearch]);

  const pages: SearchRow[] =
    filter || !q.trim()
      ? []
      : filterPaletteActions(actions, q)
          .slice(0, 6)
          .map((a) => ({ key: `page:${a.id}`, title: a.label, href: a.href, group: "Pages" }));
  const rows = mergeRows(pages, res?.items ?? []);

  useEffect(() => setActive(0), [q, filter]);

  function go(href: string, newTab: boolean) {
    if (newTab) {
      window.open(href, "_blank", "noopener");
      return;
    }
    setOpen(false);
    setQ("");
    input.current?.blur();
    navigate(href);
  }

  const showDrop = open && (q.trim().length > 0 || filter !== null || (res?.filters.length ?? 0) > 0);
  let lastGroup = "";
  return (
    <div className="global-search" ref={root}>
      <Icon name="search" size={15} />
      <input
        ref={input}
        type="search"
        role="combobox"
        aria-expanded={showDrop}
        aria-controls="global-search-list"
        aria-autocomplete="list"
        aria-label="Search everything"
        aria-keyshortcuts="Control+K Meta+K"
        placeholder="Search"
        value={q}
        onFocus={() => setOpen(true)}
        onChange={(e) => {
          setQ(e.target.value);
          setOpen(true);
        }}
        onKeyDown={(e) => {
          if (e.key === "Escape") {
            setOpen(false);
            input.current?.blur();
          } else if (e.key === "ArrowDown") {
            e.preventDefault();
            setActive((n) => Math.min(n + 1, Math.max(rows.length - 1, 0)));
          } else if (e.key === "ArrowUp") {
            e.preventDefault();
            setActive((n) => Math.max(n - 1, 0));
          } else if (e.key === "Enter" && rows[active]) {
            e.preventDefault();
            go(rows[active].href, e.ctrlKey || e.metaKey);
          }
        }}
      />
      <kbd className="global-search-kbd" aria-hidden="true">
        Ctrl K
      </kbd>
      {showDrop ? (
        <div className="global-search-drop">
          {(res?.filters.length ?? 0) > 0 ? (
            <div className="chip-row global-search-filters" aria-label="Filters">
              {res!.filters.map((f) => (
                <button
                  key={f.id}
                  type="button"
                  className="chip"
                  aria-pressed={filter?.id === f.id}
                  onMouseDown={(e) => e.preventDefault()}
                  onClick={() => setFilter((cur) => (cur?.id === f.id ? null : f))}
                >
                  {f.label}
                </button>
              ))}
            </div>
          ) : null}
          <ul id="global-search-list" role="listbox" className="global-search-list">
            {rows.map((r, i) => {
              const head = r.group !== lastGroup ? r.group : "";
              lastGroup = r.group;
              return (
                <li key={r.key} role="presentation">
                  {head ? <p className="global-search-group">{head}</p> : null}
                  <a
                    role="option"
                    aria-selected={i === active}
                    href={r.href}
                    className={"global-search-item" + (i === active ? " is-active" : "")}
                    onMouseEnter={() => setActive(i)}
                    onClick={(e) => {
                      if (e.ctrlKey || e.metaKey || e.shiftKey || e.button !== 0) {
                        return;
                      }
                      e.preventDefault();
                      go(r.href, false);
                    }}
                  >
                    <span className="global-search-title">{r.title}</span>
                    {r.subtitle ? <span className="global-search-sub">{r.subtitle}</span> : null}
                    {r.status ? <span className="chip chip-sm">{r.status}</span> : null}
                  </a>
                </li>
              );
            })}
          </ul>
          {rows.length === 0 && (q.trim().length >= 2 || filter) && !loading ? (
            <p className="muted global-search-empty">{failed ? "Search is unavailable right now." : "Nothing found."}</p>
          ) : null}
          {loading ? <p className="muted global-search-empty">Searching</p> : null}
        </div>
      ) : null}
    </div>
  );
}
