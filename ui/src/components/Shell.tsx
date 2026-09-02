import { useEffect, useState, type ReactNode } from "react";
import { getHealth } from "../api/client";
import type { HealthResponse } from "../api/types";
import { usePath } from "../router";
import { useSession } from "../session";
import { storageGet, storageSet } from "../storage";
import { CommandPalette } from "./CommandPalette";
import { Icon, navIcon } from "./Icon";
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
    label: "Compute",
    items: [
      { href: "/workloads", label: "Workloads", match: (p) => p === "/workloads" || p.startsWith("/workloads/") },
      { href: "/terminal", label: "Terminal", match: (p) => p === "/terminal" },
      { href: "/stacks", label: "Stacks", match: (p) => p === "/stacks" || p.startsWith("/stacks/") },
      { href: "/templates", label: "Templates", match: (p) => p === "/templates" },
    ],
  },
  {
    label: "Infrastructure",
    items: [
      { href: "/node", label: "Node", match: (p) => p === "/node" || p.startsWith("/node/") || p.startsWith("/nodes/") },
      { href: "/storage", label: "Storage", match: (p) => p === "/storage" || p.startsWith("/storage/") },
      { href: "/network", label: "Network", match: (p) => p === "/network" || p.startsWith("/network/") },
      { href: "/settings/cluster", label: "Cluster", match: (p) => p === "/settings/cluster" },
    ],
  },
  {
    label: "Operations",
    items: [
      { href: "/tasks", label: "Tasks", match: (p) => p === "/tasks" },
      { href: "/events", label: "Events", match: (p) => p === "/events" || p === "/node/events" },
      { href: "/alerts", label: "Alerts", match: (p) => p === "/alerts" },
      { href: "/backups", label: "Backups", match: (p) => p === "/backups" },
      { href: "/automation", label: "Automation", match: (p) => p === "/automation" },
    ],
  },
  {
    label: "Intelligence",
    items: [
      { href: "/ask", label: "Ask", match: (p) => p === "/ask" },
      { href: "/plans", label: "Plans", match: (p) => p === "/plans" },
    ],
  },
  {
    label: "Catalog",
    items: [
      { href: "/store", label: "Store", match: (p) => p === "/store" },
      { href: "/docs", label: "Docs", match: (p) => p === "/docs" },
    ],
  },
  {
    label: "Settings",
    items: [
      { href: "/settings/features", label: "Features", match: (p) => p === "/settings/features" },
      { href: "/settings/kubernetes", label: "Kubernetes", match: (p) => p === "/settings/kubernetes" },
      { href: "/settings/certificates", label: "Certificates", match: (p) => p === "/settings/certificates" },
      { href: "/settings/updates", label: "Updates", match: (p) => p === "/settings/updates" },
      { href: "/settings/mfa", label: "MFA", match: (p) => p === "/settings/mfa" },
      { href: "/groups", label: "Groups", match: (p) => p === "/groups" },
      { href: "/audit", label: "Audit", match: (p) => p === "/audit" },
      { href: "/settings/license", label: "License", match: (p) => p === "/settings/license" },
    ],
  },
];

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
  if (path.startsWith("/stacks/")) {
    return [
      { href: "/stacks", label: "Stacks" },
      { href: path, label: "Stack" },
    ];
  }
  if (path.startsWith("/nodes/") || path.startsWith("/node")) {
    const parts = path.split("/").filter(Boolean);
    const trail = [{ href: "/node", label: "Node" }];
    const tab = parts[parts.length - 1];
    if (TAB_LABELS[tab]) {
      trail.push({ href: path, label: TAB_LABELS[tab] });
    }
    return trail;
  }
  const top = GROUPS.flatMap((g) => g.items).find((item) => item.match(path));
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
    function onKey(event: KeyboardEvent) {
      if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "k") {
        event.preventDefault();
        setPaletteOpen((open) => !open);
      }
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  function toggleSidebar() {
    setCollapsed((cur) => {
      const next = !cur;
      storageSet(SIDEBAR_KEY, next ? "1" : "0");
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
            N
          </Link>
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
                  title={item.label}
                  aria-current={item.match(path) ? "page" : undefined}
                >
                  <Icon name={navIcon(item.label)} />
                  <span className="nav-link-label">{item.label}</span>
                </Link>
              ))}
            </div>
          ))}
        </nav>
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
            {user ? <AccountMenu user={user} /> : null}
          </div>
        </header>
        <CommandPalette open={paletteOpen} onClose={() => setPaletteOpen(false)} />
        <main id="main" className="shell-main">
          {children}
        </main>
      </div>
    </div>
  );
}
