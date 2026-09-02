import { useEffect, useMemo, useState } from "react";
import { listWorkloads } from "../api/client";
import type { Workload } from "../api/phase5";
import { EmptyState, ErrorState, LoadingState } from "../components/EmptyState";
import { Icon } from "../components/Icon";
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
              <Icon name="create" size={14} />
              Create system container
            </Link>
          ) : null
        }
      />
      {error ? <ErrorState>{error}</ErrorState> : null}
      <div className="stack">
        <div className="toolbar">
          <label className="search-field">
            <Icon name="search" size={14} />
            <input
              className="field-input"
              type="search"
              placeholder="Search by name"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              aria-label="Search workloads"
            />
          </label>
        </div>
        {items == null ? (
          <LoadingState />
        ) : (
          <ResourceTable
            headers={["Name", "Type", "Status", "Image", "IPv4", "Memory"]}
            numeric={[5]}
            empty={
              <EmptyState title="No system containers yet">
                Create a storage pool and guest network first if this appliance is new, then create a
                system container.
              </EmptyState>
            }
            rows={filtered.map((w) => [
              <Link key="name" href={`/workloads/${w.id}`}>
                {w.name}
              </Link>,
              <span key="kind" className="type-cell">
                <Icon name="workloads" size={14} />
                {kindLabel(w.kind)}
              </span>,
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
      </div>
    </section>
  );
}
