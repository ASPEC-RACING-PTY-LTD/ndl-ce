import { confirmAction } from "../confirm";
import { memo, useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  ProxyError,
  deleteProxySite,
  getProxies,
  proxyService,
  upsertProxySite,
  type ProxyInstance,
  type ProxyKind,
  type ProxyMachine,
  type ProxySite,
} from "../api/client";
import { ActionMenu } from "../components/ActionMenu";
import type { ContextItem } from "../components/ContextMenu";
import { EmptyState, LoadingState } from "../components/EmptyState";
import { ErrorNotice } from "../components/ErrorNotice";
import { Icon } from "../components/Icon";
import { Link } from "../components/Link";
import { PageHeader } from "../components/PageHeader";
import { StatusBadge } from "../components/StatusBadge";
import { usePref } from "../prefs";
import { usePoll } from "../query";
import { navigate } from "../router";
import { hasGrant } from "../rbac";
import { useSession } from "../session";
import { storageGet, storageSet } from "../storage";
import { Dialog } from "../ui/Dialog";
import { ColumnEditor, SortHeader, useTablePrefs, type TableColumn } from "../ui/tablePrefs";
import { nodeLabel } from "../nodeNames";

// Caddy and Nginx pages. Sites No-dal adds live in their own files (Caddy:
// /etc/caddy/ndl.d, Nginx: /etc/nginx/conf.d/ndl-*.conf); other sites are
// shown read-only. Every change is checked by the proxy before it loads.

const TITLE: Record<ProxyKind, string> = { caddy: "Caddy", nginx: "Nginx" };

type Row = ProxySite & { key: string; machine_id: string; machine_name: string };

const TLS_LABEL: Record<string, string> = {
  auto: "Automatic HTTPS",
  internal: "Internal CA",
  http: "HTTP only",
  cert: "Certificate files",
};

const COLUMNS: TableColumn<Row>[] = [
  { id: "domains", label: "Domains", required: true, sortValue: (r) => r.domains.join(" ") },
  { id: "upstream", label: "Goes to", sortValue: (r) => r.upstream ?? r.redirect ?? "" },
  { id: "tls", label: "TLS", sortValue: (r) => r.tls ?? "" },
  { id: "source", label: "Source", sortValue: (r) => (r.managed ? "" : `${r.file}:${r.line}`) },
  { id: "workload", label: "Workload", sortValue: (r) => r.workload?.name ?? "￿" },
  { id: "machine", label: "Machine", defaultHidden: true, sortValue: (r) => nodeLabel(r.machine_id, r.machine_name) },
];

const errText = (err: unknown, fallback: string) => (err instanceof Error ? err.message : fallback);
const actionItems = (items: ContextItem[]) => items.filter((i): i is Exclude<ContextItem, "sep"> => i !== "sep" && !i.disabled);

function siteURL(r: ProxySite): string | null {
  const d = r.domains.find((x) => !x.includes("*") && !x.startsWith("default ("));
  if (!d) {
    return null;
  }
  if (d.includes("://")) {
    return d;
  }
  return (r.tls === "http" ? "http://" : "https://") + d;
}

const SiteRow = memo(function SiteRow({ r, cols, itemsFor }: { r: Row; cols: string[]; itemsFor: (r: Row) => ContextItem[] }) {
  const url = siteURL(r);
  return (
    <tr className="cf-route-row">
      {cols.map((c) => {
        switch (c) {
          case "domains":
            return (
              <td key={c}>
                {url ? (
                  <a href={url} target="_blank" rel="noopener noreferrer">
                    {r.domains[0]}
                  </a>
                ) : (
                  r.domains[0]
                )}
                {r.domains.length > 1 ? <span className="meta"> +{r.domains.length - 1}: {r.domains.slice(1).join(", ")}</span> : null}
              </td>
            );
          case "upstream":
            return <td key={c}>{r.upstream ? <code>{r.upstream}</code> : r.redirect ? <span className="meta">redirects to {r.redirect}</span> : <span className="meta">-</span>}</td>;
          case "tls":
            return <td key={c}>{TLS_LABEL[r.tls ?? ""] ?? r.tls ?? "-"}</td>;
          case "source":
            return (
              <td key={c}>
                {r.managed ? (
                  <span className="tag-inline">No-dal</span>
                ) : (
                  <span className="meta" title="Not written by No-dal, so it is shown but not changed here">
                    {r.file}
                    {r.line ? `:${r.line}` : ""}
                  </span>
                )}
              </td>
            );
          case "workload":
            return <td key={c}>{r.workload ? <Link href={`/workloads/${r.workload.id}`}>{r.workload.name}</Link> : <span className="meta">-</span>}</td>;
          case "machine":
            return <td key={c}>{nodeLabel(r.machine_id, r.machine_name)}</td>;
        }
        return null;
      })}
      <td className="cell-actions">
        <ActionMenu label={`${r.domains[0]} actions`} items={actionItems(itemsFor(r))} />
      </td>
    </tr>
  );
});

/** ProxyPage lists Caddy or Nginx on this node and its system containers, with their sites. */
export function ProxyPage({ kind }: { kind: ProxyKind }) {
  const title = TITLE[kind];
  const session = useSession();
  const user = session.status === "ready" ? session.user : null;
  const canManage = hasGrant(user, "network.apply");
  const cacheKey = `ndl-proxy-${kind}`;
  const [data, setData] = useState<{ instances: ProxyInstance[]; machines: ProxyMachine[] } | null>(() => {
    try {
      const raw = storageGet(cacheKey);
      return raw ? (JSON.parse(raw) as { instances: ProxyInstance[]; machines: ProxyMachine[] }) : null;
    } catch {
      return null;
    }
  });
  const [loading, setLoading] = useState(false);
  const [query, setQuery] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [output, setOutput] = useState<{ title: string; text: string } | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [editing, setEditing] = useState<{ site: ProxySite | null; machine: string } | null>(null);
  const [installing, setInstalling] = useState(false);
  const [collapsedList, setCollapsedList] = usePref<string[]>(`proxy.${kind}.collapsed`, []);
  const table = useTablePrefs<Row>(`proxy.${kind}`, COLUMNS, null);
  const grouped = table.option("group", true);

  const reload = useCallback(
    async (refresh = false) => {
      setLoading(true);
      try {
        const next = await getProxies(kind, refresh);
        setData(next);
        storageSet(cacheKey, JSON.stringify(next));
        setError(null);
      } catch (err) {
        setError(errText(err, `${title} could not be read`));
        setData((cur) => cur ?? { instances: [], machines: [] });
      } finally {
        setLoading(false);
      }
    },
    [kind, title, cacheKey],
  );

  useEffect(() => {
    void reload();
  }, [reload]);
  usePoll(() => reload(false), 60000);

  async function act(label: string, fn: () => Promise<{ message?: string; output?: string } | void>) {
    setBusy(label);
    setError(null);
    try {
      const res = await fn();
      if (res?.message) {
        setNotice(res.message.charAt(0).toUpperCase() + res.message.slice(1) + ".");
      }
      await reload(true);
    } catch (err) {
      setError(errText(err, `${label} failed`));
      if (err instanceof ProxyError && err.output) {
        setOutput({ title: `${title} said`, text: err.output });
      }
    } finally {
      setBusy(null);
    }
  }

  const instances = useMemo(() => data?.instances ?? [], [data]);
  const machines = useMemo(() => data?.machines ?? [], [data]);
  const latest = useRef({ canManage, instances });
  latest.current = { canManage, instances };

  const rows = useMemo(() => {
    const out: Row[] = [];
    for (const in_ of instances) {
      in_.sites.forEach((s, i) => out.push({ ...s, key: `${in_.machine_id}/${s.name || `${s.file}:${s.line}:${i}`}`, machine_id: in_.machine_id, machine_name: nodeLabel(in_.machine_id, in_.machine_name) }));
    }
    return out;
  }, [instances]);
  const q = query.trim().toLowerCase();
  const shown = useMemo(
    () => table.sortRows(rows.filter((r) => !q || [r.domains.join(" "), r.upstream, r.redirect, r.file, nodeLabel(r.machine_id, r.machine_name), r.workload?.name].some((f) => f?.toLowerCase().includes(q)))),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [rows, q, table.sort?.id, table.sort?.dir],
  );

  const siteItems = useCallback(
    (r: Row): ContextItem[] => {
      const items: ContextItem[] = [];
      const url = siteURL(r);
      if (r.managed && latest.current.canManage) {
        items.push({ label: "Edit", onClick: () => setEditing({ site: r, machine: r.machine_id }) });
      }
      if (url) {
        items.push({ label: "Open site", onClick: () => window.open(url, "_blank", "noopener") });
      }
      items.push({ label: "Copy domains", onClick: () => void navigator.clipboard?.writeText(r.domains.join(" ")) });
      if (!r.managed) {
        items.push({ label: `Defined in ${r.file}${r.line ? `:${r.line}` : ""}`, disabled: true, onClick: () => undefined });
      }
      if (r.managed && latest.current.canManage) {
        items.push("sep", {
          label: "Delete",
          danger: true,
          onClick: () => {
            if (confirmAction(`Remove ${r.domains.join(", ")} from ${title} on ${nodeLabel(r.machine_id, r.machine_name)}? It stops answering after the reload.`)) {
              void act("Removing site", () => deleteProxySite(kind, r.machine_id, r.name ?? ""));
            }
          },
        });
      }
      return items;
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [kind, title],
  );

  const instanceItems = useCallback(
    (in_: ProxyInstance): ContextItem[] => {
      const items: ContextItem[] = [];
      const svc = (action: "reload" | "restart" | "start" | "stop" | "validate", label: string) => ({
        label,
        onClick: () =>
          void act(label, async () => {
            const res = await proxyService(kind, in_.machine_id, action);
            if (action === "validate") {
              setOutput({ title: `${title} on ${nodeLabel(in_.machine_id, in_.machine_name)}`, text: res.output || res.message || "The configuration is valid." });
            }
            return res;
          }),
      });
      if (latest.current.canManage) {
        items.push({ label: "Add site here", onClick: () => setEditing({ site: null, machine: in_.machine_id }) });
        items.push("sep", svc("validate", "Check configuration"), svc("reload", "Reload"), svc("restart", "Restart"));
        items.push(in_.active ? svc("stop", "Stop") : svc("start", "Start"));
      }
      if (in_.workload) {
        items.push("sep", { label: `Open ${in_.workload.name}`, onClick: () => navigate(`/workloads/${in_.workload?.id}`) });
      }
      return items;
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [kind, title],
  );
  const collapsed = new Set(collapsedList);

  const installable = machines.filter((m) => !m.installed && m.can_apt);
  const pageItems: ContextItem[] = [];
  if (canManage) {
    pageItems.push({ label: "Add site", disabled: instances.length === 0, onClick: () => setEditing({ site: null, machine: instances[0]?.machine_id ?? "host" }) });
    pageItems.push({ label: `Install ${title}`, disabled: installable.length === 0, onClick: () => setInstalling(true) });
  }
  pageItems.push({ label: "Refresh", onClick: () => void reload(true) });

  if (!data) {
    return (
      <section className="page">
        <LoadingState label={`Looking for ${title}`} />
      </section>
    );
  }

  const cols = table.visible.map((c) => c.id);
  const span = cols.length + 1;
  const running = instances.filter((i) => i.active).length;
  const managed = rows.filter((r) => r.managed).length;

  return (
    <section className="page page-wide" aria-labelledby={`${kind}-heading`}>
      <PageHeader
        id={`${kind}-heading`}
        title={title}
        kicker={`${title} on this node and in system containers. Sites No-dal adds get their own file; other sites are shown and left alone. Every change is checked by ${title} before it loads, and put back if the check fails.`}
        actions={
          <div className="btn-row is-flush">
            <button className="btn btn-secondary" type="button" disabled={loading} onClick={() => void reload(true)}>
              {loading ? "Refreshing" : "Refresh"}
            </button>
          </div>
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
          <span className="label">Running</span>
          <span className="value">
            <StatusBadge status={running === instances.length ? "healthy" : "degraded"} /> {running}/{instances.length}
          </span>
          <span className="meta">{instances.length === 1 ? "1 machine" : `${instances.length} machines`}</span>
        </div>
        <div className="status-tile">
          <span className="label">Sites</span>
          <span className="value">{rows.length}</span>
          <span className="meta">{managed} added in No-dal</span>
        </div>
      </div>
      <div className="toolbar">
        <div className="toolbar-group">
          <label className="search-field">
            <input className="field-input" type="search" value={query} placeholder="Search" aria-label={`Search ${title} sites`} onChange={(e) => setQuery(e.target.value)} />
          </label>
          {canManage ? (
            <>
              <button
                className="btn btn-sm btn-primary"
                type="button"
                disabled={instances.length === 0}
                onClick={() => setEditing({ site: null, machine: instances[0]?.machine_id ?? "host" })}
              >
                Add site
              </button>
              <button className="btn btn-sm btn-secondary" type="button" disabled={installable.length === 0} onClick={() => setInstalling(true)}>
                Install {title}
              </button>
            </>
          ) : null}
        </div>
      </div>
      {instances.length === 0 ? (
        <EmptyState title={`${title} was not found`}>
          {title} is not installed on this node or in any running system container.{" "}
          {canManage && installable.length ? (
            <button className="btn btn-sm btn-primary" type="button" onClick={() => setInstalling(true)}>
              Install {title}
            </button>
          ) : null}
        </EmptyState>
      ) : (
        <div className="table-wrap">
          <table aria-label={`${title} sites`} className={grouped ? "cf-grouped" : undefined}>
            <thead>
              <tr>
                {table.visible.map((c) => (
                  <SortHeader key={c.id} column={c} prefs={table} />
                ))}
                <th className="cell-actions">
                  <ColumnEditor
                    prefs={table}
                    label={`Edit ${title} columns`}
                    extra={
                      <label className="check-row">
                        <input type="checkbox" checked={grouped} onChange={(e) => table.setOption("group", e.target.checked)} />
                        <span>Group by machine</span>
                      </label>
                    }
                  />
                </th>
              </tr>
            </thead>
            {grouped ? (
              instances.map((in_) => {
                const list = shown.filter((r) => r.machine_id === in_.machine_id);
                if (q && list.length === 0 && !nodeLabel(in_.machine_id, in_.machine_name).toLowerCase().includes(q)) {
                  return null;
                }
                const isCollapsed = collapsed.has(in_.machine_id);
                return (
                  <tbody key={in_.machine_id}>
                    <tr className={"cf-group" + (!in_.active ? " is-issue" : "")}>
                      <td colSpan={span}>
                        <div className="cf-group-head">
                          <button
                            type="button"
                            className="cf-group-toggle"
                            aria-expanded={!isCollapsed}
                            aria-label={`${isCollapsed ? "Show" : "Hide"} sites on ${nodeLabel(in_.machine_id, in_.machine_name)}`}
                            onClick={() => setCollapsedList((cur) => (cur.includes(in_.machine_id) ? cur.filter((x) => x !== in_.machine_id) : [...cur, in_.machine_id]))}
                          >
                            <Icon name="chevron-down" size={14} className={isCollapsed ? "is-collapsed" : undefined} />
                          </button>
                          <strong className="cf-group-name">{nodeLabel(in_.machine_id, in_.machine_name)}</strong>
                          <StatusBadge status={in_.active ? "running" : in_.state === "failed" ? "critical" : "stopped"} label={in_.state} />
                          <span className="meta">
                            {in_.sites.length} site{in_.sites.length === 1 ? "" : "s"}
                            {in_.version ? ` · ${in_.version}` : ""}
                          </span>
                          <code className="meta">{in_.config_path}</code>
                          {(in_.notes ?? []).map((n) => (
                            <span key={n} className="tunnel-reason">
                              {n}
                            </span>
                          ))}
                          {(in_.errors ?? []).slice(0, 2).map((e) => (
                            <span key={e} className="tunnel-reason" title="Recent warning from the service log">
                              {e}
                            </span>
                          ))}
                          <span className="cf-group-actions">
                            <ActionMenu label={`${nodeLabel(in_.machine_id, in_.machine_name)} actions`} items={actionItems(instanceItems(in_))} />
                          </span>
                        </div>
                      </td>
                    </tr>
                    {isCollapsed
                      ? null
                      : list.length === 0
                        ? (
                            <tr className="cf-route-row is-muted">
                              <td colSpan={span}>
                                <span className="meta">No sites.</span>
                              </td>
                            </tr>
                          )
                        : list.map((r) => <SiteRow key={r.key} r={r} cols={cols} itemsFor={siteItems} />)}
                  </tbody>
                );
              })
            ) : (
              <tbody>
                {shown.map((r) => (
                  <SiteRow key={r.key} r={r} cols={cols} itemsFor={siteItems} />
                ))}
              </tbody>
            )}
          </table>
        </div>
      )}
      <SiteDialog
        kind={kind}
        state={editing}
        instances={instances}
        onClose={() => setEditing(null)}
        onSaved={(msg) => {
          setEditing(null);
          setNotice(msg);
          void reload(true);
        }}
      />
      <InstallDialog
        kind={kind}
        open={installing}
        machines={installable}
        onClose={() => setInstalling(false)}
        onDone={(msg) => {
          setInstalling(false);
          setNotice(msg);
          void reload(true);
        }}
      />
      <Dialog open={output != null} title={output?.title ?? ""} wide onClose={() => setOutput(null)}>
        <pre className="job-log-pre">{output?.text}</pre>
      </Dialog>
    </section>
  );
}

function SiteDialog({
  kind,
  state,
  instances,
  onClose,
  onSaved,
}: {
  kind: ProxyKind;
  state: { site: ProxySite | null; machine: string } | null;
  instances: ProxyInstance[];
  onClose: () => void;
  onSaved: (msg: string) => void;
}) {
  const site = state?.site ?? null;
  const [machine, setMachine] = useState("");
  const [domains, setDomains] = useState("");
  const [upstream, setUpstream] = useState("");
  const [tls, setTls] = useState("");
  const [cert, setCert] = useState("");
  const [key, setKey] = useState("");
  const [websocket, setWebsocket] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [output, setOutput] = useState("");

  useEffect(() => {
    if (!state) {
      return;
    }
    setMachine(state.machine);
    setDomains(site?.domains.map((d) => d.replace(/^https?:\/\//, "")).join(", ") ?? "");
    setUpstream(site?.upstream ?? "");
    setTls(site?.tls ?? (kind === "caddy" ? "auto" : "http"));
    setCert(site?.cert_path ?? "");
    setKey(site?.key_path ?? "");
    setWebsocket(site?.websocket ?? true);
    setError(null);
    setOutput("");
  }, [state, site, kind]);

  async function save() {
    setBusy(true);
    setError(null);
    setOutput("");
    try {
      const res = await upsertProxySite(kind, {
        machine_id: machine,
        previous: site?.name,
        site: {
          name: site?.name,
          domains: domains.split(/[\s,]+/).filter(Boolean),
          upstream: upstream.trim(),
          tls,
          cert_path: tls === "cert" ? cert.trim() : undefined,
          key_path: tls === "cert" ? key.trim() : undefined,
          websocket: kind === "nginx" ? websocket : undefined,
        },
      });
      onSaved(res.message ? res.message.charAt(0).toUpperCase() + res.message.slice(1) + "." : "Saved.");
    } catch (err) {
      setError(errText(err, "Could not save the site"));
      if (err instanceof ProxyError) {
        setOutput(err.output);
      }
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog
      open={state != null}
      title={site ? `Edit ${site.domains[0]}` : `Add a ${TITLE[kind]} site`}
      onClose={onClose}
      footer={
        <div className="btn-row">
          <button className="btn btn-ghost" type="button" onClick={onClose}>
            Cancel
          </button>
          <button className="btn btn-primary" type="button" disabled={busy || !domains.trim() || !upstream.trim() || !machine} onClick={() => void save()}>
            {busy ? "Checking and saving" : "Save"}
          </button>
        </div>
      }
    >
      {error ? <ErrorNotice error={error} /> : null}
      {output ? <pre className="job-log-pre">{output}</pre> : null}
      <div className="field">
        <label className="field-label" htmlFor="px-machine">
          Machine
        </label>
        <select id="px-machine" className="field-input" value={machine} disabled={Boolean(site)} onChange={(e) => setMachine(e.target.value)}>
          {instances.map((i) => (
            <option key={i.machine_id} value={i.machine_id}>
              {nodeLabel(i.machine_id, i.machine_name)}
            </option>
          ))}
        </select>
      </div>
      <div className="field">
        <label className="field-label" htmlFor="px-domains">
          Domains
        </label>
        <input id="px-domains" className="field-input" value={domains} placeholder="app.example.com, www.app.example.com" onChange={(e) => setDomains(e.target.value)} />
        <p className="field-hint">One or more names, separated by commas or spaces. Point their DNS at this machine.</p>
      </div>
      <div className="field">
        <label className="field-label" htmlFor="px-upstream">
          Goes to
        </label>
        <input id="px-upstream" className="field-input" value={upstream} placeholder="http://10.0.3.12:8080" onChange={(e) => setUpstream(e.target.value)} />
        <p className="field-hint">Where requests are sent, as seen from this machine: an address and port, for example a container's IP.</p>
      </div>
      <div className="field">
        <label className="field-label" htmlFor="px-tls">
          TLS
        </label>
        <select id="px-tls" className="field-input" value={tls} onChange={(e) => setTls(e.target.value)}>
          {kind === "caddy" ? (
            <>
              <option value="auto">Automatic HTTPS (Let's Encrypt or ZeroSSL)</option>
              <option value="internal">Caddy's internal CA (for private names)</option>
              <option value="http">HTTP only</option>
            </>
          ) : (
            <>
              <option value="http">HTTP only (port 80)</option>
              <option value="cert">HTTPS with certificate files</option>
            </>
          )}
        </select>
      </div>
      {kind === "nginx" && tls === "cert" ? (
        <>
          <div className="field">
            <label className="field-label" htmlFor="px-cert">
              Certificate file
            </label>
            <input id="px-cert" className="field-input" value={cert} placeholder="/etc/letsencrypt/live/app.example.com/fullchain.pem" onChange={(e) => setCert(e.target.value)} />
          </div>
          <div className="field">
            <label className="field-label" htmlFor="px-key">
              Key file
            </label>
            <input id="px-key" className="field-input" value={key} placeholder="/etc/letsencrypt/live/app.example.com/privkey.pem" onChange={(e) => setKey(e.target.value)} />
          </div>
        </>
      ) : null}
      {kind === "nginx" ? (
        <label className="check-row">
          <input type="checkbox" checked={websocket} onChange={(e) => setWebsocket(e.target.checked)} />
          <span>Pass WebSocket connections through</span>
        </label>
      ) : null}
    </Dialog>
  );
}

function InstallDialog({ kind, open, machines, onClose, onDone }: { kind: ProxyKind; open: boolean; machines: ProxyMachine[]; onClose: () => void; onDone: (msg: string) => void }) {
  const [machine, setMachine] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    if (open) {
      setMachine(machines[0]?.id ?? "");
      setError(null);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);
  return (
    <Dialog
      open={open}
      title={`Install ${TITLE[kind]}`}
      onClose={onClose}
      footer={
        <div className="btn-row">
          <button className="btn btn-ghost" type="button" onClick={onClose}>
            Cancel
          </button>
          <button
            className="btn btn-primary"
            type="button"
            disabled={busy || !machine}
            onClick={() => {
              setBusy(true);
              setError(null);
              proxyService(kind, machine, "install")
                .then((res) => onDone(res.message ? res.message.charAt(0).toUpperCase() + res.message.slice(1) + "." : "Installed."))
                .catch((err) => setError(errText(err, "Install failed")))
                .finally(() => setBusy(false));
            }}
          >
            {busy ? "Installing" : "Install"}
          </button>
        </div>
      }
    >
      {error ? <ErrorNotice error={error} /> : null}
      <p className="field-hint">
        {TITLE[kind]} is installed from the machine's own package sources with apt and started. It listens on ports 80 and 443 there, so nothing else on
        that machine may use them.
      </p>
      <div className="field">
        <label className="field-label" htmlFor="px-install-machine">
          Machine
        </label>
        <select id="px-install-machine" className="field-input" value={machine} onChange={(e) => setMachine(e.target.value)}>
          {machines.map((m) => (
            <option key={m.id} value={m.id}>
              {m.name}
            </option>
          ))}
        </select>
      </div>
    </Dialog>
  );
}
