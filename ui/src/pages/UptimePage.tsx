import { useEffect, useMemo, useState, type MouseEvent as ReactMouseEvent } from "react";
import {
  checkAllUptime,
  checkUptimeMonitor,
  deleteUptimeMonitor,
  getDocker,
  importUptime,
  listUptimeMonitors,
  saveUptimeMonitor,
  type OpsJob,
  type UptimeImportResult,
  type UptimeMonitor,
  type UptimeMonitorIn,
} from "../api/client";
import { ActionMenu } from "../components/ActionMenu";
import { ContextMenu, useContextMenu, type ContextItem } from "../components/ContextMenu";
import { EmptyState, LoadingState } from "../components/EmptyState";
import { ErrorNotice } from "../components/ErrorNotice";
import { JobLog } from "../components/JobLog";
import { JsonScriptModal } from "../components/JsonScriptModal";
import { PageHeader } from "../components/PageHeader";
import { StatusBadge } from "../components/StatusBadge";
import type { DockerContainer, DockerMachine } from "../generated/openapi";
import { formatWhen } from "../format";
import { usePoll } from "../query";
import { canMutate } from "../rbac";
import { useSession } from "../session";
import { Dialog } from "../ui/Dialog";

const UPTIME_TEMPLATE = `{
  "monitors": [
    {
      "name": "shop",
      "target": "https://shop.example.com/health",
      "expect_status": "200-399",
      "keyword": "",
      "containers": ["apps/shop-web-1", "apps/shop-db-1"],
      "auto_restart": true,
      "enabled": true
    },
    {
      "name": "database port",
      "target": "203.0.113.10:5432",
      "containers": [{ "machine": "apps", "name": "shop-db-1" }]
    }
  ],
  "delete": []
}`;

function stateTone(state: string, enabled: boolean): string {
  if (!enabled) {
    return "unknown";
  }
  if (state === "up") {
    return "healthy";
  }
  if (state === "down") {
    return "critical";
  }
  return "unknown";
}

function stateLabel(m: UptimeMonitor): string {
  if (!m.enabled) {
    return "Paused";
  }
  if (m.state === "up") {
    return "Up";
  }
  if (m.state === "down") {
    return m.failures > 1 ? `Down (${m.failures} checks)` : "Down";
  }
  return "Waiting for first check";
}

function targetHref(target: string): string | null {
  if (/^https?:\/\//.test(target)) {
    return target;
  }
  if (target.includes("/") && !target.startsWith("tcp://")) {
    return `https://${target}`;
  }
  return null;
}

function History({ events }: { events: UptimeMonitor["events"] }) {
  const list = [...(events ?? [])].reverse().slice(-40);
  if (list.length === 0) {
    return null;
  }
  return (
    <div className="uptime-history" aria-label="Recent checks">
      {list.map((e, i) => (
        <span
          key={i}
          className={"uptime-tick" + (e.ok ? " is-up" : " is-down") + (e.action && e.action !== "recovered" ? " is-action" : "")}
          title={`${formatWhen(e.at)}: ${e.ok ? `up, ${e.latency_ms} ms` : e.error || "down"}${e.action ? ` (${e.action})` : ""}`}
        />
      ))}
    </div>
  );
}

type Draft = UptimeMonitorIn & { id?: string };

function emptyDraft(): Draft {
  return { name: "", target: "", expect_status: "", keyword: "", containers: [], auto_restart: true, enabled: true };
}

function MonitorForm({
  draft,
  onChange,
  containers,
  machines,
}: {
  draft: Draft;
  onChange: (d: Draft) => void;
  containers: DockerContainer[];
  machines: DockerMachine[];
}) {
  const [q, setQ] = useState("");
  const [manual, setManual] = useState("");
  const machineName = (id: string) => machines.find((m) => m.id === id)?.name ?? id;
  const picked = (c: DockerContainer) =>
    draft.containers.some((x) => x.name === c.name && (x.machine === c.machine_id || x.machine === machineName(c.machine_id)));
  const shown = containers.filter((c) => !q || `${c.name} ${c.project ?? ""} ${machineName(c.machine_id)}`.toLowerCase().includes(q.toLowerCase()));
  function toggle(c: DockerContainer) {
    const next = picked(c)
      ? draft.containers.filter((x) => !(x.name === c.name && (x.machine === c.machine_id || x.machine === machineName(c.machine_id))))
      : [...draft.containers, { machine: machineName(c.machine_id), name: c.name }];
    onChange({ ...draft, containers: next });
  }
  return (
    <div className="oci-form">
      <div className="oci-grid">
        <div className="field">
          <label className="field-label" htmlFor="up-name">
            Name
          </label>
          <input id="up-name" className="field-input" value={draft.name} onChange={(e) => onChange({ ...draft, name: e.target.value })} />
        </div>
        <div className="field oci-span2">
          <label className="field-label" htmlFor="up-target">
            URL, host/path or IP:port
          </label>
          <input
            id="up-target"
            className="field-input"
            value={draft.target}
            placeholder="https://shop.example.com/health"
            onChange={(e) => onChange({ ...draft, target: e.target.value })}
          />
        </div>
        <div className="field">
          <label className="field-label" htmlFor="up-status">
            Expected status
          </label>
          <input
            id="up-status"
            className="field-input"
            value={draft.expect_status ?? ""}
            placeholder="200-399"
            onChange={(e) => onChange({ ...draft, expect_status: e.target.value })}
          />
        </div>
        <div className="field">
          <label className="field-label" htmlFor="up-keyword">
            Page must contain (optional)
          </label>
          <input id="up-keyword" className="field-input" value={draft.keyword ?? ""} onChange={(e) => onChange({ ...draft, keyword: e.target.value })} />
        </div>
      </div>
      <fieldset className="oci-fieldset">
        <legend>Containers to restart when it fails</legend>
        {draft.containers.length ? (
          <ul className="uptime-chips">
            {draft.containers.map((c) => (
              <li key={`${c.machine}/${c.name}`}>
                {c.machine ? `${c.machine} / ` : ""}
                {c.name}
                <button
                  type="button"
                  className="btn btn-ghost btn-sm"
                  aria-label={`Remove ${c.name}`}
                  onClick={() => onChange({ ...draft, containers: draft.containers.filter((x) => x !== c) })}
                >
                  ×
                </button>
              </li>
            ))}
          </ul>
        ) : (
          <p className="meta">None yet. Without containers the monitor only reports.</p>
        )}
        {containers.length ? (
          <>
            <input className="field-input" aria-label="Search containers" placeholder="Search containers" value={q} onChange={(e) => setQ(e.target.value)} />
            <ul className="uptime-pick">
              {shown.slice(0, 60).map((c) => (
                <li key={c.id}>
                  <label className="check-row">
                    <input type="checkbox" checked={picked(c)} onChange={() => toggle(c)} />
                    <span>
                      {c.name} <span className="meta">{machineName(c.machine_id)}{c.project ? ` · ${c.project}` : ""}</span>
                    </span>
                  </label>
                </li>
              ))}
            </ul>
          </>
        ) : null}
        <div className="oci-row">
          <input
            className="field-input"
            aria-label="Add a container by machine/name"
            placeholder="machine/container"
            value={manual}
            onChange={(e) => setManual(e.target.value)}
          />
          <button
            className="btn btn-sm btn-secondary"
            type="button"
            disabled={!manual.trim()}
            onClick={() => {
              const [m, n] = manual.includes("/") ? manual.split("/", 2) : ["", manual];
              onChange({ ...draft, containers: [...draft.containers, { machine: m.trim(), name: n.trim() }] });
              setManual("");
            }}
          >
            Add
          </button>
        </div>
      </fieldset>
      <label className="check-row">
        <input type="checkbox" checked={draft.auto_restart} onChange={(e) => onChange({ ...draft, auto_restart: e.target.checked })} />
        <span>Restart these containers when the check fails</span>
      </label>
      <label className="check-row">
        <input type="checkbox" checked={draft.enabled} onChange={(e) => onChange({ ...draft, enabled: e.target.checked })} />
        <span>Enabled</span>
      </label>
    </div>
  );
}

function ImportResults({ res }: { res: UptimeImportResult }) {
  if (!res.valid) {
    return <ErrorNotice error={res.error || "The JSON is not valid"} />;
  }
  return (
    <div className={"banner " + (res.dry_run ? "" : "banner-ok")} role="status">
      <strong>{res.dry_run ? "Test results. Nothing was saved." : "Saved."}</strong>
      <ul className="plain-list">
        {(res.results ?? []).map((r) => (
          <li key={r.name + r.change}>
            <strong>{r.name}</strong>: {r.change}
            {r.check ? (
              <>
                {" "}
                <StatusBadge status={r.check.ok ? "healthy" : "critical"} label={r.check.ok ? `up, ${r.check.latency_ms} ms` : "down"} />{" "}
                {r.check.error ? <span className="meta">{r.check.error}</span> : null}
              </>
            ) : null}
            {r.error ? <span className="meta"> {r.error}</span> : null}
          </li>
        ))}
      </ul>
    </div>
  );
}

/** UptimePage watches public URLs and restarts the containers behind them when they fail. */
export function UptimePage() {
  const session = useSession();
  const roles = session.status === "ready" ? session.user?.roles : undefined;
  const mutate = canMutate(roles);
  const ctx = useContextMenu();
  const [items, setItems] = useState<UptimeMonitor[] | null>(null);
  const [containers, setContainers] = useState<DockerContainer[]>([]);
  const [machines, setMachines] = useState<DockerMachine[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [draft, setDraft] = useState<Draft | null>(null);
  const [jsonOpen, setJsonOpen] = useState(false);
  const [job, setJob] = useState<OpsJob | null>(null);

  async function reload() {
    try {
      setItems((await listUptimeMonitors()).items ?? []);
      setError(null);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Unavailable");
      setItems((cur) => cur ?? []);
    }
  }

  useEffect(() => {
    void reload();
    void getDocker()
      .then((inv) => {
        setContainers(inv.containers ?? []);
        setMachines(inv.machines ?? []);
      })
      .catch(() => undefined);
  }, []);
  usePoll(() => reload(), 15000);

  const counts = useMemo(() => {
    const list = items ?? [];
    const up = list.filter((m) => m.enabled && m.state === "up").length;
    const down = list.filter((m) => m.enabled && m.state === "down").length;
    const paused = list.filter((m) => !m.enabled).length;
    const lat = list.filter((m) => m.enabled && m.state === "up" && m.last_latency_ms > 0);
    const avg = lat.length ? Math.round(lat.reduce((n, m) => n + m.last_latency_ms, 0) / lat.length) : 0;
    return { up, down, paused, avg };
  }, [items]);

  async function act(label: string, fn: () => Promise<unknown>, done?: string) {
    setBusy(label);
    setError(null);
    try {
      await fn();
      if (done) {
        setNotice(done);
      }
      await reload();
    } catch (err) {
      setError(err instanceof Error ? err.message : `${label} failed`);
    } finally {
      setBusy(null);
    }
  }

  function edit(m: UptimeMonitor) {
    const name = (id: string) => machines.find((x) => x.id === id)?.name ?? id;
    setDraft({
      id: m.id,
      name: m.name,
      target: m.target,
      expect_status: m.expect_status,
      keyword: m.keyword,
      containers: m.containers.map((c) => ({ machine: name(c.machine_id), name: c.name })),
      auto_restart: m.auto_restart,
      enabled: m.enabled,
    });
  }

  function monitorItems(m: UptimeMonitor): ContextItem[] {
    const items: ContextItem[] = [];
    if (mutate) {
      items.push(
        { label: "Check now", onClick: () => void act(`Checking ${m.name}`, () => checkUptimeMonitor(m.id)) },
        {
          label: "Check and restart now",
          onClick: () => void act(`Recovering ${m.name}`, () => checkUptimeMonitor(m.id, true)),
          disabled: !m.auto_restart || m.containers.length === 0,
        },
        "sep",
        { label: "Edit", onClick: () => edit(m) },
        {
          label: m.enabled ? "Pause" : "Resume",
          onClick: () => {
            const name = (id: string) => machines.find((x) => x.id === id)?.name ?? id;
            void act(m.enabled ? "Pausing" : "Resuming", () =>
              saveUptimeMonitor(
                {
                  name: m.name,
                  target: m.target,
                  expect_status: m.expect_status,
                  keyword: m.keyword,
                  containers: m.containers.map((c) => ({ machine: name(c.machine_id), name: c.name })),
                  auto_restart: m.auto_restart,
                  enabled: !m.enabled,
                },
                m.id,
              ),
            );
          },
        },
        "sep",
        {
          label: "Delete",
          danger: true,
          onClick: () => {
            if (window.confirm(`Delete the monitor ${m.name} and its history?`)) {
              void act(`Deleting ${m.name}`, () => deleteUptimeMonitor(m.id), `${m.name} deleted.`);
            }
          },
        },
      );
    }
    const href = targetHref(m.target);
    if (href) {
      items.push("sep", { label: "Open site", onClick: () => window.open(href, "_blank", "noopener") });
    }
    items.push({ label: "Copy target", onClick: () => void navigator.clipboard?.writeText(m.target) });
    return items;
  }

  async function saveDraft() {
    if (!draft) {
      return;
    }
    const { id, ...body } = draft;
    await act(id ? "Saving" : "Creating", () => saveUptimeMonitor(body, id), `${draft.name} saved. It is checked within a minute.`);
    setDraft(null);
  }

  if (items === null) {
    return (
      <section className="page">
        <LoadingState label="Loading monitors" />
      </section>
    );
  }

  const sorted = [...items].sort((a, b) => {
    const rank = (m: UptimeMonitor) => (!m.enabled ? 3 : m.state === "down" ? 0 : m.state === "up" ? 2 : 1);
    return rank(a) - rank(b) || a.name.localeCompare(b.name);
  });
  const machineName = (id: string) => machines.find((x) => x.id === id)?.name ?? id;

  return (
    <section className="page page-wide" aria-labelledby="uptime-heading">
      <PageHeader
        id="uptime-heading"
        title="Uptime"
        kicker="Each site is checked from this server the way a visitor would reach it. A failing site gets its containers restarted (and any stopped tunnel started), then is checked every minute until it works. Working sites are checked every five minutes."
        actions={
          mutate ? (
            <div className="btn-row is-flush">
              <button className="btn btn-primary" type="button" onClick={() => setDraft(emptyDraft())}>
                New monitor
              </button>
              <button
                className="btn btn-secondary"
                type="button"
                disabled={items.length === 0}
                onClick={() =>
                  void checkAllUptime()
                    .then(setJob)
                    .catch((err) => setError(err instanceof Error ? err.message : "Could not start"))
                }
              >
                Check all now
              </button>
              <button className="btn btn-secondary" type="button" onClick={() => setJsonOpen(true)}>
                JSON
              </button>
            </div>
          ) : null
        }
      />
      {error ? <ErrorNotice error={error} /> : null}
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
          <span className="label">Down</span>
          <span className="value">
            <StatusBadge status={counts.down ? "critical" : "healthy"} /> {counts.down}
          </span>
          <span className="meta">Rechecked every minute</span>
        </div>
        <div className="status-tile">
          <span className="label">Up</span>
          <span className="value">{counts.up}</span>
          <span className="meta">Checked every five minutes</span>
        </div>
        <div className="status-tile">
          <span className="label">Paused</span>
          <span className="value">{counts.paused}</span>
          <span className="meta">Not checked</span>
        </div>
        <div className="status-tile">
          <span className="label">Response</span>
          <span className="value">{counts.avg ? `${counts.avg} ms` : "n/a"}</span>
          <span className="meta">Average of working sites</span>
        </div>
      </div>
      {job ? <JobLog job={job} onDone={() => void reload()} onClose={() => setJob(null)} /> : null}
      {items.length === 0 ? (
        <EmptyState title="No monitors yet">
          Add a site and the containers that serve it. If the site stops answering, those containers are restarted automatically.
        </EmptyState>
      ) : (
        <div className="docker-grid">
          {sorted.map((m) => {
            const href = targetHref(m.target);
            return (
              <article
                key={m.id}
                className={"docker-card" + (m.enabled && m.state === "down" ? " is-issue" : "") + (!m.enabled ? " is-ignored" : "")}
                aria-label={`Monitor ${m.name}`}
                onContextMenu={(e: ReactMouseEvent) => ctx.openAt(e, m.name, monitorItems(m))}
              >
                <header className="docker-card-head">
                  <strong className="docker-head-name" title={m.name}>
                    {m.name}
                  </strong>
                  <StatusBadge status={stateTone(m.state, m.enabled)} label={stateLabel(m)} />
                  <ActionMenu label={`${m.name} actions`} items={monitorItems(m).filter((i): i is Exclude<ContextItem, "sep"> => i !== "sep" && !i.disabled)} />
                </header>
                <p className="meta docker-card-meta" title={m.target}>
                  {href ? (
                    <a href={href} target="_blank" rel="noopener noreferrer">
                      {m.target}
                    </a>
                  ) : (
                    m.target
                  )}
                </p>
                <p className="uptime-stats">
                  {m.uptime_percent != null ? <span>{m.uptime_percent}% of recent checks</span> : null}
                  {m.last_check_at ? <span>Checked {formatWhen(m.last_check_at)}</span> : <span>Not checked yet</span>}
                  {m.state === "up" && m.last_latency_ms ? <span>{m.last_latency_ms} ms</span> : null}
                  {m.last_status ? <span>HTTP {m.last_status}</span> : null}
                </p>
                {m.state === "down" && m.last_error ? <p className="tunnel-reason">{m.last_error}</p> : null}
                <History events={m.events} />
                <p className="meta">
                  {m.containers.length
                    ? `${m.auto_restart ? "Restarts" : "Linked to"} ${m.containers.map((c) => `${machineName(c.machine_id)}/${c.name}`).join(", ")}`
                    : "Reports only, no containers linked"}
                  {m.last_restart_at ? ` · last restart ${formatWhen(m.last_restart_at)}` : ""}
                </p>
              </article>
            );
          })}
        </div>
      )}

      <Dialog
        open={draft != null}
        title={draft?.id ? `Edit ${draft.name}` : "New monitor"}
        wide
        onClose={() => setDraft(null)}
        footer={
          <div className="btn-row">
            <button className="btn btn-ghost" type="button" onClick={() => setDraft(null)}>
              Cancel
            </button>
            <button className="btn btn-primary" type="button" disabled={!draft?.name.trim() || !draft?.target.trim() || busy !== null} onClick={() => void saveDraft()}>
              Save
            </button>
          </div>
        }
      >
        {draft ? <MonitorForm draft={draft} onChange={setDraft} containers={containers} machines={machines} /> : null}
      </Dialog>

      <JsonScriptModal
        open={jsonOpen}
        onClose={() => setJsonOpen(false)}
        title="Uptime monitors as JSON"
        kind="uptime"
        template={UPTIME_TEMPLATE}
        runLabel="Save monitors"
        canRun={mutate}
        help={
          <>
            <summary>Format</summary>
            <p className="field-hint">
              <code>monitors</code> are created or updated by name. <code>target</code> is a URL, host/path (checked over HTTPS) or IP:port (a
              TCP connect). <code>containers</code> are <code>"machine/name"</code> strings or <code>{"{ machine, name }"}</code> objects.{" "}
              <code>delete</code> lists monitor names to remove. Test checks every target once and saves nothing.
            </p>
          </>
        }
        onTest={async (value) => <ImportResults res={await importUptime(value, true)} />}
        onRun={async (value) => {
          const res = await importUptime(value, false);
          if (res.valid) {
            void reload();
          }
          return <ImportResults res={res} />;
        }}
      />
      <ContextMenu menu={ctx.menu} onClose={ctx.close} />
    </section>
  );
}
