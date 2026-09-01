import { useEffect, useState } from "react";
import { listWorkloads } from "../api/client";
import type { Workload } from "../api/phase5";
import { Link } from "../components/Link";
import { honestStatus } from "../format";
import { useSession } from "../session";

function canMutate(roles: string[] | undefined): boolean {
  return Boolean(roles?.includes("admin") || roles?.includes("operator"));
}

export function WorkloadsPage() {
  const session = useSession();
  const roles = session.status === "ready" ? session.user?.roles : undefined;
  const mutate = canMutate(roles);
  const [items, setItems] = useState<Workload[]>([]);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    void listWorkloads()
      .then((listed) => {
        if (!cancelled) {
          setItems(listed.items ?? []);
        }
      })
      .catch((err) => {
        if (!cancelled) {
          setError(err instanceof Error ? err.message : "Unavailable");
        }
      });
    return () => {
      cancelled = true;
    };
  }, []);

  return (
    <section className="page page-wide" aria-labelledby="workloads-heading">
      <header className="page-header">
        <h1 id="workloads-heading">Workloads</h1>
        <p className="page-kicker">System containers. VMs are a later phase.</p>
      </header>
      {error ? (
        <p className="banner banner-error" role="alert">
          {error}
        </p>
      ) : null}
      {mutate ? (
        <p>
          <Link href="/workloads/new/system-container">Create system container</Link>
        </p>
      ) : null}
      <article className="panel">
        <h2>System containers</h2>
        {items.length === 0 ? (
          <p>No system containers yet.</p>
        ) : (
          <ul className="plain-list">
            {items.map((w) => (
              <li key={w.id}>
                <Link href={`/workloads/${w.id}`}>{w.name}</Link> {w.kind} {honestStatus(w.status)}
                {w.image_pin ? ` ${w.image_pin}` : ""}
                {w.nics?.[0]?.ipv4 ? ` ${w.nics[0].ipv4}` : ""}
              </li>
            ))}
          </ul>
        )}
      </article>
    </section>
  );
}
