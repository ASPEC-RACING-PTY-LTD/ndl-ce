import { useEffect, useState } from "react";
import { createWorkload, listNetworks, listPools, listWorkloads } from "../api/client";
import type { Network } from "../api/phase4";
import type { StoragePool } from "../api/phase3";
import { ErrorState } from "../components/EmptyState";
import { Field } from "../components/Field";
import { NetworkPicker } from "../components/form/NetworkPicker";
import { OsImagePicker } from "../components/form/OsImagePicker";
import { StoragePicker } from "../components/form/StoragePicker";
import { PageHeader } from "../components/PageHeader";
import { UxModeToggle } from "../components/UxModeToggle";
import { FALLBACK_IMAGE_PINS, kindLabel, osLabel } from "../labels";
import { canMutate, isAdmin, mutateHint } from "../rbac";
import { navigate } from "../router";
import { useSession } from "../session";
import { getUxLevelDefault, isAdvanced, isExpert, type UxLevel } from "../ux-mode";

export function WorkloadCreatePage() {
  const session = useSession();
  const roles = session.status === "ready" ? session.user?.roles : undefined;
  const admin = isAdmin(roles);
  const mutate = canMutate(roles);
  const [mode, setMode] = useState<UxLevel>(getUxLevelDefault);
  const [pools, setPools] = useState<StoragePool[]>([]);
  const [nets, setNets] = useState<Network[]>([]);
  const [pins, setPins] = useState<string[]>(FALLBACK_IMAGE_PINS);
  const [name, setName] = useState("alpine");
  const [pin, setPin] = useState(FALLBACK_IMAGE_PINS[0]);
  const [cpus, setCpus] = useState("1");
  const [memoryMiB, setMemoryMiB] = useState("256");
  const [poolID, setPoolID] = useState("");
  const [networkID, setNetworkID] = useState("");
  const [privileged, setPrivileged] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    let cancelled = false;
    void Promise.all([listPools(), listNetworks(), listWorkloads()])
      .then(([p, n, w]) => {
        if (cancelled) {
          return;
        }
        const usable = (p.items ?? []).filter((item) => item.status === "available" || item.status === "warning");
        const ready = (n.items ?? []).filter((item) => item.status === "available" || item.status === "warning");
        setPools(usable);
        setNets(ready);
        if (w.image_pins && w.image_pins.length > 0) {
          setPins(w.image_pins);
          setPin(w.image_pins[0]);
        }
        if (usable[0]) {
          setPoolID(usable[0].id);
        }
        if (ready[0]) {
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

  const pool = pools.find((p) => p.id === poolID);
  const net = nets.find((n) => n.id === networkID);
  const hint = mutateHint(roles);

  return (
    <section className="page" aria-labelledby="create-ct-heading">
      <PageHeader
        id="create-ct-heading"
        title="Create system container"
        kicker="Official Linux images. Unprivileged is the default."
        actions={<UxModeToggle value={mode} onChange={setMode} />}
      />
      {error ? <ErrorState>{error}</ErrorState> : null}
      <article className="panel form form-narrow">
        <Field id="ct-name" label="Name" value={name} onChange={(e) => setName(e.target.value)} />
        <OsImagePicker
          id="ct-pin"
          label={isExpert(mode) ? "Image pin" : isAdvanced(mode) ? "Image" : "Operating system"}
          pins={pins}
          value={pin}
          onChange={setPin}
          expert={isExpert(mode)}
        />
        {isAdvanced(mode) && !isExpert(mode) ? <p className="picker-meta">{pin}</p> : null}
        <Field id="ct-cpus" label="CPUs" type="number" min={1} value={cpus} onChange={(e) => setCpus(e.target.value)} />
        <Field
          id="ct-mem"
          label="Memory (MiB)"
          type="number"
          min={64}
          value={memoryMiB}
          onChange={(e) => setMemoryMiB(e.target.value)}
        />
        <StoragePicker
          id="ct-pool"
          label="Storage"
          pools={pools}
          value={poolID}
          onChange={setPoolID}
          expert={isExpert(mode)}
        />
        <NetworkPicker
          id="ct-net"
          label="Network"
          networks={nets}
          value={networkID}
          onChange={setNetworkID}
          expert={isExpert(mode)}
        />
        {isAdvanced(mode) ? (
          admin ? (
            <label className="field-label">
              <input type="checkbox" checked={privileged} onChange={(e) => setPrivileged(e.target.checked)} /> Privileged
              (administrator only)
            </label>
          ) : (
            <p className="field-hint">Privileged containers are administrator-only.</p>
          )
        ) : null}
        <div className="review-box">
          <strong>Review</strong>
          <span>
            {name}, {osLabel(pin)}, {cpus} CPU, {memoryMiB} MiB, {pool?.name || "no pool"},{" "}
            {net ? `${net.name} (${kindLabel(net.kind)})` : "no network"}
            {privileged ? ", privileged" : ""}
          </span>
        </div>
        {hint ? <p className="field-hint">{hint}</p> : null}
        <div className="btn-row">
          <button
            className="btn btn-primary"
            type="button"
            disabled={busy || !mutate || !networkID}
            onClick={() => void onCreate()}
          >
            Create system container
          </button>
        </div>
      </article>
    </section>
  );
}
