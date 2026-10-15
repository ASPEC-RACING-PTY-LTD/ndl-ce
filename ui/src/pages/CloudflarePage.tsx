import { useEffect, useMemo, useState, type MouseEvent as ReactMouseEvent } from "react";
import {
  addCfCredential,
  cfConnectorAction,
  createCfTunnel,
  deleteCfCredential,
  deleteCfRoute,
  deleteCfTunnel,
  diagnoseCfRoute,
  getCfInventory,
  listCfCredentials,
  listCfMachines,
  moveCfRoutes,
  previewCfMove,
  renameCfTunnel,
  setCfRouteEnabled,
  syncCfRouteIP,
  updateCfCredential,
  validateCfCredential,
  type CfCheck,
  type CfCredential,
  type CfInventory,
  type CfMachine,
  type CfMovePlan,
  type CfRoute,
  type CfRouteRef,
  type CfTunnel,
  type OpsJob,
} from "../api/client";
import { RouteDialog } from "../cloudflare/RouteDialog";
import { ActionMenu } from "../components/ActionMenu";
import { ContextMenu, useContextMenu, type ContextItem } from "../components/ContextMenu";
import { EmptyState, LoadingState } from "../components/EmptyState";
import { ErrorNotice } from "../components/ErrorNotice";
import { JobLog } from "../components/JobLog";
import { Link } from "../components/Link";
import { PageHeader } from "../components/PageHeader";
import { StatusBadge } from "../components/StatusBadge";
import { formatRelative, formatWhen } from "../format";
import { usePoll } from "../query";
import { hasGrant } from "../rbac";
import { useSession } from "../session";
import { Dialog } from "../ui/Dialog";
import { Segmented } from "../ui/Segmented";

type View = "hostnames" | "tunnels" | "dns" | "accounts";

const ref = (r: CfRoute): CfRouteRef => ({ account_id: r.account_id, tunnel_id: r.tunnel_id, hostname: r.hostname, path: r.path });
const errText = (err: unknown, fallback: string) => (err instanceof Error ? err.message : fallback);

function dnsBadge(r: CfRoute): { status: string; label: string } {
  switch (r.dns?.action) {
    case "ok":
      return { status: "healthy", label: "DNS ok" };
    case "create":
      return { status: "critical", label: "No DNS record" };
    case "proxy":
      return { status: "warning", label: "Not proxied" };
    case "retarget":
      return { status: "warning", label: "Points elsewhere" };
    case "conflict":
      return { status: "critical", label: "DNS conflict" };
  }
  return { status: "unknown", label: r.zone_id ? "DNS unknown" : "No zone" };
}

function tunnelTone(status: string): string {
  return status === "healthy" ? "healthy" : status === "degraded" ? "degraded" : status === "inactive" ? "unknown" : "critical";
}

/** CloudflarePage manages every connected Cloudflare account's tunnels, hostnames and DNS in one place. */
export function CloudflarePage() {
  const session = useSession();
  const user = session.status === "ready" ? session.user : null;
  const canManage = hasGrant(user, "cloudflare.manage");
  const canCreds = hasGrant(user, "cloudflare.credentials");
  const ctx = useContextMenu();
  const [view, setView] = useState<View>("hostnames");
  const [inv, setInv] = useState<CfInventory | null>(null);
  const [creds, setCreds] = useState<CfCredential[]>([]);
  const [machines, setMachines] = useState<CfMachine[]>([]);
  const [connectorsSupported, setConnectorsSupported] = useState(true);
  const [account, setAccount] = useState("");
  const [query, setQuery] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [routeDialog, setRouteDialog] = useState<{ route: CfRoute | null } | null>(null);
  const [diagnose, setDiagnose] = useState<{ route: CfRoute; checks?: CfCheck[]; summary?: string; error?: string } | null>(null);
  const [moving, setMoving] = useState<CfRoute[] | null>(null);
  const [deleting, setDeleting] = useState<CfRoute | null>(null);
  const [tokenDialog, setTokenDialog] = useState<{ cred: CfCredential | null } | null>(null);
  const [tunnelDialog, setTunnelDialog] = useState(false);
  const [deploy, setDeploy] = useState<CfTunnel | null>(null);
  const [tunnelDelete, setTunnelDelete] = useState<CfTunnel | null>(null);
  const [job, setJob] = useState<OpsJob | null>(null);

  async function reload(refresh = false) {
    try {
      const [next, c] = await Promise.all([getCfInventory(refresh), listCfCredentials()]);
      setInv(next);
      setCreds(c.items ?? []);
      setError(null);
    } catch (err) {
      setError(errText(err, "Cloudflare is unavailable"));
      setInv((cur) => cur ?? { fetched_at: "", refreshing: false, accounts: [], tunnels: [], routes: [], dns: [], errors: [] });
    }
  }

  useEffect(() => {
    void reload();
    void listCfMachines()
      .then((m) => {
        setMachines(m.items ?? []);
        setConnectorsSupported(m.supported);
      })
      .catch(() => undefined);
  }, []);
  // Cheap: the server answers from its cache and refreshes Cloudflare itself at most every two minutes.
  usePoll(() => reload(false), 60000);

  async function act(label: string, fn: () => Promise<unknown>, done?: string) {
    setBusy(label);
    setError(null);
    try {
      await fn();
      if (done) {
        setNotice(done);
      }
      await reload(true);
    } catch (err) {
      setError(errText(err, `${label} failed`));
    } finally {
      setBusy(null);
    }
  }

  const accounts = useMemo(() => inv?.accounts ?? [], [inv]);
  const tunnels = useMemo(() => inv?.tunnels ?? [], [inv]);
  const routes = useMemo(() => inv?.routes ?? [], [inv]);
  const q = query.trim().toLowerCase();
  const matches = (...fields: (string | undefined)[]) => !q || fields.some((f) => f?.toLowerCase().includes(q));
  const shownRoutes = routes.filter(
    (r) => (!account || r.account_id === account) && matches(r.hostname, r.path, r.service, r.tunnel_name, r.account_name, r.workload?.name),
  );
  const shownTunnels = tunnels.filter((t) => (!account || t.account_tag === account) && matches(t.name, t.account_name, t.id));
  const shownDNS = (inv?.dns ?? []).filter((d) => (!account || d.account_id === account) && matches(d.record.name, d.zone_name, d.tunnel_name));
  const counts = useMemo(() => {
    const healthy = tunnels.filter((t) => t.status === "healthy").length;
    const dnsIssues = routes.filter((r) => r.enabled && r.dns && r.dns.action !== "ok").length;
    return { healthy, dnsIssues, disabled: routes.filter((r) => !r.enabled).length };
  }, [tunnels, routes]);

  function routeItems(r: CfRoute): ContextItem[] {
    const items: ContextItem[] = [];
    if (canManage && r.editable) {
      items.push({ label: "Edit", onClick: () => setRouteDialog({ route: r }) });
    }
    items.push({ label: "Diagnose", onClick: () => void runDiagnose(r) });
    if (canManage && r.editable) {
      items.push(
        {
          label: r.enabled ? "Switch off" : "Switch on",
          onClick: () =>
            void act(r.enabled ? "Switching off" : "Switching on", () => setCfRouteEnabled(ref(r), !r.enabled), `${r.hostname} switched ${r.enabled ? "off" : "on"}.`),
        },
        { label: "Move to another tunnel", onClick: () => setMoving([r]), disabled: !r.enabled },
      );
      if (r.ip_changed && r.workload) {
        items.push({
          label: `Point at ${r.workload.name}'s new address ${r.workload.current_ip}`,
          onClick: () => void act("Updating the origin", () => syncCfRouteIP(ref(r)), `${r.hostname} now goes to ${r.workload?.current_ip}.`),
        });
      }
    }
    items.push(
      "sep",
      { label: "Open site", onClick: () => window.open(`https://${r.hostname}${r.path ?? ""}`, "_blank", "noopener") },
      { label: "Copy hostname", onClick: () => void navigator.clipboard?.writeText(r.hostname) },
    );
    if (canManage) {
      items.push("sep", { label: "Delete", danger: true, onClick: () => setDeleting(r) });
    }
    return items;
  }

  async function runDiagnose(r: CfRoute) {
    setDiagnose({ route: r });
    try {
      const res = await diagnoseCfRoute(ref(r));
      setDiagnose({ route: r, checks: res.checks, summary: res.summary });
    } catch (err) {
      setDiagnose({ route: r, error: errText(err, "Diagnosis failed") });
    }
  }

  function toggle(key: string) {
    setSelected((cur) => {
      const next = new Set(cur);
      if (next.has(key)) {
        next.delete(key);
      } else {
        next.add(key);
      }
      return next;
    });
  }

  if (!inv) {
    return (
      <section className="page">
        <LoadingState label="Reading Cloudflare" />
      </section>
    );
  }

  const noAccounts = accounts.length === 0;
  const selectedRoutes = shownRoutes.filter((r) => selected.has(r.key));

  return (
    <section className="page page-wide" aria-labelledby="cf-heading">
      <PageHeader
        id="cf-heading"
        title="Cloudflare"
        kicker="Tunnels, public hostnames and DNS from every connected Cloudflare account. Each item shows the account that owns it; tokens stay on this server."
        actions={
          <div className="btn-row is-flush">
            {canManage && !noAccounts ? (
              <>
                <button className="btn btn-primary" type="button" onClick={() => setRouteDialog({ route: null })}>
                  Add hostname
                </button>
                <button className="btn btn-secondary" type="button" onClick={() => setTunnelDialog(true)}>
                  New tunnel
                </button>
              </>
            ) : null}
            {canCreds ? (
              <button className="btn btn-secondary" type="button" onClick={() => setTokenDialog({ cred: null })}>
                Connect account
              </button>
            ) : null}
            <button className="btn btn-secondary" type="button" disabled={busy !== null} onClick={() => void act("Refreshing", () => reload(true))}>
              Refresh
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
      {(inv.errors ?? []).map((e) => (
        <p key={e} className="banner banner-warn" role="status">
          {e}
        </p>
      ))}
      {noAccounts ? (
        <EmptyState title="No Cloudflare account connected">
          Connect an account with a scoped API token. Give it Account: Cloudflare Tunnel Edit, Zone: DNS Edit and Zone: Zone Read for the
          zones you publish, plus Account Settings Read so its accounts are found. Global API keys are not accepted.
          {canCreds ? (
            <>
              {" "}
              <button className="btn btn-sm btn-primary" type="button" onClick={() => setTokenDialog({ cred: null })}>
                Connect account
              </button>
            </>
          ) : null}
        </EmptyState>
      ) : (
        <>
          <div className="status-strip">
            <div className="status-tile">
              <span className="label">Accounts</span>
              <span className="value">{accounts.length}</span>
              <span className="meta">{creds.length} token(s)</span>
            </div>
            <div className="status-tile">
              <span className="label">Tunnels</span>
              <span className="value">
                <StatusBadge status={counts.healthy === tunnels.length ? "healthy" : "degraded"} /> {counts.healthy}/{tunnels.length}
              </span>
              <span className="meta">healthy</span>
            </div>
            <div className="status-tile">
              <span className="label">Hostnames</span>
              <span className="value">{routes.length}</span>
              <span className="meta">{counts.disabled ? `${counts.disabled} switched off` : "all on"}</span>
            </div>
            <div className="status-tile">
              <span className="label">DNS issues</span>
              <span className="value">
                <StatusBadge status={counts.dnsIssues ? "critical" : "healthy"} /> {counts.dnsIssues}
              </span>
              <span className="meta">
                {inv.refreshing ? "refreshing" : inv.fetched_at ? `checked ${formatRelative(inv.fetched_at)}` : ""}
              </span>
            </div>
          </div>
          <div className="toolbar">
            <div className="toolbar-group">
              <Segmented<View>
                ariaLabel="Cloudflare view"
                value={view}
                onChange={setView}
                options={[
                  { id: "hostnames", label: "Hostnames" },
                  { id: "tunnels", label: "Tunnels" },
                  { id: "dns", label: "DNS" },
                  { id: "accounts", label: "Accounts" },
                ]}
              />
              <label className="search-field">
                <input className="field-input" type="search" value={query} placeholder="Search" aria-label="Search Cloudflare" onChange={(e) => setQuery(e.target.value)} />
              </label>
              <select className="field-input" aria-label="Account filter" value={account} onChange={(e) => setAccount(e.target.value)}>
                <option value="">All accounts</option>
                {accounts.map((a) => (
                  <option key={a.id} value={a.id}>
                    {a.name}
                  </option>
                ))}
              </select>
            </div>
            {view === "hostnames" && canManage && selectedRoutes.length > 0 ? (
              <div className="toolbar-group">
                <span className="meta">{selectedRoutes.length} selected</span>
                <button className="btn btn-sm btn-secondary" type="button" onClick={() => setMoving(selectedRoutes)}>
                  Move to tunnel
                </button>
                <button className="btn btn-sm btn-ghost" type="button" onClick={() => setSelected(new Set())}>
                  Clear
                </button>
              </div>
            ) : null}
          </div>
          {job ? <JobLog job={job} onDone={() => void reload(true)} onClose={() => setJob(null)} /> : null}

          {view === "hostnames" ? (
            shownRoutes.length === 0 ? (
              <EmptyState title="No hostnames">{q || account ? "Nothing matches the filter." : "Add a public hostname to route a domain to a service."}</EmptyState>
            ) : (
              <div className="table-wrap">
                <table aria-label="Public hostnames">
                  <thead>
                    <tr>
                      {canManage ? (
                        <th>
                          <input
                            type="checkbox"
                            aria-label="Select all hostnames"
                            checked={shownRoutes.length > 0 && shownRoutes.every((r) => selected.has(r.key))}
                            onChange={(e) => setSelected(e.target.checked ? new Set(shownRoutes.map((r) => r.key)) : new Set())}
                          />
                        </th>
                      ) : null}
                      <th>Hostname</th>
                      <th>Goes to</th>
                      <th>Tunnel</th>
                      <th>Account</th>
                      <th>DNS</th>
                      <th>Workload</th>
                      <th />
                    </tr>
                  </thead>
                  <tbody>
                    {shownRoutes.map((r) => {
                      const dns = dnsBadge(r);
                      return (
                        <tr
                          key={r.key}
                          className={r.enabled ? "" : "is-muted"}
                          onContextMenu={(e: ReactMouseEvent) => ctx.openAt(e, r.hostname, routeItems(r))}
                        >
                          {canManage ? (
                            <td>
                              <input type="checkbox" aria-label={`Select ${r.hostname}`} checked={selected.has(r.key)} onChange={() => toggle(r.key)} />
                            </td>
                          ) : null}
                          <td>
                            <a href={`https://${r.hostname}${r.path ?? ""}`} target="_blank" rel="noopener noreferrer">
                              {r.hostname}
                            </a>
                            {r.path ? <span className="meta"> {r.path}</span> : null}
                            {!r.enabled ? <span className="tag-inline">off</span> : null}
                          </td>
                          <td>
                            <code>{r.service}</code>
                            {r.ip_changed ? (
                              <span className="tag-inline" title={`The workload now has ${r.workload?.current_ip}`}>
                                IP changed
                              </span>
                            ) : null}
                          </td>
                          <td>
                            <StatusBadge status={tunnelTone(r.tunnel_status)} label={r.tunnel_name} />
                            {!r.editable ? <span className="meta"> config file</span> : null}
                          </td>
                          <td>{r.account_name}</td>
                          <td title={r.dns?.reason}>
                            <StatusBadge status={dns.status} label={dns.label} />
                          </td>
                          <td>{r.workload ? <Link href={`/workloads/${r.workload.id}`}>{r.workload.name}</Link> : <span className="meta">-</span>}</td>
                          <td>
                            <ActionMenu
                              label={`${r.hostname} actions`}
                              items={routeItems(r).filter((i): i is Exclude<ContextItem, "sep"> => i !== "sep" && !i.disabled)}
                            />
                          </td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              </div>
            )
          ) : null}

          {view === "tunnels" ? (
            <div className="docker-grid">
              {shownTunnels.map((t) => (
                <article key={t.id} className={"docker-card" + (t.status !== "healthy" ? " is-issue" : "")} aria-label={`Tunnel ${t.name}`}>
                  <header className="docker-card-head">
                    <strong className="docker-head-name">{t.name}</strong>
                    <StatusBadge status={tunnelTone(t.status)} label={t.status} />
                    {canManage ? (
                      <ActionMenu
                        label={`${t.name} actions`}
                        items={[
                          {
                            label: "Rename",
                            onClick: () => {
                              const name = window.prompt("New tunnel name", t.name)?.trim();
                              if (name && name !== t.name) {
                                void act("Renaming", () => renameCfTunnel(t.id, t.account_tag, name), `Renamed to ${name}.`);
                              }
                            },
                          },
                          { label: "Deploy a connector", onClick: () => setDeploy(t) },
                          { label: "Delete tunnel", danger: true, onClick: () => setTunnelDelete(t) },
                        ]}
                      />
                    ) : null}
                  </header>
                  <p className="meta">
                    {t.account_name} · {t.remote_managed ? "managed in Cloudflare" : "managed by a config file on its connector"} · {t.route_count} route(s)
                  </p>
                  {t.error ? <p className="tunnel-reason">{t.error}</p> : null}
                  <p className="meta">
                    {t.connectors.length
                      ? `${t.connectors.length} connector(s) connected: ${t.connectors
                          .map((c) => `${c.version} via ${(c.conns ?? []).map((x) => x.colo_name).join(", ") || "edge"}`)
                          .join("; ")}`
                      : "No connector is connected, so its hostnames are down."}
                  </p>
                  {t.local.length > 0 ? (
                    <ul className="plain-list" aria-label={`${t.name} connectors on this node`}>
                      {t.local.map((l) => (
                        <li key={l.machine_id + l.unit}>
                          <StatusBadge status={l.active ? "running" : "stopped"} label={l.machine_name} /> <code>{l.unit}</code>
                          {l.managed ? <span className="tag-inline">No-dal</span> : null}
                          {canManage ? (
                            <>
                              {" "}
                              <button
                                className="btn btn-sm btn-ghost"
                                type="button"
                                onClick={() =>
                                  void act("Restarting connector", () =>
                                    cfConnectorAction(t.id, { account_id: t.account_tag, machine_id: l.machine_id, action: "restart", unit: l.unit }),
                                  )
                                }
                              >
                                Restart
                              </button>
                              <button
                                className="btn btn-sm btn-ghost"
                                type="button"
                                onClick={() =>
                                  void act("Updating cloudflared", () => cfConnectorAction(t.id, { account_id: t.account_tag, machine_id: l.machine_id, action: "update" }), "cloudflared updated.")
                                }
                              >
                                Update
                              </button>
                              {l.managed ? (
                                <button
                                  className="btn btn-sm btn-ghost btn-danger-text"
                                  type="button"
                                  onClick={() => {
                                    if (window.confirm(`Stop and remove the connector for ${t.name} on ${l.machine_name}? Its hostnames go down unless another connector runs.`)) {
                                      void act("Removing connector", () =>
                                        cfConnectorAction(t.id, { account_id: t.account_tag, machine_id: l.machine_id, action: "remove", confirm: true }),
                                      );
                                    }
                                  }}
                                >
                                  Remove
                                </button>
                              ) : null}
                            </>
                          ) : null}
                        </li>
                      ))}
                    </ul>
                  ) : (
                    <p className="meta">No connector for this tunnel runs on this node.</p>
                  )}
                </article>
              ))}
            </div>
          ) : null}

          {view === "dns" ? (
            <>
              <p className="field-hint">
                Records that send a hostname to a tunnel. A record with no route returns 404; one pointing at an unknown tunnel returns error 1016. Remove those
                records in DNS once you no longer need them.
              </p>
              <div className="table-wrap">
                <table aria-label="Tunnel DNS records">
                  <thead>
                    <tr>
                      <th>Name</th>
                      <th>Zone</th>
                      <th>Account</th>
                      <th>Tunnel</th>
                      <th>Status</th>
                    </tr>
                  </thead>
                  <tbody>
                    {shownDNS.map((d) => (
                      <tr key={d.record.id}>
                        <td>
                          {d.record.name} {d.managed ? <span className="tag-inline">No-dal</span> : null}
                          {!d.record.proxied ? <span className="tag-inline">not proxied</span> : null}
                        </td>
                        <td>{d.zone_name}</td>
                        <td>{d.account_name}</td>
                        <td>{d.tunnel_name || <code>{d.tunnel_id.slice(0, 8)}</code>}</td>
                        <td>
                          <StatusBadge
                            status={d.status === "routed" ? "healthy" : d.status === "no-route" ? "warning" : "critical"}
                            label={d.status === "routed" ? "Routed" : d.status === "no-route" ? "No route" : "Unknown tunnel"}
                          />
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </>
          ) : null}
        </>
      )}

      {view === "accounts" || noAccounts ? (
        <div className="docker-grid">
          {creds.map((c) => (
            <article key={c.id} className={"docker-card" + (c.status === "invalid" || c.status === "error" ? " is-issue" : "")} aria-label={`Token ${c.name}`}>
              <header className="docker-card-head">
                <strong className="docker-head-name">{c.name}</strong>
                <StatusBadge status={c.status === "active" ? "healthy" : c.status === "limited" ? "warning" : "critical"} label={c.status} />
                <ActionMenu
                  label={`${c.name} actions`}
                  items={[
                    ...(canManage ? [{ label: "Check again", onClick: () => void act("Checking token", () => validateCfCredential(c.id)) }] : []),
                    ...(canCreds
                      ? [
                          { label: "Replace token", onClick: () => setTokenDialog({ cred: c }) },
                          {
                            label: "Remove",
                            danger: true,
                            onClick: () => {
                              if (window.confirm(`Remove ${c.name} from No-dal? Nothing changes in Cloudflare.`)) {
                                void act("Removing token", () => deleteCfCredential(c.id), `${c.name} removed. Cloudflare is unchanged.`);
                              }
                            },
                          },
                        ]
                      : []),
                  ]}
                />
              </header>
              <p className="meta">
                {c.account_owned ? "Account token" : "User token"} ending {c.token_last4}
                {c.last_verified_at ? ` · checked ${formatWhen(c.last_verified_at)}` : ""}
                {c.expires_at ? ` · expires ${formatWhen(c.expires_at)}` : ""}
              </p>
              {c.status_reason ? <p className="tunnel-reason">{c.status_reason}</p> : null}
              <ul className="plain-list">
                {c.accounts.map((a) => (
                  <li key={a.id}>
                    <strong>{a.name}</strong>: {a.zones} zone(s), tunnels {a.tunnels ? "yes" : "no"}, DNS {a.dns ? "yes" : "no"}
                  </li>
                ))}
              </ul>
            </article>
          ))}
        </div>
      ) : null}

      <RouteDialog
        open={routeDialog != null}
        route={routeDialog?.route ?? null}
        accounts={accounts}
        tunnels={tunnels}
        onClose={() => setRouteDialog(null)}
        onSaved={(res) => {
          setRouteDialog(null);
          setNotice(`${res.hostname} now goes to ${res.service}.${res.warnings?.length ? " " + res.warnings.join(" ") : ""}`);
          void reload(true);
        }}
      />
      <TokenDialog
        state={tokenDialog}
        onClose={() => setTokenDialog(null)}
        onSaved={(msg) => {
          setTokenDialog(null);
          setNotice(msg);
          void reload(true);
        }}
      />
      <NewTunnelDialog
        open={tunnelDialog}
        accounts={accounts.filter((a) => a.credentials.length > 0)}
        onClose={() => setTunnelDialog(false)}
        onCreated={(name) => {
          setTunnelDialog(false);
          setNotice(`Tunnel ${name} created. Deploy a connector for it, then add hostnames.`);
          setView("tunnels");
          void reload(true);
        }}
      />
      <DeployDialog
        tunnel={deploy}
        machines={machines}
        supported={connectorsSupported}
        onClose={() => setDeploy(null)}
        onDone={(msg) => {
          setDeploy(null);
          setNotice(msg);
          void reload(true);
        }}
      />
      <MoveDialog
        routes={moving}
        tunnels={tunnels}
        onClose={() => setMoving(null)}
        onStarted={(j) => {
          setMoving(null);
          setSelected(new Set());
          setJob(j);
        }}
      />
      <Dialog open={diagnose != null} title={`Diagnose ${diagnose?.route.hostname ?? ""}`} onClose={() => setDiagnose(null)}>
        {diagnose?.error ? <ErrorNotice error={diagnose.error} /> : null}
        {diagnose && !diagnose.checks && !diagnose.error ? <LoadingState label="Checking the tunnel, DNS, origin and site" /> : null}
        {diagnose?.summary ? (
          <p className={"banner " + (diagnose.summary.startsWith("No problems") ? "banner-ok" : "banner-warn")}>
            <strong>{diagnose.summary}</strong>
          </p>
        ) : null}
        <ul className="plain-list" aria-label="Checks">
          {(diagnose?.checks ?? []).map((c) => (
            <li key={c.area}>
              <StatusBadge
                status={c.status === "ok" ? "healthy" : c.status === "fail" ? "critical" : c.status === "warn" ? "warning" : "unknown"}
                label={{ tunnel: "Tunnel", dns: "DNS", origin: "Origin", public: "Public" }[c.area] ?? c.area}
              />{" "}
              {c.detail}
            </li>
          ))}
        </ul>
      </Dialog>
      <DeleteRouteDialog
        route={deleting}
        onClose={() => setDeleting(null)}
        onDone={(msg) => {
          setDeleting(null);
          setNotice(msg);
          void reload(true);
        }}
      />
      <DeleteTunnelDialog
        tunnel={tunnelDelete}
        onClose={() => setTunnelDelete(null)}
        onDone={(msg) => {
          setTunnelDelete(null);
          setNotice(msg);
          void reload(true);
        }}
      />
      <ContextMenu menu={ctx.menu} onClose={ctx.close} />
    </section>
  );
}

function TokenDialog({ state, onClose, onSaved }: { state: { cred: CfCredential | null } | null; onClose: () => void; onSaved: (msg: string) => void }) {
  const cred = state?.cred ?? null;
  const [name, setName] = useState("");
  const [token, setToken] = useState("");
  const [accountID, setAccountID] = useState("");
  const [narrower, setNarrower] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    setName(cred?.name ?? "");
    setToken("");
    setAccountID(cred?.account_hint ?? "");
    setNarrower(null);
    setError(null);
  }, [state, cred]);
  async function save(allowNarrower = false) {
    setBusy(true);
    setError(null);
    try {
      if (cred) {
        await updateCfCredential(cred.id, { name: name.trim(), token: token.trim() || undefined, account_id: accountID.trim() || undefined, allow_narrower: allowNarrower });
        onSaved(token.trim() ? `${name} now uses the new token.` : `${name} saved.`);
      } else {
        const c = await addCfCredential({ name: name.trim(), token: token.trim(), account_id: accountID.trim() || undefined });
        onSaved(`${c.name} connected: ${c.accounts.map((a) => a.name).join(", ")}.`);
      }
    } catch (err) {
      const msg = errText(err, "Could not save the token");
      if (cred && msg.includes("cannot reach")) {
        setNarrower(msg);
      } else {
        setError(msg);
      }
    } finally {
      setBusy(false);
    }
  }
  return (
    <Dialog
      open={state != null}
      title={cred ? `Token ${cred.name}` : "Connect a Cloudflare account"}
      onClose={onClose}
      footer={
        <div className="btn-row">
          <button className="btn btn-ghost" type="button" onClick={onClose}>
            Cancel
          </button>
          {narrower ? (
            <button className="btn btn-danger" type="button" disabled={busy} onClick={() => void save(true)}>
              Replace anyway
            </button>
          ) : (
            <button className="btn btn-primary" type="button" disabled={busy || !name.trim() || (!cred && !token.trim())} onClick={() => void save()}>
              {busy ? "Checking the token" : cred ? "Save" : "Connect"}
            </button>
          )}
        </div>
      }
    >
      {error ? <ErrorNotice error={error} /> : null}
      {narrower ? <p className="banner banner-warn">{narrower}</p> : null}
      <div className="field">
        <label className="field-label" htmlFor="cf-tok-name">
          Name
        </label>
        <input id="cf-tok-name" className="field-input" value={name} placeholder="Racing account" onChange={(e) => setName(e.target.value)} />
      </div>
      <div className="field">
        <label className="field-label" htmlFor="cf-tok-token">
          {cred ? "New API token (leave empty to keep the current one)" : "API token"}
        </label>
        <input id="cf-tok-token" className="field-input" type="password" autoComplete="off" value={token} onChange={(e) => setToken(e.target.value)} />
        <p className="field-hint">
          Create a scoped token in Cloudflare with Account: Cloudflare Tunnel Edit, Zone: DNS Edit, Zone: Zone Read and Account Settings Read. It is
          checked, then encrypted on this server; it is never shown again.
        </p>
      </div>
      <div className="field">
        <label className="field-label" htmlFor="cf-tok-account">
          Account ID (only for account-owned tokens)
        </label>
        <input id="cf-tok-account" className="field-input" value={accountID} placeholder="32-character ID" onChange={(e) => setAccountID(e.target.value)} />
      </div>
    </Dialog>
  );
}

function NewTunnelDialog({ open, accounts, onClose, onCreated }: { open: boolean; accounts: { id: string; name: string }[]; onClose: () => void; onCreated: (name: string) => void }) {
  const [account, setAccount] = useState("");
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    if (open) {
      setAccount(accounts[0]?.id ?? "");
      setName("");
      setError(null);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);
  return (
    <Dialog
      open={open}
      title="New tunnel"
      onClose={onClose}
      footer={
        <div className="btn-row">
          <button className="btn btn-ghost" type="button" onClick={onClose}>
            Cancel
          </button>
          <button
            className="btn btn-primary"
            type="button"
            disabled={busy || !name.trim() || !account}
            onClick={() => {
              setBusy(true);
              createCfTunnel(account, name.trim())
                .then(() => onCreated(name.trim()))
                .catch((err) => setError(errText(err, "Could not create the tunnel")))
                .finally(() => setBusy(false));
            }}
          >
            Create
          </button>
        </div>
      }
    >
      {error ? <ErrorNotice error={error} /> : null}
      <p className="field-hint">One shared tunnel can serve many hostnames and workloads; a connector inside each container is not needed.</p>
      <div className="field">
        <label className="field-label" htmlFor="cf-new-account">
          Account
        </label>
        <select id="cf-new-account" className="field-input" value={account} onChange={(e) => setAccount(e.target.value)}>
          {accounts.map((a) => (
            <option key={a.id} value={a.id}>
              {a.name}
            </option>
          ))}
        </select>
      </div>
      <div className="field">
        <label className="field-label" htmlFor="cf-new-name">
          Name
        </label>
        <input id="cf-new-name" className="field-input" value={name} placeholder="shared-sydney" onChange={(e) => setName(e.target.value)} />
      </div>
    </Dialog>
  );
}

function DeployDialog({ tunnel, machines, supported, onClose, onDone }: { tunnel: CfTunnel | null; machines: CfMachine[]; supported: boolean; onClose: () => void; onDone: (msg: string) => void }) {
  const [machine, setMachine] = useState("host");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    setMachine("host");
    setError(null);
  }, [tunnel]);
  return (
    <Dialog
      open={tunnel != null}
      title={`Deploy a connector for ${tunnel?.name ?? ""}`}
      onClose={onClose}
      footer={
        <div className="btn-row">
          <button className="btn btn-ghost" type="button" onClick={onClose}>
            Cancel
          </button>
          <button
            className="btn btn-primary"
            type="button"
            disabled={busy || !supported || !tunnel}
            onClick={() => {
              if (!tunnel) {
                return;
              }
              setBusy(true);
              cfConnectorAction(tunnel.id, { account_id: tunnel.account_tag, machine_id: machine, action: "install" })
                .then((res) => onDone(`Connector ${res.unit ?? ""} started (${res.version ?? "cloudflared"}).`))
                .catch((err) => setError(errText(err, "Could not deploy the connector")))
                .finally(() => setBusy(false));
            }}
          >
            {busy ? "Installing" : "Deploy"}
          </button>
        </div>
      }
    >
      {error ? <ErrorNotice error={error} /> : null}
      {!supported ? <p className="banner banner-warn">This node's agent cannot deploy connectors yet; update No-dal on the node.</p> : null}
      <p className="field-hint">
        cloudflared is installed from Cloudflare's signed package repository if it is missing, and runs as its own service. The tunnel token is written
        only to a root-only file on that machine. Running the connector on the host lets one tunnel reach every container's address.
      </p>
      <div className="field">
        <label className="field-label" htmlFor="cf-deploy-machine">
          Run it on
        </label>
        <select id="cf-deploy-machine" className="field-input" value={machine} onChange={(e) => setMachine(e.target.value)}>
          {machines.map((m) => (
            <option key={m.id} value={m.id}>
              {m.name}
              {m.kind === "host" ? " (this host)" : ""}
            </option>
          ))}
        </select>
      </div>
    </Dialog>
  );
}

function MoveDialog({ routes, tunnels, onClose, onStarted }: { routes: CfRoute[] | null; tunnels: CfTunnel[]; onClose: () => void; onStarted: (j: OpsJob) => void }) {
  const [dest, setDest] = useState("");
  const [plan, setPlan] = useState<CfMovePlan[] | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const sources = new Set((routes ?? []).map((r) => r.tunnel_id));
  const choices = tunnels.filter((t) => t.remote_managed && !(sources.size === 1 && sources.has(t.id)));
  useEffect(() => {
    setDest("");
    setPlan(null);
    setError(null);
  }, [routes]);
  const destTunnel = tunnels.find((t) => t.id === dest);
  const refs = (routes ?? []).map(ref);
  const ready = (plan ?? []).filter((p) => p.ok).length;
  return (
    <Dialog
      open={routes != null}
      title={`Move ${routes?.length === 1 ? routes[0].hostname : `${routes?.length ?? 0} hostnames`}`}
      wide
      onClose={onClose}
      footer={
        <div className="btn-row">
          <button className="btn btn-ghost" type="button" onClick={onClose}>
            Cancel
          </button>
          <button
            className="btn btn-secondary"
            type="button"
            disabled={busy || !destTunnel}
            onClick={() => {
              if (!destTunnel) {
                return;
              }
              setBusy(true);
              setError(null);
              previewCfMove(refs, destTunnel.account_tag, destTunnel.id)
                .then((res) => setPlan(res.items))
                .catch((err) => setError(errText(err, "Preview failed")))
                .finally(() => setBusy(false));
            }}
          >
            Preview
          </button>
          <button
            className="btn btn-primary"
            type="button"
            disabled={busy || !destTunnel || !plan || ready === 0}
            onClick={() => {
              if (!destTunnel) {
                return;
              }
              setBusy(true);
              moveCfRoutes(refs, destTunnel.account_tag, destTunnel.id)
                .then(onStarted)
                .catch((err) => setError(errText(err, "Move failed")))
                .finally(() => setBusy(false));
            }}
          >
            Move {ready || ""}
          </button>
        </div>
      }
    >
      {error ? <ErrorNotice error={error} /> : null}
      <p className="field-hint">
        Each hostname is added to the new tunnel, DNS is repointed, and the site is checked through Cloudflare before it is removed from the old tunnel.
        A failed check undoes that hostname. Old tunnels are never deleted.
      </p>
      <div className="field">
        <label className="field-label" htmlFor="cf-move-dest">
          Destination tunnel
        </label>
        <select
          id="cf-move-dest"
          className="field-input"
          value={dest}
          onChange={(e) => {
            setDest(e.target.value);
            setPlan(null);
          }}
        >
          <option value="">Choose a tunnel</option>
          {choices.map((t) => (
            <option key={t.id} value={t.id}>
              {t.name} · {t.account_name} ({t.status})
            </option>
          ))}
        </select>
      </div>
      {plan ? (
        <ul className="plain-list" aria-label="Move preview">
          {plan.map((p) => (
            <li key={p.route.key}>
              <StatusBadge status={p.ok ? "healthy" : "critical"} label={p.ok ? "Ready" : "Blocked"} /> <strong>{p.route.hostname}</strong>
              {p.blockers.map((b) => (
                <div key={b} className="tunnel-reason">
                  {b}
                </div>
              ))}
              {p.warnings.map((w) => (
                <div key={w} className="meta">
                  {w}
                </div>
              ))}
            </li>
          ))}
        </ul>
      ) : null}
    </Dialog>
  );
}

function DeleteRouteDialog({ route, onClose, onDone }: { route: CfRoute | null; onClose: () => void; onDone: (msg: string) => void }) {
  const [typed, setTyped] = useState("");
  const [dns, setDNS] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    setTyped("");
    setDNS(true);
    setError(null);
  }, [route]);
  return (
    <Dialog
      open={route != null}
      title={`Delete ${route?.hostname ?? ""}`}
      onClose={onClose}
      footer={
        <div className="btn-row">
          <button className="btn btn-ghost" type="button" onClick={onClose}>
            Cancel
          </button>
          <button
            className="btn btn-danger"
            type="button"
            disabled={busy || !route || typed.trim().toLowerCase() !== route.hostname}
            onClick={() => {
              if (!route) {
                return;
              }
              setBusy(true);
              deleteCfRoute(ref(route), dns, typed.trim())
                .then((res) => onDone(`${route.hostname} deleted; ${res.note}.`))
                .catch((err) => setError(errText(err, "Delete failed")))
                .finally(() => setBusy(false));
            }}
          >
            Delete
          </button>
        </div>
      }
    >
      {error ? <ErrorNotice error={error} /> : null}
      <p>The hostname stops working as soon as it is removed from {route?.tunnel_name}.</p>
      <label className="check-row">
        <input type="checkbox" checked={dns} onChange={(e) => setDNS(e.target.checked)} />
        <span>Also remove its DNS record (only if it points at this tunnel)</span>
      </label>
      <div className="field">
        <label className="field-label" htmlFor="cf-del-confirm">
          Type {route?.hostname} to confirm
        </label>
        <input id="cf-del-confirm" className="field-input" value={typed} onChange={(e) => setTyped(e.target.value)} />
      </div>
    </Dialog>
  );
}

function DeleteTunnelDialog({ tunnel, onClose, onDone }: { tunnel: CfTunnel | null; onClose: () => void; onDone: (msg: string) => void }) {
  const [typed, setTyped] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    setTyped("");
    setError(null);
  }, [tunnel]);
  return (
    <Dialog
      open={tunnel != null}
      title={`Delete tunnel ${tunnel?.name ?? ""}`}
      onClose={onClose}
      footer={
        <div className="btn-row">
          <button className="btn btn-ghost" type="button" onClick={onClose}>
            Cancel
          </button>
          <button
            className="btn btn-danger"
            type="button"
            disabled={busy || !tunnel || typed.trim() !== tunnel.name}
            onClick={() => {
              if (!tunnel) {
                return;
              }
              setBusy(true);
              deleteCfTunnel(tunnel.id, tunnel.account_tag, typed.trim())
                .then(() => onDone(`Tunnel ${tunnel.name} deleted.`))
                .catch((err) => setError(errText(err, "Delete failed")))
                .finally(() => setBusy(false));
            }}
          >
            Delete tunnel
          </button>
        </div>
      }
    >
      {error ? <ErrorNotice error={error} /> : null}
      <p>
        The tunnel is deleted in Cloudflare. It is refused while it still has routes, DNS records pointing at it, or connected connectors, so nothing
        live is cut off.
      </p>
      <div className="field">
        <label className="field-label" htmlFor="cf-tdel-confirm">
          Type {tunnel?.name} to confirm
        </label>
        <input id="cf-tdel-confirm" className="field-input" value={typed} onChange={(e) => setTyped(e.target.value)} />
      </div>
    </Dialog>
  );
}
