import { useEffect, useState, type ReactNode } from "react";
import { getHealth } from "../api/client";
import type { HealthResponse } from "../api/types";
import { usePath } from "../router";
import { useSession } from "../session";
import { storageGet, storageSet } from "../storage";
import { Link } from "./Link";
import { AccountMenu } from "./shell/AccountMenu";
import { TaskIndicator } from "./shell/TaskIndicator";

const SIDEBAR_KEY = "ndl-sidebar-collapsed";

type NavItem = { href: string; label: string; match: (path: string) => boolean };

const GROUPS: { label: string; items: NavItem[] }[] = [
  {
    label: "Overview",
    items: [{ href: "/", label: "Dashboard", match: (p) => p === "/" }],
  },
  {
    label: "Infrastructure",
    items: [
      { href: "/workloads", label: "Workloads", match: (p) => p === "/workloads" || p.startsWith("/workloads/") },
      { href: "/node", label: "Node", match: (p) => p === "/node" || p.startsWith("/node/") || p.startsWith("/nodes/") },
      { href: "/storage", label: "Storage", match: (p) => p === "/storage" || p.startsWith("/storage/") },
      { href: "/network", label: "Network", match: (p) => p === "/network" || p.startsWith("/network/") },
    ],
  },
  {
    label: "Operations",
    items: [
      { href: "/tasks", label: "Tasks", match: (p) => p === "/tasks" },
      { href: "/events", label: "Events", match: (p) => p === "/events" },
    ],
  },
];

function crumbs(path: string): { href: string; label: string }[] {
  if (path === "/") {
    return [{ href: "/", label: "Dashboard" }];
  }
  if (path.startsWith("/workloads/new")) {
    return [
      { href: "/workloads", label: "Workloads" },
      { href: path, label: "Create system container" },
    ];
  }
  if (path.startsWith("/workloads/")) {
    const parts = path.split("/").filter(Boolean);
    const base = `/workloads/${parts[1]}`;
    const trail = [{ href: "/workloads", label: "Workloads" }, { href: base, label: parts[1] }];
    if (parts[2]) {
      trail.push({ href: path, label: parts[2][0].toUpperCase() + parts[2].slice(1) });
    }
    return trail;
  }
  if (path.startsWith("/nodes/") || path.startsWith("/node")) {
    return [{ href: "/node", label: "Node" }];
  }
  const top = GROUPS.flatMap((g) => g.items).find((item) => item.match(path));
  return top ? [{ href: top.href, label: top.label }] : [{ href: path, label: "Not found" }];
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
    void getHealth()
      .then((value) => {
        if (!cancelled) {
          setHealth(value);
        }
      })
      .catch(() => {
        if (!cancelled) {
          setHealth(null);
        }
      });
    return () => {
      cancelled = true;
    };
  }, []);

  function toggleSidebar() {
    setCollapsed((cur) => {
      const next = !cur;
      storageSet(SIDEBAR_KEY, next ? "1" : "0");
      return next;
    });
  }

  const healthOk = health?.status === "ok";

  return (
    <div className={collapsed ? "shell is-collapsed" : "shell"}>
      <a className="skip-link" href="#main">
        Skip to content
      </a>
      <aside className="sidebar">
        <div className="sidebar-brand">
          <Link href="/" className="wordmark">
            No-dal
          </Link>
          <span className="edition-badge">CE</span>
        </div>
        <nav className="sidebar-nav" aria-label="Appliance">
          {GROUPS.map((group) => (
            <div className="nav-group" key={group.label}>
              <p className="nav-group-label">{group.label}</p>
              {group.items.map((item) => (
                <Link
                  key={item.href}
                  href={item.href}
                  className="nav-link"
                  aria-label={item.label}
                  aria-current={item.match(path) ? "page" : undefined}
                >
                  <span className="nav-link-label">{item.label}</span>
                </Link>
              ))}
            </div>
          ))}
        </nav>
        <button
          className="btn btn-ghost btn-sm sidebar-collapse"
          type="button"
          aria-expanded={!collapsed}
          aria-label={collapsed ? "Expand sidebar" : "Collapse sidebar"}
          onClick={toggleSidebar}
        >
          {collapsed ? ">>" : "<<"}
        </button>
      </aside>
      <div className="shell-body">
        <header className="shell-header">
          <nav className="shell-crumbs" aria-label="Breadcrumb">
            {crumbs(path).map((crumb, i) => (
              <span key={crumb.href}>
                {i > 0 ? <span className="muted"> / </span> : null}
                <Link href={crumb.href} className="crumb">
                  {crumb.label}
                </Link>
              </span>
            ))}
          </nav>
          <div className="shell-tools">
            <span
              className={`health-dot ${healthOk ? "is-ok" : health ? "is-warn" : "is-bad"}`}
              title={healthOk ? "Control plane is available" : "Control plane health is unavailable"}
            >
              {healthOk ? "Healthy" : "Control plane"}
            </span>
            <TaskIndicator />
            {user ? <AccountMenu user={user} /> : null}
          </div>
        </header>
        <main id="main" className="shell-main">
          {children}
        </main>
      </div>
    </div>
  );
}
