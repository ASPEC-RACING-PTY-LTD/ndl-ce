import { useEffect, useMemo, useState } from "react";
import {
  ApiError,
  dockerContainerAction,
  dockerContainerLogs,
  getDocker,
} from "../api/client";
import { ActionMenu } from "../components/ActionMenu";
import { EmptyState, ErrorState, LoadingState } from "../components/EmptyState";
import { Link } from "../components/Link";
import { PageHeader } from "../components/PageHeader";
import { ResourceTable } from "../components/ResourceTable";
import { StatusBadge } from "../components/StatusBadge";
import type { DockerContainer, DockerInventory, DockerMachine, DockerProject } from "../generated/openapi";
import { formatBytes, formatWhen } from "../format";
import { navigate } from "../router";
import { canMutate } from "../rbac";
import { useSession } from "../session";
import { useTerminalWorkspace } from "../terminal/workspace";
import type { TermTarget } from "../terminal/types";

type ViewMode = "hierarchy" | "flat";

function healthTone(health?: string): string {
  if (health === "critical") {
    return "critical";
  }
  if (health === "degraded") {
    return "degraded";
  }
  if (health === "healthy") {
    return "healthy";
  }
  return health || "unknown";
}

function matchesQuery(c: DockerContainer, q: string): boolean {
  if (!q) {
    return true;
  }
  const hay = [
    c.name,
    c.image,
    c.project,
    c.service,
    c.machine_name,
    c.machine_ip,
    c.status_label,
    c.working_dir,
    ...(c.networks ?? []),
  ]
    .join(" ")
    .toLowerCase();
  return hay.includes(q);
}

function dockerTarget(c: DockerContainer): TermTarget {
  return {
    kind: "docker",
    id: `${c.machine_id}/${c.container_id}`,
    name: c.name || c.container_id,
    group: "application",
    typeLabel: c.service ? `Docker · ${c.service}` : "Docker container",
    status: c.state,
    terminalReady: c.state === "running",
  };
}

function portText(c: DockerContainer): string {
  const ports = c.ports ?? [];
  if (ports.length === 0) {
    return "None";
  }
  return ports
    .slice(0, 3)
    .map((p) => (p.public_port ? `${p.public_port}:${p.private_port}` : String(p.private_port)))
    .join(", ");
}

export function DockerPage() {
  const session = useSession();
  const roles = session.status === "ready" ? session.user?.roles : undefined;
  const mutate = canMutate(roles);
  const { openOrFocus } = useTerminalWorkspace();
  const [inv, setInv] = useState<DockerInventory | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [disabled, setDisabled] = useState(false);
  const [busy, setBusy] = useState<string | null>(null);
  const [query, setQuery] = useState("");
  const [healthFilter, setHealthFilter] = useState("all");
  const [machineFilter, setMachineFilter] = useState("all");
  const [view, setView] = useState<ViewMode>("hierarchy");
  const [openMachines, setOpenMachines] = useState<Record<string, boolean>>({});
  const [openProjects, setOpenProjects] = useState<Record<string, boolean>>({});
  const [selected, setSelected] = useState<string | null>(null);
  const [logs, setLogs] = useState<string>("");
  const [logFor, setLogFor] = useState<string | null>(null);

  async function reload(refresh = false) {
    try {
      const next = await getDocker(refresh);
      setInv(next);
      setDisabled(false);
      setError(null);
      setOpenMachines((cur) => {
        const copy = { ...cur };
        for (const m of next.machines ?? []) {
          if (copy[m.id] === undefined) {
            copy[m.id] = true;
          }
        }
        return copy;
      });
    } catch (err) {
      if (err instanceof ApiError && err.status === 404) {
        setDisabled(true);
        setInv(null);
        setError(null);
        return;
      }
      setError(err instanceof Error ? err.message : "Unavailable");
    }
  }

  useEffect(() => {
    void reload();
    const timer = window.setInterval(() => {
      void reload();
    }, 5000);
    return () => window.clearInterval(timer);
  }, []);

  const q = query.trim().toLowerCase();
  const containers = useMemo(() => {
    const items = inv?.containers ?? [];
    return items.filter((c) => {
      if (machineFilter !== "all" && c.machine_id !== machineFilter) {
        return false;
      }
      if (healthFilter !== "all" && c.health !== healthFilter) {
        return false;
      }
      return matchesQuery(c, q);
    });
  }, [inv, q, healthFilter, machineFilter]);

  const selectedRow = (inv?.containers ?? []).find((c) => c.id === selected) ?? null;

  async function runAction(c: DockerContainer, action: "start" | "stop" | "restart" | "pull" | "recreate") {
    setBusy(`${c.id}:${action}`);
    setError(null);
    try {
      await dockerContainerAction(c.machine_id, c.container_id, action);
      await reload(true);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Action failed");
    } finally {
      setBusy(null);
    }
  }

  async function loadLogs(c: DockerContainer) {
    setLogFor(c.id);
    try {
      const res = await dockerContainerLogs(c.machine_id, c.container_id, 200);
      setLogs(res.logs || "(no log output)");
    } catch (err) {
      setLogs(err instanceof Error ? err.message : "Logs unavailable");
    }
  }

  function openTerm(c: DockerContainer) {
    openOrFocus(dockerTarget(c));
    navigate("/terminal");
  }

  if (disabled) {
    return (
      <section className="page page-wide" aria-labelledby="docker-heading">
        <PageHeader
          id="docker-heading"
          title="Docker"
          kicker="Optional Docker Management across No-DAL machines. Discovery stays off until you enable the feature."
        />
        <EmptyState title="Docker Management is not enabled">
          Enable it from <Link href="/settings/features">Add Features</Link>. No Docker sockets are probed while the
          module is off.
        </EmptyState>
      </section>
    );
  }

  if (!inv && !error) {
    return (
      <section className="page">
        <LoadingState label="Discovering Docker engines" />
      </section>
    );
  }

  const summary = inv?.summary;
  const machines = inv?.machines ?? [];

  return (
    <section className="page page-wide" aria-labelledby="docker-heading">
      <PageHeader
        id="docker-heading"
        title="Docker"
        kicker="Workload and host engines, grouped by Compose project. Health rolls up from containers to projects and machines."
        actions={
          <button className="btn btn-secondary" type="button" onClick={() => void reload(true)}>
            Refresh
          </button>
        }
      />
      {error ? <ErrorState>{error}</ErrorState> : null}
      <div className="status-strip">
        <div className="status-tile">
          <span className="label">Machines</span>
          <span className="value">{summary?.machines ?? 0}</span>
          <span className="meta">{summary?.daemons_down ? `${summary.daemons_down} daemon down` : "Engines reachable"}</span>
        </div>
        <div className="status-tile">
          <span className="label">Projects</span>
          <span className="value">{summary?.projects ?? 0}</span>
          <span className="meta">
            {summary?.running ?? 0} running, {summary?.stopped ?? 0} stopped
          </span>
        </div>
        <div className="status-tile">
          <span className="label">Healthy</span>
          <span className="value">
            <StatusBadge status="healthy" /> {summary?.healthy ?? 0}
          </span>
          <span className="meta">Machine rollup</span>
        </div>
        <div className="status-tile">
          <span className="label">Attention</span>
          <span className="value">
            <StatusBadge status="degraded" /> {summary?.degraded ?? 0}
            {" · "}
            <StatusBadge status="critical" /> {summary?.critical ?? 0}
          </span>
          <span className="meta">{summary?.update_failed ? `${summary.update_failed} update failed` : "No update failures"}</span>
        </div>
      </div>

      <article className="panel docker-toolbar">
        <div className="field-row docker-filters">
          <div className="field">
            <label className="field-label" htmlFor="docker-search">
              Search
            </label>
            <input
              id="docker-search"
              className="field-input"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder="Name, image, project, IP"
            />
          </div>
          <div className="field">
            <label className="field-label" htmlFor="docker-health">
              Health
            </label>
            <select id="docker-health" className="field-input" value={healthFilter} onChange={(e) => setHealthFilter(e.target.value)}>
              <option value="all">All</option>
              <option value="healthy">Healthy</option>
              <option value="degraded">Degraded</option>
              <option value="critical">Critical</option>
            </select>
          </div>
          <div className="field">
            <label className="field-label" htmlFor="docker-machine">
              Machine
            </label>
            <select id="docker-machine" className="field-input" value={machineFilter} onChange={(e) => setMachineFilter(e.target.value)}>
              <option value="all">All machines</option>
              {machines.map((m) => (
                <option key={m.id} value={m.id}>
                  {m.name}
                </option>
              ))}
            </select>
          </div>
          <div className="field">
            <label className="field-label" htmlFor="docker-view">
              View
            </label>
            <select id="docker-view" className="field-input" value={view} onChange={(e) => setView(e.target.value as ViewMode)}>
              <option value="hierarchy">Hierarchy</option>
              <option value="flat">Flat containers</option>
            </select>
          </div>
        </div>
      </article>

      {machines.length === 0 ? (
        <EmptyState title="No Docker engines discovered">
          Install Docker in a system container or on the host. No-DAL does not install Docker Engine when this feature
          is enabled.
        </EmptyState>
      ) : view === "flat" ? (
        <article className="panel">
          <h2>Containers</h2>
          <ResourceTable
            headers={["Container", "Machine", "Project", "Status", "Image", "Ports", "Restarts", ""]}
            rows={containers.map((c) => [
              <button key="n" className="linkish" type="button" onClick={() => setSelected(c.id)}>
                {c.name}
              </button>,
              c.machine_name || c.machine_id,
              c.project || "Standalone",
              <StatusBadge key="s" status={healthTone(c.health)} label={c.status_label} />,
              c.image || "Not reported",
              portText(c),
              String(c.restart_count ?? 0),
              mutate ? (
                <ActionMenu
                  key="a"
                  items={[
                    { label: "Start", onClick: () => void runAction(c, "start") },
                    { label: "Stop", onClick: () => void runAction(c, "stop") },
                    { label: "Restart", onClick: () => void runAction(c, "restart") },
                    { label: "Logs", onClick: () => void loadLogs(c) },
                    { label: "Terminal", onClick: () => openTerm(c) },
                    { label: "Pull image", onClick: () => void runAction(c, "pull") },
                    { label: "Recreate", onClick: () => void runAction(c, "recreate") },
                  ]}
                />
              ) : (
                ""
              ),
            ])}
            empty={<p>No containers match the current filters.</p>}
          />
        </article>
      ) : (
        machines
          .filter((m) => machineFilter === "all" || m.id === machineFilter)
          .map((m) => (
            <MachineBlock
              key={m.id}
              machine={m}
              open={openMachines[m.id] !== false}
              onToggle={() => setOpenMachines((cur) => ({ ...cur, [m.id]: !(cur[m.id] !== false) }))}
              openProjects={openProjects}
              onToggleProject={(id) => setOpenProjects((cur) => ({ ...cur, [id]: !cur[id] }))}
              query={q}
              healthFilter={healthFilter}
              selected={selected}
              onSelect={setSelected}
              mutate={mutate}
              busy={busy}
              onAction={runAction}
              onLogs={loadLogs}
              onTerm={openTerm}
            />
          ))
      )}

      {selectedRow ? (
        <ContainerDetail
          container={selectedRow}
          logs={logFor === selectedRow.id ? logs : ""}
          mutate={mutate}
          busy={busy}
          onAction={runAction}
          onLogs={loadLogs}
          onTerm={openTerm}
          onClose={() => setSelected(null)}
        />
      ) : null}
    </section>
  );
}

function MachineBlock({
  machine,
  open,
  onToggle,
  openProjects,
  onToggleProject,
  query,
  healthFilter,
  selected,
  onSelect,
  mutate,
  busy,
  onAction,
  onLogs,
  onTerm,
}: {
  machine: DockerMachine;
  open: boolean;
  onToggle: () => void;
  openProjects: Record<string, boolean>;
  onToggleProject: (id: string) => void;
  query: string;
  healthFilter: string;
  selected: string | null;
  onSelect: (id: string) => void;
  mutate: boolean;
  busy: string | null;
  onAction: (c: DockerContainer, action: "start" | "stop" | "restart" | "pull" | "recreate") => void;
  onLogs: (c: DockerContainer) => void;
  onTerm: (c: DockerContainer) => void;
}) {
  const projects = (machine.projects ?? []).filter((p) =>
    (p.containers ?? []).some((c) => (healthFilter === "all" || c.health === healthFilter) && matchesQuery(c, query)),
  );
  return (
    <article className="panel docker-machine">
      <header className="docker-machine-head">
        <button className="linkish" type="button" onClick={onToggle} aria-expanded={open}>
          {open ? "▾" : "▸"} {machine.name}
        </button>
        <StatusBadge status={healthTone(machine.health)} label={machine.health_reason || honestCap(machine.health)} />
        <span className="meta">
          {machine.kind === "host" ? "Host" : "System container"}
          {machine.ipv4 ? ` · ${machine.ipv4}` : ""}
          {machine.docker_version ? ` · Docker ${machine.docker_version}` : ""}
          {` · ${machine.container_count ?? 0} containers`}
        </span>
      </header>
      {machine.daemon_error ? (
        <p className="banner banner-error" role="alert">
          {machine.daemon_error}
        </p>
      ) : null}
      {open
        ? projects.map((p) => (
            <ProjectBlock
              key={p.id}
              project={p}
              open={openProjects[p.id] !== false}
              onToggle={() => onToggleProject(p.id)}
              query={query}
              healthFilter={healthFilter}
              selected={selected}
              onSelect={onSelect}
              mutate={mutate}
              busy={busy}
              onAction={onAction}
              onLogs={onLogs}
              onTerm={onTerm}
            />
          ))
        : null}
    </article>
  );
}

function ProjectBlock({
  project,
  open,
  onToggle,
  query,
  healthFilter,
  selected,
  onSelect,
  mutate,
  busy,
  onAction,
  onLogs,
  onTerm,
}: {
  project: DockerProject;
  open: boolean;
  onToggle: () => void;
  query: string;
  healthFilter: string;
  selected: string | null;
  onSelect: (id: string) => void;
  mutate: boolean;
  busy: string | null;
  onAction: (c: DockerContainer, action: "start" | "stop" | "restart" | "pull" | "recreate") => void;
  onLogs: (c: DockerContainer) => void;
  onTerm: (c: DockerContainer) => void;
}) {
  const rows = (project.containers ?? []).filter((c) => (healthFilter === "all" || c.health === healthFilter) && matchesQuery(c, query));
  return (
    <div className="docker-project">
      <header className="docker-project-head">
        <button className="linkish" type="button" onClick={onToggle} aria-expanded={open}>
          {open ? "▾" : "▸"} {project.name}
        </button>
        <StatusBadge status={healthTone(project.health)} label={project.status_label} />
        <span className="meta">
          {project.working_dir || "No working directory"}
          {` · ${project.running ?? 0} running`}
        </span>
      </header>
      {open ? (
        <ResourceTable
          headers={["Service", "Status", "Image", "Ports", "Uptime", "CPU", ""]}
          selected={rows.findIndex((c) => c.id === selected)}
          onRowClick={(i) => onSelect(rows[i].id)}
          rows={rows.map((c) => [
            <span key="n">
              <strong>{c.service || c.name}</strong>
              <span className="meta"> {c.name}</span>
            </span>,
            <StatusBadge key="s" status={healthTone(c.health)} label={c.status_label} />,
            c.image || "Not reported",
            portText(c),
            c.uptime || "n/a",
            c.cpu_percent != null ? `${c.cpu_percent.toFixed(1)}%` : "n/a",
            mutate ? (
              <ActionMenu
                key="a"
                items={[
                  { label: "Start", onClick: () => onAction(c, "start") },
                  { label: "Stop", onClick: () => onAction(c, "stop") },
                  { label: "Restart", onClick: () => onAction(c, "restart") },
                  { label: "Logs", onClick: () => onLogs(c) },
                  { label: "Terminal", onClick: () => onTerm(c) },
                  { label: "Pull image", onClick: () => onAction(c, "pull") },
                  { label: "Recreate", onClick: () => onAction(c, "recreate") },
                ]}
              />
            ) : (
              ""
            ),
          ])}
          empty={<p>No services match.</p>}
        />
      ) : null}
      {busy ? <p className="meta">Working {busy}</p> : null}
    </div>
  );
}

function ContainerDetail({
  container,
  logs,
  mutate,
  busy,
  onAction,
  onLogs,
  onTerm,
  onClose,
}: {
  container: DockerContainer;
  logs: string;
  mutate: boolean;
  busy: string | null;
  onAction: (c: DockerContainer, action: "start" | "stop" | "restart" | "pull" | "recreate") => void;
  onLogs: (c: DockerContainer) => void;
  onTerm: (c: DockerContainer) => void;
  onClose: () => void;
}) {
  return (
    <article className="panel docker-detail" aria-labelledby="docker-detail-heading">
      <header className="page-header-row">
        <h2 id="docker-detail-heading">{container.name}</h2>
        <button className="btn btn-ghost" type="button" onClick={onClose}>
          Close
        </button>
      </header>
      <p>
        <StatusBadge status={healthTone(container.health)} label={container.status_label} />{" "}
        {container.health_reason ? <span className="meta">{container.health_reason}</span> : null}
      </p>
      <dl className="kv-grid">
        <div>
          <dt>Image</dt>
          <dd>{container.image || "Not reported"}</dd>
        </div>
        <div>
          <dt>Machine</dt>
          <dd>
            {container.machine_name}
            {container.machine_ip ? ` · ${container.machine_ip}` : ""}
          </dd>
        </div>
        <div>
          <dt>Project</dt>
          <dd>{container.project || "Standalone"}</dd>
        </div>
        <div>
          <dt>Working directory</dt>
          <dd>{container.working_dir || "Not reported"}</dd>
        </div>
        <div>
          <dt>Uptime</dt>
          <dd>{container.uptime || "Not reported"}</dd>
        </div>
        <div>
          <dt>Restarts</dt>
          <dd>{container.restart_count ?? 0}</dd>
        </div>
        <div>
          <dt>Memory</dt>
          <dd>
            {container.memory_bytes != null ? formatBytes(container.memory_bytes) : "Not reported"}
            {container.memory_limit ? ` / ${formatBytes(container.memory_limit)}` : ""}
          </dd>
        </div>
        <div>
          <dt>CPU</dt>
          <dd>{container.cpu_percent != null ? `${container.cpu_percent.toFixed(1)}%` : "Not reported"}</dd>
        </div>
        <div>
          <dt>Ports</dt>
          <dd>{portText(container)}</dd>
        </div>
        <div>
          <dt>Networks</dt>
          <dd>{(container.networks ?? []).join(", ") || "None"}</dd>
        </div>
        <div>
          <dt>Volumes</dt>
          <dd>
            {(container.mounts ?? [])
              .map((m) => `${m.destination}${m.source ? ` ← ${m.source}` : ""}`)
              .join(", ") || "None"}
          </dd>
        </div>
        <div>
          <dt>Started</dt>
          <dd>{formatWhen(container.started_at)}</dd>
        </div>
      </dl>
      {container.problems && container.problems.length > 0 ? (
        <div className="docker-errors">
          <h3>Problems</h3>
          <ul className="plain-list">
            {container.problems.map((p, i) => (
              <li key={i}>
                <StatusBadge status={p.level} /> {p.message}
              </li>
            ))}
          </ul>
        </div>
      ) : null}
      {container.recent_events && container.recent_events.length > 0 ? (
        <div>
          <h3>Recent events</h3>
          <ul className="plain-list">
            {container.recent_events
              .slice()
              .reverse()
              .slice(0, 12)
              .map((ev, i) => (
                <li key={i}>
                  <span className="meta">{formatWhen(ev.time)}</span> {ev.message}
                </li>
              ))}
          </ul>
        </div>
      ) : (
        <p className="meta">No recent Docker events for this container.</p>
      )}
      {mutate ? (
        <p className="btn-row">
          <button className="btn btn-secondary" type="button" disabled={busy !== null} onClick={() => onAction(container, "start")}>
            Start
          </button>
          <button className="btn btn-secondary" type="button" disabled={busy !== null} onClick={() => onAction(container, "stop")}>
            Stop
          </button>
          <button className="btn btn-secondary" type="button" disabled={busy !== null} onClick={() => onAction(container, "restart")}>
            Restart
          </button>
          <button className="btn btn-secondary" type="button" onClick={() => onLogs(container)}>
            Logs
          </button>
          <button className="btn btn-primary" type="button" onClick={() => onTerm(container)}>
            Terminal
          </button>
          <button className="btn btn-ghost" type="button" disabled={busy !== null} onClick={() => onAction(container, "pull")}>
            Pull
          </button>
          <button className="btn btn-ghost" type="button" disabled={busy !== null} onClick={() => onAction(container, "recreate")}>
            Recreate
          </button>
        </p>
      ) : null}
      {logs ? <pre className="code-block docker-logs">{logs}</pre> : null}
    </article>
  );
}

function honestCap(value?: string): string {
  if (!value) {
    return "Unknown";
  }
  return value.charAt(0).toUpperCase() + value.slice(1);
}
