import { useEffect, useState } from "react";
import {
  ApiError,
  getStoreApp,
  getStoreAppScans,
  getStorePolicy,
  installStoreApp,
  listStoreApps,
  setStorePolicy,
  verifyStoreApp,
} from "../api/client";
import type { StoreApp, StoreInstallation, StoreScanCheck, StorePolicy } from "../generated/openapi";
import { Field } from "../components/Field";
import { useSession } from "../session";

function canMutate(roles: string[] | undefined): boolean {
  return Boolean(roles?.includes("admin") || roles?.includes("operator"));
}

function classLabel(app: StoreApp): string {
  if (app.trust_class === "official" || app.class === "official") {
    return "Official";
  }
  if (app.trust_class === "verified") {
    return "Verified";
  }
  return "Community";
}

export function StorePage() {
  const session = useSession();
  const roles = session.status === "ready" ? session.user?.roles : undefined;
  const mutate = canMutate(roles);
  const [apps, setApps] = useState<StoreApp[]>([]);
  const [selected, setSelected] = useState<StoreApp | null>(null);
  const [result, setResult] = useState<StoreInstallation | null>(null);
  const [scans, setScans] = useState<StoreScanCheck[]>([]);
  const [policy, setPolicy] = useState<StorePolicy | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [cpus, setCpus] = useState("1");
  const [memory, setMemory] = useState("268435456");
  const [poolId, setPoolId] = useState("");
  const [networkId, setNetworkId] = useState("");

  async function reload() {
    const [next, pol] = await Promise.all([listStoreApps(), getStorePolicy().catch(() => null)]);
    setApps(next.items);
    if (pol) {
      setPolicy(pol);
    }
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
      const app = await getStoreApp(id);
      setSelected(app);
      const report = await getStoreAppScans(id);
      setScans(report.items ?? []);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Unavailable");
    }
  }

  async function onVerify() {
    if (!selected) {
      return;
    }
    setBusy(true);
    setError(null);
    try {
      const out = await verifyStoreApp(selected.id);
      setScans(out.checks ?? []);
      setSelected(await getStoreApp(selected.id));
      await reload();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : err instanceof Error ? err.message : "Verify failed");
    } finally {
      setBusy(false);
    }
  }

  async function onPolicy(next: StorePolicy["install_policy"]) {
    setBusy(true);
    setError(null);
    try {
      setPolicy(await setStorePolicy({ install_policy: next }));
    } catch (err) {
      setError(err instanceof ApiError ? err.message : err instanceof Error ? err.message : "Policy failed");
    } finally {
      setBusy(false);
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
          Declarative app install. Signatures fail closed on tamper. Unsigned Community warns. Verified-only refuses
          unsigned packages. CVE scanner unavailable is shown on the scan report.
        </p>
      </header>
      {error ? (
        <p className="banner banner-error" role="alert">
          {error}
        </p>
      ) : null}
      {policy ? (
        <p>
          Install policy {policy.install_policy}.
          {mutate ? (
            <>
              {" "}
              <button
                className="btn"
                type="button"
                disabled={busy}
                onClick={() => void onPolicy("community-allowed")}
              >
                Allow Community
              </button>{" "}
              <button
                className="btn"
                type="button"
                disabled={busy}
                onClick={() => void onPolicy("verified-only")}
              >
                Verified only
              </button>
            </>
          ) : null}
        </p>
      ) : null}
      <ul className="plain-list">
        {apps.map((app) => (
          <li key={app.id}>
            <article className="panel">
              <h2>{app.title || app.name}</h2>
              <p>
                {classLabel(app)} badge. {app.signed ? "Signed." : "Unsigned."} {app.summary}
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
          <p>
            Image {selected.image}. GPU optional {selected.gpu_optional ? "yes" : "no"}. Trust{" "}
            {selected.trust_class || selected.class}.
          </p>
          <Field id="store-cpu" label="CPU" value={cpus} onChange={(e) => setCpus(e.target.value)} />
          <Field id="store-mem" label="Memory bytes" value={memory} onChange={(e) => setMemory(e.target.value)} />
          <Field id="store-pool" label="Storage pool" value={poolId} onChange={(e) => setPoolId(e.target.value)} />
          <Field id="store-net" label="Network" value={networkId} onChange={(e) => setNetworkId(e.target.value)} />
          {mutate ? (
            <button className="btn" type="button" disabled={busy} onClick={() => void onVerify()}>
              Verify
            </button>
          ) : null}{" "}
          <button className="btn btn-primary" type="button" disabled={busy} onClick={() => void onDeploy()}>
            Deploy
          </button>
        </article>
      ) : null}
      {scans.length > 0 ? (
        <article className="panel">
          <h2>Scan report</h2>
          <ul className="plain-list">
            {scans.map((row) => (
              <li key={row.kind}>
                {row.kind}: {row.status}. {row.detail}
              </li>
            ))}
          </ul>
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
