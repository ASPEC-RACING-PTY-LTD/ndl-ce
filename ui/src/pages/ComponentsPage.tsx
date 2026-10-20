import { useEffect, useMemo, useState } from "react";
import { bulkInstallComponents, listWorkloadExtras, listWorkloads, type WorkloadExtra } from "../api/client";
import type { BulkComponentsResult } from "../generated/openapi";
import { LoadingState } from "../components/EmptyState";
import { ErrorNotice } from "../components/ErrorNotice";
import { Link } from "../components/Link";
import { PageHeader } from "../components/PageHeader";
import { SelectionCard } from "../ui/SelectionCard";
import { StatusBadge } from "../components/StatusBadge";
import { useQuery } from "../query";
import { canMutate } from "../rbac";
import { useSession } from "../session";
import { groupExtras, toggleExtra } from "../workloadExtras";

function idsFromQuery(): string[] {
  const raw = new URLSearchParams(window.location.search).get("ids") ?? "";
  return raw.split(",").filter(Boolean);
}

/**
 * ComponentsPage installs components (optional extras) on many system
 * containers at once. Each container installs in the background; its own
 * Components page shows the progress.
 */
export function ComponentsPage() {
  const session = useSession();
  const mutate = canMutate(session.status === "ready" ? session.user?.roles : undefined);
  const workloadsQ = useQuery("workloads", () => listWorkloads(), 15000);
  const containers = useMemo(() => (workloadsQ.data?.items ?? []).filter((w) => w.kind === "system-container"), [workloadsQ.data]);
  const [catalog, setCatalog] = useState<WorkloadExtra[] | null>(null);
  const [targets, setTargets] = useState<string[]>(idsFromQuery);
  const [extras, setExtras] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [result, setResult] = useState<BulkComponentsResult | null>(null);

  useEffect(() => {
    listWorkloadExtras()
      .then((body) => setCatalog(body.items ?? []))
      .catch((err: unknown) => {
        setCatalog([]);
        setError(err instanceof Error ? err.message : "Could not read components");
      });
  }, []);

  const groups = useMemo(() => groupExtras(catalog ?? []), [catalog]);
  const allOn = containers.length > 0 && targets.length === containers.length;

  async function install() {
    setBusy(true);
    setError(null);
    setResult(null);
    try {
      setResult(await bulkInstallComponents({ workload_ids: targets, extras }));
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not start");
    } finally {
      setBusy(false);
    }
  }

  const nameOf = (id: string) => containers.find((w) => w.id === id)?.name ?? id;
  return (
    <section className="page page-wide" aria-labelledby="components-heading">
      <PageHeader
        id="components-heading"
        title="Components"
        kicker="Install software on many system containers at once. Basic tools (curl, wget, jq and a few more) are already on every new container."
      />
      {error ? <ErrorNotice error={error} /> : null}
      {result ? (
        <div className="banner banner-ok" role="status">
          <p>
            Installing {result.extras.join(", ")} on {result.started.length} container{result.started.length === 1 ? "" : "s"} in the background.
          </p>
          {result.skipped.length ? (
            <ul>
              {result.skipped.map((s) => (
                <li key={s.id}>
                  {s.name || s.id}: {s.reason}
                </li>
              ))}
            </ul>
          ) : null}
          {result.started.length ? (
            <p className="meta">
              Progress:{" "}
              {result.started.map((id, i) => (
                <span key={id}>
                  {i > 0 ? ", " : ""}
                  <Link href={`/workloads/${id}/extensions`}>{nameOf(id)}</Link>
                </span>
              ))}
            </p>
          ) : null}
        </div>
      ) : null}
      <div className="components-layout">
        <div className="panel admin-box">
          <div className="components-head">
            <h2>Containers</h2>
            <button className="btn btn-ghost btn-sm" type="button" onClick={() => setTargets(allOn ? [] : containers.map((w) => w.id))}>
              {allOn ? "Clear" : "Select all"}
            </button>
          </div>
          {workloadsQ.loading && !workloadsQ.data ? <LoadingState label="Loading containers" /> : null}
          {containers.length === 0 && workloadsQ.data ? <p className="muted">No system containers yet.</p> : null}
          <ul className="components-targets">
            {containers.map((w) => (
              <li key={w.id}>
                <label className="check-row">
                  <input
                    type="checkbox"
                    checked={targets.includes(w.id)}
                    onChange={(e) => setTargets((cur) => (e.target.checked ? [...cur, w.id] : cur.filter((x) => x !== w.id)))}
                  />
                  <span>{w.name}</span>
                  <StatusBadge status={w.status} />
                </label>
              </li>
            ))}
          </ul>
        </div>
        <div className="stack">
          {catalog === null ? <LoadingState label="Loading components" /> : null}
          {groups.map((g) => (
            <section key={g.category} className="stack">
              <h2>{g.label}</h2>
              <div className="extra-tile-grid">
                {g.items.map((item) => (
                  <SelectionCard
                    key={item.id}
                    title={item.name}
                    description={item.description}
                    selected={extras.includes(item.id)}
                    onSelect={() => setExtras((cur) => toggleExtra(cur, item.id, catalog ?? []))}
                  />
                ))}
              </div>
            </section>
          ))}
        </div>
      </div>
      {mutate ? (
        <div className="sticky-actions">
          <span className="meta">
            {extras.length} component{extras.length === 1 ? "" : "s"} on {targets.length} container{targets.length === 1 ? "" : "s"}
          </span>
          <button className="btn btn-primary" type="button" disabled={busy || targets.length === 0 || extras.length === 0} onClick={() => void install()}>
            Install
          </button>
        </div>
      ) : null}
    </section>
  );
}
