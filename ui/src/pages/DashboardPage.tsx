import { useEffect, useState } from "react";
import {
  getHealth,
  getNodeMetrics,
  listEvents,
  listNetworks,
  listNodes,
  listPools,
  listTasks,
  listWorkloads,
} from "../api/client";
import type { EventItem, MetricsResponse, NodeSummary, TaskItem } from "../api/phase2";
import type { Network } from "../api/phase4";
import type { StoragePool } from "../api/phase3";
import { ErrorState } from "../components/EmptyState";
import { Icon } from "../components/Icon";
import { Link } from "../components/Link";
import { MetricChart, lastPoint } from "../components/MetricChart";
import { PageHeader } from "../components/PageHeader";
import { StatusBadge } from "../components/StatusBadge";
import { formatBytes, formatPercent, formatWhen } from "../format";
import { eventHeadline, humanTaskMessage } from "../humanize";
import { metricLabel, taskKindLabel } from "../labels";

export function DashboardPage() {
  const [node, setNode] = useState<NodeSummary | null>(null);
  const [metrics, setMetrics] = useState<MetricsResponse | null>(null);
  const [events, setEvents] = useState<EventItem[]>([]);
  const [tasks, setTasks] = useState<TaskItem[]>([]);
  const [pools, setPools] = useState<StoragePool[]>([]);
  const [networks, setNetworks] = useState<Network[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [needStorage, setNeedStorage] = useState(false);
  const [needNetwork, setNeedNetwork] = useState(false);
  const [needWorkload, setNeedWorkload] = useState(false);
  const [workloadCounts, setWorkloadCounts] = useState({ running: 0, stopped: 0, other: 0 });
  const [healthOk, setHealthOk] = useState<boolean | null>(null);
  const [loaded, setLoaded] = useState(false);

  useEffect(() => {
    let cancelled = false;
    void (async () => {
      try {
        const nodes = await listNodes();
        const first = nodes[0] ?? null;
        if (cancelled) {
          return;
        }
        setNode(first);
        const [ev, tk, met, listedPools, nets, wls, health] = await Promise.all([
          listEvents().catch(() => []),
          listTasks().catch(() => []),
          first ? getNodeMetrics(first.id).catch(() => null) : Promise.resolve(null),
          listPools().catch(() => ({ items: [] })),
          listNetworks().catch(() => ({ items: [] })),
          listWorkloads().catch(() => ({ items: [] })),
          getHealth().catch(() => null),
        ]);
        if (cancelled) {
          return;
        }
        setEvents(ev.slice(0, 6));
        setTasks(tk.slice(0, 6));
        setMetrics(met);
        setPools(listedPools.items ?? []);
        setNetworks(nets.items ?? []);
        const usable = (listedPools.items ?? []).some((p) => p.status === "available" || p.status === "warning");
        setNeedStorage(!usable);
        const netReady = (nets.items ?? []).some((n) => n.status === "available" || n.status === "warning");
        setNeedNetwork(!netReady);
        const items = wls.items ?? [];
        setNeedWorkload(usable && netReady && items.length === 0);
        setWorkloadCounts({
          running: items.filter((w) => w.status === "running").length,
          stopped: items.filter((w) => w.status === "stopped").length,
          other: items.filter((w) => w.status !== "running" && w.status !== "stopped").length,
        });
        setHealthOk(health?.status === "ok");
        setLoaded(true);
      } catch (err) {
        if (!cancelled) {
          setError(err instanceof Error ? err.message : "Unavailable");
          setLoaded(true);
        }
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  const cpuSeries = metrics?.series.find((s) => s.name === "cpu.busy_ratio");
  const memSeries = metrics?.series.find((s) => s.name === "memory.used_bytes");
  const cpuNow = lastPoint(cpuSeries);
  const memNow = lastPoint(memSeries);
  const usableBytes = pools.reduce((sum, p) => sum + (p.usable_bytes ?? 0), 0);
  const allocatedBytes = pools.reduce((sum, p) => sum + (p.allocated_bytes ?? 0), 0);
  const attention = [
    needStorage ? { href: "/storage", text: "No usable storage pool yet. Create a Directory pool on the Storage page." } : null,
    needNetwork ? { href: "/network", text: "No guest network yet. Create an isolated network on the Network page." } : null,
    needWorkload
      ? { href: "/workloads", text: "Storage and network are ready. Create a system container on the Workloads page." }
      : null,
  ].filter((item): item is { href: string; text: string } => Boolean(item));
  const failedTasks = tasks.filter((t) => t.state === "failed");
  const poolWarn = pools.find((p) => p.status === "warning" || (p.warning_text?.length ?? 0) > 0);

  return (
    <section className="page" aria-labelledby="dashboard-heading">
      <PageHeader id="dashboard-heading" title="Dashboard" kicker="Appliance health and capacity" />
      {error ? <ErrorState>{error}</ErrorState> : null}
      <div className="status-strip">
        <div className="status-tile">
          <span className="label">Node</span>
          <span className="value">
            {node ? <StatusBadge status={node.status} /> : loaded ? "Not enrolled" : "Loading"}
            {node?.stale ? " (stale)" : ""}
          </span>
          <span className="meta">{node?.name || "No local node"}</span>
        </div>
        <div className="status-tile">
          <span className="label">Workloads</span>
          <span className="value">
            {workloadCounts.running} running, {workloadCounts.stopped} stopped
            {workloadCounts.other ? `, ${workloadCounts.other} other` : ""}
          </span>
          <span className="meta">
            {healthOk == null ? "Control plane loading" : healthOk ? "Control plane available" : "Control plane unavailable"}
          </span>
        </div>
        <div className="status-tile">
          <span className="label">Memory</span>
          <span className="value">
            {memNow != null && node?.memory_bytes
              ? `${formatBytes(memNow)} / ${formatBytes(node.memory_bytes)}`
              : node
                ? formatBytes(node.memory_bytes)
                : "Not reported"}
          </span>
          <span className="meta">{cpuNow != null ? `CPU ${formatPercent(cpuNow)}` : "CPU collecting"}</span>
        </div>
        <div className="status-tile">
          <span className="label">Storage</span>
          <span className="value">{pools.length ? formatBytes(usableBytes) : "Not reported"}</span>
          <span className="meta">
            {pools.length ? `${formatBytes(allocatedBytes)} allocated` : "No pool yet"}
          </span>
        </div>
      </div>
      <div className="meter-grid">
        <div className="meter">
          <div className="meter-head">
            <h2>{metricLabel("cpu.busy_ratio")}</h2>
            <span className="meter-value">{cpuNow != null ? formatPercent(cpuNow) : "Collecting"}</span>
          </div>
          <Meter series={cpuSeries} overall={metrics?.status} />
        </div>
        <div className="meter">
          <div className="meter-head">
            <h2>{metricLabel("memory.used_bytes")}</h2>
            <span className="meter-value">
              {memNow != null && node?.memory_bytes
                ? `${formatBytes(memNow)} / ${formatBytes(node.memory_bytes)}`
                : "Collecting"}
            </span>
          </div>
          <Meter series={memSeries} overall={metrics?.status} />
        </div>
      </div>
      <section className="section">
        <div className="section-head">
          <h2>Attention</h2>
        </div>
        {attention.length > 0 || failedTasks.length > 0 || poolWarn ? (
          <div className="stack">
            {attention.map((item) => (
              <p key={item.href} className="banner banner-warn" role="status">
                {item.text} <Link href={item.href}>Open</Link>
              </p>
            ))}
            {poolWarn?.warning_text?.[0] ? (
              <p className="banner banner-warn" role="status">
                {poolWarn.warning_text[0]} <Link href="/storage">Storage</Link>
              </p>
            ) : null}
            {failedTasks.map((task) => (
              <p key={task.id} className="banner banner-error" role="alert">
                {taskKindLabel(task.kind)} failed{task.message ? `: ${task.message}` : ""}
              </p>
            ))}
          </div>
        ) : (
          <p className="attention-ok">
            <Icon name="success" size={14} />
            Nothing requires attention.
          </p>
        )}
      </section>
      {!node && loaded ? (
        <div className="empty-panel">
          <p className="empty-title">No local node</p>
          <p>The local node has not reported inventory yet.</p>
        </div>
      ) : null}
      {node ? (
        <section className="section">
          <div className="section-head">
            <h2>Infrastructure</h2>
            <Link href="/node">Open node</Link>
          </div>
          <dl className="definition-list compact">
            <div>
              <dt>Name</dt>
              <dd>
                <Link href="/node">{node.name}</Link>
              </dd>
            </div>
            <div>
              <dt>Host OS</dt>
              <dd>{node.host_os || "Not reported"}</dd>
            </div>
            <div>
              <dt>CPU</dt>
              <dd>
                {node.cpu_model || "Not reported"}
                {node.cpu_cores ? ` (${node.cpu_cores} cores, ${node.cpu_threads ?? "not reported"} threads)` : ""}
              </dd>
            </div>
            <div>
              <dt>GPU</dt>
              <dd>{node.gpu_present ? `${node.gpu_count} detected` : "None detected"}</dd>
            </div>
            <div>
              <dt>Storage</dt>
              <dd>
                {pools.length ? `${pools.length} pool${pools.length === 1 ? "" : "s"}` : "None"} ·{" "}
                <Link href="/storage">Open</Link>
              </dd>
            </div>
            <div>
              <dt>Network</dt>
              <dd>
                {networks.length ? `${networks.length} guest network${networks.length === 1 ? "" : "s"}` : "None"} ·{" "}
                <Link href="/network">Open</Link>
              </dd>
            </div>
          </dl>
        </section>
      ) : null}
      <div className="split-grid">
        <section className="section">
          <div className="section-head">
            <h2>Recent events</h2>
            <Link href="/events">All events</Link>
          </div>
          {events.length === 0 ? (
            <p>No events yet.</p>
          ) : (
            <ul className="activity-list">
              {events.map((e) => (
                <li key={e.id}>
                  <Icon name="events" size={14} />
                  <span>{eventHeadline(e.type, e.payload)}</span>
                  <span className="muted">{formatWhen(e.created_at)}</span>
                </li>
              ))}
            </ul>
          )}
        </section>
        <section className="section">
          <div className="section-head">
            <h2>Recent tasks</h2>
            <Link href="/tasks">All tasks</Link>
          </div>
          {tasks.length === 0 ? (
            <p>No tasks yet.</p>
          ) : (
            <ul className="activity-list">
              {tasks.map((t) => (
                <li key={t.id}>
                  <StatusBadge status={t.state} />
                  <span>
                    {taskKindLabel(t.kind)}
                    {humanTaskMessage(t.message) ? ` ${humanTaskMessage(t.message)}` : ""}
                  </span>
                  <span className="muted">{formatWhen(t.updated_at)}</span>
                </li>
              ))}
            </ul>
          )}
        </section>
      </div>
    </section>
  );
}

function Meter({
  series,
  overall,
}: {
  series?: import("../api/phase2").MetricSeries;
  overall?: string;
}) {
  if (overall === "stale") {
    return (
      <p className="chart-empty" role="status">
        Stale
      </p>
    );
  }
  if (overall === "unavailable") {
    return (
      <p className="chart-empty" role="status">
        Unavailable
      </p>
    );
  }
  if (!series) {
    return (
      <p className="chart-empty" role="status">
        Collecting data
      </p>
    );
  }
  return <MetricChart series={series} />;
}
