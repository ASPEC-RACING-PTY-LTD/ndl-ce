import { useEffect, useState, type ReactNode } from "react";
import { getHealth } from "../api/client";
import type { HealthResponse } from "../api/types";
import { navigate, usePath } from "../router";
import { useSession } from "../session";
import { Link } from "./Link";

type ShellProps = {
  children: ReactNode;
};

export function Shell({ children }: ShellProps) {
  const path = usePath();
  const session = useSession();
  const user = session.status === "ready" ? session.user : null;
  const [health, setHealth] = useState<HealthResponse | null>(null);

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
          <Link href="/node" aria-current={path === "/node" || path.startsWith("/node/") ? "page" : undefined}>
            Node
          </Link>
          <Link href="/tasks" aria-current={path === "/tasks" ? "page" : undefined}>
            Tasks
          </Link>
          <Link href="/events" aria-current={path === "/events" ? "page" : undefined}>
            Events
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
          <Link href="/backups" aria-current={path === "/backups" ? "page" : undefined}>
            Backups
          </Link>
        </nav>
        <div className="shell-session">
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
      <main id="main" className="shell-main">
        {children}
      </main>
      <footer className="shell-footer">
        <p>Community Edition. License activation is not required.</p>
        <p className="shell-health">
          {health
            ? `${health.service} is ${health.status}.`
            : "Control plane health is unavailable."}
        </p>
      </footer>
    </div>
  );
}
