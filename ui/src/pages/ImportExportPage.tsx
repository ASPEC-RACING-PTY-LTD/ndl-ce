import { useEffect, useMemo, useState } from "react";
import {
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
import { MigrationJobDetail } from "../components/MigrationJobDetail";
import { StatusBadge } from "../components/StatusBadge";
import {
  findActiveMigrationJob,
  isActiveMigrationState,
  isRetryableMigrationState,
  jobHeadline,
  jobStateOf,
  listedMigrationJobs,
} from "../migration/jobView";
import { PVE_TOKEN_EXAMPLE, PVE_TOKEN_FORMAT, pveTokenError } from "../migration/pveToken";
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
type DestPool = { id: string; name: string; status?: string };
type DestNet = { id: string; name: string; bridge_name?: string; status?: string };
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
  storage?: string[];
  networks?: string[];
};
type Finding = { level?: string; code?: string; message?: string };
type Strategy = {
  id: string;
  label: string;
  consistency: string;
  summary: string;
  recommended?: boolean;
  available?: boolean;
  unavailable_reason?: string;
};
type ReviewRow = {
  source_id?: string;
  name?: string;
  source?: string;
  destination?: string;
  destination_node?: string;
  migration_mode?: string;
  consistency?: string;
  source_safety?: string;
  storage?: Record<string, string>;
  network?: Record<string, string>;
  ipv4?: string;
  ipv6?: string;
  dns?: string;
  network_summary?: string;
  compatibility?: string;
  warnings?: Finding[];
  estimated_data?: number;
  source_changes?: string;
  start_after?: boolean;
};
type PlanItem = { source_id?: string; name?: string; mode?: string; findings?: Finding[]; compatibility?: string };

const PHASES = ["Select Workloads", "Review", "Progress", "Verification"] as const;
type Phase = "source" | "select" | "review" | "progress" | "verify";

const DEFAULT_STRATEGIES: Strategy[] = [
  {
    id: "consistent",
    label: "Consistent copy",
    consistency: "SAFE",
    summary: "Accept downtime. Prefer Local Host on this machine, then Offline, then an existing backup, then disk import.",
    recommended: true,
    available: true,
  },
  {
    id: "leave-running",
    label: "Leave sources running",
    consistency: "SOURCE SAFE",
    summary: "Prefer backups or disk import so running guests can stay running.",
    available: true,
  },
  {
    id: "backup",
    label: "Existing backups",
    consistency: "SAFE",
    summary: "Import captured backups only. Workloads without a backup are blocked.",
    available: true,
  },
];

function destLabel(id: string, pools: DestPool[], nets: DestNet[]): string {
  const pool = pools.find((p) => p.id === id);
  if (pool) return pool.name || id;
  const net = nets.find((n) => n.id === id);
  if (net) return net.name || net.bridge_name || id;
  return id;
}

function mapLines(rec: Record<string, string> | undefined, pools: DestPool[], nets: DestNet[]): string {
  return Object.entries(rec ?? {})
    .map(([src, dest]) => `${src} -> ${destLabel(dest, pools, nets)}`)
    .join(", ");
}

function findingNeedsAction(f: Finding): boolean {
  const level = (f.level ?? "").toUpperCase();
  return (
    level === "REQUIRES MAPPING" ||
    level === "BLOCKED" ||
    level === "UNSUPPORTED" ||
    f.code === "source-must-stop" ||
    f.code === "mode"
  );
}

export function ImportExportPage() {
  const session = useSession();
  const roles = session.status === "ready" ? session.user?.roles : undefined;
  const mutate = canMutate(roles);
  const [tab, setTab] = useState<"import" | "export">("import");
  const [phase, setPhase] = useState<Phase>("source");
  const [error, setError] = useState<string | null>(null);
  const [adapters, setAdapters] = useState<Adapter[]>([]);
  const [modes, setModes] = useState<Mode[]>([]);
  const [strategies, setStrategies] = useState<Strategy[]>(DEFAULT_STRATEGIES);
  const [strategy, setStrategy] = useState("consistent");
  const [modeOverride, setModeOverride] = useState<Record<string, string>>({});
  const [sources, setSources] = useState<Source[]>([]);
  const [pools, setPools] = useState<DestPool[]>([]);
  const [nets, setNets] = useState<DestNet[]>([]);
  const [adapter, setAdapter] = useState("proxmox");
  const [endpoint, setEndpoint] = useState("");
  const [token, setToken] = useState("");
  const [tokenError, setTokenError] = useState<string | null>(null);
  const [insecure, setInsecure] = useState(false);
  const [sourceID, setSourceID] = useState("");
  const [workloads, setWorkloads] = useState<WorkloadRow[]>([]);
  const [selected, setSelected] = useState<Record<string, boolean>>({});
  const [q, setQ] = useState("");
  const [kindFilter, setKindFilter] = useState("all");
  const [runFilter, setRunFilter] = useState("all");
  const [storageOverride, setStorageOverride] = useState<Record<string, string>>({});
  const [networkOverride, setNetworkOverride] = useState<Record<string, string>>({});
  const [modeByID, setModeByID] = useState<Record<string, string>>({});
  const [liveAck, setLiveAck] = useState<Record<string, boolean>>({});
  const [identityAck, setIdentityAck] = useState<Record<string, boolean>>({});
  const [reviews, setReviews] = useState<ReviewRow[]>([]);
  const [planItems, setPlanItems] = useState<PlanItem[]>([]);
  const [job, setJob] = useState<Record<string, unknown> | null>(null);
  const [jobs, setJobs] = useState<Record<string, unknown>[]>([]);
  const [openJobId, setOpenJobId] = useState<string | null>(null);
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
  const [advanced, setAdvanced] = useState(false);
  const [busy, setBusy] = useState(false);

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
      .then(([a, m, src, poolList, netList, listedJobs, wls]) => {
        setAdapters((a.items ?? []) as Adapter[]);
        setModes((m.items ?? []) as Mode[]);
        const listed = (m.strategies ?? []) as Strategy[];
        if (listed.length > 0) setStrategies(listed);
        setSources((src.items ?? []) as Source[]);
        const persisted = listedMigrationJobs(listedJobs);
        setJobs(persisted);
        const destPools = (poolList.items ?? []) as DestPool[];
        const destNets = (netList.items ?? []) as DestNet[];
        setPools(destPools);
        setNets(destNets);
        if (destPools.length === 1) setPoolID(destPools[0].id);
        if (destNets.length === 1) setNetID(destNets[0].id);
        const first = (wls.items ?? [])[0] as { id?: string } | undefined;
        if (first?.id) setExportID(first.id);
        const active = findActiveMigrationJob(persisted);
        if (active && typeof active.id === "string") {
          setJob(active);
          setTab("import");
          setPhase("progress");
          void getMigrationJob(active.id)
            .then((row) => {
              setJob(row);
              setPhase(row.state === "succeeded" ? "verify" : "progress");
            })
            .catch(() => undefined);
        }
      })
      .catch((err) => setError(err instanceof Error ? err.message : "Unavailable"));
  }, []);

  const activeJobId = typeof job?.id === "string" ? job.id : "";
  const activeJobState = jobStateOf(job);

  useEffect(() => {
    if (!activeJobId || !isActiveMigrationState(activeJobState)) {
      return;
    }
    const t = window.setInterval(() => {
      void Promise.all([getMigrationJob(activeJobId), listMigrationJobs()])
        .then(([row, listed]) => {
          setJob(row);
          setJobs(listedMigrationJobs(listed));
          if (row.state === "succeeded") {
            setPhase("verify");
          }
        })
        .catch(() => undefined);
    }, 1000);
    return () => window.clearInterval(t);
  }, [activeJobId, activeJobState]);

  const chosen = useMemo(() => workloads.filter((w) => selected[w.source_id]), [workloads, selected]);
  const currentAdapter = adapters.find((a) => a.id === adapter);
  const findings = useMemo(() => {
    const out: Finding[] = [];
    for (const item of planItems) {
      for (const f of item.findings ?? []) out.push(f);
    }
    for (const rev of reviews) {
      for (const f of rev.warnings ?? []) out.push(f);
    }
    return out;
  }, [planItems, reviews]);
  const actionFindings = useMemo(() => findings.filter(findingNeedsAction), [findings]);
  const unmappedStorage = useMemo(() => {
    const mapped = new Set(reviews.flatMap((r) => Object.keys(r.storage ?? {})));
    const src = new Set<string>();
    for (const w of chosen) {
      for (const name of w.storage ?? []) {
        if (!mapped.has(name) && !storageOverride[name]) src.add(name);
      }
    }
    for (const f of actionFindings) {
      if (f.code === "storage" && (f.level ?? "").toUpperCase() === "REQUIRES MAPPING") {
        const m = /Source storage (.+) has no/.exec(f.message ?? "");
        if (m?.[1] && !storageOverride[m[1]]) src.add(m[1]);
      }
    }
    return [...src];
  }, [chosen, reviews, storageOverride, actionFindings]);
  const unmappedNetwork = useMemo(() => {
    const mapped = new Set(reviews.flatMap((r) => Object.keys(r.network ?? {})));
    const src = new Set<string>();
    for (const w of chosen) {
      for (const name of w.networks ?? []) {
        if (!mapped.has(name) && !networkOverride[name]) src.add(name);
      }
    }
    for (const f of actionFindings) {
      if (f.code === "network" && (f.level ?? "").toUpperCase() === "REQUIRES MAPPING") {
        const m = /Source network (.+) has no/.exec(f.message ?? "");
        if (m?.[1] && !networkOverride[m[1]]) src.add(m[1]);
      }
    }
    return [...src];
  }, [chosen, reviews, networkOverride, actionFindings]);
  const blocked = findings.some((f) => (f.level ?? "").toUpperCase() === "BLOCKED" || (f.level ?? "").toUpperCase() === "UNSUPPORTED");
  const mustStop = chosen.some((w) => {
    const mode = modeByID[w.source_id] || reviews.find((r) => r.name === w.name)?.migration_mode;
    return Boolean(w.running && (mode === "offline" || mode === "local"));
  });
  const needsLiveAck = chosen.some((w) => (modeByID[w.source_id] || reviews.find((r) => r.name === w.name)?.migration_mode) === "live" && !liveAck[w.source_id]);
  const needsIdentityAck = startAfter && chosen.some((w) => w.running && !identityAck[w.source_id]);
  const mappingIncomplete = unmappedStorage.length > 0 || unmappedNetwork.length > 0;
  const canImport = !blocked && !needsLiveAck && !needsIdentityAck && !mappingIncomplete && reviews.length > 0;

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
      if (modeOverride[w.source_id]) {
        modesPayload[w.source_id] = modeOverride[w.source_id];
      }
      if (liveAck[w.source_id]) ack[w.source_id] = true;
      if (identityAck[w.source_id]) ident[w.source_id] = true;
    }
    const storage = { ...storageOverride };
    const network = { ...networkOverride };
    return {
      source_id: sourceID,
      selected: chosen.map((w) => w.source_id),
      strategy,
      modes: modesPayload,
      live_ack: ack,
      identity_conflict_ack: ident,
      mapping: { storage, network },
      start_after: startAfter,
    };
  }

  function applyPlan(planned: Record<string, unknown>) {
    const revs = ((planned.review as ReviewRow[]) ?? []) as ReviewRow[];
    setReviews(revs);
    const items = (((planned.plan as { items?: PlanItem[] } | undefined)?.items ?? []) as PlanItem[]) ?? [];
    setPlanItems(items);
    setModeByID((cur) => {
      const next = { ...cur };
      for (const item of items) {
        if (item.source_id && item.mode && !next[item.source_id]) {
          next[item.source_id] = item.mode;
        }
      }
      return next;
    });
  }

  async function discoverInto(id: string) {
    const d = (await discoverMigrationSource(id)) as { workloads?: WorkloadRow[] };
    setWorkloads(d.workloads ?? []);
    setSelected({});
    setReviews([]);
    setPlanItems([]);
    setModeOverride({});
    setPhase("select");
  }

  async function connectSource() {
    setError(null);
    if (adapter === "proxmox") {
      const formatErr = pveTokenError(token);
      if (formatErr) {
        setTokenError(formatErr);
        setError(formatErr);
        return;
      }
    }
    setTokenError(null);
    setBusy(true);
    try {
      const created = (await createMigrationSource({ adapter, endpoint, token, insecure, label: adapter })) as Source;
      setSourceID(created.id);
      setToken("");
      setSources((cur) => [...cur, created]);
      await discoverInto(created.id);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Connect failed");
    } finally {
      setBusy(false);
    }
  }

  async function goReview() {
    setError(null);
    setBusy(true);
    try {
      const planned = await createMigrationPlan(planBody());
      applyPlan(planned);
      setPhase("review");
    } catch (err) {
      setError(err instanceof Error ? err.message : "Review failed");
    } finally {
      setBusy(false);
    }
  }

  function refreshJobs() {
    return listMigrationJobs()
      .then((listed) => setJobs(listedMigrationJobs(listed)))
      .catch(() => undefined);
  }

  function showPersistedJob(row: Record<string, unknown>) {
    setJob(row);
    setTab("import");
    setPhase(row.state === "succeeded" ? "verify" : "progress");
  }

  async function retryPersistedJob(id: string) {
    setError(null);
    try {
      const row = await retryMigrationJob(id);
      showPersistedJob(row);
      await refreshJobs();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Retry failed");
    }
  }

  async function start() {
    setError(null);
    if (!canImport) {
      setError("Resolve the required actions before import.");
      return;
    }
    try {
      const started = await startMigrationJob(planBody());
      showPersistedJob(started);
      await refreshJobs();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Start failed");
    }
  }

  async function importFile() {
    setError(null);
    const resolvedPool = poolID || (pools.length === 1 ? pools[0].id : "");
    const resolvedNet = netID || (nets.length === 1 ? nets[0].id : "");
    if (!resolvedPool || !resolvedNet) {
      setError("Choose destination storage and network. Automatic mapping needs one unambiguous destination.");
      return;
    }
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
        pool_id: resolvedPool,
        network_id: resolvedNet,
        start_after: startAfter,
      };
      const started = isBundle ? await importMigrationBundle(body) : await importMigrationDisk(body);
      showPersistedJob(started);
      await refreshJobs();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Import failed");
    }
  }

  const status = job?.status as Record<string, unknown> | undefined;
  const reports = (status?.reports as Record<string, unknown>[] | undefined) ?? [];
  const phaseIndex = phase === "select" ? 0 : phase === "review" ? 1 : phase === "progress" ? 2 : phase === "verify" ? 3 : -1;
  const currentStrategy = strategies.find((s) => s.id === strategy);
  const strategyReady = Boolean(currentStrategy && currentStrategy.available !== false);

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
      <div className="content-grid">
        <button
          type="button"
          aria-label="Import"
          aria-pressed={tab === "import"}
          className={"selection-card" + (tab === "import" ? " is-selected" : "")}
          onClick={() => setTab("import")}
        >
          <span className="title">Import</span>
          <span className="desc">Copy workloads onto this host. The source stays unchanged.</span>
        </button>
        <button
          type="button"
          aria-label="Export"
          aria-pressed={tab === "export"}
          className={"selection-card" + (tab === "export" ? " is-selected" : "")}
          onClick={() => setTab("export")}
        >
          <span className="title">Export</span>
          <span className="desc">Create a portable package so you can leave.</span>
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
                  showPersistedJob(row);
                  return refreshJobs();
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
          {phase !== "source" ? (
            <ol className="mig-steps">
              {PHASES.map((label, i) => (
                <li key={label}>
                  <button
                    type="button"
                    className={i === phaseIndex ? "btn" : "btn btn-secondary"}
                    onClick={() => {
                      if (i === 0) setPhase("select");
                      if (i === 1 && reviews.length > 0) setPhase("review");
                      if (i === 2 && job) setPhase("progress");
                      if (i === 3 && job?.state === "succeeded") setPhase("verify");
                    }}
                  >
                    {i + 1}. {label}
                  </button>
                </li>
              ))}
            </ol>
          ) : null}

          {phase === "source" ? (
            <article className="panel">
              <h2>Source</h2>
              <p>Connect discovers workloads. Transfer starts only after Review.</p>
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
                  <Field
                    id="mig-tok"
                    label="Proxmox API token"
                    value={token}
                    autoComplete="off"
                    spellCheck={false}
                    placeholder={PVE_TOKEN_EXAMPLE}
                    error={tokenError ?? undefined}
                    hint={`Required format: ${PVE_TOKEN_FORMAT}. Example: ${PVE_TOKEN_EXAMPLE}. Paste the full value Proxmox shows once, not the secret UUID alone.`}
                    onChange={(e) => {
                      setToken(e.target.value);
                      setTokenError(null);
                    }}
                  />
                  <label>
                    <input type="checkbox" checked={insecure} onChange={(e) => setInsecure(e.target.checked)} /> Allow HTTP
                    (disclosed insecure)
                  </label>
                  <button type="button" className="btn" disabled={!mutate || busy} onClick={() => void connectSource()}>
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
                  {pools.length !== 1 ? (
                    <label htmlFor="mig-pool">
                      Destination storage
                      <select id="mig-pool" className="field-input" value={poolID} onChange={(e) => setPoolID(e.target.value)}>
                        <option value="">Choose a pool</option>
                        {pools.map((p) => (
                          <option key={p.id} value={p.id}>
                            {p.name}
                          </option>
                        ))}
                      </select>
                    </label>
                  ) : null}
                  {nets.length !== 1 ? (
                    <label htmlFor="mig-net">
                      Destination network
                      <select id="mig-net" className="field-input" value={netID} onChange={(e) => setNetID(e.target.value)}>
                        <option value="">Choose a network</option>
                        {nets.map((n) => (
                          <option key={n.id} value={n.id}>
                            {n.name}
                          </option>
                        ))}
                      </select>
                    </label>
                  ) : null}
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
                        disabled={busy}
                        onClick={() => {
                          setSourceID(s.id);
                          setAdapter(s.adapter);
                          setBusy(true);
                          void discoverInto(s.id)
                            .catch((err) => setError(err instanceof Error ? err.message : "Discover failed"))
                            .finally(() => setBusy(false));
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

          {phase === "select" ? (
            <article className="panel">
              <h2>Select Workloads</h2>
              <p>Select the guests, then one strategy. Method, storage, network, and compatibility are decided for the whole selection.</p>
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
              <fieldset className="mig-strategy">
                <legend>Strategy for {chosen.length || "selected"} workload{chosen.length === 1 ? "" : "s"}</legend>
                {strategies.map((s) => (
                  <label key={s.id} className={s.available === false ? "mig-mode-unavail" : undefined}>
                    <input
                      type="radio"
                      name="mig-strategy"
                      checked={strategy === s.id}
                      disabled={s.available === false}
                      onChange={() => setStrategy(s.id)}
                    />
                    {s.label}
                    {s.recommended ? " (recommended)" : ""} {s.consistency}
                    {s.available === false ? " Unavailable" : ""}
                    <span className="field-hint">{s.summary}</span>
                    {s.unavailable_reason ? <span className="field-hint">{s.unavailable_reason}</span> : null}
                  </label>
                ))}
              </fieldset>
              <button
                type="button"
                className="btn"
                disabled={chosen.length === 0 || busy || !strategyReady}
                onClick={() => void goReview()}
              >
                Review {chosen.length} workload{chosen.length === 1 ? "" : "s"}
              </button>
            </article>
          ) : null}

          {phase === "review" ? (
            <article className="panel">
              <h2>Review</h2>
              <p>
                Strategy: {currentStrategy?.label ?? strategy}. Method, storage, network, and compatibility were decided
                for each selected workload.
              </p>
              <div className="table-wrap">
                <table className="mig-review-table">
                  <thead>
                    <tr>
                      <th>Workload</th>
                      <th>Method</th>
                      <th>Storage</th>
                      <th>Network</th>
                      <th>Compatibility</th>
                    </tr>
                  </thead>
                  <tbody>
                    {reviews.map((rev, idx) => (
                      <tr key={rev.source_id || String(rev.name) || String(idx)}>
                        <td>
                          {rev.name} <code>{rev.source_id || rev.source}</code>
                        </td>
                        <td>
                          {rev.migration_mode} {rev.consistency ? `(${rev.consistency})` : ""}
                        </td>
                        <td>{mapLines(rev.storage, pools, nets) || "none"}</td>
                        <td>
                          <div>{mapLines(rev.network, pools, nets) || "none"}</div>
                          {rev.network_summary ? <div className="muted">{rev.network_summary}</div> : null}
                          {!rev.network_summary && (rev.ipv4 || rev.ipv6) ? (
                            <div className="muted">
                              IPv4 {rev.ipv4 || "DHCP"}, IPv6 {rev.ipv6 || "Disabled"}
                              {rev.dns ? `, DNS ${rev.dns}` : ""}
                            </div>
                          ) : null}
                        </td>
                        <td>{rev.compatibility && rev.compatibility !== "READY" ? rev.compatibility : "READY"}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>

              {actionFindings.length > 0 ? (
                <div className="banner" role="status">
                  <p>Action required before import:</p>
                  <ul className="plain-list">
                    {actionFindings.map((f, i) => (
                      <li key={`${f.code}-${i}`}>{f.message}</li>
                    ))}
                  </ul>
                </div>
              ) : null}

              {unmappedStorage.length > 0 ? (
                <fieldset>
                  <legend>Choose destination storage</legend>
                  {unmappedStorage.map((src) => (
                    <label key={src} htmlFor={`map-st-${src}`}>
                      {src}
                      <select
                        id={`map-st-${src}`}
                        className="field-input"
                        value={storageOverride[src] ?? ""}
                        onChange={(e) => setStorageOverride({ ...storageOverride, [src]: e.target.value })}
                      >
                        <option value="">Choose a pool</option>
                        {pools.map((p) => (
                          <option key={p.id} value={p.id}>
                            {p.name}
                          </option>
                        ))}
                      </select>
                    </label>
                  ))}
                </fieldset>
              ) : null}

              {unmappedNetwork.length > 0 ? (
                <fieldset>
                  <legend>Choose destination network</legend>
                  {unmappedNetwork.map((src) => (
                    <label key={src} htmlFor={`map-net-${src}`}>
                      {src}
                      <select
                        id={`map-net-${src}`}
                        className="field-input"
                        value={networkOverride[src] ?? ""}
                        onChange={(e) => setNetworkOverride({ ...networkOverride, [src]: e.target.value })}
                      >
                        <option value="">Choose a network</option>
                        {nets.map((n) => (
                          <option key={n.id} value={n.id}>
                            {n.name}
                          </option>
                        ))}
                      </select>
                    </label>
                  ))}
                </fieldset>
              ) : null}

              {mustStop ? (
                <p className="banner banner-error" role="status">
                  Offline is the compatible mode. Stop the selected running guests on the source, then continue. No-dal
                  will not stop them.
                </p>
              ) : null}

              {needsIdentityAck ? (
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

              {unmappedStorage.length > 0 || unmappedNetwork.length > 0 ? (
                <button type="button" className="btn btn-secondary" disabled={busy} onClick={() => void goReview()}>
                  Update review
                </button>
              ) : null}

              <button
                type="button"
                className="btn btn-secondary"
                onClick={() => setAdvanced((cur) => !cur)}
                aria-expanded={advanced}
              >
                  {advanced ? "Hide Advanced" : "Advanced"}
              </button>

              {advanced ? (
                <div className="mig-advanced">
                  <p>Optional per-workload method overrides. Leave unchanged to keep the automatic plan.</p>
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
                            checked={(modeOverride[w.source_id] || modeByID[w.source_id]) === m.id}
                            disabled={m.available === false}
                            onChange={() => {
                              setModeByID({ ...modeByID, [w.source_id]: m.id });
                              setModeOverride({ ...modeOverride, [w.source_id]: m.id });
                            }}
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
                  <button type="button" className="btn btn-secondary" disabled={busy} onClick={() => void goReview()}>
                    Apply Advanced to review
                  </button>
                </div>
              ) : null}

              <button type="button" className="btn" disabled={!mutate || !canImport} onClick={() => void start()}>
                Submit
              </button>
            </article>
          ) : null}

          {phase === "progress" || phase === "verify" ? (
            <article className="panel">
              <h2>{phase === "verify" ? "Verification" : "Progress"}</h2>
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
                    <button
                      type="button"
                      className="btn btn-secondary"
                      onClick={() =>
                        void cancelMigrationJob(String(job.id))
                          .then((row) => {
                            setJob(row);
                            return refreshJobs();
                          })
                          .catch((err) => setError(err instanceof Error ? err.message : "Cancel failed"))
                      }
                    >
                      Cancel
                    </button>
                  ) : null}
                  {isRetryableMigrationState(jobStateOf(job)) ? (
                    <div className="inline-actions">
                      <button type="button" className="btn" disabled={!mutate} onClick={() => void retryPersistedJob(String(job.id))}>
                        Retry
                      </button>
                      <button
                        type="button"
                        className="btn btn-secondary"
                        onClick={() =>
                          void cleanupMigrationJob(String(job.id))
                            .then((row) => setJob({ ...job, ...row }))
                            .catch((err) => setError(err instanceof Error ? err.message : "Cleanup failed"))
                        }
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
        {jobs.length === 0 ? (
          <p>No migration jobs yet.</p>
        ) : (
          <ul className="activity-list">
            {jobs.map((j) => {
              const id = String(j.id ?? "");
              const title = jobHeadline(j);
              const state = jobStateOf(j);
              return (
                <li key={id || title} className={openJobId === id ? "is-open" : undefined}>
                  <button
                    type="button"
                    className="activity-toggle"
                    onClick={() => {
                      const next = openJobId === id ? null : id;
                      setOpenJobId(next);
                      if (next && isActiveMigrationState(state)) {
                        showPersistedJob(j);
                      }
                    }}
                  >
                    <StatusBadge status={state || String(j.state ?? "unknown")} />
                    <span>{title}</span>
                    <span className="muted">{String(j.updated_at ?? id)}</span>
                  </button>
                  {openJobId === id ? (
                    <MigrationJobDetail
                      job={j}
                      canRetry={mutate}
                      onClose={() => setOpenJobId(null)}
                      onRetry={(jobId) => void retryPersistedJob(jobId)}
                      onOpenProgress={(row) => showPersistedJob(row)}
                    />
                  ) : null}
                </li>
              );
            })}
          </ul>
        )}
      </article>
    </section>
  );
}
