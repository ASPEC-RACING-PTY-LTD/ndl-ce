import { useEffect, useState, type ReactNode } from "react";
import { getHealth } from "../api/client";
import type { HealthResponse } from "../api/types";
import { useNavDisclosure, useVisibleNav } from "../nav/NavDisclosure";
import type { NavModule } from "../nav/modules";
import { ADMIN_SECTIONS, isAdminPath, isInstancePath, visibleSections } from "../nav/sections";
import { useNavLayout } from "../appearance/state";
import { selectedTargetFromPath, viewFromPath } from "../nav/match";
import { saveLastView } from "../nav/prefs";
import { navigate, usePath } from "../router";
import { useSession } from "../session";
import { hasGrant } from "../rbac";
import { HostDiskBanner } from "./HostDisk";
import { storageGet, storageSet } from "../storage";
import { BrandMark } from "./BrandMark";
import { Icon, navIcon } from "./Icon";
import { Link } from "./Link";
import { SidebarAccount } from "./shell/SidebarAccount";
import { ContextMenu, useContextMenu } from "./ContextMenu";
import { usePref } from "../prefs";
import { NAV_ORDER_KEY, applyNavOrder, moveNavItem, type NavOrder } from "../nav/disclosure";
import { NotificationBell } from "./shell/NotificationBell";
import { GlobalSearch } from "./shell/GlobalSearch";
import { UpdateBanner } from "./shell/UpdateBanner";

const SIDEBAR_KEY = "ndl-sidebar-collapsed";

type NavGroup = { label: string; items: NavModule[] };

/**
 * modernNav is the short sidebar of the modern layout: Dashboard, the
 * Compute pages and one Administration entry. Everything else is reached
 * from each area's page bar.
 */
export function modernNav(showAdmin: boolean): NavGroup[] {
  const groups: NavGroup[] = [
    { label: "Overview", items: [{ id: "dashboard", href: "/", label: "Dashboard", group: "Overview", match: (p) => p === "/" }] },
    {
      label: "Compute",
      items: [
        { id: "instance", href: "/instance", label: "Instance", group: "Compute", match: isInstancePath },
        { id: "terminal", href: "/terminal", label: "Terminal", group: "Compute", match: (p) => p === "/terminal" },
        { id: "files", href: "/files", label: "Files", group: "Compute", match: (p) => p === "/files" },
      ],
    },
  ];
  if (showAdmin) {
    groups.push({
      label: "Administration",
      items: [{ id: "admin", href: "/admin", label: "Administration", group: "Administration", match: isAdminPath }],
    });
  }
  return groups;
}

export function Shell({ children }: { children: ReactNode }) {
  const path = usePath();
  const session = useSession();
  const user = session.status === "ready" ? session.user : null;
  const [health, setHealth] = useState<HealthResponse | null>(null);
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
  const [layout] = useNavLayout();
  const { capabilityOn } = useNavDisclosure();
  const classic = useVisibleNav();
  const showAdmin = visibleSections(ADMIN_SECTIONS, user, capabilityOn).length > 0;
  const groups = applyNavOrder(layout === "classic" ? classic : modernNav(showAdmin), navOrder);
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
        </div>
      </aside>
      <div className="shell-body">
        <header className="shell-header">
          <span
            className={`health-chip ${healthOk ? "is-ok" : health ? "is-warn" : "is-bad"}`}
            title={healthOk ? "Control plane is available" : "Control plane health is unavailable"}
          >
            <Icon name={healthOk ? "success" : health ? "warning" : "error"} size={14} />
            {healthLabel}
          </span>
          <GlobalSearch />
          <div className="shell-tools">{user ? <NotificationBell /> : null}</div>
        </header>
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
