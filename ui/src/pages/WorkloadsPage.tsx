import { useEffect, useMemo, useState } from "react";
import { listWorkloads } from "../api/client";
import type { Workload } from "../api/phase5";
import { ErrorState, LoadingState } from "../components/EmptyState";
import { Link } from "../components/Link";
import { PageHeader } from "../components/PageHeader";
import { ResourceTable } from "../components/ResourceTable";
import { StatusBadge } from "../components/StatusBadge";
import { formatBytes } from "../format";
import { kindLabel, osLabel } from "../labels";
import { canMutate } from "../rbac";
import { useSession } from "../session";

export function WorkloadsPage() {
  const session = useSession();
  const roles = session.status === "ready" ? session.user?.roles : undefined;
  const mutate = canMutate(roles);
  const [items, setItems] = useState<Workload[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [query, setQuery] = useState("");

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
          setItems([]);
        }
      });
    return () => {
      cancelled = true;
    };
  }, []);

  const filtered = useMemo(() => {
    const list = items ?? [];
    const q = query.trim().toLowerCase();
    if (!q) {
      return list;
    }
    return list.filter((w) => w.name.toLowerCase().includes(q));
  }, [items, query]);

  return (
    <section className="page" aria-labelledby="workloads-heading">
      <PageHeader
        id="workloads-heading"
        title="Workloads"
        kicker="System containers on this appliance"
        actions={
          mutate ? (
            <Link className="btn btn-primary" href="/workloads/new/system-container">
              Create system container
            </Link>
          ) : null
        }
      />
      {error ? <ErrorState>{error}</ErrorState> : null}
      <article className="panel stack">
        <input
          className="field-input"
          type="search"
          placeholder="Search by name"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          aria-label="Search workloads"
        />
        {items == null ? (
          <LoadingState />
        ) : (
          <ResourceTable
            headers={["Name", "Type", "Status", "Image", "IPv4", "Memory"]}
            empty={
              <p>
                No system containers yet. Create a storage pool and guest network first if this
                appliance is new, then create a system container.
              </p>
            }
            rows={filtered.map((w) => [
              <Link key="name" href={`/workloads/${w.id}`}>
                {w.name}
              </Link>,
              kindLabel(w.kind),
              <span key="st">
                <StatusBadge status={w.status} />
                {w.status === "warning" || w.status === "failed" ? ` ${w.reason || ""}` : ""}
              </span>,
              osLabel(w.image_pin),
              w.nics?.[0]?.ipv4 || "Not reported",
              formatBytes(w.memory_bytes),
            ])}
          />
        )}
      </article>
    </section>
  );
}
