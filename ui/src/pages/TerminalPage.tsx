import { useEffect, useRef, useState } from "react";
import { getNode, getWorkload } from "../api/client";
import type { Workload } from "../api/phase5";
import { Link } from "../components/Link";
import { PageHeader } from "../components/PageHeader";
import { TerminalPane } from "../components/TerminalPane";
import { workloadGuestIO } from "../guestIO";
import { currentPath } from "../router";
import { canMutate, isAdmin } from "../rbac";
import { useSession } from "../session";
import { targetFromNode, targetFromWorkload } from "../terminal/catalog";
import { useTerminalWorkspace } from "../terminal/workspace";

function idsFromPath(): { kind: "node" | "workload"; id: string } {
  const parts = currentPath().split("/").filter(Boolean);
  if (parts[0] === "nodes") {
    return { kind: "node", id: parts[1] ?? "" };
  }
  return { kind: "workload", id: parts[1] ?? "" };
}

function cwdFromQuery(): string | null {
  return new URLSearchParams(window.location.search).get("cwd");
}

export function TerminalPage() {
  const session = useSession();
  const roles = session.status === "ready" ? session.user?.roles : undefined;
  const { kind, id } = idsFromPath();
  const host = kind === "node";
  const canOpen = host ? isAdmin(roles) : canMutate(roles);
  const cwdParam = cwdFromQuery();
  const { openOrFocus, tabs, setActive } = useTerminalWorkspace();
  const [error, setError] = useState<string | null>(null);
  const [unsupported, setUnsupported] = useState<string | null>(null);
  const [ready, setReady] = useState(kind === "node");
  const loaded = useRef<Workload | null>(null);
  const existing = [...tabs]
    .reverse()
    .find(
      (t) =>
        t.target.kind === kind &&
        t.target.id === id &&
        (t.state === "active" || t.state === "connecting" || t.state === "reconnecting"),
    );
  const existingId = existing?.tabId;

  // A live session for this target is already open: show it now instead of
  // waiting for the guest checks below.
  useEffect(() => {
    if (canOpen && !cwdParam && existingId) {
      setActive(existingId);
    }
    // Only when the page's target changes; later tab updates must not steal focus.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [kind, id]);

  useEffect(() => {
    if (kind !== "workload") {
      setReady(true);
      setUnsupported(null);
      return;
    }
    let cancelled = false;
    async function check() {
      try {
        const { workload, reason } = await workloadGuestIO(id);
        if (cancelled) {
          return;
        }
        loaded.current = workload;
        if (reason) {
          setUnsupported(reason);
          setReady(false);
          return;
        }
        setUnsupported(null);
        setReady(true);
      } catch (err) {
        if (!cancelled) {
          setError(err instanceof Error ? err.message : "Unavailable");
        }
      }
    }
    void check();
    const timer = window.setInterval(() => {
      void check();
    }, 4000);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [kind, id]);

  useEffect(() => {
    if (!canOpen || !ready || unsupported) {
      return;
    }
    let cancelled = false;
    void (async () => {
      try {
        const target =
          kind === "node"
            ? targetFromNode(await getNode(id))
            : targetFromWorkload(loaded.current?.id === id ? loaded.current : await getWorkload(id));
        if (cancelled) {
          return;
        }
        if (cwdParam) {
          openOrFocus(target, { cwd: cwdParam, forceNew: true });
        } else {
          openOrFocus(target);
        }
      } catch (err) {
        if (!cancelled) {
          setError(err instanceof Error ? err.message : "Could not open terminal");
        }
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [canOpen, ready, unsupported, kind, id, cwdParam, openOrFocus]);

  if (unsupported) {
    return (
      <section className="page" aria-labelledby="term-heading">
        <PageHeader id="term-heading" title="Terminal" />
        <p className="banner banner-warn" role="status">
          {unsupported}
        </p>
        <Link href={`/workloads/${id}`}>Back to workload</Link>
      </section>
    );
  }

  if (!canOpen) {
    return (
      <section className="page" aria-labelledby="term-heading">
        <PageHeader id="term-heading" title="Terminal" />
        <p className="banner banner-error" role="alert">
          {host ? "Host terminal requires admin." : "Terminal requires operator or admin."}
        </p>
      </section>
    );
  }

  return (
    <section className="page page-wide page-term" aria-labelledby="term-heading">
      <PageHeader id="term-heading" title="Terminal" />
      {error ? (
        <p className="banner banner-error" role="alert">
          {error}
        </p>
      ) : null}
      {host ? (
        <nav className="subnav" aria-label="IO">
          <Link href={`/nodes/${id}`}>Summary</Link>
          <Link href={`/nodes/${id}/terminal`} aria-current="page">
            Terminal
          </Link>
          <Link href={`/nodes/${id}/files`}>Files</Link>
          <Link href="/terminal">Open in Terminal workspace</Link>
        </nav>
      ) : (
        <p className="page-kicker">
          <Link href="/terminal">Open in Terminal workspace</Link>
        </p>
      )}
      <TerminalPane workspaceLink target={{ kind, id, name: existing?.target.name }} />
    </section>
  );
}
