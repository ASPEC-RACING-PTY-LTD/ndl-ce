import { useEffect, useMemo, useState } from "react";
import {
  createWorkload,
  listNetworks,
  listPools,
  listWorkloadExtras,
  listWorkloads,
  setupWorkloadExtras,
  type WorkloadExtra,
} from "../api/client";
import type { Network } from "../api/phase4";
import type { StoragePool } from "../api/phase3";
import type { Workload } from "../api/phase5";
import { ErrorState } from "../components/EmptyState";
import { Field } from "../components/Field";
import { Icon } from "../components/Icon";
import {
  ContainerIPFields,
  containerIPBody,
  defaultContainerIPForm,
  summarizeContainerIP,
} from "../components/form/ContainerIPFields";
import { NetworkPicker } from "../components/form/NetworkPicker";
import { OsImagePicker } from "../components/form/OsImagePicker";
import { StoragePicker } from "../components/form/StoragePicker";
import { PageHeader } from "../components/PageHeader";
import { UxModeToggle } from "../components/UxModeToggle";
import { FALLBACK_IMAGE_PINS, kindLabel, osLabel } from "../labels";
import { canMutate, isAdmin, mutateHint } from "../rbac";
import { navigate } from "../router";
import { useSession } from "../session";
import { uxLevel } from "../ux";
import { isAdvanced, isExpert, type UxLevel } from "../ux-mode";
import { bytesFromGB, parseMemoryGB } from "../memory";
import { Checkbox } from "../ui/Checkbox";
import { SelectionCard } from "../ui/SelectionCard";
import { toggleExtra } from "../workloadExtras";

type Step = "basics" | "resources" | "network" | "extras" | "review" | "progress";

const STEPS: { id: Step; label: string }[] = [
  { id: "basics", label: "Basics" },
  { id: "resources", label: "Resources" },
  { id: "network", label: "Network" },
  { id: "extras", label: "Extras" },
  { id: "review", label: "Review" },
];

const PRESETS = [
  { id: "small", label: "Small", cpus: "1", memoryGB: "1", diskGB: "8" },
  { id: "medium", label: "Medium", cpus: "2", memoryGB: "2", diskGB: "16" },
  { id: "large", label: "Large", cpus: "4", memoryGB: "4", diskGB: "32" },
  { id: "custom", label: "Custom", cpus: "", memoryGB: "", diskGB: "" },
];

export function WorkloadCreatePage() {
  const session = useSession();
  const roles = session.status === "ready" ? session.user?.roles : undefined;
  const admin = isAdmin(roles);
  const mutate = canMutate(roles);
  const [mode, setMode] = useState<UxLevel>(uxLevel(session.status === "ready" ? session.user : null));
  const [step, setStep] = useState<Step>("basics");
  const [pools, setPools] = useState<StoragePool[]>([]);
  const [nets, setNets] = useState<Network[]>([]);
  const [pins, setPins] = useState<string[]>(FALLBACK_IMAGE_PINS);
  const [name, setName] = useState("alpine");
  const [pin, setPin] = useState(FALLBACK_IMAGE_PINS[0]);
  const [preset, setPreset] = useState("small");
  const [cpus, setCpus] = useState("1");
  const [memoryGB, setMemoryGB] = useState("1");
  const [diskGB, setDiskGB] = useState("8");
  const [poolID, setPoolID] = useState("");
  const [networkID, setNetworkID] = useState("");
  const [ip, setIP] = useState(defaultContainerIPForm);
  const [mac, setMAC] = useState("");
  const [privileged, setPrivileged] = useState(false);
  const [autostart, setAutostart] = useState(true);
  const [extras, setExtras] = useState<string[]>([]);
  const [catalog, setCatalog] = useState<WorkloadExtra[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [created, setCreated] = useState<Workload | null>(null);
  const [progress, setProgress] = useState<{ id: string; label: string; state: "done" | "active" | "todo" }[]>([]);

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

  useEffect(() => {
    void listWorkloadExtras(pin)
      .then((body) => setCatalog(body.items ?? []))
      .catch(() => setCatalog([]));
  }, [pin]);

  function applyPreset(id: string) {
    setPreset(id);
    const found = PRESETS.find((item) => item.id === id);
    if (!found || id === "custom") {
      return;
    }
    setCpus(found.cpus);
    setMemoryGB(found.memoryGB);
    setDiskGB(found.diskGB);
  }

  const pool = pools.find((p) => p.id === poolID);
  const net = nets.find((n) => n.id === networkID);
  const hint = mutateHint(roles);
  const grouped = useMemo(() => {
    const cats = ["containers", "development", "utilities"] as const;
    return cats.map((category) => ({
      category,
      label: category === "containers" ? "Containers" : category === "development" ? "Development" : "Utilities",
      items: catalog.filter((item) => item.category === category),
    }));
  }, [catalog]);

  async function onCreate() {
    setBusy(true);
    setError(null);
    setStep("progress");
    const extraRows = extras.map((id) => catalog.find((item) => item.id === id)?.name || id);
    setProgress([
      { id: "storage", label: "Storage allocated", state: "active" },
      { id: "rootfs", label: `${osLabel(pin)} rootfs prepared`, state: "todo" },
      { id: "network", label: "Network configured", state: "todo" },
      { id: "start", label: "Container started", state: "todo" },
      ...extraRows.map((label, i) => ({ id: extras[i], label: `Installing ${label}`, state: "todo" as const })),
      { id: "final", label: "Finalising", state: "todo" },
    ]);
    try {
      const createdRow = await createWorkload(
        {
          name,
          kind: "system-container",
          image_pin: pin,
          cpus: Number(cpus) || 1,
          memory_bytes: bytesFromGB(parseMemoryGB(memoryGB, 1)),
          disk_bytes: bytesFromGB(parseMemoryGB(diskGB, 8)),
          pool_id: poolID || undefined,
          network_id: networkID,
          ...containerIPBody(ip),
          ...(mac.trim() ? { mac: mac.trim() } : {}),
          privileged: admin ? privileged : false,
          autostart,
          extras,
        },
        `ui-create-${name}`,
      );
      setCreated(createdRow);
      const failed = new Set((createdRow.setup_warnings ?? []).map((w) => w.extra));
      setProgress((cur) =>
        cur.map((stepRow) => {
          if (failed.has(stepRow.id)) {
            return { ...stepRow, state: "todo", label: `${stepRow.label} failed` };
          }
          return { ...stepRow, state: "done" };
        }),
      );
    } catch (err) {
      setError(err instanceof Error ? err.message : "Create failed");
      setStep("review");
    } finally {
      setBusy(false);
    }
  }

  async function retryFailed() {
    if (!created?.id) {
      return;
    }
    const failed = (created.setup_warnings ?? []).map((w) => w.extra).filter((id) => id && id !== "setup");
    setBusy(true);
    try {
      const next = await setupWorkloadExtras(created.id, failed);
      setCreated(next);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Retry failed");
    } finally {
      setBusy(false);
    }
  }

  const warnings = created?.setup_warnings ?? [];

  return (
    <section className="page page-wide" aria-labelledby="create-ct-heading">
      <PageHeader
        id="create-ct-heading"
        title="Create system container"
        kicker="Official LXC images. Unprivileged is the default."
        actions={<UxModeToggle value={mode} onChange={setMode} />}
      />
      {error ? <ErrorState>{error}</ErrorState> : null}
      {step !== "progress" ? (
        <div className="wizard-layout">
          <nav className="wizard-steps" aria-label="Create steps">
            {STEPS.map((item, index) => (
              <button
                key={item.id}
                type="button"
                className={item.id === step ? "is-current" : ""}
                onClick={() => setStep(item.id)}
              >
                <span className="step-index">Step {index + 1}</span>
                {item.label}
              </button>
            ))}
          </nav>
          <div className="stack">
            {step === "basics" ? (
              <>
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
              </>
            ) : null}
            {step === "resources" ? (
              <>
                <div className="content-grid">
                  {PRESETS.map((item) => (
                    <SelectionCard
                      key={item.id}
                      title={item.label}
                      description={item.id === "custom" ? "Set CPU, memory, and disk yourself" : `${item.cpus} CPU · ${item.memoryGB} GB RAM · ${item.diskGB} GB disk`}
                      selected={preset === item.id}
                      onSelect={() => applyPreset(item.id)}
                    />
                  ))}
                </div>
                <div className="field-row">
                  <Field id="ct-cpus" label="CPUs" type="number" min={1} value={cpus} onChange={(e) => { setPreset("custom"); setCpus(e.target.value); }} />
                  <Field id="ct-mem" label="Memory" type="number" min={1} step={1} value={memoryGB} suffix="GB" onChange={(e) => { setPreset("custom"); setMemoryGB(e.target.value); }} />
                  <Field
                    id="ct-disk"
                    label="Disk"
                    type="number"
                    min={1}
                    step={1}
                    value={diskGB}
                    suffix="GB"
                    onChange={(e) => {
                      setPreset("custom");
                      setDiskGB(e.target.value);
                    }}
                    hint="Growing later is supported. Shrinking is not."
                  />
                </div>
                <StoragePicker id="ct-pool" label="Storage pool" pools={pools} value={poolID} onChange={setPoolID} expert={isExpert(mode)} />
              </>
            ) : null}
            {step === "network" ? (
              <>
                <NetworkPicker id="ct-net" label="Network" networks={nets} value={networkID} onChange={setNetworkID} expert={isExpert(mode)} />
                <ContainerIPFields id="ct-ip" form={ip} onChange={setIP} />
                {isAdvanced(mode) ? (
                  <Field
                    id="ct-mac"
                    label="MAC address"
                    value={mac}
                    onChange={(e) => setMAC(e.target.value)}
                    hint="Optional. Leave blank to generate."
                    placeholder="aa:bb:cc:dd:ee:ff"
                  />
                ) : null}
              </>
            ) : null}
            {step === "extras" ? (
              grouped.map((group) =>
                group.items.length === 0 ? null : (
                  <section key={group.category} className="stack">
                    <h2>{group.label}</h2>
                    <div className="extra-tile-grid">
                      {group.items.map((item) => (
                        <SelectionCard
                          key={item.id}
                          title={item.name}
                          description={item.available ? item.description : item.unavailable_reason || item.description}
                          selected={extras.includes(item.id)}
                          disabled={!item.available}
                          onSelect={() => setExtras((cur) => toggleExtra(cur, item.id, catalog))}
                        />
                      ))}
                    </div>
                  </section>
                ),
              )
            ) : null}
            {step === "review" ? (
              <>
                <dl className="review-grid">
                  <div>
                    <dt>Name</dt>
                    <dd>{name}</dd>
                  </div>
                  <div>
                    <dt>Guest OS</dt>
                    <dd>{osLabel(pin)}</dd>
                  </div>
                  <div>
                    <dt>Type</dt>
                    <dd>System container</dd>
                  </div>
                  <div>
                    <dt>CPU</dt>
                    <dd>{cpus}</dd>
                  </div>
                  <div>
                    <dt>RAM</dt>
                    <dd>{memoryGB} GB</dd>
                  </div>
                  <div>
                    <dt>Storage</dt>
                    <dd>
                      {diskGB} GB · {pool?.name || "no pool"}
                    </dd>
                  </div>
                  <div>
                    <dt>Network</dt>
                    <dd>
                      {net ? `${net.name} (${kindLabel(net.kind)})` : "no network"} · {summarizeContainerIP(ip)}
                    </dd>
                  </div>
                  <div>
                    <dt>Autostart</dt>
                    <dd>{autostart ? "Yes" : "No"}</dd>
                  </div>
                  <div>
                    <dt>Extras</dt>
                    <dd>{extras.length ? extras.map((id) => catalog.find((item) => item.id === id)?.name || id).join(", ") : "None"}</dd>
                  </div>
                </dl>
                {isAdvanced(mode) ? (
                  admin ? (
                    <Checkbox
                      id="ct-priv"
                      label="Privileged (admin only, audited)"
                      checked={privileged}
                      onChange={(e) => setPrivileged(e.target.checked)}
                    />
                  ) : (
                    <p className="field-hint">Privileged containers are admin-only.</p>
                  )
                ) : null}
                <Checkbox id="ct-auto" label="Start automatically with the host" checked={autostart} onChange={(e) => setAutostart(e.target.checked)} />
                {isExpert(mode) ? (
                  <pre className="code-block">
                    {JSON.stringify(
                      {
                        name,
                        kind: "system-container",
                        image_pin: pin,
                        cpus: Number(cpus) || 1,
                        extras,
                      },
                      null,
                      2,
                    )}
                  </pre>
                ) : null}
              </>
            ) : null}
            {hint ? <p className="field-hint">{hint}</p> : null}
            <div className="btn-row">
              {step !== "basics" ? (
                <button
                  className="btn btn-ghost"
                  type="button"
                  onClick={() => {
                    const idx = STEPS.findIndex((item) => item.id === step);
                    setStep(STEPS[Math.max(0, idx - 1)].id);
                  }}
                >
                  Back
                </button>
              ) : null}
              {step !== "review" ? (
                <button
                  className="btn btn-primary"
                  type="button"
                  onClick={() => {
                    const idx = STEPS.findIndex((item) => item.id === step);
                    setStep(STEPS[Math.min(STEPS.length - 1, idx + 1)].id);
                  }}
                >
                  Continue
                </button>
              ) : (
                <button className="btn btn-primary" type="button" disabled={busy || !mutate || !networkID} onClick={() => void onCreate()}>
                  <Icon name="create" size={14} />
                  Create system container
                </button>
              )}
            </div>
          </div>
        </div>
      ) : (
        <article className="stack">
          <h2>{warnings.length ? "Workload created with setup warnings" : "Creating"}</h2>
          <ol className="progress-steps">
            {progress.map((item) => (
              <li key={item.id} className={"progress-step is-" + item.state}>
                <span className="progress-mark" aria-hidden="true" />
                <span>{item.label}</span>
              </li>
            ))}
          </ol>
          {warnings.length ? (
            <div className="banner banner-warn" role="status">
              <p>The container exists. Optional extras failed:</p>
              <ul>
                {warnings.map((w) => (
                  <li key={w.extra}>
                    {catalog.find((item) => item.id === w.extra)?.name || w.extra}: {w.message}
                  </li>
                ))}
              </ul>
              <button className="btn btn-secondary" type="button" disabled={busy} onClick={() => void retryFailed()}>
                Retry failed setup
              </button>
            </div>
          ) : null}
          {created ? (
            <div className="btn-row">
              <button className="btn btn-primary" type="button" onClick={() => navigate(`/workloads/${created.id}`)}>
                Open workload
              </button>
            </div>
          ) : null}
        </article>
      )}
    </section>
  );
}
