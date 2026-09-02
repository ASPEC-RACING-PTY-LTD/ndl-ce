import { useEffect, useState, type ReactNode } from "react";
import { getHealth } from "../api/client";
import type { HealthResponse } from "../api/types";
import { navigate, usePath } from "../router";
import { useSession } from "../session";
import { CommandPalette } from "./CommandPalette";
import { Link } from "./Link";

type ShellProps = {
  children: ReactNode;
};

export function Shell({ children }: ShellProps) {
  const path = usePath();
  const session = useSession();
  const user = session.status === "ready" ? session.user : null;
  const [health, setHealth] = useState<HealthResponse | null>(null);
  const [paletteOpen, setPaletteOpen] = useState(false);

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

  async function onLogout() {
    await session.signOut();
    navigate("/login", { replace: true });
  }

  return (
    <div className="shell">
      <a className="skip-link" href="#main">
        Skip to content
      </a>
      <header className="shell-header">
        <div className="shell-brand">
          <Link href="/" className="wordmark">
            No-dal
          </Link>
          <span className="edition-badge">CE</span>
        </div>
        <nav className="shell-nav" aria-label="Appliance">
          <Link href="/" aria-current={path === "/" ? "page" : undefined}>
            Dashboard
          </Link>
          <Link href="/storage" aria-current={path === "/storage" ? "page" : undefined}>
            Storage
          </Link>
          <Link href="/network" aria-current={path === "/network" ? "page" : undefined}>
            Network
          </Link>
          <Link href="/workloads" aria-current={path === "/workloads" || path.startsWith("/workloads/") ? "page" : undefined}>
            Workloads
          </Link>
          <Link href="/stacks" aria-current={path === "/stacks" || path.startsWith("/stacks/") ? "page" : undefined}>
            Stacks
          </Link>
          <Link href="/templates" aria-current={path === "/templates" ? "page" : undefined}>
            Templates
          </Link>
          <Link href="/node" aria-current={path === "/node" || path.startsWith("/node/") ? "page" : undefined}>
            Node
          </Link>
          <Link href="/settings/cluster" aria-current={path === "/settings/cluster" ? "page" : undefined}>
            Cluster
          </Link>
          <Link href="/settings/features" aria-current={path === "/settings/features" ? "page" : undefined}>
            Features
          </Link>
          <Link href="/settings/kubernetes" aria-current={path === "/settings/kubernetes" ? "page" : undefined}>
            Kubernetes
          </Link>
          <Link href="/store" aria-current={path === "/store" ? "page" : undefined}>
            Store
          </Link>
          <Link href="/tasks" aria-current={path === "/tasks" ? "page" : undefined}>
            Tasks
          </Link>
          <Link href="/events" aria-current={path === "/events" ? "page" : undefined}>
            Events
          </Link>
          <Link href="/alerts" aria-current={path === "/alerts" ? "page" : undefined}>
            Alerts
          </Link>
          <Link href="/me" aria-current={path === "/me" ? "page" : undefined}>
            Account
          </Link>
          <Link
            href="/settings/certificates"
            aria-current={path === "/settings/certificates" ? "page" : undefined}
          >
            Certificates
          </Link>
          <Link
            href="/settings/updates"
            aria-current={path === "/settings/updates" ? "page" : undefined}
          >
            Updates
          </Link>
          <Link href="/settings/mfa" aria-current={path === "/settings/mfa" ? "page" : undefined}>
            MFA
          </Link>
          <Link href="/groups" aria-current={path === "/groups" ? "page" : undefined}>
            Groups
          </Link>
          <Link href="/audit" aria-current={path === "/audit" ? "page" : undefined}>
            Audit
          </Link>
          <Link href="/backups" aria-current={path === "/backups" ? "page" : undefined}>
            Backups
          </Link>
        </nav>
        <div className="shell-session">
          <button
            type="button"
            className="btn btn-ghost"
            aria-keyshortcuts="Control+K Meta+K"
            onClick={() => setPaletteOpen(true)}
          >
            Search
          </button>
          {user ? (
            <p className="shell-user">
              Signed in as <strong>{user.username}</strong>
            </p>
          ) : null}
          <button type="button" className="btn btn-ghost" onClick={() => void onLogout()}>
            Log out
          </button>
        </div>
      </header>
      <CommandPalette open={paletteOpen} onClose={() => setPaletteOpen(false)} />
      <main id="main" className="shell-main">
        {children}
      </main>
      <footer className="shell-footer">
        <p>Community Edition. License activation is not required.</p>
        <p className="shell-health" aria-live="polite">
          {health
            ? `${health.service} is ${health.status}.`
            : "Control plane health is unavailable."}
        </p>
      </footer>
    </div>
  );
}
