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
import type { StoragePool } from "../api/phase3";
import { ErrorState } from "../components/EmptyState";
import { Link } from "../components/Link";
import { MetricChart } from "../components/MetricChart";
import { PageHeader } from "../components/PageHeader";
import { StatusBadge } from "../components/StatusBadge";
import { formatBytes } from "../format";
import { eventTypeLabel, metricLabel, taskKindLabel } from "../labels";

export function DashboardPage() {
  const [node, setNode] = useState<NodeSummary | null>(null);
  const [metrics, setMetrics] = useState<MetricsResponse | null>(null);
  const [events, setEvents] = useState<EventItem[]>([]);
  const [tasks, setTasks] = useState<TaskItem[]>([]);
  const [pools, setPools] = useState<StoragePool[]>([]);
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
        setEvents(ev.slice(0, 5));
        setTasks(tk.slice(0, 5));
        setMetrics(met);
        setPools(listedPools.items ?? []);
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

  return (
    <section className="page" aria-labelledby="dashboard-heading">
      <PageHeader id="dashboard-heading" title="Dashboard" kicker="Appliance health" />
      {error ? <ErrorState>{error}</ErrorState> : null}
      <div className="card-grid">
        <article className="panel">
          <h2>Health</h2>
          <dl className="definition-list compact">
            <div>
              <dt>Node</dt>
              <dd>
                {node ? <StatusBadge status={node.status} /> : loaded ? "Not enrolled" : "Loading"}
                {node?.stale ? " (stale)" : ""}
              </dd>
            </div>
            <div>
              <dt>Control plane</dt>
              <dd>
                {healthOk == null ? "Loading" : healthOk ? <StatusBadge status="ok" label="Available" /> : "Unavailable"}
              </dd>
            </div>
            <div>
              <dt>Workloads</dt>
              <dd>
                {workloadCounts.running} running, {workloadCounts.stopped} stopped
                {workloadCounts.other ? `, ${workloadCounts.other} other` : ""}
              </dd>
            </div>
          </dl>
        </article>
        <article className="panel">
          <h2>Capacity</h2>
          <dl className="definition-list compact">
            <div>
              <dt>Memory</dt>
              <dd>{node ? formatBytes(node.memory_bytes) : "Not reported"}</dd>
            </div>
            <div>
              <dt>Storage usable</dt>
              <dd>{pools.length ? formatBytes(usableBytes) : "Not reported"}</dd>
            </div>
            <div>
              <dt>Storage allocated</dt>
              <dd>{pools.length ? formatBytes(allocatedBytes) : "Not reported"}</dd>
            </div>
          </dl>
        </article>
        <article className="panel">
          <h2>{metricLabel("cpu.busy_ratio")}</h2>
          <Meter series={cpuSeries} overall={metrics?.status} />
        </article>
        <article className="panel">
          <h2>{metricLabel("memory.used_bytes")}</h2>
          <Meter series={memSeries} overall={metrics?.status} />
        </article>
      </div>
      {attention.length > 0 || failedTasks.length > 0 ? (
        <article className="panel stack">
          <h2>Attention</h2>
          {attention.map((item) => (
            <p key={item.href} className="banner banner-warn" role="status">
              {item.text} <Link href={item.href}>Open</Link>
            </p>
          ))}
          {failedTasks.map((task) => (
            <p key={task.id} className="banner banner-error" role="alert">
              {taskKindLabel(task.kind)} failed{task.message ? `: ${task.message}` : ""}
            </p>
          ))}
        </article>
      ) : null}
      {!node && loaded ? (
        <article className="panel empty-panel">
          <p className="empty-title">No local node</p>
          <p>The local node has not reported inventory yet.</p>
        </article>
      ) : null}
      {node ? (
        <article className="panel">
          <h2>Node</h2>
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
                {node.cpu_cores ? ` (${node.cpu_cores} cores, ${node.cpu_threads ?? "?"} threads)` : ""}
              </dd>
            </div>
            <div>
              <dt>GPU</dt>
              <dd>{node.gpu_present ? `${node.gpu_count} detected` : "None detected"}</dd>
            </div>
          </dl>
        </article>
      ) : null}
      <div className="card-grid">
        <article className="panel">
          <h2>Recent events</h2>
          {events.length === 0 ? (
            <p>No events yet.</p>
          ) : (
            <ul className="plain-list">
              {events.map((e) => (
                <li key={e.id}>{eventTypeLabel(e.type)}</li>
              ))}
            </ul>
          )}
          <p>
            <Link href="/events">All events</Link>
          </p>
        </article>
        <article className="panel">
          <h2>Recent tasks</h2>
          {tasks.length === 0 ? (
            <p>No tasks yet.</p>
          ) : (
            <ul className="plain-list">
              {tasks.map((t) => (
                <li key={t.id}>
                  {taskKindLabel(t.kind)} <StatusBadge status={t.state} />
                  {t.message ? ` ${t.message}` : ""}
                </li>
              ))}
            </ul>
          )}
          <p>
            <Link href="/tasks">All tasks</Link>
          </p>
        </article>
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
