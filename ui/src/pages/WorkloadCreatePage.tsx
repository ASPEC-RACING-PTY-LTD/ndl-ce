import { useEffect, useMemo, useState } from "react";
import {
  changeWorkloadExtensions,
  createWorkload,
  getCfInventory,
  getWorkloadCloudflare,
  listNetworks,
  listPools,
  listWorkloadExtensions,
  listWorkloadExtras,
  listWorkloads,
  publishWorkload,
  type CfInventory,
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
import { NetworkPicker, preferredGuestNetwork } from "../components/form/NetworkPicker";
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
  // Extras install in the background after create; their state is polled.
  const [extState, setExtState] = useState<Record<string, { state: string; message?: string }>>({});
  const [extWatch, setExtWatch] = useState(false);
  // Optional: publish through Cloudflare once the container has an address.
  const [publish, setPublish] = useState(false);
  const [cfInv, setCfInv] = useState<CfInventory | null>(null);
  const [cfTunnel, setCfTunnel] = useState("");
  const [cfHost, setCfHost] = useState("");
  const [cfProto, setCfProto] = useState("http");
  const [cfPort, setCfPort] = useState("80");
  const [publishState, setPublishState] = useState<{ state: "todo" | "active" | "done" | "failed"; message?: string } | null>(null);

  useEffect(() => {
    if (!extWatch || !created?.id) {
      return;
    }
    let stop = false;
    const tick = async () => {
      try {
        const res = await listWorkloadExtensions(created.id);
        const next: Record<string, { state: string; message?: string }> = {};
        for (const x of res.items) {
          next[x.id] = { state: x.state, message: x.message };
        }
        if (!stop) {
          setExtState(next);
          if (!res.busy && !res.items.some((x) => x.state === "installing")) {
            setExtWatch(false);
          }
        }
      } catch {
        /* keep polling */
      }
    };
    void tick();
    const timer = window.setInterval(() => void tick(), 3000);
    return () => {
      stop = true;
      window.clearInterval(timer);
    };
  }, [extWatch, created?.id]);

  useEffect(() => {
    setProgress((cur) =>
      cur.map((row) => {
        const x = extState[row.id];
        if (!x) {
          return row;
        }
        const label = catalog.find((c) => c.id === row.id)?.name || row.id;
        if (x.state === "installed") {
          return { ...row, state: "done", label: `${label} installed` };
        }
        if (x.state === "failed") {
          return { ...row, state: "todo", label: `${label} failed${x.message ? `: ${x.message}` : ""}` };
        }
        return { ...row, state: "active", label: `Installing ${label}` };
      }),
    );
  }, [extState, catalog]);

  useEffect(() => {
    if (!publish || cfInv) {
      return;
    }
    void getCfInventory()
      .then((inv) => {
        setCfInv(inv);
        setCfTunnel((inv.tunnels ?? []).find((t) => t.remote_managed)?.id ?? "");
      })
      .catch(() => setCfInv({ fetched_at: "", refreshing: false, accounts: [], tunnels: [], routes: [], dns: [], errors: [] }));
  }, [publish, cfInv]);

  async function publishWhenReady(id: string) {
    const tunnel = (cfInv?.tunnels ?? []).find((t) => t.id === cfTunnel);
    if (!tunnel) {
      return;
    }
    setPublishState({ state: "active", message: "Waiting for the container's address" });
    for (let i = 0; i < 30; i++) {
      try {
        const info = await getWorkloadCloudflare(id);
        if (info.current_ip) {
          const res = await publishWorkload(id, {
            account_id: tunnel.account_tag,
            tunnel_id: tunnel.id,
            hostname: cfHost.trim(),
            protocol: cfProto,
            host: info.current_ip,
            port: Number(cfPort) || 0,
            dns: true,
          });
          setPublishState({ state: "done", message: `${res.hostname} goes to ${res.service}` });
          return;
        }
      } catch (err) {
        setPublishState({ state: "failed", message: err instanceof Error ? err.message : "Publishing failed" });
        return;
      }
      await new Promise((r) => window.setTimeout(r, 3000));
    }
    setPublishState({ state: "failed", message: "The container did not get an address in time; publish it from the workload page" });
  }

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
        const preferred = preferredGuestNetwork(ready);
        if (preferred) {
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
      // The container is up; extras keep installing in the background and are
      // marked done or failed as the server reports them.
      setProgress((cur) => cur.map((stepRow) => (extras.includes(stepRow.id) ? { ...stepRow, state: "active" } : { ...stepRow, state: "done" })));
      if (createdRow.setup_status === "installing") {
        setExtWatch(true);
      }
      if (publish && cfHost.trim() && cfTunnel) {
        void publishWhenReady(createdRow.id);
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : "Create failed");
      setStep("review");
    } finally {
      setBusy(false);
    }
  }

  const failedExtras = Object.entries(extState)
    .filter(([id, x]) => x.state === "failed" && extras.includes(id))
    .map(([id, x]) => ({ extra: id, message: x.message ?? "failed" }));

  async function retryFailed() {
    if (!created?.id) {
      return;
    }
    const ids = failedExtras.map((w) => w.extra);
    setBusy(true);
    try {
      await changeWorkloadExtensions(created.id, { action: "install", extras: ids.length ? ids : extras });
      setExtWatch(true);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Retry failed");
    } finally {
      setBusy(false);
    }
  }

  const warnings = [...(created?.setup_warnings ?? []), ...(extWatch ? [] : failedExtras)];

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
                <Checkbox
                  id="ct-publish"
                  label="Publish it through Cloudflare once it has an address (optional)"
                  checked={publish}
                  onChange={(e) => setPublish(e.target.checked)}
                />
                {publish ? (
                  cfInv && (cfInv.tunnels ?? []).some((t) => t.remote_managed) ? (
                    <div className="oci-grid">
                      <div className="field">
                        <label className="field-label" htmlFor="ct-cf-tunnel">
                          Tunnel
                        </label>
                        <select id="ct-cf-tunnel" className="field-input" value={cfTunnel} onChange={(e) => setCfTunnel(e.target.value)}>
                          {(cfInv.tunnels ?? [])
                            .filter((t) => t.remote_managed)
                            .map((t) => (
                              <option key={t.id} value={t.id}>
                                {t.name} · {t.account_name}
                              </option>
                            ))}
                        </select>
                      </div>
                      <Field id="ct-cf-host" label="Public hostname" value={cfHost} placeholder="app.example.com" onChange={(e) => setCfHost(e.target.value)} />
                      <div className="field">
                        <label className="field-label" htmlFor="ct-cf-proto">
                          Protocol
                        </label>
                        <select id="ct-cf-proto" className="field-input" value={cfProto} onChange={(e) => setCfProto(e.target.value)}>
                          <option value="http">HTTP</option>
                          <option value="https">HTTPS</option>
                          <option value="tcp">TCP</option>
                          <option value="ssh">SSH</option>
                        </select>
                      </div>
                      <Field id="ct-cf-port" label="App port" inputMode="numeric" value={cfPort} onChange={(e) => setCfPort(e.target.value.replace(/[^0-9]/g, ""))} />
                    </div>
                  ) : (
                    <p className="field-hint">{cfInv ? "No dashboard-managed tunnel is available; connect Cloudflare first." : "Reading Cloudflare"}</p>
                  )
                ) : null}
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
          <h2>{warnings.length ? "Workload created with setup warnings" : extWatch ? "Created; installing extensions" : created ? "Created" : "Creating"}</h2>
          <ol className="progress-steps">
            {progress.map((item) => (
              <li key={item.id} className={"progress-step is-" + item.state}>
                <span className="progress-mark" aria-hidden="true" />
                <span>{item.label}</span>
              </li>
            ))}
          </ol>
          {extWatch ? (
            <p className="banner" role="status">
              Extensions install in the background and keep going if you leave this page. Their progress is also on the workload's Extensions page.
            </p>
          ) : null}
          {publishState ? (
            <p className={"banner " + (publishState.state === "failed" ? "banner-warn" : publishState.state === "done" ? "banner-ok" : "")} role="status">
              Cloudflare: {publishState.message}
            </p>
          ) : null}
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
              {extras.length ? (
                <button className="btn btn-secondary" type="button" onClick={() => navigate(`/workloads/${created.id}/extensions`)}>
                  Extensions
                </button>
              ) : null}
            </div>
          ) : null}
        </article>
      )}
    </section>
  );
}
