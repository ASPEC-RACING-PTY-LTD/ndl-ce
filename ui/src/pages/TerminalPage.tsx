import { useEffect, useState } from "react";
import { getNode, getWorkload } from "../api/client";
import { Link } from "../components/Link";
import { PageHeader } from "../components/PageHeader";
import { TerminalPane } from "../components/TerminalPane";
import { workloadGuestIOReason } from "../guestIO";
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
  const { openOrFocus } = useTerminalWorkspace();
  const [error, setError] = useState<string | null>(null);
  const [unsupported, setUnsupported] = useState<string | null>(null);
  const [ready, setReady] = useState(kind === "node");

  useEffect(() => {
    if (kind !== "workload") {
      setReady(true);
      setUnsupported(null);
      return;
    }
    let cancelled = false;
    async function check() {
      try {
        const reason = await workloadGuestIOReason(id);
        if (cancelled) {
          return;
        }
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
            : targetFromWorkload(await getWorkload(id));
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
      <nav className="subnav" aria-label="IO">
        <Link href={host ? `/nodes/${id}` : `/workloads/${id}`}>Summary</Link>
        <Link href={host ? `/nodes/${id}/terminal` : `/workloads/${id}/terminal`} aria-current="page">
          Terminal
        </Link>
        <Link href={host ? `/nodes/${id}/files` : `/workloads/${id}/files`}>Files</Link>
        <Link href="/terminal">Open in Terminal workspace</Link>
      </nav>
      <TerminalPane workspaceLink />
    </section>
  );
}
