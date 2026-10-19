import { useEffect, useState, type ReactNode } from "react";
import { getHealth } from "../api/client";
import type { HealthResponse } from "../api/types";
import { useVisibleNav } from "../nav/NavDisclosure";
import { isIAMPath, moduleForPath } from "../nav/modules";

const IAM_CRUMBS: Record<string, string> = {
  "/users": "Users",
  "/iam/access": "Access control",
  "/iam/approvals": "Approvals",
  "/notifications": "Notifications",
  "/api-access": "API Access",
};
import { gameServerCrumbs, isGameServersContext } from "../nav/gameServers";
import { selectedTargetFromPath, viewFromPath } from "../nav/match";
import { saveLastView } from "../nav/prefs";
import { navigate, usePath } from "../router";
import { useSession } from "../session";
import { hasGrant } from "../rbac";
import { HostDiskBanner } from "./HostDisk";
import { storageGet, storageSet } from "../storage";
import { BrandMark } from "./BrandMark";
import { CommandPalette } from "./CommandPalette";
import { OPEN_PALETTE } from "./ContextMenu";
import { Icon, navIcon } from "./Icon";
import { Link } from "./Link";
import { SidebarAccount } from "./shell/SidebarAccount";
import { ContextMenu, useContextMenu } from "./ContextMenu";
import { usePref } from "../prefs";
import { NAV_ORDER_KEY, applyNavOrder, moveNavItem, type NavOrder } from "../nav/disclosure";
import { NotificationBell } from "./shell/NotificationBell";
import { TaskIndicator } from "./shell/TaskIndicator";
import { UpdateBanner } from "./shell/UpdateBanner";

const SIDEBAR_KEY = "ndl-sidebar-collapsed";

const TAB_LABELS: Record<string, string> = {
  terminal: "Terminal",
  files: "Files",
  settings: "Settings",
  snapshots: "Snapshots",
  hardware: "Hardware",
  metrics: "Metrics",
  console: "Console",
  gpus: "GPUs",
  gpu: "GPU",
  logs: "Logs",
  clone: "Clone",
  migrate: "Migrate",
  operations: "Operations",
  machine: "USB",
};

function crumbs(path: string): { href: string; label: string }[] {
  if (path === "/") {
    return [{ href: "/", label: "Dashboard" }];
  }
  if (path === "/terminal") {
    return [{ href: "/terminal", label: "Terminal" }];
  }
  if (path === "/workloads/new/vm") {
    return [
      { href: "/workloads", label: "Workloads" },
      { href: path, label: "Create VM" },
    ];
  }
  if (path === "/workloads/new/oci") {
    return [
      { href: "/workloads", label: "Workloads" },
      { href: path, label: "Create OCI" },
    ];
  }
  if (path.startsWith("/workloads/new")) {
    return [
      { href: "/workloads", label: "Workloads" },
      { href: path, label: "Create system container" },
    ];
  }
  if (path === "/workloads/import") {
    return [
      { href: "/workloads", label: "Workloads" },
      { href: path, label: "Import VM" },
    ];
  }
  if (isGameServersContext(path)) {
    return gameServerCrumbs(path);
  }
  if (path.startsWith("/workloads/")) {
    const parts = path.split("/").filter(Boolean);
    const base = `/workloads/${parts[1]}`;
    const trail = [
      { href: "/workloads", label: "Workloads" },
      { href: base, label: "Workload" },
    ];
    if (parts[2]) {
      trail.push({ href: path, label: TAB_LABELS[parts[2]] ?? parts[2] });
    }
    return trail;
  }
  if (path.startsWith("/nodes/")) {
    const parts = path.split("/").filter(Boolean);
    const base = `/nodes/${parts[1]}`;
    const trail = [
      { href: "/workloads", label: "Workloads" },
      { href: base, label: "Host" },
    ];
    if (parts[2]) {
      trail.push({ href: path, label: TAB_LABELS[parts[2]] ?? parts[2] });
    }
    return trail;
  }
  if (path.startsWith("/node")) {
    const parts = path.split("/").filter(Boolean);
    const trail = [{ href: "/node", label: "Node" }];
    const tab = parts[parts.length - 1];
    if (TAB_LABELS[tab]) {
      trail.push({ href: path, label: TAB_LABELS[tab] });
    }
    return trail;
  }
  if (isIAMPath(path) && path !== "/iam") {
    return [
      { href: "/iam", label: "IAM" },
      { href: path, label: IAM_CRUMBS[path] ?? "IAM" },
    ];
  }
  const top = moduleForPath(path);
  return top ? [{ href: top.href, label: top.label }] : [{ href: path, label: "Page" }];
}

export function Shell({ children }: { children: ReactNode }) {
  const path = usePath();
  const session = useSession();
  const user = session.status === "ready" ? session.user : null;
  const [health, setHealth] = useState<HealthResponse | null>(null);
  const [paletteOpen, setPaletteOpen] = useState(false);
  const [collapsed, setCollapsed] = useState(() => {
    const saved = storageGet(SIDEBAR_KEY);
    if (saved === "1") {
      return true;
    }
    if (saved === "0") {
      return false;
    }
    return typeof window !== "undefined" ? window.innerWidth < 1366 : false;
  });

  useEffect(() => {
    let cancelled = false;
    async function tick() {
      try {
        const value = await getHealth();
        if (!cancelled) {
          setHealth(value);
        }
      } catch {
        if (!cancelled) {
          setHealth(null);
        }
      }
    }
    void tick();
    const id = window.setInterval(() => void tick(), 15000);
    return () => {
      cancelled = true;
      window.clearInterval(id);
    };
  }, []);

  useEffect(() => {
    const open = () => setPaletteOpen(true);
    window.addEventListener(OPEN_PALETTE, open);
    return () => window.removeEventListener(OPEN_PALETTE, open);
  }, []);

  useEffect(() => {
    function onKey(event: KeyboardEvent) {
      if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "k") {
        event.preventDefault();
        setPaletteOpen((open) => !open);
      }
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  useEffect(() => {
    if (selectedTargetFromPath(path)) {
      saveLastView(viewFromPath(path));
    }
  }, [path]);

  function toggleSidebar() {
    setCollapsed((cur) => {
      const next = !cur;
      storageSet(SIDEBAR_KEY, next ? "1" : "0");
      return next;
    });
  }

  const [navOrder, setNavOrder] = usePref<NavOrder>(NAV_ORDER_KEY, {});
  const groups = applyNavOrder(useVisibleNav(path), navOrder);
  const navMenu = useContextMenu();
  const [drag, setDrag] = useState<{ group: string; id: string } | null>(null);
  const [dropAt, setDropAt] = useState<{ id: string; after: boolean } | null>(null);

  function moveTo(group: string, ids: string[], id: string, target: string, after: boolean) {
    setNavOrder((cur) => moveNavItem(cur ?? {}, group, ids, id, target, after));
  }

  function moveBy(group: string, ids: string[], id: string, delta: number) {
    const i = ids.indexOf(id);
    const j = i + delta;
    if (i < 0 || j < 0 || j >= ids.length) {
      return;
    }
    moveTo(group, ids, id, ids[j], delta > 0);
  }

  function resetGroup(group: string) {
    setNavOrder((cur) => {
      const next = { ...(cur ?? {}) };
      delete next[group];
      return next;
    });
  }
  const healthOk = health?.status === "ok";
  const healthLabel = healthOk ? "Healthy" : health ? "Degraded" : "Unavailable";
  const trail = crumbs(path);

  return (
    <div className={collapsed ? "shell is-collapsed" : "shell"}>
      <a className="skip-link" href="#main">
        Skip to content
      </a>
      <aside className="sidebar">
        <div className="sidebar-brand">
          <Link href="/" className="brand-mark" aria-label="No-dal">
            <BrandMark size="sidebar" decorative />
          </Link>
          <Link href="/" className="wordmark">
            No-dal
          </Link>
          <span className="edition-badge">CE</span>
        </div>
        <nav className="sidebar-nav" aria-label="Appliance">
          {(
            groups.map((group) => {
              const ids = group.items.map((item) => item.id);
              return (
                <div className="nav-group" key={group.label}>
                  <p className="nav-group-label">{group.label}</p>
                  {group.items.map((item, i) => (
                    <Link
                      key={item.href}
                      href={item.href}
                      className={
                        "nav-link" +
                        (drag?.id === item.id ? " is-dragging" : "") +
                        (dropAt?.id === item.id ? (dropAt.after ? " is-drop-after" : " is-drop-before") : "")
                      }
                      aria-label={item.label}
                      title={item.label}
                      aria-current={item.match(path) ? "page" : undefined}
                      aria-keyshortcuts="Alt+ArrowUp Alt+ArrowDown"
                      draggable
                      onDragStart={(e) => {
                        e.dataTransfer.effectAllowed = "move";
                        e.dataTransfer.setData("text/plain", item.label);
                        setDrag({ group: group.label, id: item.id });
                      }}
                      onDragOver={(e) => {
                        // Entries move within their own category only.
                        if (!drag || drag.group !== group.label) {
                          return;
                        }
                        e.preventDefault();
                        const r = e.currentTarget.getBoundingClientRect();
                        const after = e.clientY > r.top + r.height / 2;
                        if (dropAt?.id !== item.id || dropAt.after !== after) {
                          setDropAt({ id: item.id, after });
                        }
                      }}
                      onDragLeave={() => setDropAt((cur) => (cur?.id === item.id ? null : cur))}
                      onDrop={(e) => {
                        e.preventDefault();
                        if (drag && drag.group === group.label && dropAt) {
                          moveTo(group.label, ids, drag.id, item.id, dropAt.after);
                        }
                        setDrag(null);
                        setDropAt(null);
                      }}
                      onDragEnd={() => {
                        setDrag(null);
                        setDropAt(null);
                      }}
                      onKeyDown={(e) => {
                        if (e.altKey && (e.key === "ArrowUp" || e.key === "ArrowDown")) {
                          e.preventDefault();
                          moveBy(group.label, ids, item.id, e.key === "ArrowUp" ? -1 : 1);
                        }
                      }}
                      onContextMenu={(e) =>
                        navMenu.openAt(e, item.label, [
                          { label: "Open", onClick: () => navigate(item.href) },
                          { label: "Open in new tab", onClick: () => window.open(item.href, "_blank", "noopener") },
                          "sep",
                          { label: "Move up", disabled: i === 0, onClick: () => moveBy(group.label, ids, item.id, -1) },
                          { label: "Move down", disabled: i === ids.length - 1, onClick: () => moveBy(group.label, ids, item.id, 1) },
                          { label: `Reset ${group.label} order`, disabled: !navOrder[group.label], onClick: () => resetGroup(group.label) },
                        ])
                      }
                    >
                      <Icon name={navIcon(item.label)} />
                      <span className="nav-link-label">{item.label}</span>
                    </Link>
                  ))}
                </div>
              );
            })
          )}
        </nav>
        <div className="sidebar-footer">
          {user ? <SidebarAccount user={user} collapsed={collapsed} /> : null}
          <button
            className="btn btn-ghost btn-sm btn-icon sidebar-collapse"
            type="button"
            aria-expanded={!collapsed}
            aria-label={collapsed ? "Expand sidebar" : "Collapse sidebar"}
            title={collapsed ? "Expand sidebar" : "Collapse sidebar"}
            onClick={toggleSidebar}
          >
            <Icon name={collapsed ? "expand" : "collapse"} />
          </button>
        </div>
      </aside>
      <div className="shell-body">
        <header className="shell-header">
          <nav className="shell-crumbs" aria-label="Breadcrumb">
            {trail.map((crumb, i) => (
              <span key={`${crumb.href}-${crumb.label}`}>
                {i > 0 ? <span className="muted"> / </span> : null}
                {i === trail.length - 1 ? (
                  <span className="crumb" aria-current="page">
                    {crumb.label}
                  </span>
                ) : (
                  <Link href={crumb.href} className="crumb">
                    {crumb.label}
                  </Link>
                )}
              </span>
            ))}
          </nav>
          <div className="shell-tools">
            <span
              className={`health-chip ${healthOk ? "is-ok" : health ? "is-warn" : "is-bad"}`}
              title={healthOk ? "Control plane is available" : "Control plane health is unavailable"}
            >
              <Icon name={healthOk ? "success" : health ? "warning" : "error"} size={14} />
              {healthLabel}
            </span>
            <TaskIndicator />
            <button
              type="button"
              className="btn btn-ghost btn-sm"
              aria-keyshortcuts="Control+K Meta+K"
              onClick={() => setPaletteOpen(true)}
            >
              Search
            </button>
            {user ? <NotificationBell /> : null}
          </div>
        </header>
        <CommandPalette open={paletteOpen} onClose={() => setPaletteOpen(false)} />
        <ContextMenu menu={navMenu.menu} onClose={navMenu.close} />
        <main id="main" className="shell-main">
          <UpdateBanner />
          <HostDiskBanner enabled={hasGrant(user, "storage.read")} />
          {children}
        </main>
      </div>
    </div>
  );
}
