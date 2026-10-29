import { confirmAction } from "../confirm";
import { useHostTunnelApproval } from "../components/HostTunnelApproval";
import { memo, useCallback, useEffect, useMemo, useRef, useState, type MouseEvent as ReactMouseEvent, type ReactNode } from "react";
import {
  addCfCredential,
  cfConnectorAction,
  createCfTunnel,
  deleteCfRoute,
  deleteCfTunnel,
  diagnoseCfRoute,
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
  type CfDNSEntry,
  type CfMachine,
  type CfMovePlan,
  type CfRoute,
  type CfRouteRef,
  type CfTunnel,
  type OpsJob,
} from "../api/client";
import { AccountsDialog } from "../cloudflare/AccountsDialog";
import { useWarningDismissals } from "../cloudflare/dismiss";
import { DnsBox, HostBox, dnsBadge, tunnelTone } from "../cloudflare/HostBox";
import { useCfInventory } from "../cloudflare/inventory";
import { RouteDialog } from "../cloudflare/RouteDialog";
import { ActionMenu } from "../components/ActionMenu";
import { ContextMenu, useContextMenu, type ContextItem } from "../components/ContextMenu";
import { EmptyState, LoadingState } from "../components/EmptyState";
import { ErrorNotice } from "../components/ErrorNotice";
import { Icon } from "../components/Icon";
import { JobLog } from "../components/JobLog";
import { PageHeader } from "../components/PageHeader";
import { StatusBadge } from "../components/StatusBadge";
import { formatRelative } from "../format";
import { usePref } from "../prefs";
import { usePoll } from "../query";
import { hasGrant } from "../rbac";
import { useSession } from "../session";
import { Dialog } from "../ui/Dialog";
import { useTablePrefs, type TableColumn } from "../ui/tablePrefs";
import "../cloudflare.css";
import { nodeLabel } from "../nodeNames";

const ref = (r: CfRoute): CfRouteRef => ({ account_id: r.account_id, tunnel_id: r.tunnel_id, hostname: r.hostname, path: r.path });
const errText = (err: unknown, fallback: string) => (err instanceof Error ? err.message : fallback);
const actionItems = (items: ContextItem[]) => items.filter((i): i is Exclude<ContextItem, "sep"> => i !== "sep" && !i.disabled);

// Sort choices for the hostname boxes.
const HOST_COLUMNS: TableColumn<CfRoute>[] = [
  { id: "hostname", label: "Hostname", required: true, sortValue: (r) => r.hostname + (r.path ?? "") },
  { id: "service", label: "Goes to", sortValue: (r) => r.service },
  { id: "tunnel", label: "Tunnel", sortValue: (r) => r.tunnel_name },
  { id: "account", label: "Account", sortValue: (r) => r.account_name },
  { id: "zone", label: "Zone", sortValue: (r) => r.zone_name ?? "" },
  { id: "dns", label: "DNS", sortValue: (r) => dnsBadge(r).label },
  { id: "workload", label: "Workload", sortValue: (r) => r.workload?.name ?? "￿" },
  { id: "state", label: "State", sortValue: (r) => (r.enabled ? 0 : 1) },
];

type GroupProps = {
  t: CfTunnel;
  count: number;
  shown: number;
  collapsed: boolean;
  selection: "all" | "some" | "none";
  canManage: boolean;
  multiAccount: boolean;
  onCollapse: (id: string) => void;
  onSelect: (t: CfTunnel, on: boolean) => void;
  itemsFor: (t: CfTunnel) => ContextItem[];
};

/** TunnelGroup is the heading bar above one tunnel's hostname boxes. */
const TunnelGroup = memo(function TunnelGroup({ t, count, shown, collapsed, selection, canManage, multiAccount, onCollapse, onSelect, itemsFor }: GroupProps) {
  const colos = [...new Set(t.connectors.flatMap((c) => (c.conns ?? []).map((x) => x.colo_name)))].join(", ");
  const versions = [...new Set(t.connectors.map((c) => c.version).filter(Boolean))].join(", ");
  return (
    <div className={"cf-bar" + (t.status !== "healthy" ? " is-issue" : "")}>
      <span className="cf-gutter">
        {canManage ? (
          <input
            type="checkbox"
            aria-label={`Select every hostname on ${t.name}`}
            disabled={count === 0}
            checked={selection === "all"}
            ref={(el) => {
              if (el) {
                el.indeterminate = selection === "some";
              }
            }}
            onChange={(e) => onSelect(t, e.target.checked)}
          />
        ) : null}
      </span>
      <div className="cf-group-head">
        <button type="button" className="cf-group-toggle" aria-expanded={!collapsed} aria-label={`${collapsed ? "Show" : "Hide"} ${t.name} hostnames`} onClick={() => onCollapse(t.id)}>
          <Icon name="chevron-down" size={14} className={collapsed ? "is-collapsed" : undefined} />
        </button>
        <strong className="cf-group-name">{t.name}</strong>
        <StatusBadge status={tunnelTone(t.status)} label={t.status} />
        <span className="meta">{shown === count ? `${count} hostname${count === 1 ? "" : "s"}` : `${shown} of ${count} hostnames`}</span>
        {multiAccount ? <span className="tag-inline">{t.account_name}</span> : null}
        {!t.remote_managed ? (
          <span className="tag-inline" title="Routes are set in a config file on the connector">
            config file
          </span>
        ) : null}
        <span className="meta cf-group-conn">
          {t.connectors.length
            ? `${t.connectors.length} connector${t.connectors.length === 1 ? "" : "s"}${colos ? ` via ${colos}` : ""}${versions ? ` · ${versions}` : ""}`
            : "No connector connected"}
        </span>
        {t.local.length ? (
          <span className="cf-group-local">
            {t.local.map((l) => (
              <span key={l.machine_id + l.unit} className="tag-inline" title={l.unit}>
                <span className={"dot " + (l.active ? "is-ok" : "is-bad")} aria-hidden="true" /> {nodeLabel(l.machine_id, l.machine_name)}
                {l.managed ? " · No-dal" : ""}
              </span>
            ))}
          </span>
        ) : null}
        {t.error ? <span className="tunnel-reason">{t.error}</span> : null}
        <span className="cf-group-actions">
          <ActionMenu label={`${t.name} actions`} items={actionItems(itemsFor(t))} />
        </span>
      </div>
    </div>
  );
});

type Warning = { id: string; title?: string; body: ReactNode; role: "alert" | "status"; actions?: ReactNode };

/** WarningBanner is one dismissible warning; right-click offers a permanent dismissal. */
function WarningBanner({ w, onDismiss, onMenu }: { w: Warning; onDismiss: (id: string) => void; onMenu: (event: ReactMouseEvent, w: Warning) => void }) {
  return (
    <div className="banner banner-warn cf-warning" role={w.role} data-warning={w.id} onContextMenu={(e) => onMenu(e, w)}>
      <div className="cf-warning-body">
        {w.title ? (
          <p>
            <strong>{w.title}</strong>
          </p>
        ) : null}
        {w.body}
        {w.actions}
      </div>
      <button type="button" className="btn btn-ghost btn-sm btn-icon" aria-label="Dismiss warning" title="Dismiss for 3 days (right-click for more)" onClick={() => onDismiss(w.id)}>
        <Icon name="x" size={14} />
      </button>
    </div>
  );
}

/** CloudflarePage manages every connected Cloudflare account's tunnels, hostnames and DNS in one place. */
export function CloudflarePage() {
  const hostGate = useHostTunnelApproval();
  const session = useSession();
  const user = session.status === "ready" ? session.user : null;
  const canManage = hasGrant(user, "cloudflare.manage");
  const canCreds = hasGrant(user, "cloudflare.credentials");
  const { inv, fromCache, error: loadError, reload: reloadInv } = useCfInventory();
  const [creds, setCreds] = useState<CfCredential[]>([]);
  const [machines, setMachines] = useState<CfMachine[]>([]);
  const [connectorsSupported, setConnectorsSupported] = useState(true);
  const [account, setAccount] = useState("");
  const [query, setQuery] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [expanded, setExpanded] = useState<string | null>(null);
  const [routeDialog, setRouteDialog] = useState<{ route: CfRoute | null; tunnelId?: string } | null>(null);
  const [diagnose, setDiagnose] = useState<{ route: CfRoute; checks?: CfCheck[]; summary?: string; error?: string } | null>(null);
  const [moving, setMoving] = useState<CfRoute[] | null>(null);
  const [deleting, setDeleting] = useState<CfRoute | null>(null);
  const [accountsOpen, setAccountsOpen] = useState(false);
  const [tokenDialog, setTokenDialog] = useState<{ cred: CfCredential | null } | null>(null);
  const [tunnelDialog, setTunnelDialog] = useState(false);
  const [deploy, setDeploy] = useState<CfTunnel | null>(null);
  const [tunnelDelete, setTunnelDelete] = useState<CfTunnel | null>(null);
  const [job, setJob] = useState<OpsJob | null>(null);
  const [collapsedList, setCollapsedList] = usePref<string[]>("cf.collapsed", []);
  const [permsOpen, setPermsOpen] = useState<Set<string>>(new Set());
  const table = useTablePrefs<CfRoute>("cf.hostnames", HOST_COLUMNS, { id: "hostname", dir: "asc" });
  const grouped = table.option("group", true);
  const warnings = useWarningDismissals();
  const ctx = useContextMenu();
  const openMenu = ctx.openAt;

  const loadCreds = useCallback(async () => {
    try {
      const c = await listCfCredentials();
      setCreds(c.items ?? []);
    } catch {
      // The inventory reports the failure.
    }
  }, []);

  const reload = useCallback(
    async (refresh = false) => {
      await Promise.all([reloadInv(refresh), loadCreds()]);
    },
    [reloadInv, loadCreds],
  );

  useEffect(() => {
    void loadCreds();
    void listCfMachines()
      .then((m) => {
        setMachines(m.items ?? []);
        setConnectorsSupported(m.supported);
      })
      .catch(() => undefined);
  }, [loadCreds]);
  // Cheap: unchanged inventories are answered with 304 and no body. While the
  // server refreshes from Cloudflare in the background, look again sooner.
  usePoll(() => reloadInv(false), inv?.refreshing ? 4000 : 30000);

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

  /** forceSync checks every saved token again, then rebuilds everything from Cloudflare. */
  function forceSync() {
    void act(
      "Syncing from Cloudflare",
      async () => {
        if (!canManage) {
          return;
        }
        const results = await Promise.allSettled(creds.map((c) => validateCfCredential(c.id)));
        const failed = results.filter((r) => r.status === "rejected").length;
        if (failed) {
          throw new Error(`${failed} token${failed === 1 ? "" : "s"} could not be checked; the rest were synced.`);
        }
      },
      "Synced from Cloudflare.",
    );
  }

  const accounts = useMemo(() => inv?.accounts ?? [], [inv]);
  const tunnels = useMemo(() => inv?.tunnels ?? [], [inv]);
  const routes = useMemo(() => inv?.routes ?? [], [inv]);
  const dnsEntries = useMemo(() => inv?.dns ?? [], [inv]);
  const q = query.trim().toLowerCase();
  const matches = (...fields: (string | undefined)[]) => !q || fields.some((f) => f?.toLowerCase().includes(q));
  const shownRoutes = useMemo(
    () =>
      table.sortRows(
        routes.filter(
          (r) =>
            (!account || r.account_id === account) &&
            (!q || [r.hostname, r.path, r.service, r.tunnel_name, r.account_name, r.workload?.name].some((f) => f?.toLowerCase().includes(q))),
        ),
      ),
    // table.sortRows changes with the saved sort.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [routes, account, q, table.sort?.id, table.sort?.dir],
  );
  // DNS records by name, shown inside each hostname's box. Records whose
  // name has no hostname get boxes of their own.
  const { recordsByName, orphanDNS } = useMemo(() => {
    const names = new Set(routes.map((r) => r.hostname.toLowerCase()));
    const byName = new Map<string, CfDNSEntry[]>();
    const orphans: CfDNSEntry[] = [];
    for (const d of dnsEntries) {
      const n = d.record.name.toLowerCase();
      if (names.has(n)) {
        byName.set(n, [...(byName.get(n) ?? []), d]);
      } else {
        orphans.push(d);
      }
    }
    return { recordsByName: byName, orphanDNS: orphans };
  }, [routes, dnsEntries]);
  const shownOrphans = orphanDNS.filter((d) => (!account || d.account_id === account) && matches(d.record.name, d.zone_name, d.tunnel_name));
  const counts = useMemo(() => {
    const healthy = tunnels.filter((t) => t.status === "healthy").length;
    const dnsIssues = routes.filter((r) => r.enabled && r.dns && r.dns.action !== "ok").length;
    return { healthy, dnsIssues, disabled: routes.filter((r) => !r.enabled).length, unreadable: routes.filter((r) => r.zone_id && !r.dns).length };
  }, [tunnels, routes]);

  // Stable callbacks, so memoised boxes only redraw when their data changes.
  const latest = useRef({ canManage, routes, tunnels, machines });
  latest.current = { canManage, routes, tunnels, machines };

  const routeItems = useCallback((r: CfRoute): ContextItem[] => {
    const items: ContextItem[] = [];
    const manage = latest.current.canManage;
    if (manage && r.editable) {
      items.push({ label: "Edit", onClick: () => setRouteDialog({ route: r }) });
      if (r.enabled && r.dns && r.dns.action !== "ok") {
        items.push({ label: "Fix DNS", onClick: () => setRouteDialog({ route: r }) });
      }
    }
    items.push({ label: "Diagnose", onClick: () => void runDiagnose(r) });
    if (manage && r.editable) {
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
    if (manage) {
      items.push("sep", { label: "Delete", danger: true, onClick: () => setDeleting(r) });
    }
    return items;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const tunnelItems = useCallback((t: CfTunnel): ContextItem[] => {
    const items: ContextItem[] = [];
    if (latest.current.canManage) {
      if (t.remote_managed) {
        items.push({ label: "Add hostname here", onClick: () => setRouteDialog({ route: null, tunnelId: t.id }) });
      }
      const onTunnel = latest.current.routes.filter((r) => r.tunnel_id === t.id && r.enabled && r.editable);
      if (onTunnel.length) {
        items.push({ label: `Move all ${onTunnel.length} hostnames`, onClick: () => setMoving(onTunnel) });
      }
      items.push(
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
      );
      for (const l of t.local) {
        items.push({
          label: `Restart connector on ${nodeLabel(l.machine_id, l.machine_name)}`,
          onClick: () =>
            void act("Restarting connector", () =>
              hostGate.guard((approve_host) =>
                cfConnectorAction(t.id, { account_id: t.account_tag, machine_id: l.machine_id, action: "restart", unit: l.unit, approve_host }),
              ),
            ),
        });
        items.push({
          label: `Update cloudflared on ${nodeLabel(l.machine_id, l.machine_name)}`,
          onClick: () =>
            void act(
              "Updating cloudflared",
              () => hostGate.guard((approve_host) => cfConnectorAction(t.id, { account_id: t.account_tag, machine_id: l.machine_id, action: "update", approve_host })),
              "cloudflared updated.",
            ),
        });
        if (l.managed) {
          items.push({
            label: `Remove connector on ${nodeLabel(l.machine_id, l.machine_name)}`,
            danger: true,
            onClick: () => {
              if (confirmAction(`Stop and remove the connector for ${t.name} on ${nodeLabel(l.machine_id, l.machine_name)}? Its hostnames go down unless another connector runs.`)) {
                void act("Removing connector", () =>
                  hostGate.guard((approve_host) =>
                    cfConnectorAction(t.id, { account_id: t.account_tag, machine_id: l.machine_id, action: "remove", confirm: true, approve_host }),
                  ),
                );
              }
            },
          });
        }
      }
    }
    items.push("sep", { label: "Copy tunnel ID", onClick: () => void navigator.clipboard?.writeText(t.id) });
    if (latest.current.canManage) {
      items.push("sep", { label: "Delete tunnel", danger: true, onClick: () => setTunnelDelete(t) });
    }
    return items;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  const toggle = useCallback((key: string) => {
    setSelected((cur) => {
      const next = new Set(cur);
      if (next.has(key)) {
        next.delete(key);
      } else {
        next.add(key);
      }
      return next;
    });
  }, []);
  const onExpand = useCallback((key: string) => setExpanded((cur) => (cur === key ? null : key)), []);
  const collapsed = useMemo(() => new Set(collapsedList), [collapsedList]);
  const onCollapse = useCallback(
    (id: string) => setCollapsedList((cur) => (cur.includes(id) ? cur.filter((x) => x !== id) : [...cur, id])),
    [setCollapsedList],
  );
  const onSelectTunnel = useCallback((t: CfTunnel, on: boolean) => {
    setSelected((cur) => {
      const next = new Set(cur);
      for (const r of latest.current.routes) {
        if (r.tunnel_id === t.id) {
          if (on) {
            next.add(r.key);
          } else {
            next.delete(r.key);
          }
        }
      }
      return next;
    });
  }, []);
  const togglePerms = useCallback((id: string) => {
    setPermsOpen((cur) => {
      const next = new Set(cur);
      if (next.has(id)) {
        next.delete(id);
      } else {
        next.add(id);
      }
      return next;
    });
  }, []);

  async function runDiagnose(r: CfRoute) {
    setDiagnose({ route: r });
    try {
      const res = await diagnoseCfRoute(ref(r));
      setDiagnose({ route: r, checks: res.checks, summary: res.summary });
    } catch (err) {
      setDiagnose({ route: r, error: errText(err, "Diagnosis failed") });
    }
  }

  const noAccounts = accounts.length === 0;
  // Accounts whose DNS Cloudflare refused on the last refresh; said first, with the fix.
  const dnsBlocked = accounts.filter((a) => a.dns_access === "denied" || a.dns_access === "partial");

  function showPermissionsFor(credIDs: string[]) {
    setAccountsOpen(true);
    setPermsOpen((cur) => new Set([...cur, ...credIDs]));
    window.setTimeout(() => document.querySelector(".cf-perms-body")?.scrollIntoView?.({ behavior: "smooth", block: "start" }), 50);
  }

  if (!inv) {
    return (
      <section className="page">
        <LoadingState label="Reading Cloudflare" />
      </section>
    );
  }

  // Warnings, each with an id that stays the same while its condition holds.
  const allWarnings: Warning[] = [
    ...(inv.errors ?? []).map((e): Warning => ({ id: `error:${e}`, body: <p>{e}</p>, role: "status" })),
    ...dnsBlocked.map(
      (a): Warning => ({
        id: `dns:${a.id}:${a.dns_access}`,
        role: "alert",
        title: a.dns_access === "denied" ? `DNS can't be read for ${a.name}` : `Some DNS in ${a.name} can't be read`,
        body: (
          <>
            <p>{a.dns_reason}</p>
            <p className="meta">
              Until then hostnames keep working and can still be added or edited, but their DNS status shows as not readable and No-dal can't create or
              fix their DNS records; add or point those in Cloudflare yourself. No-dal tries again on every refresh.
            </p>
          </>
        ),
        actions: (
          <div className="btn-row">
            <button type="button" className="btn btn-sm btn-primary" onClick={() => showPermissionsFor(a.credentials.map((c) => c.id))}>
              Show what to add
            </button>
            <button type="button" className="btn btn-sm btn-secondary" disabled={busy !== null} onClick={() => void act("Checking DNS access", () => reload(true))}>
              Try again
            </button>
          </div>
        ),
      }),
    ),
  ];
  const shownWarnings = allWarnings.filter((w) => !warnings.hidden(w.id));
  const hiddenWarnings = allWarnings.filter((w) => warnings.hidden(w.id));
  const warningMenu = (event: ReactMouseEvent, w: Warning) =>
    openMenu(event, w.title ?? "Warning", [
      { label: "Dismiss for 3 days", onClick: () => warnings.dismiss(w.id) },
      { label: "Dismiss permanently", danger: true, onClick: () => warnings.dismiss(w.id, true) },
    ]);

  const selectedRoutes = shownRoutes.filter((r) => selected.has(r.key));
  const multiAccount = accounts.length > 1;
  const recordsFor = (r: CfRoute) => recordsByName.get(r.hostname.toLowerCase()) ?? NO_RECORDS;

  // Group hostnames under their tunnel. Tunnels without hostnames are listed
  // too; a search hides them unless the tunnel's own name matches.
  const groups = (() => {
    if (!grouped) {
      return [];
    }
    const byTunnel = new Map<string, CfRoute[]>();
    for (const r of shownRoutes) {
      const list = byTunnel.get(r.tunnel_id) ?? [];
      list.push(r);
      byTunnel.set(r.tunnel_id, list);
    }
    const dnsByTunnel = new Map<string, CfDNSEntry[]>();
    for (const d of shownOrphans) {
      dnsByTunnel.set(d.tunnel_id, [...(dnsByTunnel.get(d.tunnel_id) ?? []), d]);
    }
    const totals = new Map<string, number>();
    for (const r of routes) {
      totals.set(r.tunnel_id, (totals.get(r.tunnel_id) ?? 0) + 1);
    }
    return tunnels
      .filter((t) => (!account || t.account_tag === account) && (byTunnel.has(t.id) || dnsByTunnel.has(t.id) || (q ? matches(t.name, t.id) : true)))
      .map((t) => {
        const list = byTunnel.get(t.id) ?? (q && matches(t.name, t.id) ? routes.filter((r) => r.tunnel_id === t.id) : []);
        const sel = list.filter((r) => selected.has(r.key)).length;
        return {
          t,
          list,
          dns: dnsByTunnel.get(t.id) ?? NO_RECORDS,
          total: totals.get(t.id) ?? 0,
          selection: (sel === 0 ? "none" : sel === list.length ? "all" : "some") as "all" | "some" | "none",
        };
      })
      .sort((a, b) => a.t.name.localeCompare(b.t.name, undefined, { sensitivity: "base", numeric: true }));
  })();
  const knownTunnels = new Set(tunnels.map((t) => t.id));
  // DNS records whose tunnel has no group of its own.
  const looseDNS = grouped ? shownOrphans.filter((d) => !knownTunnels.has(d.tunnel_id)) : shownOrphans;

  const hostBox = (r: CfRoute) => (
    <HostBox
      key={r.key}
      r={r}
      records={recordsFor(r)}
      expanded={expanded === r.key}
      selected={selected.has(r.key)}
      canManage={canManage}
      onSelect={toggle}
      onExpand={onExpand}
      itemsFor={routeItems}
      onMenu={openMenu}
    />
  );
  const dnsBox = (d: CfDNSEntry) => (
    <DnsBox key={d.record.id} d={d} expanded={expanded === "dns:" + d.record.id} multiAccount={multiAccount} onExpand={onExpand} onMenu={openMenu} />
  );
  const nothingShown = grouped ? groups.length === 0 && looseDNS.length === 0 : shownRoutes.length === 0 && looseDNS.length === 0;

  return (
    <section className="page page-wide" aria-labelledby="cf-heading">
      <PageHeader
        id="cf-heading"
        title="Cloudflare"
        kicker="Tunnels, public hostnames and DNS from every connected Cloudflare account. Each item shows the account that owns it; tokens stay on this server."
        actions={
          <div className="btn-row is-flush">
            <button className="btn btn-secondary" type="button" disabled={busy !== null} onClick={() => void act("Refreshing", () => reload(true))}>
              {busy === "Refreshing" ? "Refreshing" : "Refresh"}
            </button>
            <button className="btn btn-secondary" type="button" onClick={() => setAccountsOpen(true)}>
              Manage accounts
            </button>
          </div>
        }
      />
      {error || loadError ? <ErrorNotice error={(error || loadError) as string} /> : null}
      {notice ? (
        <p className="banner banner-ok" role="status">
          {notice}{" "}
          <button className="btn btn-ghost btn-sm" type="button" onClick={() => setNotice(null)}>
            Dismiss
          </button>
        </p>
      ) : null}
      {busy && busy !== "Refreshing" ? (
        <p className="banner" role="status">
          Working: {busy}
        </p>
      ) : null}
      {shownWarnings.map((w) => (
        <WarningBanner key={w.id} w={w} onDismiss={(id) => warnings.dismiss(id)} onMenu={warningMenu} />
      ))}
      {hiddenWarnings.length ? (
        <p className="meta cf-hidden-warnings">
          {hiddenWarnings.length} warning{hiddenWarnings.length === 1 ? "" : "s"} dismissed.{" "}
          <button type="button" className="linkish" onClick={() => warnings.restore(hiddenWarnings.map((w) => w.id))}>
            Show again
          </button>
        </p>
      ) : null}
      {noAccounts && !fromCache ? (
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
          {creds.length ? (
            <>
              {" "}
              <button className="btn btn-sm btn-secondary" type="button" onClick={() => setAccountsOpen(true)}>
                Check saved tokens
              </button>
            </>
          ) : null}
        </EmptyState>
      ) : null}
      {!noAccounts ? (
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
              <StatusBadge status={counts.dnsIssues ? "critical" : counts.unreadable ? "unknown" : "healthy"} /> {counts.dnsIssues}
            </span>
            <span className="meta">
              {counts.unreadable ? (
                <button type="button" className="linkish" onClick={() => showPermissionsFor(dnsBlocked.flatMap((a) => a.credentials.map((c) => c.id)))}>
                  {counts.unreadable} not readable; see permissions
                </button>
              ) : fromCache ? (
                "showing saved copy"
              ) : inv.refreshing ? (
                "refreshing"
              ) : inv.fetched_at ? (
                `checked ${formatRelative(inv.fetched_at)}`
              ) : (
                ""
              )}
            </span>
          </div>
        </div>
      ) : null}
      {!noAccounts ? (
        <div className="toolbar">
          <div className="toolbar-group">
            <label className="search-field">
              <input className="field-input" type="search" value={query} placeholder="Search" aria-label="Search Cloudflare" onChange={(e) => setQuery(e.target.value)} />
            </label>
            {multiAccount ? (
              <select className="field-input toolbar-select" aria-label="Account filter" value={account} onChange={(e) => setAccount(e.target.value)}>
                <option value="">All accounts</option>
                {accounts.map((a) => (
                  <option key={a.id} value={a.id}>
                    {a.name}
                  </option>
                ))}
              </select>
            ) : null}
            {canManage ? (
              <>
                <button className="btn btn-sm btn-primary" type="button" onClick={() => setRouteDialog({ route: null })}>
                  Add hostname
                </button>
                <button className="btn btn-sm btn-secondary" type="button" onClick={() => setTunnelDialog(true)}>
                  New tunnel
                </button>
              </>
            ) : null}
          </div>
          {canManage && selectedRoutes.length > 0 ? (
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
      ) : null}
      {job ? <JobLog job={job} onDone={() => void reload(true)} onClose={() => setJob(null)} /> : null}

      {!noAccounts && nothingShown ? (
        <EmptyState title="No hostnames">{q || account ? "Nothing matches the filter." : "Add a public hostname to route a domain to a service."}</EmptyState>
      ) : null}
      {!noAccounts && !nothingShown ? (
        <div className="cf-hosts" aria-label="Public hostnames">
          <div className="cf-cards-bar">
            {canManage ? (
              <input
                type="checkbox"
                aria-label="Select all hostnames"
                checked={shownRoutes.length > 0 && shownRoutes.every((r) => selected.has(r.key))}
                onChange={(e) => setSelected(e.target.checked ? new Set(shownRoutes.map((r) => r.key)) : new Set())}
              />
            ) : null}
            <label className="cf-sort">
              <span className="meta">Sort by</span>
              <select
                className="field-input"
                aria-label="Sort hostnames by"
                value={table.sort?.id ?? ""}
                onChange={(e) => table.setSort(e.target.value ? { id: e.target.value, dir: table.sort?.dir ?? "asc" } : null)}
              >
                <option value="">Default order</option>
                {table.columns
                  .filter((c) => c.sortValue)
                  .map((c) => (
                    <option key={c.id} value={c.id}>
                      {c.label}
                    </option>
                  ))}
              </select>
            </label>
            {table.sort ? (
              <button
                type="button"
                className="btn btn-ghost btn-sm btn-icon"
                aria-label={table.sort.dir === "asc" ? "Sorted A to Z; sort Z to A" : "Sorted Z to A; sort A to Z"}
                title={table.sort.dir === "asc" ? "A to Z" : "Z to A"}
                onClick={() => table.setSort(table.sort ? { id: table.sort.id, dir: table.sort.dir === "asc" ? "desc" : "asc" } : null)}
              >
                <Icon name={table.sort.dir === "asc" ? "sort-up" : "sort-down"} size={14} />
              </button>
            ) : null}
            <label className="check-row">
              <input type="checkbox" checked={grouped} onChange={(e) => table.setOption("group", e.target.checked)} />
              <span>Group by tunnel</span>
            </label>
            <span className="theme-spacer" />
            <span className="meta">
              {shownRoutes.length} hostname{shownRoutes.length === 1 ? "" : "s"}
              {shownOrphans.length ? ` · ${shownOrphans.length} DNS record${shownOrphans.length === 1 ? "" : "s"} without one` : ""}
            </span>
          </div>
          {grouped ? (
            groups.map((g) => (
              <section key={g.t.id} className="cf-card-group" aria-label={`Tunnel ${g.t.name}`}>
                <TunnelGroup
                  t={g.t}
                  count={g.total}
                  shown={g.list.length}
                  collapsed={collapsed.has(g.t.id)}
                  selection={g.selection}
                  canManage={canManage}
                  multiAccount={multiAccount}
                  onCollapse={onCollapse}
                  onSelect={onSelectTunnel}
                  itemsFor={tunnelItems}
                />
                {collapsed.has(g.t.id) ? null : g.list.length === 0 && g.dns.length === 0 ? (
                  <p className="meta cf-box-empty">
                    No hostnames on this tunnel yet.
                    {canManage && g.t.remote_managed ? (
                      <>
                        {" "}
                        <button type="button" className="linkish" onClick={() => setRouteDialog({ route: null, tunnelId: g.t.id })}>
                          Add one
                        </button>
                      </>
                    ) : null}
                  </p>
                ) : (
                  <div className="cf-box-grid">
                    {g.list.map(hostBox)}
                    {g.dns.map(dnsBox)}
                  </div>
                )}
              </section>
            ))
          ) : (
            <div className="cf-box-grid">{shownRoutes.map(hostBox)}</div>
          )}
          {looseDNS.length ? (
            <section className="cf-card-group" aria-label="DNS records without a hostname">
              <p className="field-hint">
                DNS records that send a name to a tunnel with no route for it (404), or to a tunnel no saved account knows (error 1016). Remove them in
                Cloudflare DNS once they are no longer needed.
              </p>
              <div className="cf-box-grid">{looseDNS.map(dnsBox)}</div>
            </section>
          ) : null}
        </div>
      ) : null}

      <ContextMenu menu={ctx.menu} onClose={ctx.close} />
      <AccountsDialog
        open={accountsOpen}
        creds={creds}
        canManage={canManage}
        canCreds={canCreds}
        busy={busy}
        fetchedAt={inv.fetched_at || undefined}
        permsOpen={permsOpen}
        onTogglePerms={togglePerms}
        onClose={() => setAccountsOpen(false)}
        onConnect={() => setTokenDialog({ cred: null })}
        onReplace={(c) => setTokenDialog({ cred: c })}
        onSync={forceSync}
        act={(label, fn, done) => void act(label, fn, done)}
      />
      <RouteDialog
        open={routeDialog != null}
        route={routeDialog?.route ?? null}
        tunnelId={routeDialog?.tunnelId}
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
          setNotice(`Tunnel ${name} created. Deploy a connector for it from its tunnel menu, then add hostnames.`);
          void reload(true);
        }}
      />
      {hostGate.dialog}
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
    </section>
  );
}

const NO_RECORDS: CfDNSEntry[] = [];


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
  // The host's own cloudflared is not No-dal's to manage, so a container is
  // the default; the host needs a SuperAdmin's approval.
  const firstGuest = machines.find((m) => m.kind !== "host")?.id ?? "host";
  const [machine, setMachine] = useState(firstGuest);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const hostGate = useHostTunnelApproval();
  useEffect(() => {
    setMachine(firstGuest);
    setError(null);
    // eslint-disable-next-line react-hooks/exhaustive-deps
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
              hostGate
                .guard((approve_host) => cfConnectorAction(tunnel.id, { account_id: tunnel.account_tag, machine_id: machine, action: "install", approve_host }))
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
      {hostGate.dialog}
      {error ? <ErrorNotice error={error} /> : null}
      {!supported ? <p className="banner banner-warn">This node's agent cannot deploy connectors yet; update No-dal on the node.</p> : null}
      <p className="field-hint">
        cloudflared is installed from Cloudflare's signed package repository if it is missing, and runs as its own service. The tunnel token is written
        only to a root-only file on that machine. No-dal does not manage the host&apos;s own cloudflared; running a connector there needs a SuperAdmin&apos;s
        approval.
      </p>
      <div className="field">
        <label className="field-label" htmlFor="cf-deploy-machine">
          Run it on
        </label>
        <select id="cf-deploy-machine" className="field-input" value={machine} onChange={(e) => setMachine(e.target.value)}>
          {machines.map((m) => (
            <option key={m.id} value={m.id}>
              {m.name}
              {m.kind === "host" ? " (this host, needs SuperAdmin approval)" : ""}
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
  const [dns, setDNS] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
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
            disabled={busy || !route}
            onClick={() => {
              if (!route) {
                return;
              }
              setBusy(true);
              deleteCfRoute(ref(route), dns, route.hostname)
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
    </Dialog>
  );
}

function DeleteTunnelDialog({ tunnel, onClose, onDone }: { tunnel: CfTunnel | null; onClose: () => void; onDone: (msg: string) => void }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
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
            disabled={busy || !tunnel}
            onClick={() => {
              if (!tunnel) {
                return;
              }
              setBusy(true);
              deleteCfTunnel(tunnel.id, tunnel.account_tag, tunnel.name)
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
    </Dialog>
  );
}
