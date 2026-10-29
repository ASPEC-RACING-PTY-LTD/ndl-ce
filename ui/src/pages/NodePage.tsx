import { useEffect, useState, type ReactNode } from "react";
import { LoadingState } from "../components/EmptyState";
import { DeleteButton } from "../components/DeleteButton";
import {
  getNode,
  getNodeCapabilities,
  getNodeHardware,
  getNodeLogs,
  getNodeMetrics,
  getNodeCapacity,
  getNodeSmart,
  listEvents,
  listNodes,
  listWG,
  createWGPeer,
} from "../api/client";
import type { Capability, EventItem, HardwareResponse, MetricSeries, MetricsResponse, NodeSummary } from "../api/phase2";
import { Link } from "../components/Link";
import { Field } from "../components/Field";
import { PageHeader } from "../components/PageHeader";
import { PageNav, type PageNavItem } from "../components/PageNav";
import { usePref } from "../prefs";
import { Segmented } from "../ui/Segmented";
import { GpuPage } from "./GpuPage";
import { formatBytes, formatWhen, honestStatus } from "../format";
import { navigate, usePath } from "../router";
import { useSession } from "../session";
import { canOnItem, hasRoleAccess } from "../rbac";
import { SummaryCard } from "../ui/SummaryCard";
import { ErrorNotice } from "../components/ErrorNotice";
import { NodeName, NodeNicknameButton } from "../components/NodeNickname";

type Inventory = {
  host?: Record<string, unknown>;
  cpu?: Record<string, unknown>;
  memory?: Record<string, unknown>;
  block_devices?: Record<string, unknown>[];
  nics?: Record<string, unknown>[];
  pci?: Record<string, unknown>[];
  usb?: Record<string, unknown>[];
  gpus?: Record<string, unknown>[];
  iommu?: Record<string, unknown>;
  temperatures?: Record<string, unknown>[];
  firmware?: Record<string, unknown>;
};

export function NodePage() {
  const path = usePath();
  const session = useSession();
  const user = session.status === "ready" ? session.user : null;
  const roleAccess = hasRoleAccess(user);
  const [nodes, setNodes] = useState<NodeSummary | "loading" | "missing">("loading");
  const [id, setId] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    void (async () => {
      try {
        const items = await listNodes();
        if (cancelled) {
          return;
        }
        const local = items.find((n) => n.role !== "worker") ?? items[0];
        if (!local) {
          setNodes("missing");
          return;
        }
        setId(local.id);
        setNodes(local);
      } catch {
        if (!cancelled) {
          setNodes("missing");
        }
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  if (nodes === "loading") {
    return (
      <section className="page">
        <h1>Node</h1>
        <LoadingState label="Collecting" />
      </section>
    );
  }
  if (nodes === "missing" || !id) {
    return (
      <section className="page">
        <h1>Node</h1>
        <p className="muted">No local node is enrolled yet.</p>
      </section>
    );
  }

  const tab = nodeTab(path);
  const elsewhere = nodeElsewhere(path);
  // Metrics and logs need a role, or metrics on this node's grant.
  const showMetrics = roleAccess || canOnItem(user, "node", id, "metrics.read");
  if (elsewhere) {
    return <NodeRedirect to={elsewhere} />;
  }
  return (
    <section className="page page-wide" aria-labelledby="node-heading">
      <PageHeader
        id="node-heading"
        title="Node"
        kicker={`${nodes.host_os || "Host OS not reported"} · ${honestStatus(nodes.status)}${nodes.stale ? " (stale)" : ""}`}
      />
      <PageNav id="node" label="Node" items={showMetrics ? NODE_ITEMS : NODE_ITEMS.filter((i) => i.id !== "metrics")} className="is-sub" />
      {tab === "summary" || !showMetrics ? (
        <>
          <NodeSummaryView id={id} fallback={nodes} />
          <NodeHardwareView id={id} />
        </>
      ) : null}
      {tab === "metrics" && showMetrics ? <NodeMonitorView id={id} /> : null}
    </section>
  );
}

export type NodeTab = "summary" | "metrics";

const NODE_ITEMS: PageNavItem[] = [
  { id: "summary", label: "Summary", href: "/node", icon: "node", match: (p) => p.startsWith("/node") && nodeTab(p) === "summary" },
  { id: "metrics", label: "Metrics and logs", href: "/node/metrics", icon: "activity", match: (p) => nodeTab(p) === "metrics" },
];

/**
 * nodeTab maps a /node address to its section. Hardware and GPUs are part
 * of Summary; metrics, events and logs share one tab. Terminal and Files
 * are not node tabs any more (see nodeElsewhere).
 */
export function nodeTab(path: string): NodeTab {
  const part = path.split("/")[2] ?? "";
  switch (part) {
    case "metrics":
    case "activity":
    case "logs":
    case "events":
      return "metrics";
    default:
      return "summary";
  }
}

/** nodeElsewhere is where the old Terminal and Files tab addresses open now. */
export function nodeElsewhere(path: string): string | null {
  const part = path.split("/")[2] ?? "";
  return part === "terminal" ? "/terminal" : part === "files" ? "/files" : null;
}

function NodeSummaryView({ id, fallback }: { id: string; fallback: NodeSummary }) {
  const [node, setNode] = useState(fallback);
  const [caps, setCaps] = useState<Capability[]>([]);

  useEffect(() => {
    let cancelled = false;
    void Promise.all([getNode(id), getNodeCapabilities(id)])
      .then(([n, c]) => {
        if (!cancelled) {
          setNode(n);
          setCaps(c.capabilities ?? []);
        }
      })
      .catch(() => undefined);
    return () => {
      cancelled = true;
    };
  }, [id]);

  return (
    <div className="stack">
      <div className="summary-grid">
        <SummaryCard
          label="Name"
          value={
            <>
              <NodeName node={node} /> <NodeNicknameButton node={node} />
            </>
          }
          meta={node.real_name ? `${node.real_name} · ${node.host_os || "Host OS not reported"}` : node.host_os || "Host OS not reported"}
        />
        <SummaryCard label="CPU" value={node.cpu_model || "Not reported"} meta={`${node.cpu_sockets ?? "?"} sockets, ${node.cpu_cores ?? "?"} cores, ${node.cpu_threads ?? "?"} threads`} />
        <SummaryCard label="Memory" value={formatBytes(node.memory_bytes)} meta={`Observed ${formatWhen(node.observed_at)}`} />
        <SummaryCard label="Support" value={node.support_tier || "Not reported"} meta={honestStatus(node.status)} />
      </div>
      <div className="card-grid">
      <article className="panel">
        <h2>Capabilities</h2>
        {caps.length === 0 ? (
          <p>Not reported</p>
        ) : (
          <ul className="plain-list">
            {caps.map((c) => (
              <li key={c.id}>
                {c.id}: {honestStatus(c.status)}
                {c.detail ? ` (${c.detail})` : ""}
              </li>
            ))}
          </ul>
        )}
      </article>
      </div>
    </div>
  );
}

/** RemoteNodeHelper adds a worker over WireGuard; it sits on Network > Cluster. */
export function RemoteNodeHelper() {
  const session = useSession();
  const mutate = Boolean(
    session.status === "ready" && (session.user?.roles.includes("superadmin") || session.user?.roles.includes("admin")),
  );
  const [workers, setWorkers] = useState<NodeSummary[]>([]);
  const [name, setName] = useState("worker-1");
  const [endpoint, setEndpoint] = useState("");
  const [snippet, setSnippet] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function reload() {
    const listed = await listWG();
    setWorkers(listed.nodes ?? []);
  }

  useEffect(() => {
    void reload().catch(() => undefined);
  }, []);

  async function onCreate() {
    setBusy(true);
    setError(null);
    try {
      const created = await createWGPeer({
        name,
        endpoint: endpoint || undefined,
      });
      setSnippet(
        JSON.stringify(
          {
            ...created.desired,
            private_key: created.worker_private_key,
            pairing_token: created.pairing_token,
          },
          null,
          2,
        ),
      );
      await reload();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Unavailable");
    } finally {
      setBusy(false);
    }
  }

  return (
    <article className="panel">
      <h2>Remote worker</h2>
      <p className="lede">
        WireGuard is pre-join connectivity. Place the control plane on this box and an agent on
        another. Join the worker from Cluster with a single-use join token. A down tunnel marks the
        worker NotReady; guests keep running.
      </p>
      {workers.length === 0 ? (
        <p className="muted">No remote workers yet.</p>
      ) : (
        <ul className="plain-list">
          {workers.map((n) => (
            <li key={n.id}>
              <strong>{n.name}</strong> {honestStatus(n.status)}
              {n.reason ? ` ${n.reason}` : ""}
              {n.listen_addr ? ` ${n.listen_addr}` : ""}
              {mutate && n.wg_peer_id ? (
                <DeleteButton
                  path={`/cluster/wg/peers/${n.wg_peer_id}`}
                  name={n.name}
                  noun="remote worker"
                  description={<p>The WireGuard interface on this host and both peer records are removed. The worker loses its tunnel.</p>}
                  onDeleted={() => reload()}
                />
              ) : null}
            </li>
          ))}
        </ul>
      )}
      {mutate ? (
        <form
          className="form"
          onSubmit={(e) => {
            e.preventDefault();
            void onCreate();
          }}
        >
          <Field id="wg-name" label="Worker name" value={name} onChange={(e) => setName(e.target.value)} />
          <Field
            id="wg-endpoint"
            label="Worker endpoint"
            value={endpoint}
            onChange={(e) => setEndpoint(e.target.value)}
            hint="Optional host:port. Copy the generated desired.json to /var/lib/ndl/wireguard/desired.json on the worker."
          />
          <button className="btn btn-primary" type="submit" disabled={busy || !name}>
            Add WireGuard worker
          </button>
        </form>
      ) : null}
      {error ? (
        <ErrorNotice error={error} />
      ) : null}
      {snippet ? (
        <pre className="code-block" tabIndex={0}>
          {snippet}
        </pre>
      ) : null}
    </article>
  );
}

function NodeHardwareView({ id }: { id: string }) {
  const [hw, setHw] = useState<HardwareResponse | null>(null);
  const [hwFailed, setHwFailed] = useState(false);
  const session = useSession();
  // The GPU tools are for roles; a grant on the node shows its hardware only.
  const roleAccess = hasRoleAccess(session.status === "ready" ? session.user : null);
  const [rawLayout, setRawLayout] = usePref<unknown>("node.hardware.layout", null);
  const layout = parseLayout(
    rawLayout,
    HARDWARE_SECTIONS.map((x) => x.id),
  );
  const setLayout = (v: HardwareSection[] | undefined) => setRawLayout(v ?? null);
  const [editing, setEditing] = useState(false);

  useEffect(() => {
    let cancelled = false;
    void getNodeHardware(id)
      .then((value) => {
        if (!cancelled) {
          setHw(value);
        }
      })
      .catch(() => {
        if (!cancelled) {
          setHwFailed(true);
        }
      });
    return () => {
      cancelled = true;
    };
  }, [id]);

  if (!hw) {
    // Hardware this person may not read is left out rather than shown as an error.
    return hwFailed ? null : <LoadingState label="Collecting" />;
  }
  if (!hw.inventory) {
    return <p>{hw.message || honestStatus(hw.status)}</p>;
  }
  const inv = hw.inventory as Inventory;
  const hasGpu = (inv.gpus ?? []).length > 0;
  const sections: Record<HardwareSection, ReactNode> = {
    cpu: <HardwareTable title="CPU" rows={[inv.cpu ?? {}]} keys={["vendor", "model", "architecture", "sockets", "cores", "threads", "status"]} />,
    memory: <HardwareTable title="Memory" rows={[inv.memory ?? {}]} keys={["total_bytes", "available_bytes", "dimm_status", "status"]} />,
    gpu: hasGpu ? (
      <article className="panel">
        <h2>GPUs</h2>
        <HardwareTable title="Detected" rows={inv.gpus ?? []} keys={["id", "vendor", "pci", "driver"]} bare />
        {roleAccess ? <GpuPage /> : null}
      </article>
    ) : null,
    disks: (
      <HardwareTable
        title="Block devices"
        rows={inv.block_devices ?? []}
        keys={["name", "model", "size_bytes", "kind", "transport", "smart_status"]}
      />
    ),
    nics: <HardwareTable title="Network interfaces" rows={inv.nics ?? []} keys={["name", "mac", "state", "speed_mbps", "driver", "kind"]} />,
    pci: <HardwareTable title="PCI" rows={inv.pci ?? []} keys={["address", "vendor", "device", "class", "driver"]} />,
    usb: <HardwareTable title="USB" rows={inv.usb ?? []} keys={["address", "vendor", "product", "name"]} />,
    temps: (
      <HardwareTable title="Temperatures" rows={inv.temperatures ?? []} keys={["id", "name", "label", "milli_c", "status"]} empty="Unavailable" />
    ),
    firmware: (
      <HardwareTable
        title="Firmware"
        rows={[inv.firmware ?? {}]}
        keys={["sys_vendor", "product", "board_vendor", "board", "bios_vendor", "bios_version", "bios_date"]}
      />
    ),
    iommu: <HardwareTable title="IOMMU groups" rows={iommuRows(inv.iommu)} keys={["id", "devices"]} empty={iommuEmpty(inv.iommu)} />,
  };
  const shown = layout.filter((k) => sections[k] !== null);
  return (
    <div className="stack">
      {hw.stale ? (
        <p className="banner" role="status">
          This inventory is stale. Values are the last observation, not zeros.
        </p>
      ) : null}
      <div className="btn-row is-flush">
        <button className="btn btn-ghost btn-sm" type="button" aria-pressed={editing} onClick={() => setEditing((v) => !v)}>
          {editing ? "Done" : "Customise layout"}
        </button>
      </div>
      {editing ? (
        <div className="panel widget-editor" aria-label="Hardware sections">
          {HARDWARE_SECTIONS.filter((x) => x.id !== "gpu" || hasGpu).map((x) => {
            const on = layout.includes(x.id);
            return (
              <div key={x.id} className="widget-editor-row">
                <label className="check-row">
                  <input
                    type="checkbox"
                    checked={on}
                    onChange={(e) => setLayout(e.target.checked ? [...layout, x.id] : layout.filter((k) => k !== x.id))}
                  />
                  <span>{x.label}</span>
                </label>
                {on ? (
                  <span className="btn-row is-flush">
                    <button
                      className="btn btn-ghost btn-sm"
                      type="button"
                      aria-label={`Move ${x.label} earlier`}
                      onClick={() => setLayout(shift(layout, x.id, -1))}
                    >
                      ↑
                    </button>
                    <button
                      className="btn btn-ghost btn-sm"
                      type="button"
                      aria-label={`Move ${x.label} later`}
                      onClick={() => setLayout(shift(layout, x.id, 1))}
                    >
                      ↓
                    </button>
                  </span>
                ) : null}
              </div>
            );
          })}
          <button className="btn btn-ghost btn-sm" type="button" onClick={() => setLayout(undefined)}>
            Reset
          </button>
        </div>
      ) : null}
      <div className="hw-grid">
        {shown.map((k) => (
          <div key={k} className={"hw-cell" + (k === "gpu" || k === "pci" || k === "iommu" ? " is-wide" : "")}>
            {sections[k]}
          </div>
        ))}
      </div>
    </div>
  );
}

type HardwareSection = "cpu" | "memory" | "gpu" | "disks" | "nics" | "pci" | "usb" | "temps" | "firmware" | "iommu";

const HARDWARE_SECTIONS: { id: HardwareSection; label: string }[] = [
  { id: "cpu", label: "CPU" },
  { id: "memory", label: "Memory" },
  { id: "gpu", label: "GPUs" },
  { id: "disks", label: "Block devices" },
  { id: "nics", label: "Network interfaces" },
  { id: "pci", label: "PCI" },
  { id: "usb", label: "USB" },
  { id: "temps", label: "Temperatures" },
  { id: "firmware", label: "Firmware" },
  { id: "iommu", label: "IOMMU groups" },
];

/** parseLayout keeps known section ids in the saved order; nothing saved means all of them. */
export function parseLayout<T extends string>(raw: unknown, all: readonly T[]): T[] {
  if (!Array.isArray(raw)) {
    return [...all];
  }
  return [...new Set(raw.filter((x): x is T => typeof x === "string" && (all as readonly string[]).includes(x)))];
}

function shift<T>(list: T[], id: T, delta: number): T[] {
  const i = list.indexOf(id);
  const j = i + delta;
  if (i < 0 || j < 0 || j >= list.length) {
    return list;
  }
  const next = [...list];
  [next[i], next[j]] = [next[j], next[i]];
  return next;
}

type MonitorKind = "all" | "metric" | "event" | "log";

type MonitorRow = { key: string; kind: Exclude<MonitorKind, "all">; source: string; detail: string; when: string };

const KIND_LABEL: Record<MonitorRow["kind"], string> = { metric: "Metric", event: "Event", log: "Log" };

/** NodeMonitorView is this node's metrics, events and service logs in one table with a filter. */
function NodeMonitorView({ id }: { id: string }) {
  const [metrics, setMetrics] = useState<MetricsResponse | null>(null);
  const [events, setEvents] = useState<EventItem[] | null>(null);
  const [logs, setLogs] = useState<{ status: string; lines: string[]; message?: string } | null>(null);
  const [kind, setKind] = usePref<MonitorKind>("node.monitor.kind", "all");
  const [range, setRange] = useState("60");
  const [unit, setUnit] = useState("ndl-agent.service");
  const [query, setQuery] = useState("");
  const [capacity, setCapacity] = useState<string>("Collecting");
  const [smart, setSmart] = useState<string>("Collecting");

  useEffect(() => {
    let cancelled = false;
    void getNodeMetrics(id, { minutes: Number(range) || 60 })
      .then((value) => !cancelled && setMetrics(value))
      .catch(() => undefined);
    void getNodeCapacity(id)
      .then((value) => {
        if (!cancelled) {
          setCapacity(value.hours_to_zero != null ? `${Math.round(value.hours_to_zero)} hours to zero usable bytes` : value.message || value.status);
        }
      })
      .catch(() => undefined);
    void getNodeSmart(id)
      .then((value) => {
        if (!cancelled) {
          setSmart(value.items?.length ? value.items.map((d) => `${d.name}: ${d.smart_status}`).join(", ") : value.status);
        }
      })
      .catch(() => undefined);
    return () => {
      cancelled = true;
    };
  }, [id, range]);

  useEffect(() => {
    let live = true;
    listEvents()
      .then((items) => live && setEvents(items.filter((e) => !e.node_id || e.node_id === id).slice(0, 200)))
      .catch(() => live && setEvents([]));
    return () => {
      live = false;
    };
  }, [id]);

  useEffect(() => {
    let live = true;
    void getNodeLogs(id, unit)
      .then((value) => live && setLogs({ status: value.status, lines: value.lines ?? [], message: value.message }))
      .catch(() => live && setLogs({ status: "unavailable", lines: [] }));
    return () => {
      live = false;
    };
  }, [id, unit]);

  const rangeLabel = RANGES.find((r) => r.id === range)?.label ?? "";
  const rows: MonitorRow[] = [
    ...(metrics?.series ?? []).map((x): MonitorRow => {
      const st = seriesStats(x);
      return {
        key: `m:${x.name}`,
        kind: "metric",
        source: x.name,
        detail: `Now ${metricValue(st.last, x.unit) || "none"} · low ${metricValue(st.min, x.unit) || "none"} · high ${metricValue(st.max, x.unit) || "none"} · average ${metricValue(st.avg, x.unit) || "none"}`,
        when: rangeLabel,
      };
    }),
    ...(events ?? []).map((e): MonitorRow => ({ key: `e:${e.id}`, kind: "event", source: e.type, detail: eventDetail(e), when: formatWhen(e.created_at) })),
    ...(logs?.lines ?? []).map((line, i): MonitorRow => ({ key: `l:${i}`, kind: "log", source: unit.replace(/\.service$/, ""), detail: line, when: "" })),
  ];
  const q = query.trim().toLowerCase();
  const shown = rows.filter((r) => (kind === "all" || r.kind === kind) && (!q || r.source.toLowerCase().includes(q) || r.detail.toLowerCase().includes(q)));
  const count = (k: MonitorRow["kind"]) => rows.filter((r) => r.kind === k).length;
  const loading = metrics === null || events === null || logs === null;

  return (
    <div className="stack node-monitor">
      <div className="toolbar">
        <div className="toolbar-group">
          <Segmented<MonitorKind>
            ariaLabel="Show"
            value={kind}
            onChange={(v) => setKind(v)}
            options={[
              { id: "all", label: `All (${rows.length})` },
              { id: "metric", label: `Metrics (${count("metric")})` },
              { id: "event", label: `Events (${count("event")})` },
              { id: "log", label: `Logs (${count("log")})` },
            ]}
          />
          <input className="field-input" type="search" aria-label="Filter rows" placeholder="Filter" value={query} onChange={(e) => setQuery(e.target.value)} />
          {kind === "all" || kind === "metric" ? (
            <label className="field-inline">
              Metrics over
              <select className="field-input" value={range} onChange={(e) => setRange(e.target.value)}>
                {RANGES.map((r) => (
                  <option key={r.id} value={r.id}>
                    {r.label}
                  </option>
                ))}
              </select>
            </label>
          ) : null}
          {kind === "all" || kind === "log" ? (
            <label className="field-inline">
              Log
              <select className="field-input" value={unit} onChange={(e) => setUnit(e.target.value)}>
                <option value="ndl-agent.service">ndl-agent</option>
                <option value="ndl-control.service">ndl-control</option>
              </select>
            </label>
          ) : null}
        </div>
        <Link href="/events">All events</Link>
      </div>
      <dl className="kv-compact">
        <dt>Capacity</dt>
        <dd>{capacity}</dd>
        <dt>SMART</dt>
        <dd>{smart}</dd>
        {metrics && (metrics.status === "stale" || metrics.status === "unavailable") ? (
          <>
            <dt>Metrics</dt>
            <dd>{honestStatus(metrics.status)}</dd>
          </>
        ) : null}
        {logs?.message ? (
          <>
            <dt>Logs</dt>
            <dd>{logs.message}</dd>
          </>
        ) : null}
      </dl>
      {loading && shown.length === 0 ? <LoadingState label="Collecting" /> : null}
      {!loading && shown.length === 0 ? <p className="muted">Nothing matches.</p> : null}
      {shown.length > 0 ? (
        <div className="table-wrap">
          <table className="data-table node-monitor-table" aria-label="Metrics, events and logs">
            <thead>
              <tr>
                <th>Type</th>
                <th>Source</th>
                <th>Detail</th>
                <th>When</th>
              </tr>
            </thead>
            <tbody>
              {shown.map((r) => (
                <tr key={r.key}>
                  <td>{KIND_LABEL[r.kind]}</td>
                  <td className="cell-mono">{r.source}</td>
                  <td className={r.kind === "log" ? "cell-mono node-monitor-log" : undefined}>{r.detail}</td>
                  <td className="muted">{r.when}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : null}
    </div>
  );
}

const RANGES = [
  { id: "60", label: "Last hour" },
  { id: "360", label: "Last 6 hours" },
  { id: "1440", label: "Last day" },
  { id: "10080", label: "Last week" },
];

/** eventDetail is a one-line summary of an event's payload. */
function eventDetail(e: EventItem): string {
  const p = e.payload ?? {};
  for (const k of ["message", "summary", "reason", "detail", "name"]) {
    if (typeof p[k] === "string" && p[k]) {
      return p[k] as string;
    }
  }
  const parts = Object.entries(p)
    .filter(([, v]) => typeof v === "string" || typeof v === "number" || typeof v === "boolean")
    .slice(0, 4)
    .map(([k, v]) => `${k.replaceAll("_", " ")} ${String(v)}`);
  return parts.join(" · ");
}

function NodeRedirect({ to }: { to: string }) {
  useEffect(() => {
    navigate(to);
  }, [to]);
  return <LoadingState label="Opening" />;
}

/** seriesStats is the latest, lowest, highest and average value of a series. */
export function seriesStats(x: MetricSeries): {
  last?: number;
  min?: number;
  max?: number;
  avg?: number;
} {
  const vals = x.points.map((p) => p.value).filter((v) => Number.isFinite(v));
  if (!vals.length) {
    return {};
  }
  return {
    last: vals[vals.length - 1],
    min: Math.min(...vals),
    max: Math.max(...vals),
    avg: vals.reduce((a, b) => a + b, 0) / vals.length,
  };
}

function metricValue(v: number | undefined, unit?: string): string {
  if (v === undefined) {
    return "";
  }
  if (unit === "bytes") {
    return formatBytes(v);
  }
  const n = Math.abs(v) >= 100 ? v.toFixed(0) : v.toFixed(2).replace(/\.?0+$/, "");
  return unit && unit !== "count" ? `${n} ${unit === "percent" ? "%" : unit}` : n;
}

function HardwareTable({
  title,
  rows,
  keys,
  empty,
  bare = false,
}: {
  title: string;
  rows: Record<string, unknown>[];
  keys: string[];
  empty?: string;
  /** bare drops the panel, for a table inside another box. */
  bare?: boolean;
}) {
  const usable = rows.filter((row) => Object.keys(row).length > 0);
  const Box = bare ? "div" : "article";
  return (
    <Box className={bare ? undefined : "panel"}>
      {bare ? <h3>{title}</h3> : <h2>{title}</h2>}
      {usable.length === 0 ? (
        <p>{empty || "Not reported"}</p>
      ) : (
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                {keys.map((k) => (
                  <th key={k}>{k.replaceAll("_", " ")}</th>
                ))}
              </tr>
            </thead>
            <tbody>
              {usable.map((row, i) => (
                <tr key={i}>
                  {keys.map((k) => (
                    <td key={k}>{cell(row[k])}</td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </Box>
  );
}

function iommuRows(iommu?: Record<string, unknown>): Record<string, unknown>[] {
  const groups = iommu?.groups;
  if (!Array.isArray(groups)) {
    return [];
  }
  return groups as Record<string, unknown>[];
}

function iommuEmpty(iommu?: Record<string, unknown>): string {
  const status = typeof iommu?.status === "string" ? iommu.status : "";
  if (status === "unavailable") {
    return "Unavailable";
  }
  return "Not reported";
}

function cell(value: unknown): ReactNode {
  if (value == null || value === "") {
    return "Not reported";
  }
  if (typeof value === "boolean") {
    return value ? "yes" : "no";
  }
  if (typeof value === "number") {
    if (value > 1024 * 1024) {
      return formatBytes(value);
    }
    return String(value);
  }
  if (Array.isArray(value)) {
    return value.length ? value.map(String).join(", ") : "Not reported";
  }
  return String(value);
}
