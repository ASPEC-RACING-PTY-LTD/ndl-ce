import { useEffect, useMemo, useState } from "react";
import {
  analyzeMigration,
  cancelMigrationJob,
  cleanupMigrationJob,
  createMigrationPlan,
  createMigrationSource,
  discoverMigrationSource,
  getMigrationJob,
  importMigrationBundle,
  importMigrationDisk,
  listMigrationAdapters,
  listMigrationJobs,
  listMigrationModes,
  listMigrationSources,
  listNetworks,
  listPools,
  listWorkloads,
  retryMigrationJob,
  startMigrationJob,
} from "../api/client";
import { Field } from "../components/Field";
import { Link } from "../components/Link";
import { useSession } from "../session";
import { canMutate } from "../ux";

type Adapter = {
  id: string;
  label: string;
  role: string;
  discovery?: boolean;
  notes?: string;
  export_kind?: string;
  credential?: string;
};
type Mode = {
  id: string;
  label: string;
  consistency: string;
  source_safety: string;
  summary: string;
  requires_ack?: boolean;
  requires_stopped?: boolean;
  available?: boolean;
  unavailable_reason?: string;
  risks?: string[];
  benefits?: string[];
  source_mutation?: string;
};
type Source = { id: string; adapter: string; label: string; endpoint: string; has_credentials?: boolean };
type WorkloadRow = {
  source_id: string;
  name: string;
  kind: string;
  type_label?: string;
  running?: boolean;
  node?: string;
  cpus?: number;
  memory_bytes?: number;
  disk_bytes?: number;
  estimated_bytes?: number;
  firmware?: string;
  snapshots?: number;
  backups?: number;
  capabilities?: string[];
};
type ReviewRow = {
  name?: string;
  source?: string;
  destination?: string;
  destination_node?: string;
  migration_mode?: string;
  consistency?: string;
  source_safety?: string;
  storage?: Record<string, string>;
  network?: Record<string, string>;
  compatibility?: string;
  warnings?: { level?: string; message?: string }[];
  estimated_data?: number;
  source_changes?: string;
  start_after?: boolean;
};

const STEPS = ["Source", "Discovery", "Workloads", "Mapping", "Mode", "Compatibility", "Review", "Progress", "Verification"];

export function ImportExportPage() {
  const session = useSession();
  const roles = session.status === "ready" ? session.user?.roles : undefined;
  const mutate = canMutate(roles);
  const [tab, setTab] = useState<"import" | "export">("import");
  const [step, setStep] = useState(0);
  const [error, setError] = useState<string | null>(null);
  const [adapters, setAdapters] = useState<Adapter[]>([]);
  const [modes, setModes] = useState<Mode[]>([]);
  const [sources, setSources] = useState<Source[]>([]);
  const [adapter, setAdapter] = useState("proxmox");
  const [endpoint, setEndpoint] = useState("");
  const [token, setToken] = useState("");
  const [insecure, setInsecure] = useState(false);
  const [sourceID, setSourceID] = useState("");
  const [workloads, setWorkloads] = useState<WorkloadRow[]>([]);
  const [selected, setSelected] = useState<Record<string, boolean>>({});
  const [q, setQ] = useState("");
  const [kindFilter, setKindFilter] = useState("all");
  const [runFilter, setRunFilter] = useState("all");
  const [storageMap, setStorageMap] = useState("");
  const [networkMap, setNetworkMap] = useState("");
  const [modeByID, setModeByID] = useState<Record<string, string>>({});
  const [liveAck, setLiveAck] = useState<Record<string, boolean>>({});
  const [identityAck, setIdentityAck] = useState<Record<string, boolean>>({});
  const [compatItems, setCompatItems] = useState<Record<string, unknown>[]>([]);
  const [reviews, setReviews] = useState<ReviewRow[]>([]);
  const [job, setJob] = useState<Record<string, unknown> | null>(null);
  const [jobs, setJobs] = useState<Record<string, unknown>[]>([]);
  const [diskPath, setDiskPath] = useState("");
  const [diskName, setDiskName] = useState("imported");
  const [diskKind, setDiskKind] = useState("vm");
  const [cpus, setCpus] = useState("2");
  const [memory, setMemory] = useState("2147483648");
  const [firmware, setFirmware] = useState("bios");
  const [poolID, setPoolID] = useState("");
  const [netID, setNetID] = useState("");
  const [exportID, setExportID] = useState("");
  const [exportKind, setExportKind] = useState("nodal-bundle");
  const [startAfter, setStartAfter] = useState(false);
  const [learn, setLearn] = useState<string | null>(null);

  useEffect(() => {
    void Promise.all([
      listMigrationAdapters(),
      listMigrationModes(),
      listMigrationSources(),
      listPools(),
      listNetworks(),
      listMigrationJobs(),
      listWorkloads(),
    ])
      .then(([a, m, src, pools, nets, listedJobs, wls]) => {
        setAdapters((a.items ?? []) as Adapter[]);
        setModes((m.items ?? []) as Mode[]);
        setSources((src.items ?? []) as Source[]);
        setJobs((listedJobs.items ?? []) as Record<string, unknown>[]);
        const p = pools.items?.[0]?.id ?? "";
        const n = nets.items?.[0]?.id ?? "";
        if (p) setPoolID(p);
        if (n) setNetID(n);
        const first = (wls.items ?? [])[0] as { id?: string } | undefined;
        if (first?.id) setExportID(first.id);
      })
      .catch((err) => setError(err instanceof Error ? err.message : "Unavailable"));
  }, []);

  useEffect(() => {
    const id = typeof job?.id === "string" ? job.id : "";
    if (!id || (job?.state !== "running" && job?.state !== "canceling")) {
      return;
    }
    const t = window.setInterval(() => {
      void getMigrationJob(id)
        .then((row) => {
          setJob(row);
          if (row.state === "succeeded") setStep(8);
        })
        .catch(() => undefined);
    }, 1000);
    return () => window.clearInterval(t);
  }, [job]);

  const chosen = useMemo(() => workloads.filter((w) => selected[w.source_id]), [workloads, selected]);
  const currentAdapter = adapters.find((a) => a.id === adapter);

  function mapping(): { storage: Record<string, string>; network: Record<string, string> } {
    const storage: Record<string, string> = {};
    const network: Record<string, string> = {};
    for (const line of storageMap.split("\n")) {
      const [k, v] = line.split("->").map((s) => s.trim());
      if (k && v) storage[k] = v;
    }
    for (const line of networkMap.split("\n")) {
      const [k, v] = line.split("->").map((s) => s.trim());
      if (k && v) network[k] = v;
    }
    return { storage, network };
  }

  const filtered = workloads.filter((w) => {
    if (q && !`${w.name} ${w.source_id} ${w.node ?? ""}`.toLowerCase().includes(q.toLowerCase())) return false;
    if (kindFilter !== "all" && w.kind !== kindFilter) return false;
    if (runFilter === "running" && !w.running) return false;
    if (runFilter === "stopped" && w.running) return false;
    return true;
  });

  function planBody() {
    const modesPayload: Record<string, string> = {};
    const ack: Record<string, boolean> = {};
    const ident: Record<string, boolean> = {};
    for (const w of chosen) {
      modesPayload[w.source_id] = modeByID[w.source_id];
      if (liveAck[w.source_id]) ack[w.source_id] = true;
      if (identityAck[w.source_id]) ident[w.source_id] = true;
    }
    return {
      source_id: sourceID,
      selected: chosen.map((w) => w.source_id),
      modes: modesPayload,
      live_ack: ack,
      identity_conflict_ack: ident,
      mapping: mapping(),
      pool_id: poolID,
      network_id: netID,
      start_after: startAfter,
    };
  }

  async function connectSource() {
    setError(null);
    try {
      const created = (await createMigrationSource({ adapter, endpoint, token, insecure, label: adapter })) as Source;
      setSourceID(created.id);
      setToken("");
      setSources((cur) => [...cur, created]);
      setStep(1);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Connect failed");
    }
  }

  async function discover() {
    setError(null);
    try {
      const d = (await discoverMigrationSource(sourceID)) as { workloads?: WorkloadRow[] };
      setWorkloads(d.workloads ?? []);
      setStep(2);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Discover failed");
    }
  }

  async function runCompat() {
    setError(null);
    try {
      for (const w of chosen) {
        if (!modeByID[w.source_id]) {
          setError("Select a migration mode for each workload. No-dal will not choose one for you.");
          return;
        }
        const mode = modes.find((m) => m.id === modeByID[w.source_id]);
        if (mode && mode.available === false) {
          setError(`${mode.label} is unavailable. ${mode.unavailable_reason ?? ""}`.trim());
          return;
        }
        if (modeByID[w.source_id] === "live" && !liveAck[w.source_id]) {
          setError("Live migration requires acknowledgement: I understand the risks of live migration.");
          return;
        }
      }
      const body = planBody();
      const compat = await analyzeMigration(body);
      setCompatItems(((compat.items as Record<string, unknown>[]) ?? []) as Record<string, unknown>[]);
      const planned = await createMigrationPlan(body);
      setReviews(((planned.review as ReviewRow[]) ?? []) as ReviewRow[]);
      setStep(6);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Compatibility failed");
    }
  }

  async function start() {
    setError(null);
    try {
      for (const w of chosen) {
        if (modeByID[w.source_id] === "live" && !liveAck[w.source_id]) {
          setError("Live migration requires acknowledgement: I understand the risks of live migration.");
          return;
        }
      }
      const started = await startMigrationJob(planBody());
      setJob(started);
      setStep(7);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Start failed");
    }
  }

  async function importFile() {
    setError(null);
    try {
      const isBundle = diskPath.endsWith("manifest.json") || adapter === "nodal";
      const body = {
        path: diskPath,
        name: diskName,
        kind: diskKind,
        mode: adapter === "backup" ? "backup" : "disk",
        cpus: Number(cpus),
        memory_bytes: Number(memory),
        firmware,
        pool_id: poolID,
        network_id: netID,
        start_after: startAfter,
      };
      const started = isBundle ? await importMigrationBundle(body) : await importMigrationDisk(body);
      setJob(started);
      setStep(7);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Import failed");
    }
  }

  const status = job?.status as Record<string, unknown> | undefined;
  const reports = (status?.reports as Record<string, unknown>[] | undefined) ?? [];

  return (
    <section className="page page-wide" aria-labelledby="mig-heading">
      <header className="page-header">
        <h1 id="mig-heading">Import / Export</h1>
        <p className="page-kicker">
          Copy-first. Source destruction is not a migration operation. A completed migration means: Migration verified.
          Source remains unchanged.
        </p>
      </header>
      <p>
        Library qcow2 import remains at <Link href="/workloads/import">Import VM</Link>. CE does not require Cloud.
      </p>
      <div className="inline-actions">
        <button type="button" className={tab === "import" ? "btn" : "btn btn-secondary"} onClick={() => setTab("import")}>
          Import
        </button>
        <button type="button" className={tab === "export" ? "btn" : "btn btn-secondary"} onClick={() => setTab("export")}>
          Export
        </button>
      </div>
      {error ? (
        <p className="banner banner-error" role="alert">
          {error}
        </p>
      ) : null}
      <p className="banner" role="status">
        SOURCE SAFETY PROTECTED. No-dal does not delete or clean up the source workload.
      </p>
      {!mutate ? <p>Connecting sources and starting transfers requires operator or admin.</p> : null}

      {tab === "export" ? (
        <article className="panel">
          <h2>Export</h2>
          <p>No-dal helps you leave. Portable bundle is an open documented format. Compatible packages are labeled as such.</p>
          <Field id="ex-id" label="Workload ID" value={exportID} onChange={(e) => setExportID(e.target.value)} />
          <label htmlFor="ex-kind">
            Format
            <select id="ex-kind" className="field-input" value={exportKind} onChange={(e) => setExportKind(e.target.value)}>
              <option value="nodal-bundle">No-dal portable bundle (DIRECT EXPORT of the open format)</option>
              <option value="vm-image">VM image (DIRECT EXPORT of converted disks)</option>
              <option value="container-archive">Container archive (DIRECT EXPORT of rootfs tar)</option>
              <option value="ovf">OVF compatible package</option>
              <option value="proxmox">Proxmox compatible package</option>
            </select>
          </label>
          <button
            type="button"
            className="btn"
            disabled={!mutate}
            onClick={() => {
              void startMigrationJob({ direction: "export", workload_id: exportID, export_kind: exportKind, adapter: "nodal", mode: "disk" })
                .then((row) => {
                  setJob(row);
                  setTab("import");
                  setStep(7);
                })
                .catch((err) => setError(err instanceof Error ? err.message : "Export failed"));
            }}
          >
            Start export
          </button>
        </article>
      ) : null}

      {tab === "import" ? (
        <>
          <ol className="mig-steps">
            {STEPS.map((label, i) => (
              <li key={label}>
                <button type="button" className={i === step ? "btn" : "btn btn-secondary"} onClick={() => setStep(i)}>
                  {i + 1}. {label}
                </button>
              </li>
            ))}
          </ol>

          {step === 0 ? (
            <article className="panel">
              <h2>Source</h2>
              <label htmlFor="mig-adapter">
                Adapter
                <select id="mig-adapter" className="field-input" value={adapter} onChange={(e) => setAdapter(e.target.value)}>
                  {adapters.map((a) => (
                    <option key={a.id} value={a.id}>
                      {a.label}
                    </option>
                  ))}
                </select>
              </label>
              {currentAdapter?.notes ? <p>{currentAdapter.notes}</p> : null}
              {currentAdapter?.credential ? <p>{currentAdapter.credential}</p> : null}
              {adapter === "proxmox" ? (
                <>
                  <Field id="mig-ep" label="Endpoint" value={endpoint} onChange={(e) => setEndpoint(e.target.value)} />
                  <Field id="mig-tok" label="API token" value={token} onChange={(e) => setToken(e.target.value)} />
                  <label>
                    <input type="checkbox" checked={insecure} onChange={(e) => setInsecure(e.target.checked)} /> Allow HTTP
                    (disclosed insecure)
                  </label>
                  <button type="button" className="btn" disabled={!mutate} onClick={() => void connectSource()}>
                    Connect
                  </button>
                </>
              ) : (
                <>
                  <Field id="mig-path" label="Path" value={diskPath} onChange={(e) => setDiskPath(e.target.value)} />
                  <Field id="mig-name" label="Name" value={diskName} onChange={(e) => setDiskName(e.target.value)} />
                  <label htmlFor="mig-kind">
                    Kind
                    <select id="mig-kind" className="field-input" value={diskKind} onChange={(e) => setDiskKind(e.target.value)}>
                      <option value="vm">Virtual machine</option>
                      <option value="system-container">System container</option>
                    </select>
                  </label>
                  <Field id="mig-cpu" label="vCPU" value={cpus} onChange={(e) => setCpus(e.target.value)} />
                  <Field id="mig-mem" label="Memory bytes" value={memory} onChange={(e) => setMemory(e.target.value)} />
                  <label htmlFor="mig-fw">
                    Firmware
                    <select id="mig-fw" className="field-input" value={firmware} onChange={(e) => setFirmware(e.target.value)}>
                      <option value="bios">BIOS</option>
                      <option value="uefi">UEFI</option>
                    </select>
                  </label>
                  <label>
                    <input type="checkbox" checked={startAfter} onChange={(e) => setStartAfter(e.target.checked)} /> Start
                    destination after successful migration
                  </label>
                  <p>A disk image does not contain missing hardware. Supply CPU, RAM, firmware, and network.</p>
                  <button type="button" className="btn" disabled={!mutate} onClick={() => void importFile()}>
                    Import file
                  </button>
                </>
              )}
              {sources.length > 0 ? (
                <ul className="plain-list">
                  {sources.map((s) => (
                    <li key={s.id}>
                      <button
                        type="button"
                        className="btn btn-secondary"
                        onClick={() => {
                          setSourceID(s.id);
                          setAdapter(s.adapter);
                          setStep(1);
                        }}
                      >
                        {s.label} {s.endpoint}
                      </button>
                    </li>
                  ))}
                </ul>
              ) : null}
            </article>
          ) : null}

          {step === 1 ? (
            <article className="panel">
              <h2>Discovery</h2>
              <p>Discovery does not begin migration.</p>
              <button type="button" className="btn" disabled={!mutate || !sourceID} onClick={() => void discover()}>
                Discover
              </button>
            </article>
          ) : null}

          {step === 2 ? (
            <article className="panel">
              <h2>Workloads</h2>
              <div className="inline-actions">
                <input className="field-input" placeholder="Search" value={q} onChange={(e) => setQ(e.target.value)} />
                <select className="field-input" value={kindFilter} onChange={(e) => setKindFilter(e.target.value)}>
                  <option value="all">All types</option>
                  <option value="vm">VM</option>
                  <option value="system-container">System container</option>
                </select>
                <select className="field-input" value={runFilter} onChange={(e) => setRunFilter(e.target.value)}>
                  <option value="all">Any state</option>
                  <option value="running">Running</option>
                  <option value="stopped">Stopped</option>
                </select>
                <button
                  type="button"
                  className="btn btn-secondary"
                  onClick={() => {
                    const next = { ...selected };
                    for (const w of filtered) next[w.source_id] = true;
                    setSelected(next);
                  }}
                >
                  Select all
                </button>
              </div>
              <div className="table-wrap">
                <table>
                  <thead>
                    <tr>
                      <th> </th>
                      <th>Name</th>
                      <th>Type</th>
                      <th>State</th>
                      <th>CPU</th>
                      <th>Memory</th>
                      <th>Disk</th>
                      <th>Node</th>
                      <th>Caps</th>
                    </tr>
                  </thead>
                  <tbody>
                    {filtered.map((w) => (
                      <tr key={w.source_id}>
                        <td>
                          <input
                            type="checkbox"
                            checked={Boolean(selected[w.source_id])}
                            onChange={(e) => setSelected({ ...selected, [w.source_id]: e.target.checked })}
                            aria-label={`Select ${w.name}`}
                          />
                        </td>
                        <td>
                          {w.name} <code>{w.source_id}</code>
                        </td>
                        <td>{w.type_label ?? w.kind}</td>
                        <td>{w.running ? "Running" : "Stopped"}</td>
                        <td>{w.cpus ?? ""}</td>
                        <td>{w.memory_bytes ? `${Math.round(w.memory_bytes / (1 << 30))} GB` : ""}</td>
                        <td>{w.estimated_bytes ? `${Math.round(w.estimated_bytes / (1 << 30))} GB` : ""}</td>
                        <td>{w.node}</td>
                        <td>{(w.capabilities ?? []).join(", ")}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
              <button type="button" className="btn" disabled={chosen.length === 0} onClick={() => setStep(3)}>
                Continue
              </button>
            </article>
          ) : null}

          {step === 3 ? (
            <article className="panel">
              <h2>Mapping</h2>
              <p>Bulk maps apply to the selection. One mapping per line as SOURCE -&gt; DEST. Never guess a destructive mapping.</p>
              <label htmlFor="map-st">
                Storage
                <textarea id="map-st" className="field-input" rows={4} value={storageMap} onChange={(e) => setStorageMap(e.target.value)} />
              </label>
              <label htmlFor="map-net">
                Network
                <textarea id="map-net" className="field-input" rows={4} value={networkMap} onChange={(e) => setNetworkMap(e.target.value)} />
              </label>
              <button type="button" className="btn" onClick={() => setStep(4)}>
                Continue
              </button>
            </article>
          ) : null}

          {step === 4 ? (
            <article className="panel">
              <h2>Migration mode</h2>
              <p>You choose the mode. No-dal will not silently fall back.</p>
              {chosen.map((w) => (
                <fieldset key={w.source_id}>
                  <legend>
                    {w.name} ({w.source_id})
                  </legend>
                  {modes.map((m) => (
                    <label key={m.id} className={m.available === false ? "mig-mode-unavail" : undefined}>
                      <input
                        type="radio"
                        name={`mode-${w.source_id}`}
                        checked={modeByID[w.source_id] === m.id}
                        disabled={m.available === false}
                        onChange={() => setModeByID({ ...modeByID, [w.source_id]: m.id })}
                      />
                      {m.label} {m.consistency}
                      {m.requires_ack ? " NO GUARANTEES" : ""}
                      {m.available === false ? " Unavailable" : ""}
                      <button type="button" className="btn btn-secondary" onClick={() => setLearn(learn === m.id ? null : m.id)}>
                        Learn why
                      </button>
                      {learn === m.id ? (
                        <p>
                          {m.summary} {(m.benefits ?? m.risks ?? []).join(". ")} {m.unavailable_reason} {m.source_mutation}
                        </p>
                      ) : null}
                    </label>
                  ))}
                  {modeByID[w.source_id] === "live" ? (
                    <label>
                      <input
                        type="checkbox"
                        checked={Boolean(liveAck[w.source_id])}
                        onChange={(e) => setLiveAck({ ...liveAck, [w.source_id]: e.target.checked })}
                      />
                      I understand the risks of live migration.
                    </label>
                  ) : null}
                </fieldset>
              ))}
              <label>
                <input type="checkbox" checked={startAfter} onChange={(e) => setStartAfter(e.target.checked)} /> Start
                destination after successful migration
              </label>
              <button type="button" className="btn" disabled={chosen.some((w) => !modeByID[w.source_id])} onClick={() => setStep(5)}>
                Continue
              </button>
            </article>
          ) : null}

          {step === 5 ? (
            <article className="panel">
              <h2>Compatibility</h2>
              <p>Risk, compatibility, and source safety are separate.</p>
              <button type="button" className="btn" onClick={() => void runCompat()}>
                Check compatibility
              </button>
              {compatItems.map((row) => (
                <div key={String(row.source_id)} className="mig-review">
                  <strong>
                    {String(row.name)} {String(row.compatibility)}
                  </strong>
                  <p>
                    Consistency {String(row.consistency)}. Source safety {String(row.source_safety)}. Mode {String(row.mode)}.
                  </p>
                </div>
              ))}
            </article>
          ) : null}

          {step === 6 ? (
            <article className="panel">
              <h2>Review</h2>
              <p>This review is generated from the actual migration plan.</p>
              {reviews.map((rev) => (
                <dl key={String(rev.name)} className="mig-review">
                  <dt>Workload</dt>
                  <dd>{rev.name}</dd>
                  <dt>Source</dt>
                  <dd>{rev.source}</dd>
                  <dt>Destination</dt>
                  <dd>
                    {rev.destination} {rev.destination_node}
                  </dd>
                  <dt>Migration mode</dt>
                  <dd>{rev.migration_mode}</dd>
                  <dt>Consistency</dt>
                  <dd>{rev.consistency}</dd>
                  <dt>Source safety</dt>
                  <dd>{rev.source_safety}</dd>
                  <dt>Storage</dt>
                  <dd>{Object.entries(rev.storage ?? {}).map(([k, v]) => `${k} -> ${v}`).join(", ") || "none"}</dd>
                  <dt>Network</dt>
                  <dd>{Object.entries(rev.network ?? {}).map(([k, v]) => `${k} -> ${v}`).join(", ") || "none"}</dd>
                  <dt>Compatibility</dt>
                  <dd>
                    {rev.compatibility} {(rev.warnings ?? []).length} warnings
                  </dd>
                  <dt>Estimated data</dt>
                  <dd>{rev.estimated_data ? `${Math.round(Number(rev.estimated_data) / (1 << 30))} GB` : "unknown"}</dd>
                  <dt>Source changes</dt>
                  <dd>{rev.source_changes}</dd>
                </dl>
              ))}
              {chosen.some((w) => w.running && startAfter) ? (
                <label>
                  <input
                    type="checkbox"
                    checked={chosen.every((w) => !w.running || identityAck[w.source_id])}
                    onChange={(e) => {
                      const next = { ...identityAck };
                      for (const w of chosen) if (w.running) next[w.source_id] = e.target.checked;
                      setIdentityAck(next);
                    }}
                  />
                  NETWORK IDENTITY CONFLICT. The source may remain online with the same MAC. I accept starting the destination.
                </label>
              ) : null}
              <button type="button" className="btn" disabled={!mutate || reviews.length === 0} onClick={() => void start()}>
                Start migration
              </button>
            </article>
          ) : null}

          {step === 7 || step === 8 ? (
            <article className="panel">
              <h2>{step === 8 ? "Verification" : "Progress"}</h2>
              {job ? (
                <>
                  <p>
                    State {String(job.state)} stage {String(job.stage)}. Source untouched: {String(job.source_untouched ?? true)}.
                  </p>
                  {status ? (
                    <p>
                      {String(status.workload ?? "")} {String(status.percent ?? 0)}% {String(status.message ?? "")}
                    </p>
                  ) : null}
                  {job.state === "running" ? (
                    <button type="button" className="btn btn-secondary" onClick={() => void cancelMigrationJob(String(job.id)).then(setJob)}>
                      Cancel
                    </button>
                  ) : null}
                  {job.state === "failed" || job.state === "canceled" ? (
                    <div className="inline-actions">
                      <button type="button" className="btn" onClick={() => void retryMigrationJob(String(job.id)).then(setJob)}>
                        Retry
                      </button>
                      <button
                        type="button"
                        className="btn btn-secondary"
                        onClick={() => void cleanupMigrationJob(String(job.id)).then((row) => setJob({ ...job, ...row }))}
                      >
                        Clean No-dal staging
                      </button>
                    </div>
                  ) : null}
                </>
              ) : (
                <p>No active job.</p>
              )}
              {job?.state === "succeeded" ? (
                <>
                  <p role="status">Migration verified. Source remains unchanged.</p>
                  {reports.map((rep) => (
                    <dl key={String(rep.name)} className="mig-review">
                      <dt>Workload</dt>
                      <dd>{String(rep.name)}</dd>
                      <dt>Destination</dt>
                      <dd>{String(rep.destination)}</dd>
                      <dt>Observed</dt>
                      <dd>{((rep.observed as string[]) ?? []).join(", ")}</dd>
                      <dt>Unobserved</dt>
                      <dd>{((rep.unobserved as string[]) ?? []).join(", ")}</dd>
                      <dt>Source</dt>
                      <dd>{String(rep.source_state)}</dd>
                    </dl>
                  ))}
                </>
              ) : null}
            </article>
          ) : null}
        </>
      ) : null}

      <article className="panel">
        <h2>Recent jobs</h2>
        <ul className="plain-list">
          {jobs.map((j) => (
            <li key={String(j.id)}>
              {String(j.adapter)} {String(j.direction)} {String(j.state)} {String(j.id)}
            </li>
          ))}
        </ul>
      </article>
    </section>
  );
}
