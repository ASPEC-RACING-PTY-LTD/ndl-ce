import { useEffect, useState, type ReactNode } from "react";
import {
  getNode,
  getNodeCapabilities,
  getNodeHardware,
  getNodeMetrics,
  listNodes,
} from "../api/client";
import type { Capability, HardwareResponse, MetricsResponse, NodeSummary } from "../api/phase2";
import { LoadingState } from "../components/EmptyState";
import { Icon } from "../components/Icon";
import { Link } from "../components/Link";
import { lastPoint, MetricChart } from "../components/MetricChart";
import { PageHeader } from "../components/PageHeader";
import { StatusBadge } from "../components/StatusBadge";
import { formatBytes, formatMbps, formatMetricValue, formatNicState, formatTempMilliC, formatWhen } from "../format";
import { capabilityLabel, hardwareKeyLabel, metricLabel } from "../labels";
import { currentPath } from "../router";

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

function nodeTab(): "summary" | "hardware" | "metrics" {
  const path = currentPath();
  if (path.endsWith("/hardware") || path.startsWith("/node/hardware")) {
    return "hardware";
  }
  if (path.endsWith("/metrics") || path.startsWith("/node/metrics")) {
    return "metrics";
  }
  return "summary";
}

export function NodePage() {
  const tab = nodeTab();
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
        const parts = currentPath().split("/").filter(Boolean);
        const fromPath = parts[0] === "nodes" ? parts[1] : undefined;
        const chosen = items.find((n) => n.id === fromPath) ?? items[0];
        if (!chosen) {
          setNodes("missing");
          return;
        }
        setId(chosen.id);
        setNodes(chosen);
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
      <section className="page" aria-labelledby="node-heading">
        <PageHeader id="node-heading" title="Node" />
        <LoadingState />
      </section>
    );
  }
  if (nodes === "missing" || !id) {
    return (
      <section className="page" aria-labelledby="node-heading">
        <PageHeader id="node-heading" title="Node" kicker="No local node is enrolled yet" />
      </section>
    );
  }

  return (
    <section className="page" aria-labelledby="node-heading">
      <PageHeader
        id="node-heading"
        title="Node"
        kicker={
          <>
            {nodes.host_os || "Host OS not reported"} · <StatusBadge status={nodes.status} />
            {nodes.stale ? " (stale)" : ""}
          </>
        }
      />
      <nav className="subnav" aria-label="Node sections">
        <Link href={`/nodes/${id}`} aria-current={tab === "summary" ? "page" : undefined}>
          Summary
        </Link>
        <Link href={`/nodes/${id}/hardware`} aria-current={tab === "hardware" ? "page" : undefined}>
          Hardware
        </Link>
        <Link href={`/nodes/${id}/metrics`} aria-current={tab === "metrics" ? "page" : undefined}>
          Metrics
        </Link>
        <Link href={`/nodes/${id}/terminal`}>
          <Icon name="terminal" size={14} />
          Terminal
        </Link>
        <Link href={`/nodes/${id}/files`}>
          <Icon name="files" size={14} />
          Files
        </Link>
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
    <div className="split-grid">
      <section className="section">
        <h2>Summary</h2>
        <dl className="definition-list compact">
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
              {node.cpu_sockets ?? "Not reported"} sockets, {node.cpu_cores ?? "Not reported"} cores,{" "}
              {node.cpu_threads ?? "Not reported"} threads
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
        <p>
          <Link href="/events">Events</Link>
        </p>
      </section>
      <section className="section">
        <h2>Capabilities</h2>
        {caps.length === 0 ? (
          <p>Not reported</p>
        ) : (
          <ul className="plain-list">
            {caps.map((c) => (
              <li key={c.id}>
                {capabilityLabel(c.id)}: <StatusBadge status={c.status} />
                {c.detail ? ` (${c.detail})` : ""}
              </li>
            ))}
          </ul>
        )}
      </section>
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
    return <LoadingState />;
  }
  if (!hw.inventory) {
    return <p>{hw.message || "Not reported"}</p>;
  }
  const inv = hw.inventory as Inventory;
  return (
    <div className="stack">
      {hw.stale ? (
        <p className="banner banner-warn" role="status">
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
      <HardwareTable title="IOMMU groups" rows={iommuRows(inv.iommu)} keys={["id", "devices"]} empty={iommuEmpty(inv.iommu)} />
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
    return (
      <p className="chart-empty" role="status">
        Collecting data
      </p>
    );
  }
  if (metrics.status === "stale") {
    return <p className="chart-empty">Stale</p>;
  }
  if (metrics.status === "unavailable") {
    return <p className="chart-empty">Unavailable</p>;
  }
  if (!metrics.series.length) {
    return (
      <p className="chart-empty" role="status">
        Collecting data
      </p>
    );
  }
  return (
    <div className="meter-grid">
      {metrics.series.map((series) => {
        const now = lastPoint(series);
        return (
          <div className="meter" key={series.name}>
            <div className="meter-head">
              <h2>{metricLabel(series.name)}</h2>
              <span className="meter-value">
                {now != null ? formatMetricValue(series.name, now, series.unit) : "Collecting"}
              </span>
            </div>
            <MetricChart series={series} />
          </div>
        );
      })}
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
    <section className="section">
      <h2>{title}</h2>
      {usable.length === 0 ? (
        <p>{empty || "Not reported"}</p>
      ) : (
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                {keys.map((k) => (
                  <th key={k}>{hardwareKeyLabel(k)}</th>
                ))}
              </tr>
            </thead>
            <tbody>
              {usable.map((row, i) => (
                <tr key={i}>
                  {keys.map((k) => (
                    <td key={k}>{cell(row[k], k)}</td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
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

function cell(value: unknown, key?: string): ReactNode {
  if (value == null || value === "") {
    return "Not reported";
  }
  if (key === "milli_c" && typeof value === "number") {
    return formatTempMilliC(value);
  }
  if (key === "speed_mbps" && typeof value === "number") {
    return formatMbps(value);
  }
  if (key === "state" && typeof value === "string") {
    return formatNicState(value);
  }
  if (typeof value === "boolean") {
    return value ? "Yes" : "No";
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
