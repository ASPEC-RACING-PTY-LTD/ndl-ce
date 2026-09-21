import { useEffect, useState } from "react";
import { createWorkload, listImages, listNetworks, listPhysicalDisks, listPools } from "../api/client";
import type { PhysicalDisk } from "../api/client";
import type { LibraryItem, StoragePool } from "../api/phase3";
import type { Network } from "../api/phase4";
import { Field } from "../components/Field";
import { formatBytes } from "../format";
import { preferredGuestNetwork } from "../components/form/NetworkPicker";
import { navigate } from "../router";
import { useSession } from "../session";
import { canMutate, uxLevel } from "../ux";
import { buildVmCreateBody } from "../vmCreate";

const STEPS = ["Basics", "Compute", "Storage", "Network", "Boot", "Cloud-init", "Review"] as const;

function physicalDiskOption(d: PhysicalDisk): string {
  const kind = d.rotational === false ? "SSD" : d.rotational ? "HDD" : "disk";
  const transport = (d.transport || "disk").toUpperCase();
  const size = d.size_bytes ? formatBytes(d.size_bytes) : "";
  const reason = d.eligible ? "" : ` (${(d.reasons ?? []).join("; ") || "unavailable"})`;
  return `${d.display_name || d.model || d.id} ${size} ${transport} ${kind}${reason}`.replace(/\s+/g, " ").trim();
}

export function VmCreatePage() {
  const session = useSession();
  const roles = session.status === "ready" ? session.user?.roles : undefined;
  const mutate = canMutate(roles);
  const level = uxLevel(session.status === "ready" ? session.user : null);
  const guided = level === "guided";
  const [step, setStep] = useState(0);
  const [pools, setPools] = useState<StoragePool[]>([]);
  const [nets, setNets] = useState<Network[]>([]);
  const [images, setImages] = useState<LibraryItem[]>([]);
  const [name, setName] = useState("vm-1");
  const [cpus, setCpus] = useState("2");
  const [memoryGB, setMemoryGB] = useState("2");
  const [poolID, setPoolID] = useState("");
  const [bootDisk, setBootDisk] = useState<"virtual" | "physical">("virtual");
  const [physicalDeviceID, setPhysicalDeviceID] = useState("");
  const [physicalDisks, setPhysicalDisks] = useState<PhysicalDisk[]>([]);
  const [networkID, setNetworkID] = useState("");
  const [firmware, setFirmware] = useState("bios");
  const [cloudImageID, setCloudImageID] = useState("");
  const [isoID, setIsoID] = useState("");
  const [autostart, setAutostart] = useState(false);
  const [placement, setPlacement] = useState("automatic");
  const [requireGPU, setRequireGPU] = useState(false);
  const [hostname, setHostname] = useState("vm-1");
  const [username, setUsername] = useState("debian");
  const [sshKeys, setSshKeys] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    let cancelled = false;
    void Promise.all([listPools(), listNetworks(), listImages(), listPhysicalDisks().catch(() => ({ items: [] }))])
      .then(([poolRes, netRes, imgs, disks]) => {
        if (cancelled) {
          return;
        }
        setPools(poolRes.items ?? []);
        setNets(netRes.items ?? []);
        setImages(imgs ?? []);
        setPhysicalDisks(disks.items ?? []);
        if (!poolID && poolRes.items?.[0]?.id) {
          setPoolID(poolRes.items[0].id);
        }
        const preferred = preferredGuestNetwork(netRes.items ?? []);
        if (!networkID && preferred) {
          setNetworkID(preferred.id);
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

  const body = buildVmCreateBody({
    name,
    cpus,
    memoryGB,
    networkID,
    poolID,
    firmware,
    autostart,
    cloudImageID,
    isoID,
    hostname,
    username,
    sshKeys,
    placement,
    requireGPU,
    bootDisk,
    physicalDeviceID,
  });

  async function onCreate() {
    setBusy(true);
    setError(null);
    try {
      const created = await createWorkload(body);
      navigate(`/workloads/${created.id}`);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Create failed");
    } finally {
      setBusy(false);
    }
  }

  if (!mutate) {
    return (
      <section className="page">
        <h1>Create VM</h1>
        <p className="banner banner-error">Creating a VM requires operator or admin.</p>
      </section>
    );
  }

  const selectedPhysical = physicalDisks.find((d) => d.id === physicalDeviceID);
  const canCreate = Boolean(networkID) && (bootDisk === "virtual" || Boolean(physicalDeviceID && selectedPhysical?.eligible));
  const clouds = images.filter((item) => item.kind === "cloud-image");
  const isos = images.filter((item) => item.kind === "iso");
  const showBasics = !guided || step === 0 || step === 6;
  const showCompute = !guided || step === 1 || step === 6;
  const showStorage = !guided || step === 2 || step === 6;
  const showNetwork = !guided || step === 3 || step === 6;
  const showBoot = !guided || step === 4 || step === 6;
  const showCloud = !guided || step === 5 || step === 6;
  const reviewOnly = guided && step === 6;

  return (
    <section className="page page-wide" aria-labelledby="vm-create-heading">
      <header className="page-header">
        <h1 id="vm-create-heading">Create VM</h1>
        <p className="page-kicker">
          {guided
            ? `Step ${step + 1} of ${STEPS.length}: ${STEPS[step]}`
            : level === "expert"
              ? "Expert view. Same create API as Guided and Advanced."
              : "All fields on one form. Same create API as Guided."}
        </p>
      </header>
      {error ? (
        <p className="banner banner-error" role="alert">
          {error}
        </p>
      ) : null}
      {showBasics && !reviewOnly ? (
        <article className="panel">
          <Field id="vm-name" label="Name" value={name} onChange={(e) => setName(e.target.value)} />
        </article>
      ) : null}
      {showCompute && !reviewOnly ? (
        <article className="panel">
          <Field id="vm-cpus" label="CPUs" type="number" min={1} value={cpus} onChange={(e) => setCpus(e.target.value)} />
          <Field
            id="vm-mem"
            label="Memory (GB)"
            type="number"
            min={1}
            value={memoryGB}
            onChange={(e) => setMemoryGB(e.target.value)}
          />
          <fieldset>
            <legend className="field-label">Placement</legend>
            <label className="field-label">
              <input type="radio" name="vm-placement" checked={placement === "automatic"} onChange={() => setPlacement("automatic")} />{" "}
              Automatic
            </label>
            <label className="field-label">
              <input type="radio" name="vm-placement" checked={placement === "node"} onChange={() => setPlacement("node")} /> Specific
              node
            </label>
          </fieldset>
          <label className="field-label">
            <input type="checkbox" checked={requireGPU} onChange={(e) => setRequireGPU(e.target.checked)} /> Require GPU
          </label>
        </article>
      ) : null}
      {showStorage && !reviewOnly ? (
        <article className="panel">
          <fieldset>
            <legend className="field-label">Add disk</legend>
            <label className="field-label">
              <input type="radio" name="vm-boot-disk" checked={bootDisk === "virtual"} onChange={() => setBootDisk("virtual")} />{" "}
              Virtual disk
            </label>
            <label className="field-label">
              <input
                type="radio"
                name="vm-boot-disk"
                checked={bootDisk === "physical"}
                onChange={() => {
                  setBootDisk("physical");
                  setCloudImageID("");
                  if (firmware === "bios") {
                    setFirmware("uefi");
                  }
                }}
              />{" "}
              Physical disk
            </label>
          </fieldset>
          {bootDisk === "virtual" ? (
            <>
              <label htmlFor="vm-pool">
                Storage pool
                <select id="vm-pool" value={poolID} onChange={(e) => setPoolID(e.target.value)}>
                  {pools.map((p) => (
                    <option key={p.id} value={p.id}>
                      {p.name}
                    </option>
                  ))}
                </select>
              </label>
              <label htmlFor="vm-cloud">
                Cloud image (optional)
                <select id="vm-cloud" value={cloudImageID} onChange={(e) => setCloudImageID(e.target.value)}>
                  <option value="">None</option>
                  {clouds.map((item) => (
                    <option key={item.id} value={item.id}>
                      {item.display_name}
                    </option>
                  ))}
                </select>
              </label>
            </>
          ) : (
            <>
              <p className="banner">
                This attaches an existing host disk. No-DAL will not format, partition, mount, or convert it. The current
                contents, including an installed OS, stay intact.
              </p>
              <label htmlFor="vm-phys">
                Physical disk
                <select id="vm-phys" value={physicalDeviceID} onChange={(e) => setPhysicalDeviceID(e.target.value)}>
                  <option value="">Select a disk</option>
                  {physicalDisks.map((d) => (
                    <option key={d.id} value={d.id} disabled={!d.eligible}>
                      {physicalDiskOption(d)}
                    </option>
                  ))}
                </select>
              </label>
              {selectedPhysical ? (
                <dl className="definition-list">
                  <div>
                    <dt>Stable path</dt>
                    <dd>{selectedPhysical.by_id_path || selectedPhysical.id}</dd>
                  </div>
                  <div>
                    <dt>Capacity</dt>
                    <dd>{selectedPhysical.size_bytes ? formatBytes(selectedPhysical.size_bytes) : "Not reported"}</dd>
                  </div>
                  <div>
                    <dt>Existing data</dt>
                    <dd>
                      {selectedPhysical.existing_data
                        ? `Detected (${(selectedPhysical.fs_signatures ?? []).join(", ") || "partitions present"})`
                        : "No filesystem signatures detected"}
                    </dd>
                  </div>
                  {selectedPhysical.reasons?.length ? (
                    <div>
                      <dt>Eligibility</dt>
                      <dd>{selectedPhysical.reasons.join("; ")}</dd>
                    </div>
                  ) : null}
                </dl>
              ) : null}
            </>
          )}
        </article>
      ) : null}
      {showNetwork && !reviewOnly ? (
        <article className="panel">
          <label htmlFor="vm-net">
            Network
            <select id="vm-net" value={networkID} onChange={(e) => setNetworkID(e.target.value)}>
              {nets.map((n) => (
                <option key={n.id} value={n.id}>
                  {n.name}
                </option>
              ))}
            </select>
          </label>
        </article>
      ) : null}
      {showBoot && !reviewOnly ? (
        <article className="panel">
          <label htmlFor="vm-fw">
            Firmware
            <select id="vm-fw" value={firmware} onChange={(e) => setFirmware(e.target.value)}>
              <option value="bios">Legacy BIOS</option>
              <option value="uefi">UEFI</option>
            </select>
          </label>
          <label htmlFor="vm-iso">
            Installation ISO (optional)
            <select id="vm-iso" value={isoID} onChange={(e) => setIsoID(e.target.value)}>
              <option value="">None</option>
              {isos.map((item) => (
                <option key={item.id} value={item.id}>
                  {item.display_name}
                </option>
              ))}
            </select>
          </label>
          <label>
            <input type="checkbox" checked={autostart} onChange={(e) => setAutostart(e.target.checked)} /> Autostart after host
            reboot
          </label>
        </article>
      ) : null}
      {showCloud && !reviewOnly ? (
        <article className="panel">
          <Field id="vm-host" label="Hostname" value={hostname} onChange={(e) => setHostname(e.target.value)} />
          <Field id="vm-user" label="Username" value={username} onChange={(e) => setUsername(e.target.value)} />
          <label htmlFor="vm-keys">
            SSH authorized keys
            <textarea id="vm-keys" rows={4} value={sshKeys} onChange={(e) => setSshKeys(e.target.value)} />
          </label>
        </article>
      ) : null}
      {reviewOnly || !guided ? (
        <article className="panel">
          <h2>Review</h2>
          <dl className="definition-list">
            <div>
              <dt>Name</dt>
              <dd>{name}</dd>
            </div>
            <div>
              <dt>CPUs</dt>
              <dd>{cpus}</dd>
            </div>
            <div>
              <dt>Memory</dt>
              <dd>{memoryGB} GB</dd>
            </div>
            <div>
              <dt>Boot disk</dt>
              <dd>
                {bootDisk === "physical"
                  ? `Physical disk ${selectedPhysical?.display_name || physicalDeviceID} (not formatted)`
                  : `Virtual disk on ${pools.find((p) => p.id === poolID)?.name || poolID}`}
              </dd>
            </div>
            <div>
              <dt>Firmware</dt>
              <dd>{firmware}</dd>
            </div>
            <div>
              <dt>Network</dt>
              <dd>{nets.find((n) => n.id === networkID)?.name || networkID}</dd>
            </div>
          </dl>
        </article>
      ) : null}
      {level === "expert" ? (
        <article className="panel">
          <h2>Request body</h2>
          <p className="field-hint">Read-only preview of the same POST /workloads body Guided and Advanced send.</p>
          <pre className="code-block">{JSON.stringify(body, null, 2)}</pre>
        </article>
      ) : null}
      <div className="btn-row">
        {guided ? (
          <>
            <button className="btn" type="button" disabled={step === 0 || busy} onClick={() => setStep((n) => n - 1)}>
              Back
            </button>
            {step < STEPS.length - 1 ? (
              <button className="btn btn-primary" type="button" disabled={busy} onClick={() => setStep((n) => n + 1)}>
                Next
              </button>
            ) : (
              <button className="btn btn-primary" type="button" disabled={busy || !canCreate} onClick={() => void onCreate()}>
                Create VM
              </button>
            )}
          </>
        ) : (
          <button className="btn btn-primary" type="button" disabled={busy || !canCreate} onClick={() => void onCreate()}>
            Create VM
          </button>
        )}
      </div>
    </section>
  );
}
