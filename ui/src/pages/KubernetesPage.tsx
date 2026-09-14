import { useEffect, useState } from "react";
import { ApiError, getKubernetes, startKubernetes, stopKubernetes } from "../api/client";
import type { KubernetesStatus } from "../generated/openapi";
import { useSession } from "../session";

function canManage(roles: string[] | undefined): boolean {
  return Boolean(roles?.includes("admin") || roles?.includes("operator"));
}

export function KubernetesPage() {
  const session = useSession();
  const roles = session.status === "ready" ? session.user?.roles : undefined;
  const mutate = canManage(roles);
  const [status, setStatus] = useState<KubernetesStatus | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function reload() {
    setStatus(await getKubernetes());
  }

  useEffect(() => {
    let cancelled = false;
    void reload().catch((err) => {
      if (!cancelled) {
        setError(err instanceof Error ? err.message : "Unavailable");
      }
    });
    return () => {
      cancelled = true;
    };
  }, []);

  async function onStart() {
    setBusy(true);
    setError(null);
    try {
      setStatus(await startKubernetes());
    } catch (err) {
      setError(err instanceof ApiError ? err.message : err instanceof Error ? err.message : "Start failed");
    } finally {
      setBusy(false);
    }
  }

  async function onStop() {
    setBusy(true);
    setError(null);
    try {
      setStatus(await stopKubernetes());
    } catch (err) {
      setError(err instanceof ApiError ? err.message : err instanceof Error ? err.message : "Stop failed");
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="page">
      <header className="page-header">
        <h1>Kubernetes</h1>
        <p className="lede">
          Optional runtime. Virtual machines and system containers do not require Kubernetes. Default install has no
          kubelet process.
        </p>
      </header>
      {error ? (
        <p className="banner banner-error" role="alert">
          {error}
        </p>
      ) : null}
      {status ? (
        <article className="panel">
          <p>Enabled {status.enabled ? "yes" : "no"}.</p>
          <p>Kubelet started {status.kubelet_started ? "yes" : "no"}.</p>
          <p>Kube process {status.kube_process ? "yes" : "no"}.</p>
          <p>{status.reason}</p>
          {mutate ? (
            <>
              <button className="btn btn-primary" type="button" disabled={busy} onClick={() => void onStart()}>
                Start kubelet
              </button>{" "}
              <button className="btn" type="button" disabled={busy} onClick={() => void onStop()}>
                Stop kubelet
              </button>
            </>
          ) : null}
        </article>
      ) : (
        <p>Collecting</p>
      )}
    </section>
  );
}
