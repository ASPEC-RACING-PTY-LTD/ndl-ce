import { confirmAction } from "../confirm";
import { useEffect, useMemo, useRef, useState, type MouseEvent as ReactMouseEvent } from "react";
import {
  checkAllUptime,
  checkUptimeMonitor,
  deleteUptimeMonitor,
  getDockerTunnels,
  importUptime,
  saveUptimeMonitor,
  type OpsJob,
  type UptimeImportResult,
  type UptimeMonitor,
  type UptimeMonitorIn,
} from "../api/client";
import { ActionMenu, type ActionItem } from "../components/ActionMenu";
import { ContextMenu, useContextMenu, type ContextItem } from "../components/ContextMenu";
import { EmptyState, LoadingState } from "../components/EmptyState";
import { ErrorNotice } from "../components/ErrorNotice";
import { JobLog } from "../components/JobLog";
import { JsonScriptModal } from "../components/JsonScriptModal";
import { StatusBadge } from "../components/StatusBadge";
import type { DockerContainer, DockerMachine } from "../generated/openapi";
import { discoverMonitors, type UptimeCandidate } from "../uptime/discover";
import { formatWhen } from "../format";
import { Dialog } from "../ui/Dialog";
import { Segmented } from "../ui/Segmented";
import { ColumnEditor, SortHeader, useTablePrefs, type TableColumn } from "../ui/tablePrefs";

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

function stateKey(m: UptimeMonitor): string {
  if (!m.enabled) {
    return "paused";
  }
  return m.state === "up" || m.state === "down" ? m.state : "pending";
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

function ScanDialog({ open, existing, canSave, onClose, onSaved }: {
  open: boolean;
  existing: UptimeMonitor[];
  canSave: boolean;
  onClose: () => void;
  onSaved: () => void;
}) {
  const [found, setFound] = useState<UptimeCandidate[] | null>(null);
  const [picked, setPicked] = useState<Set<string>>(new Set());
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [result, setResult] = useState<UptimeImportResult | null>(null);

  async function scan(refresh: boolean) {
    setBusy(refresh ? "Looking again" : "Looking for sites");
    setError(null);
    setResult(null);
    try {
      const list = discoverMonitors((await getDockerTunnels(refresh)).items ?? [], existing);
      setFound(list);
      setPicked(new Set(list.filter((c) => !c.existing).map((c) => c.hostname)));
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not look for sites");
      setFound((cur) => cur ?? []);
    } finally {
      setBusy(null);
    }
  }

  useEffect(() => {
    if (open) {
      setFound(null);
      void scan(false);
    }
    // existing is read when the dialog opens.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  const chosen = (found ?? []).filter((c) => !c.existing && picked.has(c.hostname));
  const fresh = (found ?? []).filter((c) => !c.existing);

  async function apply(dryRun: boolean) {
    setBusy(dryRun ? "Checking each site once" : "Adding monitors");
    setError(null);
    try {
      const res = await importUptime({ monitors: chosen.map((c) => c.monitor), delete: [] }, dryRun);
      setResult(res);
      if (!dryRun && res.valid) {
        onSaved();
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not save the monitors");
    } finally {
      setBusy(null);
    }
  }

  function toggle(hostname: string) {
    setPicked((cur) => {
      const next = new Set(cur);
      if (next.has(hostname)) {
        next.delete(hostname);
      } else {
        next.add(hostname);
      }
      return next;
    });
  }

  return (
    <Dialog
      open={open}
      title="Find sites"
      wide
      onClose={onClose}
      footer={
        <div className="btn-row">
          <button className="btn btn-ghost" type="button" onClick={onClose}>
            Close
          </button>
          <button className="btn btn-secondary" type="button" disabled={busy !== null} onClick={() => void scan(true)}>
            Look again
          </button>
          {canSave ? (
            <>
              <button className="btn btn-secondary" type="button" disabled={busy !== null || chosen.length === 0} onClick={() => void apply(true)}>
                Test
              </button>
              <button className="btn btn-primary" type="button" disabled={busy !== null || chosen.length === 0} onClick={() => void apply(false)}>
                {chosen.length === 1 ? "Add 1 monitor" : `Add ${chosen.length} monitors`}
              </button>
            </>
          ) : null}
        </div>
      }
    >
      <p className="field-hint">
        Every public hostname routed to a web service in a container becomes a monitor for <code>https://hostname</code>, linked to the
        container serving it. Sites already watched are left alone. SSH, TCP and wildcard routes are skipped.
      </p>
      {busy ? (
        <p className="banner" role="status">
          Working: {busy}
        </p>
      ) : null}
      {error ? <ErrorNotice error={error} /> : null}
      {result ? <ImportResults res={result} /> : null}
      {found === null ? (
        <LoadingState label="Looking for sites" />
      ) : found.length === 0 ? (
        <EmptyState title="No sites found">
          No public hostnames were found. Routes can take a minute to show after a service starts; try Look again.
        </EmptyState>
      ) : (
        <>
          {fresh.length > 0 ? (
            <div className="btn-row is-flush">
              <button className="btn btn-ghost btn-sm" type="button" onClick={() => setPicked(new Set(fresh.map((c) => c.hostname)))}>
                Select all new
              </button>
              <button className="btn btn-ghost btn-sm" type="button" onClick={() => setPicked(new Set())}>
                Select none
              </button>
            </div>
          ) : null}
          <ul className="plain-list" aria-label="Sites found">
            {found.map((c) => (
              <li key={c.hostname}>
                <label className="check-row">
                  <input
                    type="checkbox"
                    disabled={!!c.existing || !canSave}
                    checked={!c.existing && picked.has(c.hostname)}
                    onChange={() => toggle(c.hostname)}
                  />
                  <span>
                    <strong>{c.hostname}</strong>{" "}
                    <span className="meta">
                      {c.existing
                        ? `already watched by ${c.existing}`
                        : c.monitor.containers.length
                          ? `restarts ${c.monitor.containers.map((x) => `${x.machine}/${x.name}`).join(", ")}`
                          : `${c.machines.join(", ")} → ${c.services.join(", ")}, no container matched (reports only)`}
                    </span>
                  </span>
                </label>
              </li>
            ))}
          </ul>
        </>
      )}
    </Dialog>
  );
}

export type SiteFilter = "down" | "up" | "paused" | "all";

const SITE_COLUMNS: TableColumn<UptimeMonitor>[] = [
  { id: "name", label: "Site", required: true, sortValue: (m) => m.name.toLowerCase() },
  { id: "status", label: "Status", sortValue: (m) => statusRank(m) },
  { id: "target", label: "Address", sortValue: (m) => m.target },
  { id: "response", label: "Response", sortValue: (m) => m.last_latency_ms || Number.MAX_SAFE_INTEGER },
  { id: "uptime", label: "Uptime", sortValue: (m) => m.uptime_percent ?? 101 },
  { id: "checked", label: "Checked", sortValue: (m) => -Date.parse(m.last_check_at ?? "1970-01-01") },
  { id: "history", label: "Recent checks" },
  { id: "containers", label: "Restarts", sortValue: (m) => m.containers.map((c) => c.name).join(",") },
];

function statusRank(m: UptimeMonitor): number {
  return !m.enabled ? 3 : m.state === "down" ? 0 : m.state === "up" ? 2 : 1;
}

/** siteCounts counts sites per filter. */
export function siteCounts(list: UptimeMonitor[]): Record<SiteFilter, number> {
  const out: Record<SiteFilter, number> = { down: 0, up: 0, paused: 0, all: list.length };
  for (const m of list) {
    const k = stateKey(m);
    if (k === "down" || k === "up" || k === "paused") {
      out[k] += 1;
    }
  }
  return out;
}

/** monitorDown is true for an enabled site that fails its check. */
export function monitorDown(m: UptimeMonitor): boolean {
  return m.enabled && m.state === "down";
}

/** SiteBadge is a site's state as a status badge. */
export function SiteBadge({ m, withName }: { m: UptimeMonitor; withName?: boolean }) {
  return <StatusBadge status={stateTone(m.state, m.enabled)} label={withName ? `${m.name}: ${stateLabel(m).toLowerCase()}` : stateLabel(m)} />;
}

/**
 * UptimeSites is the Sites part of the Docker page: public URLs checked from
 * this server, and the containers restarted when one fails.
 */
export function UptimeSites({
  monitors,
  machines,
  containers,
  mutate,
  check = mutate,
  reload,
  focus,
}: {
  monitors: UptimeMonitor[] | null;
  machines: DockerMachine[];
  containers: DockerContainer[];
  /** mutate adds, edits and deletes monitors (compute.modify). */
  mutate: boolean;
  /** check runs checks now and restarts on failure (compute.lifecycle). */
  check?: boolean;
  reload: () => Promise<void> | void;
  focus?: boolean;
}) {
  const [filter, setFilter] = useState<SiteFilter>(focus ? "all" : "down");
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [draft, setDraft] = useState<Draft | null>(null);
  const [jsonOpen, setJsonOpen] = useState(false);
  const [scanOpen, setScanOpen] = useState(false);
  const [job, setJob] = useState<OpsJob | null>(null);
  const table = useTablePrefs<UptimeMonitor>("docker.sites", SITE_COLUMNS, null);
  const ctx = useContextMenu();
  const ref = useRef<HTMLElement>(null);

  useEffect(() => {
    if (focus) {
      ref.current?.scrollIntoView?.({ block: "start" });
    }
  }, [focus]);

  const items = useMemo(() => monitors ?? [], [monitors]);
  const counts = siteCounts(items);
  const machineName = (id: string) => machines.find((x) => x.id === id)?.name ?? id;

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

  function toInput(m: UptimeMonitor, enabled = m.enabled): UptimeMonitorIn {
    return {
      name: m.name,
      target: m.target,
      expect_status: m.expect_status,
      keyword: m.keyword,
      containers: m.containers.map((c) => ({ machine: machineName(c.machine_id), name: c.name })),
      auto_restart: m.auto_restart,
      enabled,
    };
  }

  function monitorItems(m: UptimeMonitor): ContextItem[] {
    const out: ContextItem[] = [];
    if (check) {
      out.push(
        { label: "Check now", onClick: () => void act(`Checking ${m.name}`, () => checkUptimeMonitor(m.id)) },
        {
          label: "Check and restart now",
          onClick: () => void act(`Recovering ${m.name}`, () => checkUptimeMonitor(m.id, true)),
          disabled: !m.auto_restart || m.containers.length === 0,
        },
      );
    }
    if (mutate) {
      out.push(
        ...(check ? ["sep" as const] : []),
        { label: "Edit", onClick: () => setDraft({ id: m.id, ...toInput(m) }) },
        { label: m.enabled ? "Pause" : "Resume", onClick: () => void act(m.enabled ? "Pausing" : "Resuming", () => saveUptimeMonitor(toInput(m, !m.enabled), m.id)) },
      );
    }
    const href = targetHref(m.target);
    out.push("sep");
    if (href) {
      out.push({ label: "Open site", onClick: () => window.open(href, "_blank", "noopener") });
    }
    out.push({ label: "Copy address", onClick: () => void navigator.clipboard?.writeText(m.target) });
    if (mutate) {
      out.push("sep", {
        label: "Delete",
        danger: true,
        onClick: () => {
          if (confirmAction(`Delete the monitor ${m.name} and its history?`)) {
            void act(`Deleting ${m.name}`, () => deleteUptimeMonitor(m.id), `${m.name} deleted.`);
          }
        },
      });
    }
    return out;
  }

  async function saveDraft() {
    if (!draft) {
      return;
    }
    const { id, ...body } = draft;
    await act(id ? "Saving" : "Creating", () => saveUptimeMonitor(body, id), `${draft.name} saved. It is checked within a minute.`);
    setDraft(null);
  }

  const shown = table.sort
    ? table.sortRows(items.filter((m) => filter === "all" || stateKey(m) === filter))
    : items.filter((m) => filter === "all" || stateKey(m) === filter).sort((a, b) => statusRank(a) - statusRank(b) || a.name.localeCompare(b.name));

  function cell(id: string, m: UptimeMonitor) {
    const href = targetHref(m.target);
    switch (id) {
      case "name":
        return (
          <td key={id}>
            <strong className="dk-clip" title={m.name}>
              {m.name}
            </strong>
          </td>
        );
      case "status":
        return (
          <td key={id} title={m.state === "down" ? m.last_error : undefined}>
            <SiteBadge m={m} />
            {m.state === "down" && m.last_error ? (
              <span className="dk-clip meta dk-reason" title={m.last_error}>
                {m.last_error}
              </span>
            ) : null}
          </td>
        );
      case "target":
        return (
          <td key={id}>
            {href ? (
              <a className="dk-clip" href={href} target="_blank" rel="noopener noreferrer" title={m.target} onClick={(e) => e.stopPropagation()}>
                {m.target}
              </a>
            ) : (
              <span className="dk-clip" title={m.target}>
                {m.target}
              </span>
            )}
          </td>
        );
      case "response":
        return (
          <td key={id} className="num">
            {m.state === "up" && m.last_latency_ms ? `${m.last_latency_ms} ms` : "-"}
          </td>
        );
      case "uptime":
        return (
          <td key={id} className="num">
            {m.uptime_percent != null ? `${m.uptime_percent}%` : "-"}
          </td>
        );
      case "checked":
        return <td key={id}>{m.last_check_at ? formatWhen(m.last_check_at) : "Not yet"}</td>;
      case "history":
        return (
          <td key={id}>
            <History events={m.events} />
          </td>
        );
      case "containers": {
        const text = m.containers.map((c) => `${machineName(c.machine_id)}/${c.name}`).join(", ");
        return (
          <td key={id}>
            <span className="dk-clip meta" title={text}>
              {m.containers.length ? `${m.auto_restart ? "" : "Linked: "}${text}` : "Reports only"}
            </span>
          </td>
        );
      }
      default:
        return <td key={id} />;
    }
  }

  const tools: ActionItem[] = [
    ...(mutate ? [{ label: "Find sites", onClick: () => setScanOpen(true) }] : []),
    ...(check
      ? [
          {
            label: "Check all now",
            onClick: () =>
              void checkAllUptime()
                .then(setJob)
                .catch((err) => setError(err instanceof Error ? err.message : "Could not start")),
          },
        ]
      : []),
    mutate ? { label: "Edit as JSON", onClick: () => setJsonOpen(true) } : { label: "View as JSON", onClick: () => setJsonOpen(true) },
  ];

  return (
    <section ref={ref} className="dk-section" aria-labelledby="dk-sites-heading">
      <div className="dk-section-head">
        <h2 id="dk-sites-heading">Sites</h2>
        {items.length ? (
          <Segmented<SiteFilter>
            ariaLabel="Show sites"
            value={filter}
            onChange={setFilter}
            options={[
              { id: "down", label: `Down (${counts.down})` },
              { id: "up", label: `Up (${counts.up})` },
              { id: "paused", label: `Paused (${counts.paused})` },
              { id: "all", label: `All (${counts.all})` },
            ]}
          />
        ) : null}
        {mutate ? (
          <button className="btn btn-sm btn-secondary" type="button" onClick={() => setDraft(emptyDraft())}>
            New monitor
          </button>
        ) : null}
        <ActionMenu label="Site tools" items={tools} />
      </div>
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
      {job ? <JobLog job={job} onDone={() => void reload()} onClose={() => setJob(null)} /> : null}
      {monitors === null ? (
        <LoadingState label="Loading sites" />
      ) : items.length === 0 ? (
        <p className="dk-empty muted">
          No sites are watched yet. A watched site that stops answering gets its containers restarted.
          {mutate ? " Use New monitor, or Find sites under Site tools." : ""}
        </p>
      ) : shown.length === 0 ? (
        <p className="dk-empty muted">
          {filter === "down" ? "No sites are down." : "No sites match."}{" "}
          {filter !== "all" ? (
            <button className="btn btn-ghost btn-sm" type="button" onClick={() => setFilter("all")}>
              Show all sites
            </button>
          ) : null}
        </p>
      ) : (
        <div className="table-wrap dk-table-wrap">
          <table className="dk-table" aria-label="Sites">
            <thead>
              <tr>
                {table.visible.map((c) => (
                  <SortHeader key={c.id} column={c} prefs={table} className={c.id === "response" || c.id === "uptime" ? "num" : undefined} />
                ))}
                <th className="cell-actions">
                  <ColumnEditor prefs={table} label="Edit site columns" />
                </th>
              </tr>
            </thead>
            <tbody>
              {shown.map((m) => (
                <tr
                  key={m.id}
                  aria-label={`Site ${m.name}`}
                  className={"dk-row" + (monitorDown(m) ? " is-bad" : "") + (!m.enabled ? " is-muted" : "")}
                  onContextMenu={(e: ReactMouseEvent) => ctx.openAt(e, m.name, monitorItems(m))}
                >
                  {table.visible.map((c) => cell(c.id, m))}
                  <td className="cell-actions">
                    <ActionMenu label={`${m.name} actions`} items={menuItems(monitorItems(m))} />
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <ContextMenu menu={ctx.menu} onClose={ctx.close} />

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
      <ScanDialog open={scanOpen} existing={items} canSave={mutate} onClose={() => setScanOpen(false)} onSaved={() => void reload()} />
    </section>
  );
}

/** menuItems turns context items into "…" menu items: no separators or disabled entries. */
export function menuItems(items: ContextItem[]): ActionItem[] {
  return items.filter((i): i is Exclude<ContextItem, "sep"> => i !== "sep" && !i.disabled);
}
