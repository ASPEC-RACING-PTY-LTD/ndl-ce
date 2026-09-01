import { useEffect, useState, type ReactNode } from "react";
import {
  getNode,
  getNodeCapabilities,
  getNodeHardware,
  getNodeLogs,
  getNodeMetrics,
  getNodeCapacity,
  getNodeSmart,
  listNodes,
  listWG,
  createWGPeer,
} from "../api/client";
import type { Capability, HardwareResponse, MetricsResponse, NodeSummary } from "../api/phase2";
import { MetricChart } from "../components/MetricChart";
import { Link } from "../components/Link";
import { Field } from "../components/Field";
import { GpuPage } from "./GpuPage";
import { formatBytes, formatWhen, honestStatus } from "../format";
import { usePath } from "../router";
import { useSession } from "../session";

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
        <p>Collecting</p>
      </section>
    );
  }
  if (nodes === "missing" || !id) {
    return (
      <section className="page">
        <h1>Node</h1>
        <p>No local node is enrolled yet.</p>
      </section>
    );
  }

  const tab = path.startsWith("/node/hardware")
    ? "hardware"
    : path.startsWith("/node/metrics")
      ? "metrics"
      : path.startsWith("/node/logs")
        ? "logs"
      : path.startsWith("/node/events")
        ? "events"
      : path.startsWith("/node/gpu")
        ? "gpu"
        : "summary";

  return (
    <section className="page page-wide" aria-labelledby="node-heading">
      <header className="page-header">
        <h1 id="node-heading">Node</h1>
        <p className="page-kicker">
          {nodes.host_os || "Host OS not reported"} · {honestStatus(nodes.status)}
          {nodes.stale ? " (stale)" : ""}
        </p>
      </header>
      <nav className="subnav" aria-label="Node sections">
        <Link href="/node" aria-current={tab === "summary" ? "page" : undefined}>
          Summary
        </Link>
        <Link href="/node/hardware" aria-current={tab === "hardware" ? "page" : undefined}>
          Hardware
        </Link>
        <Link href="/node/gpu" aria-current={tab === "gpu" ? "page" : undefined}>
          GPU
        </Link>
        <Link href="/node/metrics" aria-current={tab === "metrics" ? "page" : undefined}>
          Metrics
        </Link>
        <Link href="/node/logs" aria-current={tab === "logs" ? "page" : undefined}>
          Logs
        </Link>
        <Link href="/events" aria-current={tab === "events" ? "page" : undefined}>
          Events
        </Link>
        <Link href={`/nodes/${id}/terminal`}>Terminal</Link>
        <Link href={`/nodes/${id}/files`}>Files</Link>
      </nav>
      {tab === "summary" ? <NodeSummaryView id={id} fallback={nodes} /> : null}
      {tab === "hardware" ? <NodeHardwareView id={id} /> : null}
      {tab === "gpu" ? <GpuPage /> : null}
      {tab === "metrics" ? <NodeMetricsView id={id} /> : null}
      {tab === "logs" ? <NodeLogsView id={id} /> : null}
    </section>
  );
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
  );
}

function RemoteNodeHelper() {
  const session = useSession();
  const mutate = Boolean(
    session.status === "ready" && (session.user?.roles.includes("admin") || session.user?.roles.includes("operator")),
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
      const created = await createWGPeer({ name, endpoint: endpoint || undefined });
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
        another. Cluster join remains Phase 30. A down tunnel marks the worker NotReady; guests keep
        running.
      </p>
      {workers.length === 0 ? (
        <p>No remote workers yet.</p>
      ) : (
        <ul className="plain-list">
          {workers.map((n) => (
            <li key={n.id}>
              <strong>{n.name}</strong> {honestStatus(n.status)}
              {n.reason ? ` ${n.reason}` : ""}
              {n.listen_addr ? ` ${n.listen_addr}` : ""}
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
        <p className="banner banner-error" role="alert">
          {error}
        </p>
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
    return <p>Collecting</p>;
  }
  if (!hw.inventory) {
    return <p>{hw.message || honestStatus(hw.status)}</p>;
  }
  const inv = hw.inventory as Inventory;
  return (
    <div className="stack">
      {hw.stale ? (
        <p className="banner" role="status">
          This inventory is stale. Values are the last observation, not zeros.
        </p>
      ) : null}
      <HardwareTable title="CPU" rows={[inv.cpu ?? {}]} keys={["vendor", "model", "architecture", "sockets", "cores", "threads", "status"]} />
      <HardwareTable title="Memory" rows={[inv.memory ?? {}]} keys={["total_bytes", "available_bytes", "dimm_status", "status"]} />
      <HardwareTable title="Block devices" rows={inv.block_devices ?? []} keys={["name", "model", "size_bytes", "kind", "transport", "smart_status"]} />
      <HardwareTable title="Network interfaces" rows={inv.nics ?? []} keys={["name", "mac", "state", "speed_mbps", "driver", "kind"]} />
      <HardwareTable title="GPUs" rows={inv.gpus ?? []} keys={["id", "vendor", "pci", "driver"]} empty="None detected" />
      <HardwareTable title="PCI" rows={inv.pci ?? []} keys={["address", "vendor", "device", "class", "driver"]} />
      <HardwareTable title="USB" rows={inv.usb ?? []} keys={["address", "vendor", "product", "name"]} />
      <HardwareTable title="Temperatures" rows={inv.temperatures ?? []} keys={["id", "name", "label", "milli_c", "status"]} empty="Unavailable" />
      <HardwareTable title="Firmware" rows={[inv.firmware ?? {}]} keys={["sys_vendor", "product", "board", "bios_vendor", "bios_version", "status"]} />
      <HardwareTable
        title="IOMMU groups"
        rows={iommuRows(inv.iommu)}
        keys={["id", "devices"]}
        empty={iommuEmpty(inv.iommu)}
      />
    </div>
  );
}

function NodeMetricsView({ id }: { id: string }) {
  const [metrics, setMetrics] = useState<MetricsResponse | null>(null);
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

  return (
    <div className="stack">
      <label>
        Range
        <select value={range} onChange={(e) => setRange(e.target.value)}>
          <option value="60">Last hour</option>
          <option value="360">Last 6 hours</option>
          <option value="1440">Last day</option>
          <option value="10080">Last week</option>
        </select>
      </label>
      <p className="muted">Capacity: {capacity}</p>
      <p className="muted">SMART: {smart}</p>
      {!metrics ? (
        <p>Collecting data</p>
      ) : metrics.status === "stale" ? (
        <p className="chart-empty">Stale</p>
      ) : metrics.status === "unavailable" ? (
        <p className="chart-empty">Unavailable</p>
      ) : !metrics.series.length ? (
        <p className="chart-empty">Collecting data</p>
      ) : (
        <div className="card-grid">
          {metrics.series.map((series) => (
            <article className="panel" key={series.name}>
              <h2>{series.name}</h2>
              <MetricChart series={series} />
            </article>
          ))}
        </div>
      )}
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
      <label>
        Unit
        <select value={unit} onChange={(e) => setUnit(e.target.value)}>
          <option value="ndl-agent.service">ndl-agent</option>
          <option value="ndl-control.service">ndl-control</option>
        </select>
      </label>
      {status === "unavailable" ? <p className="chart-empty">Unavailable</p> : null}
      {message ? <p className="muted">{message}</p> : null}
      {lines.length === 0 && status !== "unavailable" ? <p>No log lines in this window</p> : null}
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
}: {
  title: string;
  rows: Record<string, unknown>[];
  keys: string[];
  empty?: string;
}) {
  const usable = rows.filter((row) => Object.keys(row).length > 0);
  return (
    <article className="panel">
      <h2>{title}</h2>
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
    </article>
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
