import { useEffect, useState } from "react";
import {
  ApiError,
  createWorkloadSnapshot,
  flattenWorkloadSnapshots,
  getWorkload,
  listWorkloadSnapshots,
  rollbackSnapshot,
} from "../api/client";
import type { Snapshot, SnapshotCapability, SnapshotListResponse } from "../generated/openapi";
import type { Workload } from "../api/phase5";
import { Field } from "../components/Field";
import { Link } from "../components/Link";
import { formatWhen, honestStatus } from "../format";
import { currentPath } from "../router";
import { useSession } from "../session";

function workloadIDFromPath(): string {
  const parts = currentPath().split("/").filter(Boolean);
  return parts[0] === "workloads" ? (parts[1] ?? "") : "";
}

function canMutate(roles: string[] | undefined): boolean {
  return Boolean(roles?.includes("admin") || roles?.includes("operator"));
}

function capabilityBanner(capability: SnapshotCapability | null): string | null {
  if (!capability) {
    return null;
  }
  if (capability.supported) {
    return null;
  }
  const reason = capability.reason?.trim();
  if (reason) {
    return reason;
  }
  return "Unsupported";
}

export function SnapshotsPage() {
  const session = useSession();
  const roles = session.status === "ready" ? session.user?.roles : undefined;
  const mutate = canMutate(roles);
  const id = workloadIDFromPath();

  const [workload, setWorkload] = useState<Workload | null>(null);
  const [items, setItems] = useState<Snapshot[] | null>(null);
  const [capability, setCapability] = useState<SnapshotCapability | null>(null);
  const [name, setName] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [loadState, setLoadState] = useState<"collecting" | "ready" | "unavailable">("collecting");
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    let cancelled = false;
    setLoadState("collecting");
    setError(null);
    void (async () => {
      try {
        const [w, listed] = await Promise.all([getWorkload(id), listWorkloadSnapshots(id)]);
        if (cancelled) {
          return;
        }
        setWorkload(w);
        applyList(listed);
        setLoadState("ready");
      } catch (err) {
        if (!cancelled) {
          setLoadState("unavailable");
          setError(err instanceof Error ? err.message : "Unavailable");
        }
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [id]);

  async function reload() {
    const [w, listed] = await Promise.all([getWorkload(id), listWorkloadSnapshots(id)]);
    setWorkload(w);
    applyList(listed);
    setLoadState("ready");
  }

  function applyList(listed: SnapshotListResponse) {
    setItems(listed.items ?? []);
    setCapability(listed.capability ?? null);
  }

  async function onCreate() {
    const trimmed = name.trim();
    if (!trimmed) {
      setError("Name is required");
      return;
    }
    setBusy(true);
    setError(null);
    try {
      await createWorkloadSnapshot(id, { name: trimmed });
      setName("");
      await reload();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : err instanceof Error ? err.message : "Create failed");
    } finally {
      setBusy(false);
    }
  }

  async function onRollback(snap: Snapshot) {
    if (!window.confirm(`Roll back to snapshot "${snap.name}"? This restores the volume to that point in time.`)) {
      return;
    }
    setBusy(true);
    setError(null);
    try {
      await rollbackSnapshot(snap.id);
      await reload();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : err instanceof Error ? err.message : "Rollback failed");
    } finally {
      setBusy(false);
    }
  }

  async function onFlatten() {
    if (!window.confirm("Flatten the snapshot chain? This consolidates overlays on the same pool.")) {
      return;
    }
    setBusy(true);
    setError(null);
    try {
      const listed = await flattenWorkloadSnapshots(id);
      applyList(listed);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : err instanceof Error ? err.message : "Flatten failed");
    } finally {
      setBusy(false);
    }
  }

  const unsupportedReason = capabilityBanner(capability);
  const supported = Boolean(capability?.supported);

  return (
    <section className="page page-wide" aria-labelledby="snapshots-heading">
      <header className="page-header">
        <h1 id="snapshots-heading">Snapshots</h1>
        <p className="page-kicker">Point-in-time restore on the same pool. This is not a backup.</p>
      </header>

      <nav className="subnav" aria-label="Workload">
        <Link href={`/workloads/${id}`}>Summary</Link>
        <Link href={`/workloads/${id}/terminal`}>Terminal</Link>
        <Link href={`/workloads/${id}/files`}>Files</Link>
        <Link href={`/workloads/${id}/snapshots`} aria-current="page">
          Snapshots
        </Link>
        {workload ? <span className="muted">{workload.name}</span> : null}
      </nav>

      {error ? (
        <p className="banner banner-error" role="alert">
          {error}
        </p>
      ) : null}

      {loadState === "collecting" ? (
        <p className="banner" role="status">
          Collecting
        </p>
      ) : null}

      {loadState === "unavailable" && !error ? (
        <p className="banner banner-error" role="alert">
          Unavailable
        </p>
      ) : null}

      {loadState === "ready" && unsupportedReason ? (
        <p className="banner banner-warn" role="status">
          Unsupported. {unsupportedReason}
        </p>
      ) : null}

      {loadState === "ready" && capability ? (
        <article className="panel">
          <h2>Capability</h2>
          <dl className="definition-list">
            <div>
              <dt>Supported</dt>
              <dd>{capability.supported ? "yes" : "no"}</dd>
            </div>
            <div>
              <dt>Mechanism</dt>
              <dd>{capability.mechanism || "None"}</dd>
            </div>
            <div>
              <dt>Chain depth</dt>
              <dd>
                {capability.chain_depth} / {capability.chain_max}
              </dd>
            </div>
            <div>
              <dt>Reason</dt>
              <dd>{capability.reason || "None"}</dd>
            </div>
          </dl>
        </article>
      ) : null}

      {loadState === "ready" && supported && mutate ? (
        <article className="panel">
          <h2>Create</h2>
          <Field
            id="snap-name"
            label="Name"
            value={name}
            onChange={(e) => setName(e.target.value)}
            autoComplete="off"
          />
          <div className="btn-row">
            <button className="btn btn-primary" type="button" disabled={busy} onClick={() => void onCreate()}>
              Create snapshot
            </button>
            {capability?.mechanism === "qcow2-overlay" ? (
              <button className="btn" type="button" disabled={busy} onClick={() => void onFlatten()}>
                Flatten chain
              </button>
            ) : null}
          </div>
        </article>
      ) : null}

      {loadState === "ready" ? (
        <article className="panel">
          <h2>Snapshots</h2>
          {items == null ? (
            <p>Collecting</p>
          ) : items.length === 0 ? (
            <p>{supported ? "No snapshots yet." : "No snapshots."}</p>
          ) : (
            <div className="table-wrap">
              <table>
                <thead>
                  <tr>
                    <th>Name</th>
                    <th>Status</th>
                    <th>Mechanism</th>
                    <th>Depth</th>
                    <th>Created</th>
                    <th>Actions</th>
                  </tr>
                </thead>
                <tbody>
                  {items.map((snap) => (
                    <tr key={snap.id}>
                      <td>{snap.name}</td>
                      <td>{honestStatus(snap.status)}</td>
                      <td>{snap.mechanism || "Not reported"}</td>
                      <td>{snap.chain_depth}</td>
                      <td>{formatWhen(snap.created_at)}</td>
                      <td>
                        {mutate && supported ? (
                          <button className="btn" type="button" disabled={busy} onClick={() => void onRollback(snap)}>
                            Rollback
                          </button>
                        ) : (
                          <span className="muted">None</span>
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </article>
      ) : null}
    </section>
  );
}
