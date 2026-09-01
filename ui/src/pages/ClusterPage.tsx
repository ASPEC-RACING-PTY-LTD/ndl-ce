import { useEffect, useState } from "react";
import { ApiError, createJoinToken, getCluster, revokeClusterNode, type ClusterInventory } from "../api/client";
import { useSession } from "../session";

function canJoin(roles: string[] | undefined): boolean {
  return Boolean(roles?.includes("admin") || roles?.includes("operator"));
}

export function ClusterPage() {
  const session = useSession();
  const roles = session.status === "ready" ? session.user?.roles : undefined;
  const mutate = canJoin(roles);
  const [inv, setInv] = useState<ClusterInventory | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [tokenOnce, setTokenOnce] = useState<string | null>(null);
  const [expires, setExpires] = useState<string | null>(null);

  async function reload() {
    const next = await getCluster();
    setInv(next);
  }

  useEffect(() => {
    let cancelled = false;
    void getCluster()
      .then((value) => {
        if (!cancelled) {
          setInv(value);
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

  const nodes = inv?.nodes ?? [];

  return (
    <section className="page">
      <h1>Cluster</h1>
      <p className="lede">
        One control plane writer. A second node is a worker. Hostname is a locator; identity is the node UUID.
        Existing guests on the control node stay where they are. Pairing tokens are not join tokens.
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
