import { useCallback, useEffect, useMemo, useState, type MouseEvent as ReactMouseEvent } from "react";
import {
  ApiError,
  bulkDeploy,
  cancelDeployment,
  checkAllDeployProjects,
  checkDeployProject,
  deleteDeployProject,
  deployProject,
  forceDeployment,
  listDeployProjects,
  listProjectDeployments,
  listWorkloads,
  putDeploySettings,
  type DeployDiscovered,
  type DeployProject,
  type DeployProjectList,
  type DeploySettings,
  type Deployment,
} from "../api/client";
import { confirmAction } from "../confirm";
import { ActionMenu } from "../components/ActionMenu";
import { ContextMenu, useContextMenu, type ContextItem } from "../components/ContextMenu";
import { EmptyState, LoadingState } from "../components/EmptyState";
import { ErrorNotice } from "../components/ErrorNotice";
import { StatusBadge } from "../components/StatusBadge";
import { usePref } from "../prefs";
import { formatRelative, formatWhen } from "../format";
import { usePoll } from "../query";
import { Dialog } from "../ui/Dialog";
import { ACTIVE, badgeStatus, DeploymentLog, DeployProgress, deployStatusLabel, formatSeconds, shortCommit } from "../deploy/parts";
import { NotifySection, WebhookSection } from "../deploy/NotifyAndHook";
import { ProjectEditor, type EditorSeed } from "../deploy/ProjectEditor";
import "../deploy/deploy.css";
import "../docker.css";
import { menuItems } from "./UptimePage";

type Container = { id: string; name: string };

function errorText(err: unknown, fallback: string): string {
  return err instanceof ApiError || err instanceof Error ? err.message : fallback;
}

/** UpdateState is what update detection found for a project. */
export function UpdateState({ p }: { p: DeployProject }) {
  const st = p.state;
  if (!p.config.detect) {
    return <span className="bk-hint">Not checked</span>;
  }
  if (st.check_error) {
    return (
      <span className="bk-pill is-danger" title={st.check_error}>
        Check failed
      </span>
    );
  }
  if (!st.checked_at) {
    return <span className="bk-hint">Not checked yet</span>;
  }
  const pills = [];
  if (st.due || (st.behind ?? 0) > 0) {
    pills.push(
      <span key="due" className="bk-pill is-warn" title={st.remote_subject ? `${st.remote_subject} (${st.remote_author ?? ""})` : undefined}>
        {(st.behind ?? 0) > 0 ? `${st.behind} behind` : "Update available"}
      </span>,
    );
  } else {
    pills.push(
      <span key="ok" className="bk-pill is-ok">
        Up to date
      </span>,
    );
  }
  if ((st.ahead ?? 0) > 0) {
    pills.push(
      <span key="ahead" className="bk-pill is-warn" title="The folder has commits the remote does not">
        {st.ahead} ahead
      </span>,
    );
  }
  if (st.dirty) {
    pills.push(
      <span key="dirty" className="bk-pill is-warn" title="The folder has uncommitted changes">
        Uncommitted changes
      </span>,
    );
  }
  return <>{pills}</>;
}

function LastResult({ p }: { p: DeployProject }) {
  const st = p.state;
  if (!st.last_deploy_status) {
    return <span className="bk-hint">Never deployed</span>;
  }
  return (
    <span title={formatWhen(st.last_deploy_at)}>
      <StatusBadge status={badgeStatus(st.last_deploy_status)} label={deployStatusLabel({ status: st.last_deploy_status as Deployment["status"] })} />{" "}
      <span className="bk-hint">{formatRelative(st.last_deploy_at)}</span>
    </span>
  );
}

/** ProjectDrawer shows a project's live deployment, history and settings. */
type DrawerTab = "live" | "history" | "settings";

function ProjectDrawer({
  project,
  list,
  containers,
  initialTab = "live",
  onChanged,
  onClose,
}: {
  project: DeployProject;
  list: DeployProjectList;
  containers: Container[];
  initialTab?: DrawerTab;
  onChanged: () => void;
  onClose: () => void;
}) {
  const [tab, setTab] = useState<DrawerTab>(initialTab === "settings" && !list.can_configure ? "live" : initialTab);
  const [history, setHistory] = useState<Deployment[]>([]);
  const [shown, setShown] = useState<Deployment | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const loadHistory = useCallback(async () => {
    try {
      const res = await listProjectDeployments(project.id);
      setHistory(res.deployments);
      setShown((cur) => {
        if (cur) {
          return res.deployments.find((d) => d.id === cur.id) ?? cur;
        }
        return res.deployments[0] ?? null;
      });
    } catch (err) {
      setError(errorText(err, "Could not read the history"));
    }
  }, [project.id]);

  useEffect(() => {
    void loadHistory();
  }, [loadHistory]);
  usePoll(loadHistory, 2000, !!shown && ACTIVE.has(shown.status));

  async function act(fn: () => Promise<unknown>) {
    setBusy(true);
    setError(null);
    try {
      await fn();
      await loadHistory();
      onChanged();
    } catch (err) {
      setError(errorText(err, "That did not work"));
    } finally {
      setBusy(false);
    }
  }

  const active = shown && ACTIVE.has(shown.status);
  return (
    <Dialog open drawer wide title={`${project.name} · ${project.workload_name ?? ""}`} onClose={onClose}>
      <div className="dp-page">
        <p className="dp-sub">{project.working_dir}</p>
        {project.config.detect ? (
          <div className="bk-stats" aria-label="Git">
            <UpdateState p={project} />
            {project.state.branch ? (
              <span className="bk-stat">
                Branch <b>{project.state.branch}</b>
              </span>
            ) : null}
            {project.state.provider ? (
              <span className="bk-stat" title={project.state.remote_url}>
                Via <b>{project.state.provider === "codehold" ? "CodeHold (ch)" : project.state.provider === "github" ? "GitHub (gh)" : "Git"}</b>
                {project.state.remote ? ` · ${project.state.remote}` : ""}
              </span>
            ) : null}
            {project.state.local_commit ? (
              <span className="bk-stat dp-commit">
                At <b>{shortCommit(project.state.local_commit)}</b>
                {project.state.remote_commit && project.state.remote_commit !== project.state.local_commit ? (
                  <>
                    {" "}
                    latest <b>{shortCommit(project.state.remote_commit)}</b>
                  </>
                ) : null}
              </span>
            ) : null}
            {project.state.checked_at ? <span className="bk-hint">Checked {formatRelative(project.state.checked_at)}</span> : null}
          </div>
        ) : null}
        {project.state.check_error ? <p className="bk-hint is-danger">Update check: {project.state.check_error}</p> : null}
        {project.state.check_note ? <p className="bk-hint">{project.state.check_note}</p> : null}
        {project.state.due && project.state.remote_subject ? (
          <p className="bk-hint">
            Newest commit: <b>{project.state.remote_subject}</b>
            {project.state.remote_author ? ` by ${project.state.remote_author}` : ""}
          </p>
        ) : null}
        <div className="dp-tabs" role="tablist">
          {(["live", "history", ...(list.can_configure ? ["settings"] : [])] as const).map((t) => (
            <button key={t} type="button" role="tab" aria-selected={tab === t} onClick={() => setTab(t as typeof tab)}>
              {t === "live" ? "Deployment" : t === "history" ? "History" : "Settings"}
            </button>
          ))}
        </div>
        {error ? <ErrorNotice error={error} /> : null}
        {tab === "live" ? (
          shown ? (
            <>
              <div className="bk-head">
                <div className="dp-project">
                  <span>
                    <StatusBadge status={badgeStatus(shown.status)} label={deployStatusLabel(shown)} />{" "}
                    <span className="bk-hint">
                      {shown.trigger} by {shown.actor || "NDL-CE"} · {formatWhen(shown.started_at ?? shown.queued_at)}
                      {shown.duration_sec != null && !active ? ` · ${formatSeconds(shown.duration_sec)}` : ""}
                    </span>
                  </span>
                  {shown.commit_before || shown.commit_after ? (
                    <span className="dp-commit">
                      {shortCommit(shown.commit_before) || "?"} → {shortCommit(shown.commit_after) || (active ? "…" : "?")}
                    </span>
                  ) : null}
                </div>
                <div className="bk-actions">
                  {list.can_deploy && shown.status === "waiting" && !shown.force ? (
                    <button className="btn btn-sm" type="button" disabled={busy} onClick={() => void act(() => forceDeployment(shown.id))}>
                      Deploy anyway
                    </button>
                  ) : null}
                  {list.can_deploy && active ? (
                    <button
                      className="btn btn-sm btn-danger-text"
                      type="button"
                      disabled={busy || shown.cancel_requested}
                      onClick={() => {
                        if (confirmAction(`Cancel this deployment of ${project.name}? Every process it started is stopped.`)) {
                          void act(() => cancelDeployment(shown.id));
                        }
                      }}
                    >
                      Cancel
                    </button>
                  ) : null}
                  {list.can_deploy && !active ? (
                    <button className="btn btn-sm btn-primary" type="button" disabled={busy || project.workload_missing} onClick={() => void act(() => deployProject(project.id))}>
                      Deploy now
                    </button>
                  ) : null}
                </div>
              </div>
              {active ? <DeployProgress d={shown} estimate={project.estimate_sec} /> : null}
              {shown.error ? <p className="bk-hint is-danger">{shown.error}</p> : null}
              {shown.health?.length ? <p className="bk-hint">Containers afterwards (information only): {shown.health.join(", ")}</p> : null}
              <DeploymentLog d={shown} />
            </>
          ) : (
            <EmptyState title="Not deployed yet">
              {list.can_deploy ? (
                <button className="btn btn-primary" type="button" disabled={busy} onClick={() => void act(() => deployProject(project.id))}>
                  Deploy now
                </button>
              ) : null}
            </EmptyState>
          )
        ) : null}
        {tab === "history" ? (
          <div className="bk-scroll is-tall">
            <table className="bk-table" aria-label="Deployment history">
              <thead>
                <tr>
                  <th>Result</th>
                  <th>When</th>
                  <th>Trigger</th>
                  <th>Commits</th>
                  <th className="bk-num">Took</th>
                  <th />
                </tr>
              </thead>
              <tbody>
                {history.map((d) => (
                  <tr key={d.id}>
                    <td>
                      <StatusBadge status={badgeStatus(d.status)} label={deployStatusLabel(d)} />
                      {d.exit_code ? <span className="bk-hint"> exit {d.exit_code}</span> : null}
                    </td>
                    <td title={formatWhen(d.queued_at)}>{formatRelative(d.queued_at)}</td>
                    <td>
                      {d.trigger} · {d.actor || "NDL-CE"}
                    </td>
                    <td className="dp-commit">
                      {shortCommit(d.commit_before)}
                      {d.commit_after && d.commit_after !== d.commit_before ? ` → ${shortCommit(d.commit_after)}` : ""}
                    </td>
                    <td className="bk-num">{formatSeconds(d.duration_sec)}</td>
                    <td>
                      <button
                        type="button"
                        className="btn btn-sm btn-ghost"
                        onClick={() => {
                          setShown(d);
                          setTab("live");
                        }}
                      >
                        Log
                      </button>
                    </td>
                  </tr>
                ))}
                {history.length === 0 ? (
                  <tr>
                    <td colSpan={6} className="bk-hint">
                      No deployments yet.
                    </td>
                  </tr>
                ) : null}
              </tbody>
            </table>
          </div>
        ) : null}
        {tab === "settings" && list.can_configure ? (
          <>
            <ProjectEditor
              project={project}
              containers={containers}
              settings={list.settings}
              extra={(cfg, set) => <NotifySection cfg={cfg} set={set} />}
              onSaved={() => {
                onChanged();
                setTab("live");
              }}
              onCancel={() => setTab("live")}
            />
            <WebhookSection project={project} onChanged={onChanged} />
            <div className="bk-actions">
              <button
                type="button"
                className="btn btn-sm btn-ghost btn-danger-text"
                disabled={busy}
                onClick={() => {
                  if (confirmAction(`Forget ${project.name}? Its history and logs are deleted; the folder is not touched.`)) {
                    void act(async () => {
                      await deleteDeployProject(project.id);
                      onClose();
                    });
                  }
                }}
              >
                Remove project
              </button>
            </div>
          </>
        ) : null}
      </div>
    </Dialog>
  );
}

function SettingsDialog({ settings, onSaved, onClose }: { settings: DeploySettings; onSaved: () => void; onClose: () => void }) {
  const [s, setS] = useState<DeploySettings>(settings);
  const [error, setError] = useState<string | null>(null);
  const num = (k: keyof DeploySettings, v: string) => setS((cur) => ({ ...cur, [k]: v === "" ? 0 : Number(v) }));
  return (
    <Dialog
      open
      title="Deployment settings"
      onClose={onClose}
      footer={
        <div className="bk-actions">
          <button className="btn" type="button" onClick={onClose}>
            Cancel
          </button>
          <button
            className="btn btn-primary"
            type="button"
            onClick={() => {
              setError(null);
              putDeploySettings(s)
                .then(() => onSaved())
                .catch((err) => setError(errorText(err, "Could not save the settings")));
            }}
          >
            Save
          </button>
        </div>
      }
    >
      <div className="dp-form">
        {error ? <ErrorNotice error={error} /> : null}
        <fieldset>
          <legend>Limits</legend>
          <div className="form-grid">
            <label className="field">
              <span className="field-label">At once, in total</span>
              <input className="field-input" type="number" min={1} max={50} value={s.max_concurrent ?? 3} onChange={(e) => num("max_concurrent", e.target.value)} />
            </label>
            <label className="field">
              <span className="field-label">At once, per container</span>
              <input className="field-input" type="number" min={1} max={20} value={s.per_container ?? 1} onChange={(e) => num("per_container", e.target.value)} />
            </label>
            <label className="field">
              <span className="field-label">Default time limit (minutes)</span>
              <input
                className="field-input"
                type="number"
                min={1}
                max={1440}
                value={Math.round((s.default_timeout_sec ?? 1800) / 60)}
                onChange={(e) => num("default_timeout_sec", String(Number(e.target.value) * 60))}
              />
            </label>
            <label className="field">
              <span className="field-label">Check for updates every (minutes)</span>
              <input
                className="field-input"
                type="number"
                min={1}
                max={10080}
                value={Math.round((s.detect_interval_sec ?? 600) / 60)}
                onChange={(e) => num("detect_interval_sec", String(Number(e.target.value) * 60))}
              />
            </label>
          </div>
        </fieldset>
        <fieldset>
          <legend>Logs</legend>
          <p className="bk-hint">
            Every deployment keeps its complete log as a file on the node. Old ones are removed whole, never shortened; each
            project's latest deployment is always kept.
          </p>
          <label className="field">
            <span className="field-label">Log folder on the node</span>
            <input className="field-input" value={s.log_dir ?? ""} onChange={(e) => setS((cur) => ({ ...cur, log_dir: e.target.value }))} />
          </label>
          <div className="form-grid">
            <label className="field">
              <span className="field-label">Keep for (days, 0 = forever)</span>
              <input className="field-input" type="number" min={0} value={s.retention_days ?? 30} onChange={(e) => num("retention_days", e.target.value)} />
            </label>
            <label className="field">
              <span className="field-label">Keep per project (0 = all)</span>
              <input className="field-input" type="number" min={0} value={s.keep_per_project ?? 50} onChange={(e) => num("keep_per_project", e.target.value)} />
            </label>
            <label className="field">
              <span className="field-label">Total size limit (GB, 0 = none)</span>
              <input className="field-input" type="number" min={0} step="0.5" value={s.max_log_gb ?? 10} onChange={(e) => num("max_log_gb", e.target.value)} />
            </label>
          </div>
        </fieldset>
        <fieldset>
          <legend>New projects start with</legend>
          <textarea
            className="field-input"
            aria-label="Default commands, one per line"
            rows={3}
            spellCheck={false}
            value={(s.default_commands ?? []).map((c) => c.run).join("\n")}
            onChange={(e) => setS((cur) => ({ ...cur, default_commands: e.target.value.split("\n").map((run) => ({ run })) }))}
          />
          <p className="bk-hint">One command per line.</p>
        </fieldset>
      </div>
    </Dialog>
  );
}

function foundState(f: DeployDiscovered): string {
  const running = f.running ?? 0;
  const services = f.services ?? 0;
  if (running === 0) {
    return "stopped";
  }
  return running >= services ? "running" : "partial";
}

/**
 * FoundInContainers lists Compose projects in system containers that have
 * no update commands yet, as one compact table that folds away.
 */
function FoundInContainers({
  found,
  canAdd,
  defaultOpen,
  onAdd,
}: {
  found: DeployDiscovered[];
  canAdd: boolean;
  defaultOpen: boolean;
  onAdd: (f: DeployDiscovered) => void;
}) {
  const [saved, setSaved] = usePref<boolean | null>("deploy.foundOpen", null);
  const open = saved ?? defaultOpen;
  const [query, setQuery] = useState("");
  const q = query.trim().toLowerCase();
  const rows = found
    .filter((f) => !q || `${f.name} ${f.workload_name} ${f.working_dir}`.toLowerCase().includes(q))
    .sort((a, b) => (a.workload_name || a.workload_id).localeCompare(b.workload_name || b.workload_id) || a.name.localeCompare(b.name));
  const tone = (f: DeployDiscovered) => {
    const st = foundState(f);
    return st === "running" ? "healthy" : st === "partial" ? "degraded" : "unknown";
  };
  return (
    <section className="dk-section" aria-labelledby="dp-found-heading">
      <div className="dk-section-head">
        <h2 id="dp-found-heading">
          <button className="dp-found-toggle" type="button" aria-expanded={open} onClick={() => setSaved(!open)}>
            {open ? "▾" : "▸"} Found in containers <span className="meta">{found.length}</span>
          </button>
        </h2>
        {open && found.length > 8 ? (
          <input
            className="field-input dk-search"
            type="search"
            aria-label="Search found projects"
            placeholder="Search project, container, folder"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
          />
        ) : null}
      </div>
      {open ? (
        <>
          <p className="bk-hint">Compose projects running in system containers that have no update commands yet.</p>
          {rows.length === 0 ? (
            <p className="dk-empty muted">No projects match.</p>
          ) : (
            <div className="table-wrap dk-table-wrap">
              <table className="dk-table" aria-label="Found projects">
                <thead>
                  <tr>
                    <th>Project</th>
                    <th>Container</th>
                    <th>Folder</th>
                    <th>Services</th>
                    {canAdd ? <th className="cell-actions" /> : null}
                  </tr>
                </thead>
                <tbody>
                  {rows.map((f) => (
                    <tr key={`${f.workload_id}:${f.working_dir}`} className={"dk-row" + (foundState(f) === "stopped" ? " is-muted" : "")}>
                      <td>
                        <strong className="dk-clip" title={f.name}>
                          {f.name}
                        </strong>
                      </td>
                      <td>{f.workload_name || f.workload_id}</td>
                      <td>
                        <span className="dk-clip dp-sub" title={f.working_dir}>
                          {f.working_dir}
                        </span>
                      </td>
                      <td>
                        <StatusBadge status={tone(f)} label={`${f.running ?? 0} of ${f.services ?? 0} running`} />
                      </td>
                      {canAdd ? (
                        <td className="cell-actions">
                          <button type="button" className="btn btn-sm" aria-label={`Add ${f.name}`} onClick={() => onAdd(f)}>
                            Add
                          </button>
                        </td>
                      ) : null}
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </>
      ) : null}
    </section>
  );
}

/**
 * DeployPage lists Docker Compose projects in system containers, shows
 * which have new commits, and deploys them with their own commands.
 */
export function DeployPage() {
  const [list, setList] = useState<DeployProjectList | null>(null);
  const [containers, setContainers] = useState<Container[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [openAt, setOpenAt] = useState<{ id: string; tab: DrawerTab } | null>(null);
  const [adding, setAdding] = useState<EditorSeed | null>(null);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [busy, setBusy] = useState<string | null>(null);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const ctx = useContextMenu();

  const load = useCallback(async () => {
    try {
      setList(await listDeployProjects());
      setError(null);
    } catch (err) {
      setError(errorText(err, "Could not load deployments"));
    }
  }, []);

  useEffect(() => {
    void load();
    listWorkloads()
      .then((res) =>
        setContainers(
          (res.items ?? [])
            .filter((w) => w.kind === "system-container")
            .map((w) => ({ id: w.id, name: w.name }))
            .sort((a, b) => a.name.localeCompare(b.name)),
        ),
      )
      .catch(() => undefined);
  }, [load]);

  const anyActive = (list?.projects ?? []).some((p) => p.active_deployment);
  usePoll(load, anyActive ? 2000 : 15000);

  const projects = useMemo(() => list?.projects ?? [], [list]);
  const open = projects.find((p) => p.id === openAt?.id) ?? null;
  const due = projects.filter((p) => p.state.due && !p.active_deployment);
  const chosen = projects.filter((p) => selected.has(p.id) && !p.active_deployment);
  const canDeploy = Boolean(list?.can_deploy);
  const canConfigure = Boolean(list?.can_configure);

  async function runBulk(body: { project_ids?: string[]; due?: boolean }, what: string) {
    setBusy("bulk");
    setNotice(null);
    setError(null);
    try {
      const res = await bulkDeploy(body);
      const skipped = res.skipped.map((x) => `${x.name || x.id}: ${x.reason}`).join("; ");
      setNotice(`${what}: ${res.queued.length} queued${skipped ? `; skipped ${skipped}` : ""}.`);
      setSelected(new Set());
      await load();
    } catch (err) {
      setError(errorText(err, "Could not queue the deployments"));
    } finally {
      setBusy(null);
    }
  }

  async function checkOne(p: DeployProject) {
    setBusy("check:" + p.id);
    try {
      await checkDeployProject(p.id);
      await load();
    } catch (err) {
      setError(errorText(err, "Could not check for updates"));
    } finally {
      setBusy(null);
    }
  }

  function checkAll() {
    setBusy("checkall");
    checkAllDeployProjects()
      .then((r) => setNotice(r.started ? "Checking every project for new commits." : (r.message ?? "A check is already running.")))
      .catch((err) => setError(errorText(err, "Could not start the check")))
      .finally(() => setBusy(null));
  }

  function toggle(id: string, on: boolean) {
    setSelected((cur) => {
      const next = new Set(cur);
      if (on) {
        next.add(id);
      } else {
        next.delete(id);
      }
      return next;
    });
  }

  async function deployOne(p: DeployProject) {
    setBusy(p.id);
    setNotice(null);
    try {
      const d = await deployProject(p.id);
      setNotice(`${p.name}: ${deployStatusLabel(d).toLowerCase()}.`);
      await load();
    } catch (err) {
      setError(errorText(err, "Could not start the deployment"));
    } finally {
      setBusy(null);
    }
  }

  async function removeProject(p: DeployProject) {
    if (!confirmAction(`Forget ${p.name}? Its history and logs are deleted; the folder is not touched.`)) {
      return;
    }
    setError(null);
    try {
      await deleteDeployProject(p.id);
      setNotice(`${p.name} removed.`);
      await load();
    } catch (err) {
      setError(errorText(err, "Could not remove the project"));
    }
  }

  function projectItems(p: DeployProject): ContextItem[] {
    const d = p.active_deployment;
    const items: ContextItem[] = [{ label: d ? "Watch deployment" : "Open", onClick: () => setOpenAt({ id: p.id, tab: "live" }) }];
    if (canDeploy) {
      items.push(
        { label: "Deploy now", onClick: () => void deployOne(p), disabled: Boolean(d) || p.workload_missing || busy === p.id },
        { label: "Check for updates", onClick: () => void checkOne(p), disabled: Boolean(d) || !p.config.detect || busy === "check:" + p.id },
      );
    }
    items.push({ label: "History", onClick: () => setOpenAt({ id: p.id, tab: "history" }) });
    if (canConfigure) {
      items.push({ label: "Settings", onClick: () => setOpenAt({ id: p.id, tab: "settings" }) });
    }
    items.push("sep", { label: "Copy folder path", onClick: () => void navigator.clipboard?.writeText(p.working_dir) });
    if (canConfigure) {
      items.push("sep", { label: "Remove project", danger: true, onClick: () => void removeProject(p) });
    }
    return items;
  }

  if (!list && !error) {
    return <LoadingState label="Loading deployments" />;
  }

  const more = [
    ...(canDeploy ? [{ label: busy === "checkall" ? "Checking" : "Check all for updates", onClick: checkAll }] : []),
    ...(canConfigure ? [{ label: "Deployment settings", onClick: () => setSettingsOpen(true) }] : []),
  ];

  return (
    <div className="dp-page">
      <div className="dp-bar">
        <div className="bk-stats" aria-label="Deployment summary">
          <span className="bk-stat">
            Projects <b>{projects.length}</b>
          </span>
          <span className={"bk-stat" + (list?.running ? " is-active" : "")}>
            Deploying <b>{list?.running ?? 0}</b>
          </span>
          <span className="bk-stat">
            Queued <b>{list?.queued ?? 0}</b>
          </span>
          <span className={"bk-stat" + (due.length ? " is-warn" : "")}>
            Updates <b>{due.length}</b>
          </span>
        </div>
        <div className="bk-actions">
          {canDeploy && chosen.length ? (
            <button className="btn" type="button" disabled={busy === "bulk"} onClick={() => void runBulk({ project_ids: chosen.map((p) => p.id) }, "Deploy selected")}>
              Deploy selected ({chosen.length})
            </button>
          ) : null}
          {canDeploy ? (
            <button
              className="btn btn-primary"
              type="button"
              disabled={busy === "bulk" || due.length === 0}
              onClick={() => {
                if (confirmAction(`Deploy ${due.length} project(s) with updates: ${due.map((p) => p.name).join(", ")}?`)) {
                  void runBulk({ due: true }, "Deploy all updates");
                }
              }}
            >
              Deploy all updates{due.length ? ` (${due.length})` : ""}
            </button>
          ) : null}
          {canConfigure ? (
            <button className="btn btn-secondary" type="button" onClick={() => setAdding({})}>
              Add project
            </button>
          ) : null}
          <ActionMenu label="More deployment actions" items={more} />
        </div>
      </div>
      {error ? <ErrorNotice error={error} /> : null}
      {notice ? (
        <p className="banner banner-ok" role="status">
          {notice}
        </p>
      ) : null}

      <section className="dk-section" aria-labelledby="dp-projects-heading">
        <div className="dk-section-head">
          <h2 id="dp-projects-heading">Projects</h2>
        </div>
        {projects.length === 0 ? (
          <EmptyState title="No projects yet">
            Add a Docker Compose project's folder in a system container and the commands that update it. Projects found in your
            containers are listed below.
          </EmptyState>
        ) : (
          <div className="table-wrap dk-table-wrap">
            <table className="dk-table" aria-label="Deployment projects">
              <thead>
                <tr>
                  {canDeploy ? (
                    <th className="dp-check">
                      <input
                        type="checkbox"
                        aria-label="Select every project"
                        checked={projects.length > 0 && projects.every((p) => selected.has(p.id))}
                        onChange={(e) => setSelected(e.target.checked ? new Set(projects.map((p) => p.id)) : new Set())}
                      />
                    </th>
                  ) : null}
                  <th>Project</th>
                  <th>Updates</th>
                  <th>Deployment</th>
                  <th className="cell-actions" />
                </tr>
              </thead>
              <tbody>
                {projects.map((p) => {
                  const d = p.active_deployment;
                  return (
                    <tr
                      key={p.id}
                      className={"dk-row" + (d ? " dp-row-active" : "")}
                      onContextMenu={(e: ReactMouseEvent) => ctx.openAt(e, p.name, projectItems(p))}
                    >
                      {canDeploy ? (
                        <td className="dp-check">
                          <input type="checkbox" aria-label={`Select ${p.name}`} checked={selected.has(p.id)} onChange={(e) => toggle(p.id, e.target.checked)} />
                        </td>
                      ) : null}
                      <td>
                        <div className="dp-project">
                          <button type="button" className="dp-project-name" onClick={() => setOpenAt({ id: p.id, tab: "live" })}>
                            {p.name}
                          </button>
                          <span className="dp-sub dk-clip" title={p.working_dir}>
                            {p.workload_missing ? "Container removed" : p.workload_name} · {p.working_dir}
                          </span>
                        </div>
                      </td>
                      <td>
                        <UpdateState p={p} />
                      </td>
                      <td>{d ? <DeployProgress d={d} estimate={p.estimate_sec} /> : <LastResult p={p} />}</td>
                      <td className="cell-actions">
                        <div className="bk-actions">
                          {canDeploy && !d ? (
                            <button className="btn btn-sm btn-primary" type="button" disabled={busy === p.id || p.workload_missing} onClick={() => void deployOne(p)}>
                              Deploy
                            </button>
                          ) : null}
                          <ActionMenu label={`${p.name} actions`} items={menuItems(projectItems(p))} />
                        </div>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
      </section>

      {list?.discovered?.length ? (
        <FoundInContainers
          found={list.discovered}
          canAdd={canConfigure}
          defaultOpen={projects.length === 0}
          onAdd={(f) => setAdding({ workload_id: f.workload_id, working_dir: f.working_dir, name: f.name })}
        />
      ) : null}

      <ContextMenu menu={ctx.menu} onClose={ctx.close} />

      {open && list ? (
        <ProjectDrawer
          key={`${open.id}:${openAt?.tab}`}
          project={open}
          list={list}
          containers={containers}
          initialTab={openAt?.tab}
          onChanged={() => void load()}
          onClose={() => setOpenAt(null)}
        />
      ) : null}
      {adding && list ? (
        <Dialog open wide title="Add project" onClose={() => setAdding(null)}>
          <ProjectEditor
            seed={adding}
            containers={containers}
            settings={list.settings}
            extra={(cfg, set) => <NotifySection cfg={cfg} set={set} />}
            onSaved={(p) => {
              setAdding(null);
              setNotice(`${p.name} added.`);
              void load();
            }}
            onCancel={() => setAdding(null)}
          />
        </Dialog>
      ) : null}
      {settingsOpen && list ? (
        <SettingsDialog
          settings={list.settings}
          onClose={() => setSettingsOpen(false)}
          onSaved={() => {
            setSettingsOpen(false);
            void load();
          }}
        />
      ) : null}
    </div>
  );
}
