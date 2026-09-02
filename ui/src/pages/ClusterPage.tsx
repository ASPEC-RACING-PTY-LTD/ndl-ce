import { useEffect, useState } from "react";
import {
  ApiError,
  createJoinToken,
  fenceClusterHA,
  getCluster,
  getClusterHA,
  getClusterUpdate,
  promoteClusterHA,
  revokeClusterNode,
  runClusterUpdate,
  type ClusterInventory,
} from "../api/client";
import type { HAStatus, RollingUpdatePreview } from "../generated/openapi";
import { useSession } from "../session";

function canJoin(roles: string[] | undefined): boolean {
  return Boolean(roles?.includes("admin") || roles?.includes("operator"));
}

function canPromote(roles: string[] | undefined): boolean {
  return Boolean(roles?.includes("admin"));
}

export function ClusterPage() {
  const session = useSession();
  const roles = session.status === "ready" ? session.user?.roles : undefined;
  const mutate = canJoin(roles);
  const promote = canPromote(roles);
  const [inv, setInv] = useState<ClusterInventory | null>(null);
  const [ha, setHa] = useState<HAStatus | null>(null);
  const [rolling, setRolling] = useState<RollingUpdatePreview | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [tokenOnce, setTokenOnce] = useState<string | null>(null);
  const [expires, setExpires] = useState<string | null>(null);

  async function reload() {
    const [next, status, plan] = await Promise.all([getCluster(), getClusterHA(), getClusterUpdate()]);
    setInv(next);
    setHa(status);
    setRolling(plan);
  }

  useEffect(() => {
    let cancelled = false;
    void reload()
      .catch((err) => {
        if (!cancelled) {
          setError(err instanceof Error ? err.message : "Unavailable");
        }
      });
    return () => {
      cancelled = true;
    };
  }, []);

  async function onCreateToken() {
    setBusy(true);
    setError(null);
    try {
      const created = await createJoinToken();
      setTokenOnce(created.token);
      setExpires(created.expires_at);
      await reload();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : err instanceof Error ? err.message : "Unavailable");
    } finally {
      setBusy(false);
    }
  }

  async function onRevoke(id: string) {
    setBusy(true);
    setError(null);
    try {
      await revokeClusterNode(id);
      await reload();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : err instanceof Error ? err.message : "Unavailable");
    } finally {
      setBusy(false);
    }
  }

  async function onFence() {
    if (!window.confirm("Fence records that the old writer is isolated. STONITH is not implemented.")) {
      return;
    }
    setBusy(true);
    setError(null);
    try {
      await fenceClusterHA();
      await reload();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : err instanceof Error ? err.message : "Fence failed");
    } finally {
      setBusy(false);
    }
  }

  async function onPromote() {
    if (!window.confirm("Promote this process to the single writer after fence or lease expiry?")) {
      return;
    }
    setBusy(true);
    setError(null);
    try {
      await promoteClusterHA();
      await reload();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : err instanceof Error ? err.message : "Promote failed");
    } finally {
      setBusy(false);
    }
  }

  async function onRolling() {
    if (
      !window.confirm(
        "Roll one node at a time: drain, then Phase 12 update on this control node. Guests are not stopped. Worker apply stays unavailable until dest agent is connected.",
      )
    ) {
      return;
    }
    setBusy(true);
    setError(null);
    try {
      await runClusterUpdate();
      await reload();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : err instanceof Error ? err.message : "Rolling update failed");
    } finally {
      setBusy(false);
    }
  }

  const nodes = inv?.nodes ?? [];
  const replicaLabel = ha?.replica_status === "unavailable" ? "Unavailable" : "Not configured";

  return (
    <section className="page">
      <h1>Cluster</h1>
      <p className="lede">
        One control plane writer. A second node is a worker. Hostname is a locator; identity is the node UUID.
        Existing guests on the control node stay where they are. Join tokens are not pairing tokens. HA is leader plus
        replica foundations, not multi-master. STONITH is not implemented.
      </p>
      {error ? (
        <p className="banner banner-error" role="alert">
          {error}
        </p>
      ) : null}
      <article className="panel">
        <h2>Nodes</h2>
        {nodes.length === 0 ? (
          <p>No cluster members yet.</p>
        ) : (
          <ul className="plain-list">
            {nodes.map((n) => (
              <li key={n.id}>
                <strong>{n.name}</strong> {n.role ?? "control"} {n.status}
                {n.hostname ? ` locator ${n.hostname}` : ""} {n.id}
                {mutate && n.role === "worker" && !n.revoked ? (
                  <>
                    {" "}
                    <button className="btn btn-ghost" type="button" disabled={busy} onClick={() => void onRevoke(n.id)}>
                      Revoke
                    </button>
                  </>
                ) : null}
              </li>
            ))}
          </ul>
        )}
      </article>
      <article className="panel">
        <h2>HA</h2>
        <p className="lede">
          Single writer lease. STONITH is not implemented. Fence records that the operator isolated the old writer, then
          promote takes the lease. Guests keep running when the control plane moves.
        </p>
        {ha ? (
          <p>
            Mode {ha.mode}. Writer {ha.writer ? "yes" : "no"}. Replica {replicaLabel}. Fencing {ha.fencing_mode}.
            Multi-master {ha.multi_master ? "yes" : "no"}.{ha.lease_holder ? ` Lease ${ha.lease_holder}.` : ""}
          </p>
        ) : (
          <p>Collecting</p>
        )}
        {promote ? (
          <div className="btn-row">
            <button className="btn" type="button" disabled={busy} onClick={() => void onFence()}>
              Fence old writer
            </button>
            <button className="btn btn-primary" type="button" disabled={busy} onClick={() => void onPromote()}>
              Promote this writer
            </button>
          </div>
        ) : (
          <p>Promotion is privileged.</p>
        )}
      </article>
      <article className="panel">
        <h2>Rolling update</h2>
        <p className="lede">
          Drain one node, apply the Phase 12 package update on this control node, then the next. Guests are not stopped.
          Worker package apply stays unavailable until the dest agent is connected.
        </p>
        {rolling?.preview && rolling.preview.length > 0 ? (
          <ol>
            {rolling.preview.map((step) => (
              <li key={`${step.node_id}-${step.ordinal}`}>
                {step.action} {step.name || step.node_id}
                {step.status ? ` (${step.status})` : ""}
              </li>
            ))}
          </ol>
        ) : (
          <p>No nodes in the rolling preview yet.</p>
        )}
        {mutate ? (
          <button className="btn btn-primary" type="button" disabled={busy} onClick={() => void onRolling()}>
            Run rolling update
          </button>
        ) : (
          <p>Rolling update requires operator or admin.</p>
        )}
      </article>
      <article className="panel">
        <h2>Add node</h2>
        <p className="lede">
          Create a single-use join token, then on the worker run{" "}
          <code>nodalctl cluster join --url URL --token TOKEN</code>. The token is shown once.
        </p>
        {mutate ? (
          <button className="btn btn-primary" type="button" disabled={busy} onClick={() => void onCreateToken()}>
            Create join token
          </button>
        ) : (
          <p>Join tokens require operator or admin.</p>
        )}
        {tokenOnce ? (
          <pre className="code-block" tabIndex={0}>
            {`token ${tokenOnce}${expires ? `\nexpires ${expires}` : ""}`}
          </pre>
        ) : null}
      </article>
    </section>
  );
}
