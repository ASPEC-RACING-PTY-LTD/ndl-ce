import { useEffect, useMemo, useState } from "react";
import type { MouseEvent as ReactMouseEvent } from "react";
import {
  ApiError,
  dockerContainerAction,
  dockerContainerLogs,
  getDocker,
  listDockerPrefs,
  listNodes,
  putDockerPref,
  type DockerPref,
} from "../api/client";
import { ActionMenu } from "../components/ActionMenu";
import { ContextMenu, useContextMenu, type ContextItem } from "../components/ContextMenu";
import { EmptyState, ErrorState, LoadingState } from "../components/EmptyState";
import { Link } from "../components/Link";
import { PageHeader } from "../components/PageHeader";
import { ResourceTable } from "../components/ResourceTable";
import { StatusBadge } from "../components/StatusBadge";
import { ErrorNotice } from "../components/ErrorNotice";
import { Dialog } from "../ui/Dialog";
import type { DockerContainer, DockerInventory, DockerMachine, DockerProject } from "../generated/openapi";
import { filesWorkspaceHref } from "../files/workspace";
import { formatBytes, formatWhen } from "../format";
import { navigate } from "../router";
import { canMutate, isAdmin } from "../rbac";
import { useSession } from "../session";
import { storageGet, storageSet } from "../storage";
import { usePoll } from "../query";
import { useTerminalWorkspace } from "../terminal/workspace";
import type { TermTarget } from "../terminal/types";

type ViewMode = "groups" | "table";
type Act = "start" | "stop" | "restart" | "pull" | "recreate" | "remove";

const COLLAPSE_KEY = "ndl-docker-collapsed";
const ROWS_SHOWN = 6;

function healthTone(health?: string): string {
  if (health === "critical" || health === "degraded" || health === "healthy") {
    return health;
  }
  return health || "unknown";
}

function isIssue(health?: string): boolean {
  return health === "degraded" || health === "critical";
}

function matchesQuery(c: DockerContainer, q: string): boolean {
  if (!q) {
    return true;
  }
  const hay = [c.name, c.image, c.project, c.service, c.machine_name, c.machine_ip, c.status_label, c.working_dir, ...(c.networks ?? [])]
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

function honestCap(value?: string): string {
  if (!value) {
    return "Unknown";
  }
  return value.charAt(0).toUpperCase() + value.slice(1);
}

function copy(text: string) {
  void navigator.clipboard?.writeText(text);
}

type Group = {
  key: string;
  machine: DockerMachine;
  project: DockerProject;
  containers: DockerContainer[];
  ignored: boolean;
  issues: DockerContainer[];
};

export function DockerPage() {
  const session = useSession();
  const roles = session.status === "ready" ? session.user?.roles : undefined;
  const mutate = canMutate(roles);
  const admin = isAdmin(roles);
  const { openOrFocus } = useTerminalWorkspace();
  const ctx = useContextMenu();
  const [inv, setInv] = useState<DockerInventory | null>(null);
  const [prefs, setPrefs] = useState<Map<string, DockerPref>>(new Map());
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [disabled, setDisabled] = useState(false);
  const [busy, setBusy] = useState<string | null>(null);
  const [query, setQuery] = useState("");
  const [machineFilter, setMachineFilter] = useState("all");
  const [view, setView] = useState<ViewMode>("groups");
  const [collapsed, setCollapsed] = useState<Record<string, boolean>>(() => {
    try {
      return JSON.parse(storageGet(COLLAPSE_KEY) || "{}") as Record<string, boolean>;
    } catch {
      return {};
    }
  });
  const [showAll, setShowAll] = useState<Record<string, boolean>>({});
  const [ignoredOpen, setIgnoredOpen] = useState(false);
  const [idleOpen, setIdleOpen] = useState(false);
  const [selected, setSelected] = useState<string | null>(null);
  const [logs, setLogs] = useState<string>("");
  const [logFor, setLogFor] = useState<string | null>(null);
  const [hostNodeId, setHostNodeId] = useState<string | null>(null);

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

  useEffect(() => {
    void reload();
  }, []);
  usePoll(() => reload(), 5000);

  const q = query.trim().toLowerCase();
  const machines = inv?.machines ?? [];

  function containerIgnored(c: DockerContainer): boolean {
    return (
      Boolean(prefs.get(prefKey(c.machine_id, "container", c.name))?.ignored) ||
      Boolean(c.project && prefs.get(prefKey(c.machine_id, "project", c.project))?.ignored)
    );
  }

  const groups = useMemo<Group[]>(() => {
    const out: Group[] = [];
    for (const m of machines) {
      if (machineFilter !== "all" && m.id !== machineFilter) {
        continue;
      }
      for (const p of m.projects ?? []) {
        const all = p.containers ?? [];
        const groupHit = !q || `${p.name} ${m.name} ${p.working_dir ?? ""}`.toLowerCase().includes(q);
        const containers = groupHit ? all : all.filter((c) => matchesQuery(c, q));
        if (containers.length === 0) {
          continue;
        }
        const ignored = !p.standalone && Boolean(prefs.get(prefKey(m.id, "project", p.name))?.ignored);
        const issues = all.filter((c) => isIssue(c.health) && !containerIgnored(c));
        out.push({ key: `${m.id}/${p.id}`, machine: m, project: p, containers, ignored, issues });
      }
    }
    return out;
    // containerIgnored reads prefs, which is listed.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [machines, machineFilter, q, prefs]);

  const attention = groups.filter((g) => !g.ignored && g.issues.length > 0);
  const healthy = groups.filter((g) => !g.ignored && g.issues.length === 0);
  const ignoredGroups = groups.filter((g) => g.ignored);
  const downEngines = machines.filter((m) => !m.daemon_ok && (machineFilter === "all" || m.id === machineFilter));
  const idleMachines = machines.filter(
    (m) => m.daemon_ok && (m.container_count ?? 0) === 0 && (machineFilter === "all" || m.id === machineFilter),
  );
  const ignoredCount = (inv?.containers ?? []).filter((c) => containerIgnored(c)).length;
  const flat = (inv?.containers ?? []).filter((c) => (machineFilter === "all" || c.machine_id === machineFilter) && matchesQuery(c, q));
  const selectedRow = (inv?.containers ?? []).find((c) => c.id === selected) ?? null;

  function toggleCollapsed(key: string, fallback: boolean) {
    setCollapsed((cur) => {
      const next = { ...cur, [key]: !(cur[key] ?? fallback) };
      storageSet(COLLAPSE_KEY, JSON.stringify(next));
      return next;
    });
  }

  async function runAction(c: DockerContainer, action: Act) {
    if (action === "remove" && !window.confirm(`Remove container ${c.name || c.container_id}? It is stopped and deleted. Named volumes are kept.`)) {
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

  async function runGroup(g: Group, action: "start" | "stop" | "restart") {
    setBusy(`${g.project.name}: ${action}`);
    setError(null);
    const failed: string[] = [];
    for (const c of g.project.containers ?? []) {
      try {
        await dockerContainerAction(c.machine_id, c.container_id, action);
      } catch (err) {
        failed.push(`${c.name}: ${err instanceof Error ? err.message : "failed"}`);
      }
    }
    setBusy(null);
    if (failed.length) {
      setError(`Some containers did not ${action}. ${failed.join("; ")}`);
    }
    await reload(true);
  }

  async function setIgnored(machineId: string, scope: "container" | "project", name: string, ignored: boolean) {
    setError(null);
    let note = "";
    if (ignored) {
      const typed = window.prompt(
        `Why is ${name} ignored? (optional) It stays out of Needs attention until someone stops ignoring it.`,
        "",
      );
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

  function machineOf(c: DockerContainer): DockerMachine | undefined {
    return machines.find((m) => m.id === c.machine_id);
  }

  function containerItems(c: DockerContainer): ContextItem[] {
    const m = machineOf(c);
    const dir = c.working_dir;
    const termOK = mutate && (m?.kind !== "host" || admin);
    const ignoredSelf = Boolean(prefs.get(prefKey(c.machine_id, "container", c.name))?.ignored);
    const ignoredProject = Boolean(c.project && prefs.get(prefKey(c.machine_id, "project", c.project))?.ignored);
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
        "sep",
        ignoredSelf
          ? { label: "Stop ignoring container", onClick: () => void setIgnored(c.machine_id, "container", c.name, false) }
          : { label: "Ignore container", onClick: () => void setIgnored(c.machine_id, "container", c.name, true) },
      );
      if (c.project) {
        items.push(
          ignoredProject
            ? { label: "Stop ignoring group", onClick: () => void setIgnored(c.machine_id, "project", c.project!, false) }
            : { label: "Ignore whole group", onClick: () => void setIgnored(c.machine_id, "project", c.project!, true) },
        );
      }
    }
    items.push("sep", { label: "Copy name", onClick: () => copy(c.name) }, { label: "Copy container ID", onClick: () => copy(c.container_id) });
    if (c.image) {
      items.push({ label: "Copy image", onClick: () => copy(c.image!) });
    }
    if (m && m.kind !== "host") {
      items.push({ label: "Go to machine", onClick: () => navigate(`/workloads/${encodeURIComponent(m.id)}`) });
    }
    if (mutate) {
      items.push("sep", { label: "Remove container", onClick: () => void runAction(c, "remove"), danger: true });
    }
    return items;
  }

  function groupItems(g: Group, isCollapsed: boolean): ContextItem[] {
    const dir = g.project.working_dir;
    const termOK = mutate && (g.machine.kind !== "host" || admin);
    const items: ContextItem[] = [
      { label: isCollapsed ? "Expand" : "Collapse", onClick: () => toggleCollapsed(g.key, isCollapsed) },
      { label: dir ? "Open terminal in compose folder" : "Open terminal on machine", onClick: () => void openTerminalAt(g.machine, dir), disabled: !termOK },
      { label: dir ? "Open compose folder in Files" : "Open machine files", onClick: () => void openFilesAt(g.machine, dir), disabled: g.machine.kind === "host" && !admin },
    ];
    if (mutate) {
      items.push(
        "sep",
        { label: "Start all", onClick: () => void runGroup(g, "start") },
        { label: "Stop all", onClick: () => void runGroup(g, "stop") },
        { label: "Restart all", onClick: () => void runGroup(g, "restart") },
      );
      if (!g.project.standalone) {
        items.push(
          "sep",
          g.ignored
            ? { label: "Stop ignoring group", onClick: () => void setIgnored(g.machine.id, "project", g.project.name, false) }
            : { label: "Ignore group", onClick: () => void setIgnored(g.machine.id, "project", g.project.name, true) },
        );
      }
    }
    if (dir) {
      items.push("sep", { label: "Copy compose folder path", onClick: () => copy(dir) });
    }
    if (g.machine.kind !== "host") {
      items.push({ label: "Go to machine", onClick: () => navigate(`/workloads/${encodeURIComponent(g.machine.id)}`) });
    }
    return items;
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
          Enable it from <Link href="/settings/features">Add Features</Link>. No Docker sockets are probed while the module is off.
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

  const issueCount = attention.reduce((n, g) => n + g.issues.length, 0);

  function renderCard(g: Group, tone: "issue" | "healthy" | "ignored") {
    const fallback = tone !== "issue" && g.containers.length > ROWS_SHOWN;
    const isCollapsed = collapsed[g.key] ?? fallback;
    const ordered = [...g.containers].sort((a, b) => {
      const ai = isIssue(a.health) && !containerIgnored(a) ? 0 : 1;
      const bi = isIssue(b.health) && !containerIgnored(b) ? 0 : 1;
      return ai - bi || (a.service || a.name).localeCompare(b.service || b.name);
    });
    const rows = showAll[g.key] ? ordered : ordered.slice(0, ROWS_SHOWN);
    const running = (g.project.containers ?? []).filter((c) => c.state === "running").length;
    const total = (g.project.containers ?? []).length;
    const groupStatus = tone === "ignored" ? "Ignored" : g.issues.length ? `${g.issues.length} need attention` : g.project.status_label || "Healthy";
    return (
      <article
        key={g.key}
        className={`docker-card is-${tone}`}
        data-group={g.project.name}
        onContextMenu={(e: ReactMouseEvent) => ctx.openAt(e, g.project.name, groupItems(g, isCollapsed))}
      >
        <header className="docker-card-head">
          <button
            className="linkish docker-head-name"
            type="button"
            aria-expanded={!isCollapsed}
            aria-label={`Group ${g.project.name}`}
            title={g.project.name}
            onClick={() => toggleCollapsed(g.key, fallback)}
          >
            {isCollapsed ? "▸" : "▾"} {g.project.standalone ? "Standalone containers" : g.project.name}
          </button>
          <StatusBadge status={tone === "ignored" ? "unknown" : g.issues.length ? healthTone(g.issues[0].health) : "healthy"} label={groupStatus} />
          <ActionMenu label={`${g.project.name} group actions`} items={groupItems(g, isCollapsed).filter((i): i is Exclude<ContextItem, "sep"> => i !== "sep" && !i.disabled)} />
        </header>
        <p className="meta docker-card-meta" title={g.project.working_dir || ""}>
          {g.machine.name} · {running}/{total} running
          {g.project.working_dir ? ` · ${g.project.working_dir}` : ""}
        </p>
        {isCollapsed ? null : (
          <ul className="docker-rows">
            {rows.map((c) => {
              const ign = containerIgnored(c);
              return (
                <li
                  key={c.id}
                  className={"docker-row" + (isIssue(c.health) && !ign ? " is-issue" : "") + (ign ? " is-ignored" : "")}
                  onContextMenu={(e: ReactMouseEvent) => ctx.openAt(e, c.name, containerItems(c))}
                >
                  <button className="linkish docker-row-name" type="button" title={c.name} onClick={() => setSelected(c.id)}>
                    <strong>{c.service || c.name}</strong>
                    {c.service && c.name !== c.service ? <span className="meta"> {c.name}</span> : null}
                  </button>
                  <span className="docker-row-status">
                    <StatusBadge status={ign ? "unknown" : healthTone(c.health)} label={ign ? `Ignored · ${c.status_label}` : c.status_label} />
                  </span>
                  <span className="meta docker-row-meta" title={c.health_reason || c.image}>
                    {isIssue(c.health) && c.health_reason ? c.health_reason : c.uptime || c.image || ""}
                  </span>
                  <ActionMenu label={`${c.name} actions`} items={containerItems(c).filter((i): i is Exclude<ContextItem, "sep"> => i !== "sep" && !i.disabled)} />
                </li>
              );
            })}
            {ordered.length > ROWS_SHOWN ? (
              <li className="docker-row-more">
                <button className="btn btn-ghost btn-sm" type="button" onClick={() => setShowAll((cur) => ({ ...cur, [g.key]: !cur[g.key] }))}>
                  {showAll[g.key] ? "Show fewer" : `Show ${ordered.length - ROWS_SHOWN} more`}
                </button>
              </li>
            ) : null}
          </ul>
        )}
      </article>
    );
  }

  return (
    <section className="page page-wide" aria-labelledby="docker-heading">
      <PageHeader
        id="docker-heading"
        title="Docker"
        kicker="Compose projects across the host and system containers. Right-click a group or container for its tools."
        actions={
          <button className="btn btn-secondary" type="button" onClick={() => void reload(true)}>
            Refresh
          </button>
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
      <div className="status-strip">
        <div className="status-tile">
          <span className="label">Need attention</span>
          <span className="value">
            <StatusBadge status={issueCount ? "critical" : "healthy"} /> {issueCount}
          </span>
          <span className="meta">
            {attention.length} group(s){downEngines.length ? ` · ${downEngines.length} engine(s) down` : ""}
          </span>
        </div>
        <div className="status-tile">
          <span className="label">Healthy groups</span>
          <span className="value">{healthy.length}</span>
          <span className="meta">
            {inv?.summary?.running ?? 0} running, {inv?.summary?.stopped ?? 0} stopped
          </span>
        </div>
        <div className="status-tile">
          <span className="label">Ignored</span>
          <span className="value">{ignoredCount}</span>
          <span className="meta">{ignoredGroups.length} group(s) ignored</span>
        </div>
        <div className="status-tile">
          <span className="label">Engines</span>
          <span className="value">{inv?.summary?.machines ?? 0}</span>
          <span className="meta">{inv?.summary?.daemons_down ? `${inv.summary.daemons_down} down` : "All reachable"}</span>
        </div>
      </div>

      <div className="docker-toolbar">
        <input
          id="docker-search"
          className="field-input"
          aria-label="Search containers"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder="Search name, image, project, IP"
        />
        <select className="field-input" aria-label="Machine" value={machineFilter} onChange={(e) => setMachineFilter(e.target.value)}>
          <option value="all">All machines</option>
          {machines.map((m) => (
            <option key={m.id} value={m.id}>
              {m.name}
            </option>
          ))}
        </select>
        <div className="segmented" role="group" aria-label="View">
          <button type="button" aria-pressed={view === "groups"} onClick={() => setView("groups")}>
            Groups
          </button>
          <button type="button" aria-pressed={view === "table"} onClick={() => setView("table")}>
            Table
          </button>
        </div>
      </div>

      {machines.length === 0 ? (
        <EmptyState title="No Docker engines discovered">
          Install Docker in a system container or on the host. No-DAL does not install Docker Engine when this feature is enabled.
        </EmptyState>
      ) : view === "table" ? (
        <article className="panel">
          <ResourceTable
            className="docker-table-flat"
            headers={["Container", "Machine", "Project", "Status", "Image", "Ports", "Restarts", ""]}
            numeric={[6]}
            rows={flat.map((c) => [
              <button
                key="n"
                className="linkish docker-clip"
                type="button"
                title={c.name}
                onClick={() => setSelected(c.id)}
                onContextMenu={(e) => ctx.openAt(e, c.name, containerItems(c))}
              >
                {c.name}
              </button>,
              <span key="m" className="docker-clip" title={c.machine_name || c.machine_id}>
                {c.machine_name || c.machine_id}
              </span>,
              <span key="p" className="docker-clip" title={c.project || "Standalone"}>
                {c.project || "Standalone"}
              </span>,
              <StatusBadge
                key="s"
                status={containerIgnored(c) ? "unknown" : healthTone(c.health)}
                label={containerIgnored(c) ? `Ignored · ${c.status_label}` : c.status_label}
              />,
              <span key="i" className="docker-clip" title={c.image || "Not reported"}>
                {c.image || "Not reported"}
              </span>,
              <span key="pt" className="docker-clip" title={portText(c)}>
                {portText(c)}
              </span>,
              String(c.restart_count ?? 0),
              <ActionMenu key="a" label={`${c.name} actions`} items={containerItems(c).filter((i): i is Exclude<ContextItem, "sep"> => i !== "sep" && !i.disabled)} />,
            ])}
            empty={<p className="muted">No containers match the current filters.</p>}
          />
        </article>
      ) : (
        <>
          <section className="docker-section" aria-labelledby="docker-attention">
            <h2 id="docker-attention" className="docker-section-title">
              Needs attention <span className="meta">{attention.length + downEngines.length}</span>
            </h2>
            {attention.length === 0 && downEngines.length === 0 ? (
              <p className="muted">Nothing needs attention.</p>
            ) : (
              <div className="docker-grid">
                {downEngines.map((m) => (
                  <article key={`down-${m.id}`} className="docker-card is-issue">
                    <header className="docker-card-head">
                      <strong className="docker-head-name">{m.name}</strong>
                      <StatusBadge status="critical" label="Engine unreachable" />
                    </header>
                    <ErrorNotice error={m.daemon_error || "The Docker daemon did not answer."} />
                  </article>
                ))}
                {attention.map((g) => renderCard(g, "issue"))}
              </div>
            )}
          </section>
          <section className="docker-section" aria-labelledby="docker-healthy">
            <h2 id="docker-healthy" className="docker-section-title">
              Healthy <span className="meta">{healthy.length}</span>
            </h2>
            {healthy.length === 0 ? <p className="muted">No healthy groups match.</p> : <div className="docker-grid">{healthy.map((g) => renderCard(g, "healthy"))}</div>}
          </section>
          {ignoredGroups.length > 0 ? (
            <section className="docker-section" aria-labelledby="docker-ignored">
              <h2 id="docker-ignored" className="docker-section-title">
                <button className="linkish" type="button" aria-expanded={ignoredOpen} onClick={() => setIgnoredOpen((v) => !v)}>
                  {ignoredOpen ? "▾" : "▸"} Ignored groups
                </button>{" "}
                <span className="meta">{ignoredGroups.length}</span>
              </h2>
              {ignoredOpen ? <div className="docker-grid">{ignoredGroups.map((g) => renderCard(g, "ignored"))}</div> : null}
            </section>
          ) : null}
          {idleMachines.length > 0 ? (
            <section className="docker-section docker-idle" aria-labelledby="docker-idle">
              <h2 id="docker-idle" className="docker-section-title">
                <button className="linkish" type="button" aria-expanded={idleOpen} onClick={() => setIdleOpen((v) => !v)}>
                  {idleOpen ? "▾" : "▸"} Engines with no containers
                </button>{" "}
                <span className="meta">{idleMachines.length}</span>
              </h2>
              {idleOpen ? (
                <div className="docker-grid">
                  {idleMachines.map((m) => (
                    <article key={m.id} className="docker-card is-healthy">
                      <header className="docker-card-head">
                        <strong className="docker-head-name">{m.name}</strong>
                        <StatusBadge status={healthTone(m.health)} label={m.health_reason || honestCap(m.health) || "Reachable"} />
                      </header>
                      <p className="meta docker-card-meta">
                        {m.kind === "host" ? "Host" : "System container"}
                        {m.ipv4 ? ` · ${m.ipv4}` : ""}
                        {m.docker_version ? ` · Docker ${m.docker_version}` : ""}
                      </p>
                    </article>
                  ))}
                </div>
              ) : null}
            </section>
          ) : null}
        </>
      )}

      <Dialog open={selectedRow != null} title={selectedRow?.name ?? "Container"} wide onClose={() => setSelected(null)}>
        {selectedRow ? (
          <ContainerDetail
            container={selectedRow}
            ignored={containerIgnored(selectedRow)}
            logs={logFor === selectedRow.id ? logs : ""}
            mutate={mutate}
            busy={busy}
            onAction={runAction}
            onLogs={loadLogs}
            onShell={openShell}
          />
        ) : null}
      </Dialog>
      <ContextMenu menu={ctx.menu} onClose={ctx.close} />
    </section>
  );
}

function ContainerDetail({
  container,
  ignored,
  logs,
  mutate,
  busy,
  onAction,
  onLogs,
  onShell,
}: {
  container: DockerContainer;
  ignored: boolean;
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
          <dd>{(container.mounts ?? []).map((m) => `${m.destination}${m.source ? ` ← ${m.source}` : ""}`).join(", ") || "None"}</dd>
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
