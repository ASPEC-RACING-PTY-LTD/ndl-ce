import { confirmAction } from "../confirm";
import { useEffect, useMemo, useState, type MouseEvent as ReactMouseEvent } from "react";
import {
  ApiError,
  checkUptimeMonitor,
  dockerBulk,
  dockerContainerAction,
  dockerContainerLogs,
  getDocker,
  listDockerPrefs,
  listNodes,
  listUptimeMonitors,
  putDockerPref,
  runDockerScript,
  type DockerPref,
  type OpsJob,
  type UptimeMonitor,
} from "../api/client";
import { ActionMenu, type ActionItem } from "../components/ActionMenu";
import { ContextMenu, useContextMenu, type ContextItem } from "../components/ContextMenu";
import { EmptyState, ErrorState, LoadingState } from "../components/EmptyState";
import { Link } from "../components/Link";
import { PageHeader } from "../components/PageHeader";
import { StatusBadge } from "../components/StatusBadge";
import { Dialog } from "../ui/Dialog";
import { Segmented } from "../ui/Segmented";
import { ColumnEditor, SortHeader, useTablePrefs, type TableColumn } from "../ui/tablePrefs";
import { JobLog } from "../components/JobLog";
import { JsonScriptModal } from "../components/JsonScriptModal";
import type { DockerContainer, DockerInventory, DockerMachine } from "../generated/openapi";
import { filesWorkspaceHref } from "../files/workspace";
import { formatBytes, formatWhen } from "../format";
import { navigate } from "../router";
import { canMutate, isAdmin } from "../rbac";
import { DockerPrunePanel } from "../components/DockerPrune";
import { useSession } from "../session";
import { usePoll } from "../query";
import { useTerminalWorkspace } from "../terminal/workspace";
import type { TermTarget } from "../terminal/types";
import { menuItems, monitorDown, SiteBadge, UptimeSites } from "./UptimePage";
import "../docker.css";
import { nodeLabel } from "../nodeNames";

type Act = "start" | "stop" | "restart" | "pull" | "recreate" | "remove";
type Bulk = "start-all" | "stop-all" | "restart-all" | "fix";

/** ContainerFilter picks which containers the list shows. Unhealthy is the default. */
export type ContainerFilter = "unhealthy" | "healthy" | "ignored" | "all";

const SCRIPT_TEMPLATE = `{
  "name": "Restart shop in order and check it",
  "continue_on_error": false,
  "steps": [
    { "action": "restart", "machine": "apps", "project": "shop" },
    { "action": "wait-healthy", "machine": "apps", "container": "shop-web-1", "timeout": 120 },
    { "action": "uptime-check" }
  ]
}`;

function ScriptHelp() {
  return (
    <>
      <summary>Script reference</summary>
      <p className="field-hint">
        A script runs once, top to bottom, and stops at the first failure unless <code>continue_on_error</code> is true. Use
        Automations to repeat it. <code>machine</code> is a machine name (or <code>host</code>); <code>container</code> is a
        container name.
      </p>
      <ul className="plain-list json-ref">
        <li>
          <code>start</code>, <code>stop</code>, <code>restart</code>, <code>pull</code>, <code>recreate</code>: a <code>container</code> or every
          container in a <code>project</code>, in dependency order.
        </li>
        <li>
          <code>start-all</code>, <code>stop-all</code>, <code>restart-all</code>: everything, or one <code>machine</code> or <code>project</code>.
        </li>
        <li>
          <code>fix</code>: restart unhealthy containers, their dependencies first. Ignored containers are skipped unless{" "}
          <code>include_ignored</code> is true.
        </li>
        <li>
          <code>exec</code>: run <code>command</code> (a list, for example <code>["sh","-c","php artisan migrate"]</code>) in a container. Admin only.
        </li>
        <li>
          <code>wait</code> (<code>seconds</code>), <code>wait-healthy</code> (<code>container</code>, <code>timeout</code>).
        </li>
        <li>
          <code>uptime-check</code>: check site monitors (all, or <code>monitor</code>) and recover failing ones.
        </li>
      </ul>
    </>
  );
}

function healthTone(health?: string): string {
  if (health === "critical" || health === "degraded" || health === "healthy") {
    return health;
  }
  return health || "unknown";
}

function badHealth(health?: string): boolean {
  return health === "degraded" || health === "critical" || health === "unhealthy";
}

/** containerProblem says why a container needs attention, or "" when it is healthy. Exported for tests. */
export function containerProblem(c: DockerContainer, sites: UptimeMonitor[] = []): string {
  if (c.state !== "running") {
    return c.health_reason || c.error || c.status_label || "Not running";
  }
  if (c.restarting || c.restart_loop) {
    return c.health_reason || "Restarting";
  }
  if (badHealth(c.health) || c.healthcheck === "unhealthy") {
    return c.health_reason || c.status_label || "Unhealthy";
  }
  const down = sites.filter(monitorDown);
  if (down.length) {
    return down.length === 1 ? `Site ${down[0].name} is down` : `${down.length} sites are down`;
  }
  return "";
}

function matchesQuery(c: DockerContainer, q: string): boolean {
  if (!q) {
    return true;
  }
  const hay = [c.name, c.image, c.project, c.service, nodeLabel(c.machine_id, c.machine_name), c.machine_ip, c.status_label, c.working_dir, ...(c.networks ?? [])]
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

function prefKey(machineId: string, scope: "container" | "project", name: string): string {
  return `${machineId}|${scope}|${name}`;
}

function copy(text: string) {
  void navigator.clipboard?.writeText(text);
}

type Row = {
  c: DockerContainer;
  ignored: boolean;
  problem: string;
  sites: UptimeMonitor[];
  rank: number;
};

function rowKind(r: Row): Exclude<ContainerFilter, "all"> {
  return r.ignored ? "ignored" : r.problem ? "unhealthy" : "healthy";
}

const COLUMNS: TableColumn<Row>[] = [
  { id: "name", label: "Container", required: true, sortValue: (r) => (r.c.name || "").toLowerCase() },
  { id: "status", label: "Status", sortValue: (r) => r.rank },
  { id: "problem", label: "Detail", sortValue: (r) => r.problem || r.c.uptime || "" },
  { id: "project", label: "Project", sortValue: (r) => r.c.project || "" },
  { id: "machine", label: "Machine", sortValue: (r) => nodeLabel(r.c.machine_id, r.c.machine_name) || r.c.machine_id },
  { id: "sites", label: "Sites", sortValue: (r) => (r.sites.some(monitorDown) ? 0 : r.sites.length ? 1 : 2) },
  { id: "restarts", label: "Restarts", sortValue: (r) => r.c.restart_count ?? 0 },
  { id: "image", label: "Image", defaultHidden: true, sortValue: (r) => r.c.image || "" },
  { id: "ports", label: "Ports", defaultHidden: true, sortValue: (r) => portText(r.c) },
];

type Props = {
  /** focus scrolls to the Sites section (the old Uptime address). */
  focus?: "sites";
};

/** DockerPage is containers and the sites they serve, on every Docker engine. */
export function DockerPage({ focus }: Props = {}) {
  const session = useSession();
  const roles = session.status === "ready" ? session.user?.roles : undefined;
  const mutate = canMutate(roles);
  const admin = isAdmin(roles);
  const { openOrFocus } = useTerminalWorkspace();
  const [inv, setInv] = useState<DockerInventory | null>(null);
  const [prefs, setPrefs] = useState<Map<string, DockerPref>>(new Map());
  const [monitors, setMonitors] = useState<UptimeMonitor[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [disabled, setDisabled] = useState(false);
  const [busy, setBusy] = useState<string | null>(null);
  const [query, setQuery] = useState("");
  const [machineFilter, setMachineFilter] = useState("all");
  // Not saved on purpose: the page always opens on what needs attention.
  const [filter, setFilter] = useState<ContainerFilter>("unhealthy");
  const [selected, setSelected] = useState<string | null>(null);
  const [logs, setLogs] = useState<string>("");
  const [logFor, setLogFor] = useState<string | null>(null);
  const [hostNodeId, setHostNodeId] = useState<string | null>(null);
  const [job, setJob] = useState<OpsJob | null>(null);
  const [jsonOpen, setJsonOpen] = useState(false);
  const [enginesOpen, setEnginesOpen] = useState(false);
  const [cleanupOpen, setCleanupOpen] = useState(false);
  const table = useTablePrefs<Row>("docker.containers", COLUMNS, null);
  const ctx = useContextMenu();

  async function reload(refresh = false) {
    try {
      const [next, saved] = await Promise.all([getDocker(refresh), listDockerPrefs().catch(() => null)]);
      setInv(next);
      if (saved) {
        setPrefs(new Map((saved.items ?? []).map((p) => [prefKey(p.machine_id, p.scope, p.name), p])));
      }
      setDisabled(false);
      setError(null);
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

  async function reloadSites() {
    try {
      setMonitors((await listUptimeMonitors()).items ?? []);
    } catch {
      // Sites are optional; the containers still show.
      setMonitors((cur) => cur ?? []);
    }
  }

  useEffect(() => {
    void reload();
    void reloadSites();
  }, []);
  usePoll(() => reload(), 5000);
  usePoll(() => reloadSites(), 15000);

  const q = query.trim().toLowerCase();
  const machines = useMemo(() => inv?.machines ?? [], [inv]);
  // The flat list is preferred; older servers only fill it per project.
  const containers = useMemo(() => {
    const flat = inv?.containers ?? [];
    if (flat.length) {
      return flat;
    }
    return (inv?.machines ?? []).flatMap((m) => (m.projects ?? []).flatMap((p) => p.containers ?? []));
  }, [inv]);

  function ignoredSelf(c: DockerContainer): boolean {
    return Boolean(prefs.get(prefKey(c.machine_id, "container", c.name))?.ignored);
  }
  function ignoredProject(machineId: string, project?: string): boolean {
    return Boolean(project && prefs.get(prefKey(machineId, "project", project))?.ignored);
  }

  const rows = useMemo<Row[]>(() => {
    return containers.map((c) => {
      const sites = (monitors ?? []).filter((m) => m.containers.some((x) => x.machine_id === c.machine_id && x.name === c.name));
      const ignored = ignoredSelf(c) || ignoredProject(c.machine_id, c.project);
      const problem = containerProblem(c, sites);
      const rank = ignored ? 3 : !problem ? 2 : c.state !== "running" || c.health === "critical" ? 0 : 1;
      return { c, ignored, problem, sites, rank };
    });
    // ignoredSelf and ignoredProject read prefs, which is listed.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [containers, monitors, prefs]);

  const scoped = rows.filter((r) => (machineFilter === "all" || r.c.machine_id === machineFilter) && matchesQuery(r.c, q));
  const counts: Record<ContainerFilter, number> = { unhealthy: 0, healthy: 0, ignored: 0, all: scoped.length };
  for (const r of scoped) {
    counts[rowKind(r)] += 1;
  }
  const filtered = scoped.filter((r) => filter === "all" || rowKind(r) === filter);
  const shown = table.sort
    ? table.sortRows(filtered)
    : [...filtered].sort(
        (a, b) =>
          a.rank - b.rank ||
          (nodeLabel(a.c.machine_id, a.c.machine_name) || "").localeCompare(nodeLabel(b.c.machine_id, b.c.machine_name) || "") ||
          (a.c.project || "~").localeCompare(b.c.project || "~") ||
          a.c.name.localeCompare(b.c.name),
      );
  const downEngines = machines.filter((m) => !m.daemon_ok);
  const selectedRow = rows.find((r) => r.c.id === selected) ?? null;

  async function runAction(c: DockerContainer, action: Act) {
    if (action === "remove" && !confirmAction(`Remove container ${c.name || c.container_id}? It is stopped and deleted. Named volumes are kept.`)) {
      return;
    }
    setBusy(`${c.name}: ${action}`);
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

  async function bulk(action: Bulk, scope: { machine_id?: string; project?: string } = {}) {
    const where = scope.project
      ? `every container in ${scope.project}`
      : scope.machine_id
        ? `every container on ${machines.find((m) => m.id === scope.machine_id)?.name ?? scope.machine_id}`
        : "every container on every machine";
    if (action === "stop-all" && !confirmAction(`Stop ${where}? Sites they serve go offline until started again.`)) {
      return;
    }
    if (action === "restart-all" && !confirmAction(`Restart ${where}, dependencies first?`)) {
      return;
    }
    setError(null);
    try {
      setJob(await dockerBulk({ action, ...scope }));
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not start");
    }
  }

  function scopeNow(): { machine_id?: string } {
    return machineFilter !== "all" ? { machine_id: machineFilter } : {};
  }

  async function setIgnored(machineId: string, scope: "container" | "project", name: string, ignored: boolean) {
    setError(null);
    let note = "";
    if (ignored) {
      const typed = window.prompt(`Why is ${name} ignored? (optional) It stays out of Unhealthy until someone stops ignoring it.`, "");
      if (typed === null) {
        return;
      }
      note = typed.trim();
    }
    try {
      await putDockerPref({ machine_id: machineId, scope, name, ignored, note });
      setNotice(ignored ? `${name} is ignored. This is saved for everyone until it is changed.` : `${name} is no longer ignored.`);
      await reload();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not save");
    }
  }

  async function loadLogs(c: DockerContainer) {
    setSelected(c.id);
    setLogFor(c.id);
    setLogs("Loading logs");
    try {
      const res = await dockerContainerLogs(c.machine_id, c.container_id, 200);
      setLogs(res.logs || "(no log output)");
    } catch (err) {
      setLogs(err instanceof Error ? err.message : "Logs unavailable");
    }
  }

  async function checkSites(sites: UptimeMonitor[]) {
    setBusy("Checking sites");
    setError(null);
    try {
      for (const m of sites) {
        await checkUptimeMonitor(m.id);
      }
      await reloadSites();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Check failed");
    } finally {
      setBusy(null);
    }
  }

  function openShell(c: DockerContainer) {
    openOrFocus(dockerTarget(c), { forceNew: true });
    navigate("/terminal");
  }

  async function machineTermTarget(m: DockerMachine): Promise<TermTarget | null> {
    if (m.kind === "host") {
      let id = hostNodeId;
      if (!id) {
        const nodes = await listNodes().catch(() => []);
        id = (nodes.find((n) => n.local) ?? nodes[0])?.id ?? null;
        setHostNodeId(id);
      }
      if (!id) {
        setError("Could not find this host's node.");
        return null;
      }
      return { kind: "node", id, name: m.name || "Host", group: "host", typeLabel: "Host", status: "online", terminalReady: true };
    }
    return {
      kind: "workload",
      id: m.id,
      name: m.name,
      group: "system-container",
      typeLabel: "System container",
      status: "running",
      terminalReady: true,
    };
  }

  async function openTerminalAt(m: DockerMachine, dir?: string) {
    const target = await machineTermTarget(m);
    if (!target) {
      return;
    }
    openOrFocus(target, { cwd: dir || "/", forceNew: true, title: dir ? `${m.name}: ${dir.split("/").filter(Boolean).pop() ?? dir}` : undefined });
    navigate("/terminal");
  }

  async function openFilesAt(m: DockerMachine, dir?: string) {
    if (m.kind === "host") {
      const target = await machineTermTarget(m);
      if (target) {
        navigate(filesWorkspaceHref({ kind: "node", id: target.id, name: m.name }, dir || "/"));
      }
      return;
    }
    navigate(filesWorkspaceHref({ kind: "workload", id: m.id, name: m.name }, dir || "/"));
  }

  function rowItems(r: Row): ContextItem[] {
    const c = r.c;
    const m = machines.find((x) => x.id === c.machine_id);
    const dir = c.working_dir;
    const termOK = mutate && (m?.kind !== "host" || admin);
    const items: ContextItem[] = [
      { label: "Details and logs", onClick: () => void loadLogs(c) },
      { label: "Open shell in container", onClick: () => openShell(c), disabled: !mutate || c.state !== "running" },
    ];
    if (m) {
      items.push(
        { label: dir ? "Open terminal in compose folder" : "Open terminal on machine", onClick: () => void openTerminalAt(m, dir), disabled: !termOK },
        { label: dir ? "Open compose folder in Files" : "Open machine files", onClick: () => void openFilesAt(m, dir), disabled: m.kind === "host" && !admin },
      );
    }
    if (mutate) {
      items.push(
        "sep",
        { label: "Start", onClick: () => void runAction(c, "start"), disabled: c.state === "running" },
        { label: "Stop", onClick: () => void runAction(c, "stop"), disabled: c.state !== "running" },
        { label: "Restart", onClick: () => void runAction(c, "restart") },
        { label: "Pull image", onClick: () => void runAction(c, "pull") },
        { label: "Recreate", onClick: () => void runAction(c, "recreate") },
      );
      if (c.project) {
        const scope = { machine_id: c.machine_id, project: c.project };
        items.push(
          "sep",
          { label: `Fix project ${c.project}`, onClick: () => void bulk("fix", scope) },
          { label: `Restart project ${c.project}`, onClick: () => void bulk("restart-all", scope) },
          { label: `Start project ${c.project}`, onClick: () => void bulk("start-all", scope) },
          { label: `Stop project ${c.project}`, onClick: () => void bulk("stop-all", scope) },
        );
      }
    }
    if (r.sites.length && mutate) {
      items.push("sep", { label: r.sites.length === 1 ? "Check its site now" : "Check its sites now", onClick: () => void checkSites(r.sites) });
    }
    if (mutate) {
      items.push(
        "sep",
        ignoredSelf(c)
          ? { label: "Stop ignoring container", onClick: () => void setIgnored(c.machine_id, "container", c.name, false) }
          : { label: "Ignore container", onClick: () => void setIgnored(c.machine_id, "container", c.name, true) },
      );
      if (c.project) {
        items.push(
          ignoredProject(c.machine_id, c.project)
            ? { label: "Stop ignoring project", onClick: () => void setIgnored(c.machine_id, "project", c.project!, false) }
            : { label: "Ignore whole project", onClick: () => void setIgnored(c.machine_id, "project", c.project!, true) },
        );
      }
    }
    items.push("sep", { label: "Copy name", onClick: () => copy(c.name) }, { label: "Copy container ID", onClick: () => copy(c.container_id) });
    if (c.image) {
      items.push({ label: "Copy image", onClick: () => copy(c.image!) });
    }
    if (dir) {
      items.push({ label: "Copy compose folder path", onClick: () => copy(dir) });
    }
    if (m && m.kind !== "host") {
      items.push({ label: "Go to machine", onClick: () => navigate(`/workloads/${encodeURIComponent(m.id)}`) });
    }
    if (mutate) {
      items.push("sep", { label: "Remove container", onClick: () => void runAction(c, "remove"), danger: true });
    }
    return items;
  }

  function engineItems(m: DockerMachine): ContextItem[] {
    const termOK = mutate && (m.kind !== "host" || admin);
    const items: ContextItem[] = [
      {
        label: "Show its containers",
        onClick: () => {
          setMachineFilter(m.id);
          setFilter("all");
          setEnginesOpen(false);
        },
      },
      { label: "Open terminal on machine", onClick: () => void openTerminalAt(m), disabled: !termOK },
      { label: "Open machine files", onClick: () => void openFilesAt(m), disabled: m.kind === "host" && !admin },
    ];
    if (mutate && m.daemon_ok) {
      items.push(
        "sep",
        { label: "Fix unhealthy here", onClick: () => void bulk("fix", { machine_id: m.id }) },
        { label: "Restart all here", onClick: () => void bulk("restart-all", { machine_id: m.id }) },
        { label: "Start all here", onClick: () => void bulk("start-all", { machine_id: m.id }) },
        { label: "Stop all here", onClick: () => void bulk("stop-all", { machine_id: m.id }) },
      );
    }
    if (m.kind !== "host") {
      items.push("sep", { label: "Go to machine", onClick: () => navigate(`/workloads/${encodeURIComponent(m.id)}`) });
    }
    return items;
  }

  function cell(id: string, r: Row) {
    const c = r.c;
    switch (id) {
      case "name":
        return (
          <td key={id}>
            <span className="dk-name">
              <strong className="dk-clip" title={c.name}>
                {c.name}
              </strong>
              {c.service && c.service !== c.name ? <span className="meta dk-clip">{c.service}</span> : null}
            </span>
          </td>
        );
      case "status":
        return (
          <td key={id}>
            <StatusBadge
              status={r.ignored ? "unknown" : c.state !== "running" ? "critical" : r.problem ? (c.health === "critical" ? "critical" : "degraded") : healthTone(c.health)}
              label={r.ignored ? `Ignored · ${c.status_label}` : c.status_label}
            />
          </td>
        );
      case "problem":
        return (
          <td key={id}>
            <span className={"dk-clip" + (r.problem && !r.ignored ? " dk-reason" : " meta")} title={r.problem || c.uptime || ""}>
              {r.problem || (c.uptime ? `Up ${c.uptime}` : "")}
            </span>
          </td>
        );
      case "project":
        return (
          <td key={id}>
            <span className="dk-clip" title={c.working_dir || c.project || ""}>
              {c.project || <span className="meta">Standalone</span>}
            </span>
          </td>
        );
      case "machine":
        return (
          <td key={id}>
            <span className="dk-clip" title={c.machine_ip || ""}>
              {nodeLabel(c.machine_id, c.machine_name) || c.machine_id}
            </span>
          </td>
        );
      case "sites":
        return (
          <td key={id}>
            {r.sites.length ? (
              <span className="dk-sites">
                {r.sites.slice(0, 2).map((m) => (
                  <SiteBadge key={m.id} m={m} withName />
                ))}
                {r.sites.length > 2 ? <span className="meta">+{r.sites.length - 2}</span> : null}
              </span>
            ) : (
              <span className="meta">-</span>
            )}
          </td>
        );
      case "restarts":
        return (
          <td key={id} className="num">
            {c.restart_count ?? 0}
          </td>
        );
      case "image":
        return (
          <td key={id}>
            <span className="dk-clip" title={c.image || ""}>
              {c.image || "-"}
            </span>
          </td>
        );
      case "ports":
        return <td key={id}>{portText(c)}</td>;
      default:
        return <td key={id} />;
    }
  }

  const sites = (
    <UptimeSites monitors={monitors} machines={machines} containers={containers} mutate={mutate} reload={reloadSites} focus={focus === "sites"} />
  );

  if (disabled) {
    return (
      <section className="page page-wide" aria-labelledby="docker-heading">
        <PageHeader id="docker-heading" title="Docker" kicker="Optional Docker Management across No-DAL machines. Discovery stays off until you enable the feature." />
        <EmptyState title="Docker Management is not enabled">
          Enable it from <Link href="/settings/features">Add Features</Link>. No Docker sockets are probed while the module is off.
        </EmptyState>
        {sites}
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

  const more: ActionItem[] = [
    ...(mutate
      ? [
          { label: "Restart all", onClick: () => void bulk("restart-all", scopeNow()) },
          { label: "Start all", onClick: () => void bulk("start-all", scopeNow()) },
          { label: "Stop all", onClick: () => void bulk("stop-all", scopeNow()), danger: true },
          { label: "Run JSON script", onClick: () => setJsonOpen(true) },
        ]
      : []),
    { label: "Engines", onClick: () => setEnginesOpen(true) },
    { label: "Image cleanup", onClick: () => setCleanupOpen(true) },
  ];

  return (
    <section className="page page-wide dk-page" aria-labelledby="docker-heading">
      <PageHeader
        id="docker-heading"
        title="Docker"
        kicker="Containers on the host and system containers, and the sites they serve. Right-click a row for its tools."
        actions={
          <div className="btn-row is-flush">
            {mutate ? (
              <button className="btn btn-primary" type="button" disabled={counts.unhealthy === 0} onClick={() => void bulk("fix", scopeNow())}>
                Fix unhealthy
              </button>
            ) : null}
            <button className="btn btn-secondary" type="button" onClick={() => void Promise.all([reload(true), reloadSites()])}>
              Refresh
            </button>
            <ActionMenu label="More Docker actions" items={more} />
          </div>
        }
      />
      {error ? <ErrorState>{error}</ErrorState> : null}
      {notice ? (
        <p className="banner banner-ok" role="status">
          {notice}{" "}
          <button className="btn btn-ghost btn-sm" type="button" onClick={() => setNotice(null)}>
            Dismiss
          </button>
        </p>
      ) : null}
      {busy ? (
        <p className="banner" role="status">
          Working: {busy}
        </p>
      ) : null}
      {downEngines.length ? (
        <p className="banner banner-warn dk-engines-down" role="status">
          {downEngines.length === 1 ? "1 Docker engine is unreachable: " : `${downEngines.length} Docker engines are unreachable: `}
          {downEngines.map((m) => m.name).join(", ")}.{" "}
          <button className="btn btn-ghost btn-sm" type="button" onClick={() => setEnginesOpen(true)}>
            Engines
          </button>
        </p>
      ) : null}
      {job ? <JobLog job={job} onDone={() => void reload(true)} onClose={() => setJob(null)} /> : null}

      {machines.length === 0 ? (
        <EmptyState title="No Docker engines discovered">
          Install Docker in a system container or on the host. No-DAL does not install Docker Engine when this feature is enabled.
        </EmptyState>
      ) : (
        <section className="dk-section" aria-labelledby="dk-containers-heading">
          <div className="dk-section-head">
            <h2 id="dk-containers-heading">Containers</h2>
            <Segmented<ContainerFilter>
              ariaLabel="Show containers"
              value={filter}
              onChange={setFilter}
              options={[
                { id: "unhealthy", label: `Unhealthy (${counts.unhealthy})` },
                { id: "healthy", label: `Healthy (${counts.healthy})` },
                ...(counts.ignored || filter === "ignored" ? [{ id: "ignored" as const, label: `Ignored (${counts.ignored})` }] : []),
                { id: "all", label: `All (${counts.all})` },
              ]}
            />
            <input
              className="field-input dk-search"
              type="search"
              aria-label="Search containers"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder="Search name, image, project, IP"
            />
            {machines.length > 1 ? (
              <select className="field-input dk-select" aria-label="Machine" value={machineFilter} onChange={(e) => setMachineFilter(e.target.value)}>
                <option value="all">All machines</option>
                {machines.map((m) => (
                  <option key={m.id} value={m.id}>
                    {m.name}
                  </option>
                ))}
              </select>
            ) : null}
          </div>
          {shown.length === 0 ? (
            <p className="dk-empty muted">
              {filter === "unhealthy" && !q ? "Every container is healthy." : "No containers match."}{" "}
              {filter !== "all" && counts.all > 0 ? (
                <button className="btn btn-ghost btn-sm" type="button" onClick={() => setFilter(filter === "unhealthy" ? "healthy" : "all")}>
                  {filter === "unhealthy" ? `Show healthy (${counts.healthy})` : "Show all"}
                </button>
              ) : null}
            </p>
          ) : (
            <div className="table-wrap dk-table-wrap">
              <table className="dk-table" aria-label="Containers">
                <thead>
                  <tr>
                    {table.visible.map((c) => (
                      <SortHeader key={c.id} column={c} prefs={table} className={c.id === "restarts" ? "num" : undefined} />
                    ))}
                    <th className="cell-actions">
                      <ColumnEditor prefs={table} label="Edit container columns" />
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {shown.map((r) => (
                    <tr
                      key={r.c.id}
                      aria-label={`Container ${r.c.name}`}
                      className={"dk-row is-clickable" + (r.ignored ? " is-muted" : r.problem ? " is-bad" : "")}
                      onClick={() => setSelected(r.c.id)}
                      onContextMenu={(e: ReactMouseEvent) => ctx.openAt(e, r.c.name, rowItems(r))}
                    >
                      {table.visible.map((col) => cell(col.id, r))}
                      <td className="cell-actions" onClick={(e) => e.stopPropagation()}>
                        <ActionMenu label={`${r.c.name} actions`} items={menuItems(rowItems(r))} />
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </section>
      )}

      {sites}

      <ContextMenu menu={ctx.menu} onClose={ctx.close} />

      <Dialog open={enginesOpen} title="Docker engines" wide onClose={() => setEnginesOpen(false)}>
        <EnginesTable machines={machines} itemsFor={engineItems} onContext={(e, m) => ctx.openAt(e, m.name, engineItems(m))} />
      </Dialog>

      <Dialog open={cleanupOpen} title="Image cleanup" wide onClose={() => setCleanupOpen(false)}>
        <DockerPrunePanel mutate={mutate} />
      </Dialog>

      <Dialog open={selectedRow != null} title={selectedRow?.c.name ?? "Container"} wide onClose={() => setSelected(null)}>
        {selectedRow ? (
          <ContainerDetail
            container={selectedRow.c}
            ignored={selectedRow.ignored}
            problem={selectedRow.problem}
            sites={selectedRow.sites}
            logs={logFor === selectedRow.c.id ? logs : ""}
            mutate={mutate}
            busy={busy}
            onAction={runAction}
            onLogs={loadLogs}
            onShell={openShell}
          />
        ) : null}
      </Dialog>
      <JsonScriptModal
        open={jsonOpen}
        onClose={() => setJsonOpen(false)}
        title="Run a Docker script"
        kind="docker"
        template={SCRIPT_TEMPLATE}
        help={<ScriptHelp />}
        runLabel="Run once"
        onTest={async (value) => {
          const res = await runDockerScript(value, true);
          if (res.valid === false && res.error) {
            throw new Error(res.error);
          }
          return <PlanView job={res} />;
        }}
        onRun={async (value) => {
          const res = await runDockerScript(value, false);
          if (res.valid === false && res.error) {
            throw new Error(res.error);
          }
          setJob(res);
          setJsonOpen(false);
        }}
      />
    </section>
  );
}

function EnginesTable({
  machines,
  itemsFor,
  onContext,
}: {
  machines: DockerMachine[];
  itemsFor: (m: DockerMachine) => ContextItem[];
  onContext: (e: ReactMouseEvent, m: DockerMachine) => void;
}) {
  const sorted = [...machines].sort((a, b) => Number(a.daemon_ok) - Number(b.daemon_ok) || a.name.localeCompare(b.name));
  return (
    <div className="table-wrap dk-table-wrap">
      <table className="dk-table" aria-label="Docker engines">
        <thead>
          <tr>
            <th>Machine</th>
            <th>Status</th>
            <th>Kind</th>
            <th>Address</th>
            <th>Docker</th>
            <th className="num">Containers</th>
            <th className="cell-actions" />
          </tr>
        </thead>
        <tbody>
          {sorted.map((m) => (
            <tr key={m.id} className={"dk-row" + (m.daemon_ok ? "" : " is-bad")} onContextMenu={(e: ReactMouseEvent) => onContext(e, m)}>
              <td>
                <strong>{m.name}</strong>
              </td>
              <td>
                {m.daemon_ok ? (
                  <StatusBadge status={healthTone(m.health)} label={m.health_reason || "Reachable"} />
                ) : (
                  <span title={m.daemon_error || ""}>
                    <StatusBadge status="critical" label="Unreachable" /> <span className="meta dk-clip">{m.daemon_error || "The Docker daemon did not answer."}</span>
                  </span>
                )}
              </td>
              <td>{m.kind === "host" ? "Host" : "System container"}</td>
              <td>{m.ipv4 || "-"}</td>
              <td>{m.docker_version || "-"}</td>
              <td className="num">{m.container_count ?? 0}</td>
              <td className="cell-actions">
                <ActionMenu label={`${m.name} engine actions`} items={menuItems(itemsFor(m))} />
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function PlanView({ job }: { job: OpsJob }) {
  const lines = job.lines ?? [];
  return (
    <div className={"banner " + (job.failed ? "banner-error" : "banner-ok")} role="status">
      <strong>{job.failed ? `The script has ${job.failed} problem(s)` : "The script is valid. Nothing was changed."}</strong>
      <ol className="job-lines">
        {lines.map((l, i) => (
          <li key={i} className={`job-line is-${l.level}`}>
            <span className="job-level">{l.level}</span>
            <span className="job-msg">{l.message}</span>
          </li>
        ))}
      </ol>
    </div>
  );
}

function ContainerDetail({
  container,
  ignored,
  problem,
  sites,
  logs,
  mutate,
  busy,
  onAction,
  onLogs,
  onShell,
}: {
  container: DockerContainer;
  ignored: boolean;
  problem: string;
  sites: UptimeMonitor[];
  logs: string;
  mutate: boolean;
  busy: string | null;
  onAction: (c: DockerContainer, action: Act) => void;
  onLogs: (c: DockerContainer) => void;
  onShell: (c: DockerContainer) => void;
}) {
  return (
    <div className="docker-detail">
      <p>
        <StatusBadge status={healthTone(container.health)} label={container.status_label} />{" "}
        {ignored ? <StatusBadge status="unknown" label="Ignored" /> : null}{" "}
        {problem || container.health_reason ? <span className="meta">{problem || container.health_reason}</span> : null}
      </p>
      <dl className="kv-grid">
        <div>
          <dt>Image</dt>
          <dd>{container.image || "Not reported"}</dd>
        </div>
        <div>
          <dt>Machine</dt>
          <dd>
            {nodeLabel(container.machine_id, container.machine_name)}
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
          <dd>{(container.mounts ?? []).map((m) => `${m.destination}${m.source ? ` ← ${m.source}` : ""}`).join(", ") || "None"}</dd>
        </div>
        <div>
          <dt>Started</dt>
          <dd>{formatWhen(container.started_at)}</dd>
        </div>
      </dl>
      {sites.length ? (
        <div>
          <h3>Sites</h3>
          <ul className="plain-list">
            {sites.map((m) => (
              <li key={m.id}>
                <SiteBadge m={m} withName /> <span className="meta">{m.target}</span>
                {monitorDown(m) && m.last_error ? <span className="meta"> · {m.last_error}</span> : null}
              </li>
            ))}
          </ul>
        </div>
      ) : null}
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
      <p className="btn-row">
        {mutate ? (
          <>
            <button className="btn btn-secondary" type="button" disabled={busy !== null} onClick={() => onAction(container, "start")}>
              Start
            </button>
            <button className="btn btn-secondary" type="button" disabled={busy !== null} onClick={() => onAction(container, "stop")}>
              Stop
            </button>
            <button className="btn btn-secondary" type="button" disabled={busy !== null} onClick={() => onAction(container, "restart")}>
              Restart
            </button>
            <button className="btn btn-primary" type="button" disabled={container.state !== "running"} onClick={() => onShell(container)}>
              Shell
            </button>
          </>
        ) : null}
        <button className="btn btn-ghost" type="button" onClick={() => onLogs(container)}>
          {logs ? "Reload logs" : "Logs"}
        </button>
      </p>
      {logs ? <pre className="code-block docker-logs">{logs}</pre> : null}
    </div>
  );
}
