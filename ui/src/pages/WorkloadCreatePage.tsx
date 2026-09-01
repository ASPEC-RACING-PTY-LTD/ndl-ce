import { useEffect, useState } from "react";
import { createWorkload, listNetworks, listPools } from "../api/client";
import type { Network } from "../api/phase4";
import type { StoragePool } from "../api/phase3";
import { Field } from "../components/Field";
import { navigate } from "../router";
import { useSession } from "../session";
import { canMutate, uxLevel } from "../ux";

const PINS = [
  "alpine/3.21/amd64/default",
  "alpine/3.20/amd64/default",
  "debian/trixie/amd64/default",
  "debian/bookworm/amd64/default",
];

export function WorkloadCreatePage() {
  const session = useSession();
  const roles = session.status === "ready" ? session.user?.roles : undefined;
  const admin = Boolean(roles?.includes("admin"));
  const mutate = canMutate(roles);
  const level = uxLevel(session.status === "ready" ? session.user : null);
  const [pools, setPools] = useState<StoragePool[]>([]);
  const [nets, setNets] = useState<Network[]>([]);
  const [name, setName] = useState("alpine");
  const [pin, setPin] = useState(PINS[0]);
  const [cpus, setCpus] = useState("1");
  const [memoryMiB, setMemoryMiB] = useState("256");
  const [poolID, setPoolID] = useState("");
  const [networkID, setNetworkID] = useState("");
  const [privileged, setPrivileged] = useState(false);
  const [more, setMore] = useState(level !== "guided");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    let cancelled = false;
    void Promise.all([listPools(), listNetworks()])
      .then(([p, n]) => {
        if (cancelled) {
          return;
        }
        const usable = (p.items ?? []).filter((item) => item.status === "available" || item.status === "warning");
        const ready = (n.items ?? []).filter((item) => item.status === "available" || item.status === "warning");
        setPools(usable);
        setNets(ready);
        if (!poolID && usable[0]) {
          setPoolID(usable[0].id);
        }
        if (!networkID && ready[0]) {
          setNetworkID(ready[0].id);
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
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  async function onCreate() {
    setBusy(true);
    setError(null);
    try {
      const created = await createWorkload(
        {
          name,
          kind: "system-container",
          image_pin: pin,
          cpus: Number(cpus) || 1,
          memory_bytes: (Number(memoryMiB) || 256) * 1024 * 1024,
          pool_id: poolID || undefined,
          network_id: networkID,
          privileged: admin ? privileged : false,
        },
        `ui-create-${name}`,
      );
      navigate(`/workloads/${created.id}`);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Create failed");
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="page page-wide" aria-labelledby="create-ct-heading">
      <header className="page-header">
        <h1 id="create-ct-heading">Create system container</h1>
        <p className="page-kicker">
          Official LXC images. Unprivileged is the default. Guided, Advanced, and Expert post the same body.
        </p>
      </header>
      {error ? (
        <p className="banner banner-error" role="alert">
          {error}
        </p>
      ) : null}
      <article className="panel">
        <Field id="ct-name" label="Name" value={name} onChange={(e) => setName(e.target.value)} />
        <div className="field">
          <label className="field-label" htmlFor="ct-pin">
            Image pin
          </label>
          <select id="ct-pin" className="field-input" value={pin} onChange={(e) => setPin(e.target.value)}>
            {PINS.map((p) => (
              <option key={p} value={p}>
                {p}
              </option>
            ))}
          </select>
        </div>
        <Field id="ct-cpus" label="CPUs" type="number" min={1} value={cpus} onChange={(e) => setCpus(e.target.value)} />
        <Field
          id="ct-mem"
          label="Memory (MiB)"
          type="number"
          min={64}
          value={memoryMiB}
          onChange={(e) => setMemoryMiB(e.target.value)}
        />
        <div className="field">
          <label className="field-label" htmlFor="ct-pool">
            Storage pool
          </label>
          <select id="ct-pool" className="field-input" value={poolID} onChange={(e) => setPoolID(e.target.value)}>
            {pools.map((p) => (
              <option key={p.id} value={p.id}>
                {p.name} ({p.status})
              </option>
            ))}
          </select>
        </div>
        <div className="field">
          <label className="field-label" htmlFor="ct-net">
            Network
          </label>
          <select id="ct-net" className="field-input" value={networkID} onChange={(e) => setNetworkID(e.target.value)}>
            {nets.map((n) => (
              <option key={n.id} value={n.id}>
                {n.name} ({n.status})
              </option>
            ))}
          </select>
        </div>
        <p>
          <button className="btn btn-ghost" type="button" onClick={() => setMore(!more)}>
            {more ? "Hide more options" : "More options"}
          </button>
        </p>
        {more && admin ? (
          <label className="field-label">
            <input type="checkbox" checked={privileged} onChange={(e) => setPrivileged(e.target.checked)} /> Privileged
            (admin only, audited)
          </label>
        ) : null}
        {more && !admin ? <p className="field-hint">Privileged containers are admin-only.</p> : null}
        <div className="btn-row">
          <button className="btn btn-primary" type="button" disabled={busy || !mutate || !networkID} onClick={() => void onCreate()}>
            Create system container
          </button>
        </div>
      </article>
    </section>
  );
}
