import { useEffect, useState } from "react";
import { createWorkload, listNetworks, listRegistries } from "../api/client";
import type { Network } from "../api/phase4";
import type { Registry } from "../api/client";
import { Field } from "../components/Field";
import { navigate } from "../router";
import { useSession } from "../session";
import { canMutate, uxLevel } from "../ux";

export function OciCreatePage() {
  const session = useSession();
  const roles = session.status === "ready" ? session.user?.roles : undefined;
  const admin = Boolean(roles?.includes("admin"));
  const mutate = canMutate(roles);
  const level = uxLevel(session.status === "ready" ? session.user : null);
  const [nets, setNets] = useState<Network[]>([]);
  const [regs, setRegs] = useState<Registry[]>([]);
  const [name, setName] = useState("app");
  const [image, setImage] = useState("docker.io/library/nginx:alpine");
  const [registryID, setRegistryID] = useState("");
  const [networkID, setNetworkID] = useState("");
  const [cpus, setCpus] = useState("1");
  const [memoryMiB, setMemoryMiB] = useState("256");
  const [healthPath, setHealthPath] = useState("/healthz");
  const [healthPort, setHealthPort] = useState("8080");
  const [privileged, setPrivileged] = useState(false);
  const [more, setMore] = useState(level !== "guided");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    let cancelled = false;
    void Promise.all([listNetworks(), listRegistries()])
      .then(([n, r]) => {
        if (cancelled) {
          return;
        }
        const ready = (n.items ?? []).filter((item) => item.status === "available" || item.status === "warning");
        setNets(ready);
        setRegs(r.items ?? []);
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
      const health =
        healthPath.trim() && Number(healthPort) > 0
          ? { http_path: healthPath.trim(), port: Number(healthPort) || 0 }
          : undefined;
      const created = await createWorkload(
        {
          name,
          kind: "oci",
          image_pin: image,
          registry_id: registryID || undefined,
          network_id: networkID || undefined,
          cpus: Number(cpus) || 1,
          memory_bytes: (Number(memoryMiB) || 256) * 1024 * 1024,
          health,
          privileged: admin ? privileged : false,
        },
        `ui-oci-${name}`,
      );
      navigate(`/workloads/${created.id}`);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Create failed");
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="page page-wide" aria-labelledby="create-oci-heading">
      <header className="page-header">
        <h1 id="create-oci-heading">Create OCI application</h1>
        <p className="page-kicker">
          containerd runtime. Unprivileged by default. Health stays collecting or not configured until observed.
        </p>
      </header>
      {error ? (
        <p className="banner banner-error" role="alert">
          {error}
        </p>
      ) : null}
      <article className="panel">
        <Field id="oci-name" label="Name" value={name} onChange={(e) => setName(e.target.value)} />
        <Field id="oci-image" label="Image" value={image} onChange={(e) => setImage(e.target.value)} />
        <div className="field">
          <label className="field-label" htmlFor="oci-registry">
            Registry (optional)
          </label>
          <select
            id="oci-registry"
            className="field-input"
            value={registryID}
            onChange={(e) => setRegistryID(e.target.value)}
          >
            <option value="">None</option>
            {regs.map((r) => (
              <option key={r.id} value={r.id}>
                {r.name} ({r.has_credentials ? "creds" : "no creds"})
              </option>
            ))}
          </select>
        </div>
        <Field id="oci-cpus" label="CPUs" type="number" min={1} value={cpus} onChange={(e) => setCpus(e.target.value)} />
        <Field
          id="oci-mem"
          label="Memory (MiB)"
          type="number"
          min={64}
          value={memoryMiB}
          onChange={(e) => setMemoryMiB(e.target.value)}
        />
        <div className="field">
          <label className="field-label" htmlFor="oci-net">
            Network (optional)
          </label>
          <select id="oci-net" className="field-input" value={networkID} onChange={(e) => setNetworkID(e.target.value)}>
            <option value="">None</option>
            {nets.map((n) => (
              <option key={n.id} value={n.id}>
                {n.name} ({n.status})
              </option>
            ))}
          </select>
        </div>
        <p>
          <button type="button" className="btn btn-ghost" onClick={() => setMore((v) => !v)}>
            {more ? "Hide advanced" : "Show advanced"}
          </button>
        </p>
        {more ? (
          <>
            <Field
              id="oci-health-path"
              label="Health HTTP path"
              value={healthPath}
              onChange={(e) => setHealthPath(e.target.value)}
            />
            <Field
              id="oci-health-port"
              label="Health port"
              type="number"
              min={0}
              value={healthPort}
              onChange={(e) => setHealthPort(e.target.value)}
            />
            {admin ? (
              <label className="field-check">
                <input type="checkbox" checked={privileged} onChange={(e) => setPrivileged(e.target.checked)} />
                Privileged (admin only)
              </label>
            ) : null}
          </>
        ) : null}
        <p>
          <button type="button" className="btn btn-primary" disabled={!mutate || busy} onClick={() => void onCreate()}>
            {busy ? "Creating" : "Create"}
          </button>
        </p>
      </article>
    </section>
  );
}
