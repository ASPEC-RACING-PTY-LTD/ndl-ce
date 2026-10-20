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
import { MetricChart } from "../components/MetricChart";
import { Link } from "../components/Link";
import { Field } from "../components/Field";
import { PageHeader } from "../components/PageHeader";
import { PageNav, type PageNavItem } from "../components/PageNav";
import { FilesBrowser } from "../components/FilesBrowser";
import { defaultFilesPath } from "../files/workspace";
import { TerminalPage } from "./TerminalPage";
import { usePref } from "../prefs";
import { Segmented } from "../ui/Segmented";
import { GpuPage } from "./GpuPage";
import { formatBytes, formatWhen, honestStatus } from "../format";
import { usePath } from "../router";
import { useSession } from "../session";
import { SummaryCard } from "../ui/SummaryCard";
import { ErrorNotice } from "../components/ErrorNotice";

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
  return (
    <section className="page page-wide" aria-labelledby="node-heading">
      <PageHeader
        id="node-heading"
        title="Node"
        kicker={`${nodes.host_os || "Host OS not reported"} · ${honestStatus(nodes.status)}${nodes.stale ? " (stale)" : ""}`}
      />
      <PageNav id="node" label="Node" icon="node" items={NODE_ITEMS} />
      {tab === "summary" ? <NodeSummaryView id={id} fallback={nodes} /> : null}
      {tab === "hardware" ? <NodeHardwareView id={id} /> : null}
      {tab === "metrics" ? <NodeMetricsView id={id} /> : null}
      {tab === "activity" ? <NodeActivityView id={id} /> : null}
      {tab === "terminal" ? <TerminalPage key={id} target={{ kind: "node", id }} embedded /> : null}
      {tab === "files" ? (
        <div className="embed-files">
          <div className="btn-row is-flush embed-actions">
            <Link className="btn btn-ghost btn-sm" href="/files">
              Open in Files
            </Link>
          </div>
          <FilesBrowser key={id} kind="node" id={id} initialPath={defaultFilesPath("node")} embedded />
        </div>
      ) : null}
    </section>
  );
}

export type NodeTab = "summary" | "hardware" | "metrics" | "activity" | "terminal" | "files";

const NODE_ITEMS: PageNavItem[] = [
  { id: "summary", label: "Summary", href: "/node", icon: "node", match: (p) => p === "/node" },
  { id: "hardware", label: "Hardware", href: "/node/hardware", icon: "diagnostics", match: (p) => nodeTab(p) === "hardware" },
  { id: "metrics", label: "Metrics", href: "/node/metrics", icon: "activity" },
  { id: "activity", label: "Events and logs", href: "/node/activity", icon: "events", match: (p) => nodeTab(p) === "activity" },
  { id: "terminal", label: "Terminal", href: "/node/terminal", icon: "terminal" },
  { id: "files", label: "Files", href: "/node/files", icon: "files" },
];

/** nodeTab maps a /node address to its section; old GPU, logs and events addresses land on their new homes. */
export function nodeTab(path: string): NodeTab {
  const part = path.split("/")[2] ?? "";
  switch (part) {
    case "hardware":
    case "gpu":
      return "hardware";
    case "metrics":
      return "metrics";
    case "activity":
    case "logs":
    case "events":
      return "activity";
    case "terminal":
      return "terminal";
    case "files":
      return "files";
    default:
      return "summary";
  }
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
        <SummaryCard label="Name" value={node.name} meta={node.host_os || "Host OS not reported"} />
        <SummaryCard label="CPU" value={node.cpu_model || "Not reported"} meta={`${node.cpu_sockets ?? "?"} sockets, ${node.cpu_cores ?? "?"} cores, ${node.cpu_threads ?? "?"} threads`} />
        <SummaryCard label="Memory" value={formatBytes(node.memory_bytes)} meta={`Observed ${formatWhen(node.observed_at)}`} />
        <SummaryCard label="Support" value={node.support_tier || "Not reported"} meta={honestStatus(node.status)} />
      </div>
      <div className="card-grid">
      <article className="panel">
        <h2>Summary</h2>
        <dl className="definition-list">
          <div>
            <dt>Name</dt>
            <dd>{node.name}</dd>
          </div>
          <div>
            <dt>Host OS</dt>
            <dd>{node.host_os || "Not reported"}</dd>
          </div>
          <div>
            <dt>Support tier</dt>
            <dd>{node.support_tier || "Not reported"}</dd>
          </div>
          <div>
            <dt>CPU</dt>
            <dd>{node.cpu_model || "Not reported"}</dd>
          </div>
          <div>
            <dt>Topology</dt>
            <dd>
              {node.cpu_sockets ?? "?"} sockets, {node.cpu_cores ?? "?"} cores,{" "}
              {node.cpu_threads ?? "?"} threads
            </dd>
          </div>
          <div>
            <dt>Memory</dt>
            <dd>{formatBytes(node.memory_bytes)}</dd>
          </div>
          <div>
            <dt>Observed</dt>
            <dd>{formatWhen(node.observed_at)}</dd>
          </div>
        </dl>
      </article>
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
      <RemoteNodeHelper />
      </div>
    </div>
  );
}

function RemoteNodeHelper() {
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
      .catch(() => undefined);
    return () => {
      cancelled = true;
    };
  }, [id]);

  if (!hw) {
    return <LoadingState label="Collecting" />;
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
        <GpuPage />
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
        title="IOMMU groups"
        rows={iommuRows(inv.iommu)}
        keys={["id", "devices"]}
        empty={iommuEmpty(inv.iommu)}
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

function NodeMetricsView({ id }: { id: string }) {
  const [metrics, setMetrics] = useState<MetricsResponse | null>(null);
  const [view, setView] = usePref<"table" | "grid">("node.metrics.view", "table");
  const [rawGrid, setRawGrid] = usePref<unknown>("node.metrics.grid", null);
  const [range, setRange] = useState("60");
  const [capacity, setCapacity] = useState<string>("Collecting");
  const [smart, setSmart] = useState<string>("Collecting");

  useEffect(() => {
    let cancelled = false;
    void getNodeMetrics(id, { minutes: Number(range) || 60 })
      .then((value) => {
        if (!cancelled) {
          setMetrics(value);
        }
      })
      .catch(() => undefined);
    void getNodeCapacity(id)
      .then((value) => {
        if (cancelled) return;
        if (value.hours_to_zero != null) {
          setCapacity(`${Math.round(value.hours_to_zero)} hours to zero usable bytes`);
        } else {
          setCapacity(value.message || value.status);
        }
      })
      .catch(() => undefined);
    void getNodeSmart(id)
      .then((value) => {
        if (cancelled) return;
        if (!value.items?.length) {
          setSmart(value.status);
          return;
        }
        setSmart(value.items.map((d) => `${d.name}: ${d.smart_status}`).join(", "));
      })
      .catch(() => undefined);
    return () => {
      cancelled = true;
    };
  }, [id, range]);

  const series = metrics?.series ?? [];
  const gridNames = parseLayout(
    rawGrid,
    series.map((x) => x.name),
  );
  return (
    <div className="stack">
      <div className="toolbar">
        <div className="toolbar-group">
          <label className="field-inline">
            Range
            <select className="field-input" value={range} onChange={(e) => setRange(e.target.value)}>
              <option value="60">Last hour</option>
              <option value="360">Last 6 hours</option>
              <option value="1440">Last day</option>
              <option value="10080">Last week</option>
            </select>
          </label>
          <Segmented<"table" | "grid">
            ariaLabel="Metrics view"
            value={view === "grid" ? "grid" : "table"}
            onChange={(v) => setView(v)}
            options={[
              { id: "table", label: "Table" },
              { id: "grid", label: "Charts" },
            ]}
          />
        </div>
      </div>
      <dl className="kv-compact">
        <dt>Capacity</dt>
        <dd>{capacity}</dd>
        <dt>SMART</dt>
        <dd>{smart}</dd>
      </dl>
      {!metrics ? (
        <p>Collecting data</p>
      ) : metrics.status === "stale" ? (
        <p className="chart-empty">Stale</p>
      ) : metrics.status === "unavailable" ? (
        <p className="chart-empty">Unavailable</p>
      ) : !series.length ? (
        <p className="chart-empty">Collecting data</p>
      ) : view !== "grid" ? (
        <div className="table-wrap">
          <table aria-label="Metrics">
            <thead>
              <tr>
                <th>Metric</th>
                <th className="num">Now</th>
                <th className="num">Lowest</th>
                <th className="num">Highest</th>
                <th className="num">Average</th>
              </tr>
            </thead>
            <tbody>
              {series.map((x) => {
                const st = seriesStats(x);
                return (
                  <tr key={x.name}>
                    <td>{x.name}</td>
                    <td className="num">{metricValue(st.last, x.unit)}</td>
                    <td className="num">{metricValue(st.min, x.unit)}</td>
                    <td className="num">{metricValue(st.max, x.unit)}</td>
                    <td className="num">{metricValue(st.avg, x.unit)}</td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      ) : (
        <>
          <details className="panel metrics-pick">
            <summary>Charts shown ({gridNames.length})</summary>
            <div className="chip-row">
              {series.map((x) => {
                const on = gridNames.includes(x.name);
                return (
                  <button
                    key={x.name}
                    type="button"
                    className="chip"
                    aria-pressed={on}
                    onClick={() => setRawGrid(on ? gridNames.filter((n) => n !== x.name) : [...gridNames, x.name])}
                  >
                    {x.name}
                  </button>
                );
              })}
            </div>
          </details>
          <div className="fluid-grid">
            {gridNames.map((name) => {
              const x = series.find((v) => v.name === name);
              return x ? (
                <article className="panel" key={name}>
                  <h2>{name}</h2>
                  <MetricChart series={x} />
                </article>
              ) : null;
            })}
          </div>
        </>
      )}
    </div>
  );
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

/** NodeActivityView is this node's events and its service logs on one page. */
function NodeActivityView({ id }: { id: string }) {
  const [events, setEvents] = useState<EventItem[] | null>(null);
  useEffect(() => {
    let live = true;
    listEvents()
      .then((items) => live && setEvents(items.filter((e) => !e.node_id || e.node_id === id).slice(0, 100)))
      .catch(() => live && setEvents([]));
    return () => {
      live = false;
    };
  }, [id]);
  return (
    <div className="node-activity">
      <article className="panel">
        <h2>Events</h2>
        {events === null ? <LoadingState label="Loading events" /> : null}
        {events && events.length === 0 ? <p className="muted">No events for this node.</p> : null}
        {events && events.length > 0 ? (
          <ul className="event-list">
            {events.map((e) => (
              <li key={e.id}>
                <span className="cell-mono">{e.type}</span>
                <span className="muted">{formatWhen(e.created_at)}</span>
              </li>
            ))}
          </ul>
        ) : null}
        <Link href="/events">All events</Link>
      </article>
      <NodeLogsView id={id} />
    </div>
  );
}

function NodeLogsView({ id }: { id: string }) {
  const [unit, setUnit] = useState("ndl-agent.service");
  const [status, setStatus] = useState("collecting");
  const [lines, setLines] = useState<string[]>([]);
  const [message, setMessage] = useState<string | undefined>();

  useEffect(() => {
    let cancelled = false;
    void getNodeLogs(id, unit)
      .then((value) => {
        if (cancelled) return;
        setStatus(value.status);
        setLines(value.lines ?? []);
        setMessage(value.message);
      })
      .catch(() => {
        if (!cancelled) {
          setStatus("unavailable");
          setLines([]);
        }
      });
    return () => {
      cancelled = true;
    };
  }, [id, unit]);

  return (
    <article className="panel">
      <h2>Logs</h2>
      <label>
        Unit
        <select value={unit} onChange={(e) => setUnit(e.target.value)}>
          <option value="ndl-agent.service">ndl-agent</option>
          <option value="ndl-control.service">ndl-control</option>
        </select>
      </label>
      {status === "unavailable" ? <p className="chart-empty">Unavailable</p> : null}
      {message ? <p className="muted">{message}</p> : null}
      {lines.length === 0 && status !== "unavailable" ? <p className="muted">No log lines in this window</p> : null}
      {lines.length > 0 ? (
        <pre className="log-view">
          {lines.join("\n")}
        </pre>
      ) : null}
    </article>
  );
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
