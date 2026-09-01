import { useEffect, useState } from "react";
import { getNodeMetrics, listEvents, listNetworks, listNodes, listPools, listTasks, listWorkloads } from "../api/client";
import type { EventItem, MetricsResponse, NodeSummary, TaskItem } from "../api/phase2";
import { MetricChart } from "../components/MetricChart";
import { Link } from "../components/Link";
import { formatBytes, formatWhen, honestStatus } from "../format";
import { useSession } from "../session";

export function DashboardPage() {
  const session = useSession();
  const username = session.status === "ready" ? session.user?.username : undefined;
  const [node, setNode] = useState<NodeSummary | null>(null);
  const [metrics, setMetrics] = useState<MetricsResponse | null>(null);
  const [events, setEvents] = useState<EventItem[]>([]);
  const [tasks, setTasks] = useState<TaskItem[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [needStorage, setNeedStorage] = useState(false);
  const [needNetwork, setNeedNetwork] = useState(false);
  const [needWorkload, setNeedWorkload] = useState(false);

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
        const [ev, tk, met, pools, nets, wls] = await Promise.all([
          listEvents().catch(() => []),
          listTasks().catch(() => []),
          first ? getNodeMetrics(first.id).catch(() => null) : Promise.resolve(null),
          listPools().catch(() => ({ items: [] })),
          listNetworks().catch(() => ({ items: [] })),
          listWorkloads().catch(() => ({ items: [] })),
        ]);
        if (cancelled) {
          return;
        }
        setEvents(ev.slice(0, 5));
        setTasks(tk.slice(0, 5));
        setMetrics(met);
        const usable = (pools.items ?? []).some((p) => p.status === "available" || p.status === "warning");
        setNeedStorage(!usable);
        const netReady = (nets.items ?? []).some((n) => n.status === "available" || n.status === "warning");
        setNeedNetwork(!netReady);
        const haveWL = (wls.items ?? []).length > 0;
        setNeedWorkload(usable && netReady && !haveWL);
      } catch (err) {
        if (!cancelled) {
          setError(err instanceof Error ? err.message : "Unavailable");
        }
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  const cpuSeries = metrics?.series.find((s) => s.name === "cpu.busy_ratio");
  const memSeries = metrics?.series.find((s) => s.name === "memory.used_bytes");

  return (
    <section className="page page-wide" aria-labelledby="dashboard-heading">
      <header className="page-header">
        <h1 id="dashboard-heading">Dashboard</h1>
        {username ? <p className="page-kicker">Signed in as {username}</p> : null}
      </header>
      {error ? (
        <p className="banner banner-error" role="alert">
          {error}
        </p>
      ) : null}
      {needStorage ? (
        <p className="banner banner-warn" role="status">
          No usable storage pool yet. Create a Directory pool on the{" "}
          <Link href="/storage">Storage</Link> page.
        </p>
      ) : null}
      {needNetwork ? (
        <p className="banner banner-warn" role="status">
          No guest network yet. Create an isolated network on the{" "}
          <Link href="/network">Network</Link> page.
        </p>
      ) : null}
      {needWorkload ? (
        <p className="banner banner-warn" role="status">
          Storage and network are ready. Create a system container on the{" "}
          <Link href="/workloads">Workloads</Link> page.
        </p>
      ) : null}
      {!node ? (
        <div className="panel empty-panel">
          <p className="empty-title">Collecting</p>
          <p>The local node has not reported inventory yet.</p>
        </div>
      ) : (
        <div className="card-grid">
          <article className="panel">
            <h2>Local node</h2>
            <dl className="definition-list">
              <div>
                <dt>Name</dt>
                <dd>{node.name}</dd>
              </div>
              <div>
                <dt>Status</dt>
                <dd>{honestStatus(node.status)}</dd>
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
                <dt>Memory</dt>
                <dd>{formatBytes(node.memory_bytes)}</dd>
              </div>
              <div>
                <dt>Disks</dt>
                <dd>
                  {node.status === "collecting"
                    ? "Collecting"
                    : `${node.disk_count ?? "Not reported"} (${formatBytes(node.disk_bytes)})`}
                </dd>
              </div>
              <div>
                <dt>NICs</dt>
                <dd>{node.status === "collecting" ? "Collecting" : String(node.nic_count ?? "Not reported")}</dd>
              </div>
              <div>
                <dt>GPU</dt>
                <dd>{node.gpu_present ? `${node.gpu_count} detected` : "None detected"}</dd>
              </div>
            </dl>
            <p>
              <Link href="/node">Node details</Link>
            </p>
          </article>
          <article className="panel">
            <h2>CPU</h2>
            <Meter series={cpuSeries} overall={metrics?.status} />
          </article>
          <article className="panel">
            <h2>Memory</h2>
            <Meter series={memSeries} overall={metrics?.status} />
          </article>
          <article className="panel">
            <h2>Recent events</h2>
            {events.length === 0 ? (
              <p>No events yet.</p>
            ) : (
              <ul className="plain-list">
                {events.map((e) => (
                  <li key={e.id}>
                    {e.type} <span className="muted">{formatWhen(e.created_at)}</span>
                  </li>
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
                    {t.kind} {t.state}
                    {t.stage ? ` (${t.stage})` : ""}
                  </li>
                ))}
              </ul>
            )}
            <p>
              <Link href="/tasks">All tasks</Link>
            </p>
          </article>
        </div>
      )}
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
