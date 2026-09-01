import { useEffect, useState, type ReactNode } from "react";
import {
  getNode,
  getNodeCapabilities,
  getNodeHardware,
  getNodeMetrics,
  listNodes,
} from "../api/client";
import type { Capability, HardwareResponse, MetricsResponse, NodeSummary } from "../api/phase2";
import { MetricChart } from "../components/MetricChart";
import { Link } from "../components/Link";
import { formatBytes, formatWhen, honestStatus } from "../format";
import { usePath } from "../router";

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
        if (!items[0]) {
          setNodes("missing");
          return;
        }
        setId(items[0].id);
        setNodes(items[0]);
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
      : path.startsWith("/node/events")
        ? "events"
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
        <Link href="/node/metrics" aria-current={tab === "metrics" ? "page" : undefined}>
          Metrics
        </Link>
        <Link href="/events" aria-current={tab === "events" ? "page" : undefined}>
          Events
        </Link>
        <Link href={`/nodes/${id}/terminal`}>Terminal</Link>
        <Link href={`/nodes/${id}/files`}>Files</Link>
      </nav>
      {tab === "summary" ? <NodeSummaryView id={id} fallback={nodes} /> : null}
      {tab === "hardware" ? <NodeHardwareView id={id} /> : null}
      {tab === "metrics" ? <NodeMetricsView id={id} /> : null}
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
    </div>
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

  useEffect(() => {
    let cancelled = false;
    void getNodeMetrics(id)
      .then((value) => {
        if (!cancelled) {
          setMetrics(value);
        }
      })
      .catch(() => undefined);
    return () => {
      cancelled = true;
    };
  }, [id]);

  if (!metrics) {
    return <p>Collecting data</p>;
  }
  if (metrics.status === "stale") {
    return <p className="chart-empty">Stale</p>;
  }
  if (metrics.status === "unavailable") {
    return <p className="chart-empty">Unavailable</p>;
  }
  if (!metrics.series.length) {
    return <p className="chart-empty">Collecting data</p>;
  }
  return (
    <div className="card-grid">
      {metrics.series.map((series) => (
        <article className="panel" key={series.name}>
          <h2>{series.name}</h2>
          <MetricChart series={series} />
        </article>
      ))}
    </div>
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
