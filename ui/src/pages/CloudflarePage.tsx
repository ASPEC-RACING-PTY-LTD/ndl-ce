import { memo, useCallback, useEffect, useMemo, useRef, useState, type MouseEvent as ReactMouseEvent, type ReactNode } from "react";
import {
  addCfCredential,
  cfConnectorAction,
  createCfTunnel,
  deleteCfCredential,
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
  type CfMachine,
  type CfMovePlan,
  type CfRoute,
  type CfRouteRef,
  type CfTunnel,
  type OpsJob,
} from "../api/client";
import { useCfInventory } from "../cloudflare/inventory";
import { CfPermissions } from "../cloudflare/PermissionsGuide";
import { RouteDialog } from "../cloudflare/RouteDialog";
import { ActionMenu } from "../components/ActionMenu";
import { ContextMenu, useContextMenu, usePageContextItems, type ContextItem } from "../components/ContextMenu";
import { EmptyState, LoadingState } from "../components/EmptyState";
import { ErrorNotice } from "../components/ErrorNotice";
import { Icon } from "../components/Icon";
import { JobLog } from "../components/JobLog";
import { Link } from "../components/Link";
import { PageHeader } from "../components/PageHeader";
import { StatusBadge } from "../components/StatusBadge";
import { formatRelative, formatWhen } from "../format";
import { usePref } from "../prefs";
import { usePoll } from "../query";
import { hasGrant } from "../rbac";
import { useSession } from "../session";
import { Dialog } from "../ui/Dialog";
import { Segmented } from "../ui/Segmented";
import { ColumnEditor, SortHeader, useTablePrefs, type TableColumn, type TablePrefs } from "../ui/tablePrefs";

type View = "hostnames" | "dns" | "accounts";

const ref = (r: CfRoute): CfRouteRef => ({ account_id: r.account_id, tunnel_id: r.tunnel_id, hostname: r.hostname, path: r.path });
const errText = (err: unknown, fallback: string) => (err instanceof Error ? err.message : fallback);
const actionItems = (items: ContextItem[]) => items.filter((i): i is Exclude<ContextItem, "sep"> => i !== "sep" && !i.disabled);

function dnsBadge(r: CfRoute): { status: string; label: string; title?: string } {
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
  if (r.zone_id) {
    return { status: "unknown", label: "Not readable", title: "No saved token may read DNS in this zone. See Accounts > Permissions." };
  }
  return { status: "unknown", label: "No zone", title: "The domain is not in a zone a saved token can see." };
}

function tunnelTone(status: string): string {
  return status === "healthy" ? "healthy" : status === "degraded" ? "degraded" : status === "inactive" ? "unknown" : "critical";
}

const HOST_COLUMNS: TableColumn<CfRoute>[] = [
  { id: "hostname", label: "Hostname", required: true, sortValue: (r) => r.hostname + (r.path ?? "") },
  { id: "service", label: "Goes to", sortValue: (r) => r.service },
  { id: "tunnel", label: "Tunnel", defaultHidden: true, sortValue: (r) => r.tunnel_name },
  { id: "account", label: "Account", sortValue: (r) => r.account_name },
  { id: "zone", label: "Zone", defaultHidden: true, sortValue: (r) => r.zone_name ?? "" },
  { id: "dns", label: "DNS", sortValue: (r) => dnsBadge(r).label },
  { id: "workload", label: "Workload", sortValue: (r) => r.workload?.name ?? "￿" },
  { id: "state", label: "State", defaultHidden: true, sortValue: (r) => (r.enabled ? 0 : 1) },
];

type RowProps = {
  r: CfRoute;
  cols: string[];
  selected: boolean;
  canManage: boolean;
  grouped: boolean;
  onToggle: (key: string) => void;
  onMenu: (e: ReactMouseEvent, r: CfRoute) => void;
  itemsFor: (r: CfRoute) => ContextItem[];
};

/** RouteRow redraws only when its route, selection or columns change. */
const RouteRow = memo(function RouteRow({ r, cols, selected, canManage, grouped, onToggle, onMenu, itemsFor }: RowProps) {
  const dns = dnsBadge(r);
  return (
    <tr className={(r.enabled ? "" : "is-muted") + (grouped ? " cf-route-row" : "")} onContextMenu={(e) => onMenu(e, r)}>
      {canManage ? (
        <td className="cell-check">
          <input type="checkbox" aria-label={`Select ${r.hostname}`} checked={selected} onChange={() => onToggle(r.key)} />
        </td>
      ) : null}
      {cols.map((c) => {
        switch (c) {
          case "hostname":
            return (
              <td key={c}>
                <a href={`https://${r.hostname}${r.path ?? ""}`} target="_blank" rel="noopener noreferrer">
                  {r.hostname}
                </a>
                {r.path ? <span className="meta"> {r.path}</span> : null}
                {!r.enabled ? <span className="tag-inline">off</span> : null}
              </td>
            );
          case "service":
            return (
              <td key={c}>
                <code>{r.service}</code>
                {r.ip_changed ? (
                  <span className="tag-inline" title={`The workload now has ${r.workload?.current_ip}`}>
                    IP changed
                  </span>
                ) : null}
              </td>
            );
          case "tunnel":
            return (
              <td key={c}>
                <StatusBadge status={tunnelTone(r.tunnel_status)} label={r.tunnel_name} />
                {!r.editable ? <span className="meta"> config file</span> : null}
              </td>
            );
          case "account":
            return <td key={c}>{r.account_name}</td>;
          case "zone":
            return <td key={c}>{r.zone_name ?? <span className="meta">-</span>}</td>;
          case "dns":
            return (
              <td key={c} title={r.dns?.reason ?? dns.title}>
                <StatusBadge status={dns.status} label={dns.label} />
              </td>
            );
          case "workload":
            return <td key={c}>{r.workload ? <Link href={`/workloads/${r.workload.id}`}>{r.workload.name}</Link> : <span className="meta">-</span>}</td>;
          case "state":
            return <td key={c}>{r.enabled ? "On" : "Off"}</td>;
        }
        return null;
      })}
      <td className="cell-actions">
        <ActionMenu label={`${r.hostname} actions`} items={actionItems(itemsFor(r))} />
      </td>
    </tr>
  );
});

type GroupProps = {
  t: CfTunnel;
  count: number;
  shown: number;
  span: number;
  collapsed: boolean;
  selection: "all" | "some" | "none";
  canManage: boolean;
  multiAccount: boolean;
  onCollapse: (id: string) => void;
  onSelect: (t: CfTunnel, on: boolean) => void;
  onMenu: (e: ReactMouseEvent, t: CfTunnel) => void;
  itemsFor: (t: CfTunnel) => ContextItem[];
  /** asBar draws the heading as a bar above cards instead of a table row. */
  asBar?: boolean;
};

/** TunnelGroup is the heading row of one tunnel's hostnames. */
const TunnelGroup = memo(function TunnelGroup({ t, count, shown, span, collapsed, selection, canManage, multiAccount, onCollapse, onSelect, onMenu, itemsFor, asBar }: GroupProps) {
  const colos = [...new Set(t.connectors.flatMap((c) => (c.conns ?? []).map((x) => x.colo_name)))].join(", ");
  const versions = [...new Set(t.connectors.map((c) => c.version).filter(Boolean))].join(", ");
  const check = canManage ? (
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
  ) : null;
  const head = (
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
              <span className={"dot " + (l.active ? "is-ok" : "is-bad")} aria-hidden="true" /> {l.machine_name}
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
  );
  if (asBar) {
    return (
      <div className={"cf-bar" + (t.status !== "healthy" ? " is-issue" : "")} onContextMenu={(e) => onMenu(e, t)}>
        <span className="cf-gutter">{check}</span>
        {head}
      </div>
    );
  }
  return (
    <tr className={"cf-group" + (t.status !== "healthy" ? " is-issue" : "")} onContextMenu={(e) => onMenu(e, t)}>
      {canManage ? <td className="cell-check">{check}</td> : null}
      <td colSpan={span}>{head}</td>
    </tr>
  );
});

/** RouteCard is one hostname as a card: field names down the left, values beside them. */
const RouteCard = memo(function RouteCard({ r, cols, selected, canManage, onToggle, onMenu, itemsFor }: Omit<RowProps, "grouped">) {
  const dns = dnsBadge(r);
  const fields: { id: string; label: string; value: ReactNode; title?: string }[] = [];
  for (const c of cols) {
    switch (c) {
      case "service":
        fields.push({
          id: c,
          label: "Goes to",
          value: (
            <>
              <code>{r.service}</code>
              {r.ip_changed ? (
                <span className="tag-inline" title={`The workload now has ${r.workload?.current_ip}`}>
                  IP changed
                </span>
              ) : null}
            </>
          ),
        });
        break;
      case "tunnel":
        fields.push({ id: c, label: "Tunnel", value: <StatusBadge status={tunnelTone(r.tunnel_status)} label={r.tunnel_name} /> });
        break;
      case "account":
        fields.push({ id: c, label: "Account", value: r.account_name });
        break;
      case "zone":
        fields.push({ id: c, label: "Zone", value: r.zone_name ?? <span className="meta">-</span> });
        break;
      case "dns":
        fields.push({ id: c, label: "DNS", value: <StatusBadge status={dns.status} label={dns.label} />, title: r.dns?.reason ?? dns.title });
        break;
      case "workload":
        fields.push({ id: c, label: "Workload", value: r.workload ? <Link href={`/workloads/${r.workload.id}`}>{r.workload.name}</Link> : <span className="meta">-</span> });
        break;
      case "state":
        fields.push({ id: c, label: "State", value: r.enabled ? "On" : "Off" });
        break;
    }
  }
  return (
    <article className={"cf-card" + (r.enabled ? "" : " is-muted") + (selected ? " is-selected" : "")} aria-label={r.hostname} onContextMenu={(e) => onMenu(e, r)}>
      <header className="cf-card-head">
        {canManage ? (
          <span className="cf-gutter">
            <input type="checkbox" aria-label={`Select ${r.hostname}`} checked={selected} onChange={() => onToggle(r.key)} />
          </span>
        ) : null}
        <a className="cf-card-title" href={`https://${r.hostname}${r.path ?? ""}`} target="_blank" rel="noopener noreferrer">
          {r.hostname}
          {r.path ? <span className="meta"> {r.path}</span> : null}
        </a>
        {!r.enabled ? <span className="tag-inline">off</span> : null}
        <span className="cf-group-actions">
          <ActionMenu label={`${r.hostname} actions`} items={actionItems(itemsFor(r))} />
        </span>
      </header>
      <dl className="cf-card-fields">
        {fields.map((f) => (
          <div key={f.id} title={f.title}>
            <dt>{f.label}</dt>
            <dd>{f.value}</dd>
          </div>
        ))}
      </dl>
    </article>
  );
});

/** CloudflarePage manages every connected Cloudflare account's tunnels, hostnames and DNS in one place. */
export function CloudflarePage() {
  const session = useSession();
  const user = session.status === "ready" ? session.user : null;
  const canManage = hasGrant(user, "cloudflare.manage");
  const canCreds = hasGrant(user, "cloudflare.credentials");
  const ctx = useContextMenu();
  const [view, setView] = usePref<View>("cf.view", "hostnames");
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
  const [routeDialog, setRouteDialog] = useState<{ route: CfRoute | null; tunnelId?: string } | null>(null);
  const [diagnose, setDiagnose] = useState<{ route: CfRoute; checks?: CfCheck[]; summary?: string; error?: string } | null>(null);
  const [moving, setMoving] = useState<CfRoute[] | null>(null);
  const [deleting, setDeleting] = useState<CfRoute | null>(null);
  const [tokenDialog, setTokenDialog] = useState<{ cred: CfCredential | null } | null>(null);
  const [tunnelDialog, setTunnelDialog] = useState(false);
  const [deploy, setDeploy] = useState<CfTunnel | null>(null);
  const [tunnelDelete, setTunnelDelete] = useState<CfTunnel | null>(null);
  const [job, setJob] = useState<OpsJob | null>(null);
  const [collapsedList, setCollapsedList] = usePref<string[]>("cf.collapsed", []);
  const [permsOpen, setPermsOpen] = useState<Set<string>>(new Set());
  const table = useTablePrefs<CfRoute>("cf.hostnames", HOST_COLUMNS, { id: "hostname", dir: "asc" });
  const grouped = table.option("group", true);
  // Cards put field names down the left and fit two or three hostnames across.
  const asCards = table.option("cards", true);

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

  const accounts = useMemo(() => inv?.accounts ?? [], [inv]);
  const tunnels = useMemo(() => inv?.tunnels ?? [], [inv]);
  const routes = useMemo(() => inv?.routes ?? [], [inv]);
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
  const shownDNS = (inv?.dns ?? []).filter((d) => (!account || d.account_id === account) && matches(d.record.name, d.zone_name, d.tunnel_name));
  const counts = useMemo(() => {
    const healthy = tunnels.filter((t) => t.status === "healthy").length;
    const dnsIssues = routes.filter((r) => r.enabled && r.dns && r.dns.action !== "ok").length;
    return { healthy, dnsIssues, disabled: routes.filter((r) => !r.enabled).length, unreadable: routes.filter((r) => r.zone_id && !r.dns).length };
  }, [tunnels, routes]);

  // Stable callbacks, so memoised rows only redraw when their data changes.
  const latest = useRef({ canManage, routes, tunnels, machines });
  latest.current = { canManage, routes, tunnels, machines };

  const routeItems = useCallback((r: CfRoute): ContextItem[] => {
    const items: ContextItem[] = [];
    const manage = latest.current.canManage;
    if (manage && r.editable) {
      items.push({ label: "Edit", onClick: () => setRouteDialog({ route: r }) });
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
          label: `Restart connector on ${l.machine_name}`,
          onClick: () => void act("Restarting connector", () => cfConnectorAction(t.id, { account_id: t.account_tag, machine_id: l.machine_id, action: "restart", unit: l.unit })),
        });
        items.push({
          label: `Update cloudflared on ${l.machine_name}`,
          onClick: () => void act("Updating cloudflared", () => cfConnectorAction(t.id, { account_id: t.account_tag, machine_id: l.machine_id, action: "update" }), "cloudflared updated."),
        });
        if (l.managed) {
          items.push({
            label: `Remove connector on ${l.machine_name}`,
            danger: true,
            onClick: () => {
              if (window.confirm(`Stop and remove the connector for ${t.name} on ${l.machine_name}? Its hostnames go down unless another connector runs.`)) {
                void act("Removing connector", () => cfConnectorAction(t.id, { account_id: t.account_tag, machine_id: l.machine_id, action: "remove", confirm: true }));
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

  const onRouteMenu = useCallback((e: ReactMouseEvent, r: CfRoute) => ctx.openAt(e, r.hostname, routeItems(r)), [ctx, routeItems]);
  const onTunnelMenu = useCallback((e: ReactMouseEvent, t: CfTunnel) => ctx.openAt(e, t.name, tunnelItems(t)), [ctx, tunnelItems]);
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
    setView("accounts");
    setPermsOpen((cur) => new Set([...cur, ...credIDs]));
    window.setTimeout(() => document.querySelector(".cf-perms-body")?.scrollIntoView({ behavior: "smooth", block: "start" }), 50);
  }

  const pageItems: ContextItem[] = [];
  if (canManage && !noAccounts) {
    pageItems.push({ label: "Add hostname", onClick: () => setRouteDialog({ route: null }) }, { label: "New tunnel", onClick: () => setTunnelDialog(true) });
  }
  if (canCreds) {
    pageItems.push({ label: "Connect account", onClick: () => setTokenDialog({ cred: null }) });
  }
  pageItems.push(
    { label: "Refresh from Cloudflare", disabled: busy !== null, onClick: () => void act("Refreshing", () => reload(true)) },
    "sep",
    { label: "Show hostnames", disabled: view === "hostnames", onClick: () => setView("hostnames") },
    { label: "Show DNS", disabled: view === "dns", onClick: () => setView("dns") },
    { label: "Show accounts", disabled: view === "accounts", onClick: () => setView("accounts") },
  );
  usePageContextItems("Cloudflare", pageItems);

  if (!inv) {
    return (
      <section className="page">
        <LoadingState label="Reading Cloudflare" />
      </section>
    );
  }

  const selectedRoutes = shownRoutes.filter((r) => selected.has(r.key));
  const cols = table.visible.map((c) => c.id);
  const span = cols.length + 1;
  const multiAccount = accounts.length > 1;

  // Group hostnames under their tunnel. Tunnels without hostnames are listed
  // too (they replace the old Tunnels view); a search hides them unless the
  // tunnel's own name matches.
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
    const totals = new Map<string, number>();
    for (const r of routes) {
      totals.set(r.tunnel_id, (totals.get(r.tunnel_id) ?? 0) + 1);
    }
    return tunnels
      .filter((t) => (!account || t.account_tag === account) && (byTunnel.has(t.id) || (q ? matches(t.name, t.id) : true)))
      .map((t) => {
        const list = byTunnel.get(t.id) ?? (q && matches(t.name, t.id) ? routes.filter((r) => r.tunnel_id === t.id) : []);
        const sel = list.filter((r) => selected.has(r.key)).length;
        return { t, list, total: totals.get(t.id) ?? 0, selection: (sel === 0 ? "none" : sel === list.length ? "all" : "some") as "all" | "some" | "none" };
      })
      .sort((a, b) => a.t.name.localeCompare(b.t.name, undefined, { sensitivity: "base", numeric: true }));
  })();

  const editorExtra = (
    <>
      <label className="check-row">
        <input type="checkbox" checked={grouped} onChange={(e) => table.setOption("group", e.target.checked)} />
        <span>Group by tunnel</span>
      </label>
      <label className="check-row">
        <input type="checkbox" checked={asCards} onChange={(e) => table.setOption("cards", e.target.checked)} />
        <span>Show as cards</span>
      </label>
    </>
  );

  const actionButtons = (
    <>
      {canManage && !noAccounts ? (
        <>
          <button className="btn btn-sm btn-primary" type="button" onClick={() => setRouteDialog({ route: null })}>
            Add hostname
          </button>
          <button className="btn btn-sm btn-secondary" type="button" onClick={() => setTunnelDialog(true)}>
            New tunnel
          </button>
        </>
      ) : null}
      {canCreds ? (
        <button className="btn btn-sm btn-secondary" type="button" onClick={() => setTokenDialog({ cred: null })}>
          Connect account
        </button>
      ) : null}
    </>
  );

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
      {(inv.errors ?? []).map((e) => (
        <p key={e} className="banner banner-warn" role="status">
          {e}
        </p>
      ))}
      {dnsBlocked.map((a) => (
        <div key={a.id} className="banner banner-warn cf-dns-warning" role="alert">
          <p>
            <strong>{a.dns_access === "denied" ? `DNS can't be read for ${a.name}` : `Some DNS in ${a.name} can't be read`}</strong>
          </p>
          <p>{a.dns_reason}</p>
          <p className="meta">
            Until then hostnames keep working and can still be added or edited, but their DNS status shows as not readable and No-dal can't create or
            fix their DNS records; add or point those in Cloudflare yourself. No-dal tries again on every refresh.
          </p>
          <div className="btn-row">
            <button type="button" className="btn btn-sm btn-primary" onClick={() => showPermissionsFor(a.credentials.map((c) => c.id))}>
              Show what to add
            </button>
            <button type="button" className="btn btn-sm btn-secondary" disabled={busy !== null} onClick={() => void act("Checking DNS access", () => reload(true))}>
              Try again
            </button>
          </div>
        </div>
      ))}
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
        </EmptyState>
      ) : null}
      {!noAccounts ? (
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
        </>
      ) : null}
      <div className="toolbar">
        <div className="toolbar-group">
          {!noAccounts ? (
            <Segmented<View>
              ariaLabel="Cloudflare view"
              value={view}
              onChange={setView}
              options={[
                { id: "hostnames", label: "Hostnames" },
                { id: "dns", label: "DNS" },
                { id: "accounts", label: "Accounts" },
              ]}
            />
          ) : null}
          {!noAccounts ? (
            <>
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
            </>
          ) : null}
          {actionButtons}
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

      {!noAccounts && view === "hostnames" && asCards && !(grouped ? groups.length === 0 : shownRoutes.length === 0) ? (
        <div className="cf-cards" aria-label="Public hostnames">
          <div className="cf-cards-bar">
            <span className="cf-gutter">
              {canManage ? (
                <input
                  type="checkbox"
                  aria-label="Select all hostnames"
                  checked={shownRoutes.length > 0 && shownRoutes.every((r) => selected.has(r.key))}
                  onChange={(e) => setSelected(e.target.checked ? new Set(shownRoutes.map((r) => r.key)) : new Set())}
                />
              ) : null}
            </span>
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
                  .filter((c) => c.sortValue && table.isVisible(c))
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
            <span className="theme-spacer" />
            <span className="meta">
              {shownRoutes.length} hostname{shownRoutes.length === 1 ? "" : "s"}
            </span>
            <ColumnEditor prefs={table as TablePrefs<CfRoute>} label="Edit hostname fields" extra={editorExtra} />
          </div>
          {grouped ? (
            groups.map((g) => (
              <section key={g.t.id} className="cf-card-group" aria-label={`Tunnel ${g.t.name}`}>
                <TunnelGroup
                  t={g.t}
                  count={g.total}
                  shown={g.list.length}
                  span={span}
                  collapsed={collapsed.has(g.t.id)}
                  selection={g.selection}
                  canManage={canManage}
                  multiAccount={multiAccount}
                  onCollapse={onCollapse}
                  onSelect={onSelectTunnel}
                  onMenu={onTunnelMenu}
                  itemsFor={tunnelItems}
                  asBar
                />
                {collapsed.has(g.t.id) ? null : g.list.length === 0 ? (
                  <p className="meta cf-card-empty">
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
                  <div className="cf-card-grid">
                    {g.list.map((r) => (
                      <RouteCard key={r.key} r={r} cols={cols} selected={selected.has(r.key)} canManage={canManage} onToggle={toggle} onMenu={onRouteMenu} itemsFor={routeItems} />
                    ))}
                  </div>
                )}
              </section>
            ))
          ) : (
            <div className="cf-card-grid">
              {shownRoutes.map((r) => (
                <RouteCard key={r.key} r={r} cols={cols} selected={selected.has(r.key)} canManage={canManage} onToggle={toggle} onMenu={onRouteMenu} itemsFor={routeItems} />
              ))}
            </div>
          )}
        </div>
      ) : !noAccounts && view === "hostnames" ? (
        (grouped ? groups.length === 0 : shownRoutes.length === 0) ? (
          <EmptyState title="No hostnames">{q || account ? "Nothing matches the filter." : "Add a public hostname to route a domain to a service."}</EmptyState>
        ) : (
          <div className="table-wrap">
            <table aria-label="Public hostnames" className={grouped ? "cf-grouped" : undefined}>
              <thead>
                <tr>
                  {canManage ? (
                    <th className="cell-check">
                      <input
                        type="checkbox"
                        aria-label="Select all hostnames"
                        checked={shownRoutes.length > 0 && shownRoutes.every((r) => selected.has(r.key))}
                        onChange={(e) => setSelected(e.target.checked ? new Set(shownRoutes.map((r) => r.key)) : new Set())}
                      />
                    </th>
                  ) : null}
                  {table.visible.map((c) => (
                    <SortHeader key={c.id} column={c} prefs={table} />
                  ))}
                  <th className="cell-actions">
                    <ColumnEditor prefs={table as TablePrefs<CfRoute>} label="Edit hostname columns" extra={editorExtra} />
                  </th>
                </tr>
              </thead>
              {grouped ? (
                groups.map((g) => (
                  <tbody key={g.t.id}>
                    <TunnelGroup
                      t={g.t}
                      count={g.total}
                      shown={g.list.length}
                      span={span}
                      collapsed={collapsed.has(g.t.id)}
                      selection={g.selection}
                      canManage={canManage}
                      multiAccount={multiAccount}
                      onCollapse={onCollapse}
                      onSelect={onSelectTunnel}
                      onMenu={onTunnelMenu}
                      itemsFor={tunnelItems}
                    />
                    {collapsed.has(g.t.id)
                      ? null
                      : g.list.length === 0
                        ? (
                            <tr className="cf-route-row is-muted">
                              <td colSpan={span + (canManage ? 1 : 0)}>
                                <span className="meta">No hostnames on this tunnel yet.</span>
                                {canManage && g.t.remote_managed ? (
                                  <>
                                    {" "}
                                    <button type="button" className="linkish" onClick={() => setRouteDialog({ route: null, tunnelId: g.t.id })}>
                                      Add one
                                    </button>
                                  </>
                                ) : null}
                              </td>
                            </tr>
                          )
                        : g.list.map((r) => (
                            <RouteRow
                              key={r.key}
                              r={r}
                              cols={cols}
                              selected={selected.has(r.key)}
                              canManage={canManage}
                              grouped
                              onToggle={toggle}
                              onMenu={onRouteMenu}
                              itemsFor={routeItems}
                            />
                          ))}
                  </tbody>
                ))
              ) : (
                <tbody>
                  {shownRoutes.map((r) => (
                    <RouteRow
                      key={r.key}
                      r={r}
                      cols={cols}
                      selected={selected.has(r.key)}
                      canManage={canManage}
                      grouped={false}
                      onToggle={toggle}
                      onMenu={onRouteMenu}
                      itemsFor={routeItems}
                    />
                  ))}
                </tbody>
              )}
            </table>
          </div>
        )
      ) : null}

      {!noAccounts && view === "dns" ? (
        <>
          <p className="field-hint">
            Records that send a hostname to a tunnel. A record with no route returns 404; one pointing at an unknown tunnel returns error 1016. Remove those
            records in DNS once you no longer need them.
            {counts.unreadable ? " Zones the saved tokens cannot read are not listed; see Accounts > Permissions." : ""}
          </p>
          <div className="table-wrap">
            <table aria-label="Tunnel DNS records">
              <thead>
                <tr>
                  <th>Name</th>
                  <th>Zone</th>
                  {multiAccount ? <th>Account</th> : null}
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
                    {multiAccount ? <td>{d.account_name}</td> : null}
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
                    {(a.problems ?? []).map((p) => (
                      <div key={p} className="tunnel-reason">
                        {p}
                      </div>
                    ))}
                  </li>
                ))}
              </ul>
              <CfPermissions
                cred={c}
                open={permsOpen.has(c.id)}
                onToggle={() =>
                  setPermsOpen((cur) => {
                    const next = new Set(cur);
                    if (next.has(c.id)) {
                      next.delete(c.id);
                    } else {
                      next.add(c.id);
                    }
                    return next;
                  })
                }
                onCheckAgain={canManage ? () => void act("Checking token", () => validateCfCredential(c.id), `${c.name} checked again.`) : undefined}
              />
            </article>
          ))}
        </div>
      ) : null}

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
          setNotice(`Tunnel ${name} created. Deploy a connector for it from its row, then add hostnames.`);
          setView("hostnames");
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
