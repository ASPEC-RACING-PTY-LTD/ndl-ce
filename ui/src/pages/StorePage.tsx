import { useEffect, useState } from "react";
import { ApiError, getStoreApp, installStoreApp, listStoreApps } from "../api/client";
import type { StoreApp, StoreInstallation } from "../generated/openapi";
import { Field } from "../components/Field";
import { useSession } from "../session";

function canInstall(roles: string[] | undefined): boolean {
  return Boolean(roles?.includes("admin") || roles?.includes("operator"));
}

export function StorePage() {
  const session = useSession();
  const roles = session.status === "ready" ? session.user?.roles : undefined;
  const mutate = canInstall(roles);
  const [apps, setApps] = useState<StoreApp[]>([]);
  const [selected, setSelected] = useState<StoreApp | null>(null);
  const [result, setResult] = useState<StoreInstallation | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [cpus, setCpus] = useState("1");
  const [memory, setMemory] = useState("268435456");
  const [poolId, setPoolId] = useState("");
  const [networkId, setNetworkId] = useState("");

  async function reload() {
    const next = await listStoreApps();
    setApps(next.items);
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

  async function onSelect(id: string) {
    setError(null);
    setResult(null);
    try {
      setSelected(await getStoreApp(id));
    } catch (err) {
      setError(err instanceof Error ? err.message : "Unavailable");
    }
  }

  async function onDeploy() {
    if (!selected) {
      return;
    }
    setBusy(true);
    setError(null);
    try {
      const created = await installStoreApp(selected.id, {
        name: selected.name,
        pool_id: poolId || undefined,
        network_id: networkId || undefined,
        cpus: Number(cpus) || undefined,
        memory_bytes: Number(memory) || undefined,
      });
      setResult(created);
      await reload();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : err instanceof Error ? err.message : "Install failed");
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="page">
      <header className="page-header">
        <h1>Store</h1>
        <p className="lede">
          Declarative app install. Manifests are not helper scripts. Unsigned Community packages warn. GPU is optional.
        </p>
      </header>
      {error ? (
        <p className="banner banner-error" role="alert">
          {error}
        </p>
      ) : null}
      <ul className="plain-list">
        {apps.map((app) => (
          <li key={app.id}>
            <article className="panel">
              <h2>{app.title || app.name}</h2>
              <p>
                {app.class}. {app.summary}
                {app.unsigned ? " Unsigned Community." : ""}
              </p>
              {mutate ? (
                <button className="btn btn-primary" type="button" onClick={() => void onSelect(app.id)}>
                  Install
                </button>
              ) : null}
            </article>
          </li>
        ))}
      </ul>
      {selected ? (
        <article className="panel">
          <h2>Deploy {selected.title || selected.name}</h2>
          {selected.warning ? <p>{selected.warning}</p> : null}
          <p>Image {selected.image}. GPU optional {selected.gpu_optional ? "yes" : "no"}.</p>
          <Field id="store-cpu" label="CPU" value={cpus} onChange={(e) => setCpus(e.target.value)} />
          <Field id="store-mem" label="Memory bytes" value={memory} onChange={(e) => setMemory(e.target.value)} />
          <Field id="store-pool" label="Storage pool" value={poolId} onChange={(e) => setPoolId(e.target.value)} />
          <Field id="store-net" label="Network" value={networkId} onChange={(e) => setNetworkId(e.target.value)} />
          <button className="btn btn-primary" type="button" disabled={busy} onClick={() => void onDeploy()}>
            Deploy
          </button>
        </article>
      ) : null}
      {result ? (
        <p>
          Status {result.status}. Workload {result.workload_id}. Stack {result.stack_id}.
        </p>
      ) : null}
    </section>
  );
}
