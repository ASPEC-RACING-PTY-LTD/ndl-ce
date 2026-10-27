import { useCallback, useEffect, useMemo, useState } from "react";
import {
  applyDDoS,
  banDDoS,
  createDDoSTarget,
  deleteDDoSTarget,
  getDDoS,
  listDDoSEvents,
  listWorkloads,
  putDDoSSettings,
  unbanDDoS,
  updateDDoSTarget,
  type DDoSEvent,
  type DDoSLimits,
  type DDoSOverview,
  type DDoSProfile,
  type DDoSProtocol,
  type DDoSSample,
  type DDoSSettings,
  type DDoSTarget,
  type DDoSTargetInput,
  type DDoSTargetKind,
} from "../api/client";
import { confirmAction } from "../confirm";
import { ActionMenu, type ActionItem } from "../components/ActionMenu";
import { EmptyState, LoadingState } from "../components/EmptyState";
import { ErrorNotice } from "../components/ErrorNotice";
import { Link } from "../components/Link";
import { PageHeader } from "../components/PageHeader";
import { StatusBadge } from "../components/StatusBadge";
import { formatBytes, formatRelative } from "../format";
import { listGameServers } from "../gameservers/api";
import type { GameServer } from "../gameservers/types";
import { usePoll } from "../query";
import { navigate } from "../router";
import { hasGrant } from "../rbac";
import { useSession } from "../session";
import { Dialog } from "../ui/Dialog";
import { Segmented } from "../ui/Segmented";

// DDoS protection. Rules are nftables tables on this node's uplink, so
// floods are dropped before they reach a game server, a workload or the
// host. Targets are game servers (protected by default), workloads (by
// their address), host services (by port) and custom destinations.

type Tab = "targets" | "bans" | "attacks" | "settings";

const KIND_LABEL: Record<DDoSTargetKind, string> = {
  gameserver: "Game server",
  workload: "Workload",
  host: "Host service",
  custom: "Custom",
};

const PROTO_LABEL: Record<DDoSProtocol, string> = { udp: "UDP", tcp: "TCP", both: "UDP and TCP", any: "Every protocol" };

/** REASON_LABEL says why packets were dropped. */
export const REASON_LABEL: Record<string, string> = {
  block: "Blocked networks",
  ban: "Banned sources",
  bad: "Malformed packets",
  amp: "Reflection traffic",
  query: "Server query floods",
  syn: "SYN floods",
  flood: "Per-source floods",
  cap: "Total packet cap",
  conns: "Too many connections",
};

const num = new Intl.NumberFormat("en");
const n = (v?: number) => num.format(Math.round(v ?? 0));
const errText = (err: unknown, fallback: string) => (err instanceof Error ? err.message : fallback);
const lines = (v: string) =>
  v
    .split(/[\s,]+/)
    .map((x) => x.trim())
    .filter(Boolean);

function duration(secs: number): string {
  if (secs < 60) {
    return `${secs}s`;
  }
  if (secs < 3600) {
    return `${Math.round(secs / 60)} min`;
  }
  if (secs < 86400) {
    return `${(secs / 3600).toFixed(1)} h`;
  }
  return `${(secs / 86400).toFixed(1)} days`;
}

/** Sparkline draws traffic in (line) and dropped (filled) over the last hour. */
function Sparkline({ samples }: { samples?: DDoSSample[] }) {
  if (!samples || samples.length < 2) {
    return <span className="meta">Collecting</span>;
  }
  const w = 120;
  const h = 28;
  const max = Math.max(1, ...samples.map((s) => Math.max(s.in_pps, s.drop_pps)));
  const x = (i: number) => (i / (samples.length - 1)) * w;
  const y = (v: number) => h - (v / max) * (h - 2) - 1;
  const inLine = samples.map((s, i) => `${x(i).toFixed(1)},${y(s.in_pps).toFixed(1)}`).join(" ");
  const drop = `0,${h} ` + samples.map((s, i) => `${x(i).toFixed(1)},${y(s.drop_pps).toFixed(1)}`).join(" ") + ` ${w},${h}`;
  return (
    <svg className="ddos-spark" width={w} height={h} viewBox={`0 0 ${w} ${h}`} role="img" aria-label={`Peak ${n(max)} packets per second in the last hour`}>
      <polygon points={drop} className="ddos-spark-drop" />
      <polyline points={inLine} className="ddos-spark-in" fill="none" />
    </svg>
  );
}

function targetWhere(t: DDoSTarget): string {
  const ports = t.rule?.ports?.length ? t.rule.ports : t.ports;
  const p = ports && ports.length ? ports.join(", ") : "every port";
  const proto = t.protocol === "any" ? "" : `${PROTO_LABEL[t.protocol]} `;
  if (t.kind === "workload" || t.kind === "custom") {
    const dest = t.rule?.dest?.length ? t.rule.dest : t.dest;
    return `${(dest ?? []).join(", ") || "no address yet"} · ${proto}${p}`;
  }
  return `This node · ${proto}${p}`;
}

function targetStatus(t: DDoSTarget): { status: string; label: string } {
  if (!t.enabled) {
    return { status: "stopped", label: "Off" };
  }
  if (t.traffic.under_attack) {
    return { status: "critical", label: "Under attack" };
  }
  if (t.warnings?.length || !t.rule) {
    return { status: "warning", label: "Not protected" };
  }
  return { status: "ok", label: "Protected" };
}

function refHref(t: DDoSTarget): string | null {
  if (!t.ref_id) {
    return null;
  }
  return t.kind === "gameserver" ? `/game-servers/${t.ref_id}` : t.kind === "workload" ? `/workloads/${t.ref_id}` : null;
}

/** DDoSPage is Network > DDoS Protection. */
export function DDoSPage() {
  const session = useSession();
  const user = session.status === "ready" ? session.user : null;
  const canManage = hasGrant(user, "ddos.manage");
  const [data, setData] = useState<DDoSOverview | null>(null);
  const [events, setEvents] = useState<DDoSEvent[]>([]);
  const [tab, setTab] = useState<Tab>("targets");
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [editing, setEditing] = useState<{ target: DDoSTarget | null } | null>(null);
  const [banning, setBanning] = useState(false);

  const reload = useCallback(async () => {
    try {
      const [d, e] = await Promise.all([getDDoS(), listDDoSEvents().catch(() => ({ items: [] as DDoSEvent[] }))]);
      setData(d);
      setEvents(e.items);
      setError(null);
    } catch (err) {
      setError(errText(err, "DDoS protection could not be loaded"));
    }
  }, []);
  useEffect(() => {
    void reload();
  }, [reload]);
  usePoll(reload, 15000);

  async function act(label: string, fn: () => Promise<DDoSOverview | void>, done?: string) {
    setBusy(label);
    setError(null);
    try {
      const res = await fn();
      if (res) {
        setData(res);
      }
      if (done) {
        setNotice(done);
      }
      void reload();
    } catch (err) {
      setError(errText(err, `${label} failed`));
    } finally {
      setBusy(null);
    }
  }

  const targets = useMemo(() => data?.targets ?? [], [data]);
  if (!data) {
    return error ? <ErrorNotice error={error} /> : <LoadingState label="Loading DDoS protection" />;
  }
  const st = data.status;
  const protectedCount = targets.filter((t) => t.enabled && t.rule).length;
  const dropNow = targets.reduce((a, t) => a + (t.traffic.drop_pps || 0), 0);
  const inNow = targets.reduce((a, t) => a + (t.traffic.in_pps || 0), 0);
  const attacked = targets.filter((t) => t.traffic.under_attack);
  const monitor = data.settings.mode === "monitor";

  const targetItems = (t: DDoSTarget): ActionItem[] => {
    const items: ActionItem[] = [];
    const href = refHref(t);
    if (href) {
      items.push({ label: `Open ${t.ref_name || t.name}`, onClick: () => navigate(href) });
    }
    if (!canManage) {
      return items;
    }
    items.push({ label: "Edit protection", onClick: () => setEditing({ target: t }) });
    items.push({
      label: t.enabled ? "Turn protection off" : "Turn protection on",
      onClick: () => void act(t.enabled ? "Turning off" : "Turning on", () => updateDDoSTarget(t.id, { ...inputFrom(t), enabled: !t.enabled })),
    });
    if (!t.auto) {
      items.push({
        label: t.kind === "gameserver" ? "Reset to default" : "Remove",
        danger: t.kind !== "gameserver",
        onClick: () => {
          const msg =
            t.kind === "gameserver"
              ? `Reset ${t.name} to the default protection for its game?`
              : `Stop protecting ${t.name}? Its rules are removed from the filter.`;
          if (confirmAction(msg)) {
            void act("Removing", () => deleteDDoSTarget(t.id));
          }
        },
      });
    }
    return items;
  };

  return (
    <section className="page page-wide" aria-labelledby="ddos-heading">
      <PageHeader
        id="ddos-heading"
        title="DDoS Protection"
        kicker="Filters floods on this node's uplink before they reach game servers, workloads or the host. Limits are per source address, so real players keep playing while an attacker is dropped."
        actions={
          <div className="btn-row is-flush">
            <button className="btn btn-secondary" type="button" onClick={() => void reload()}>
              Refresh
            </button>
            {canManage ? (
              <button className="btn btn-secondary" type="button" disabled={busy != null} onClick={() => void act("Applying", applyDDoS, "Rules re-applied.")}>
                Apply now
              </button>
            ) : null}
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
      {attacked.length > 0 ? (
        <p className="banner banner-error" role="alert">
          Attack in progress on {attacked.map((t) => t.name).join(", ")}: dropping {n(attacked.reduce((a, t) => a + t.traffic.drop_pps, 0))} packets per second.
          {monitor ? " Monitor mode is on, so nothing is being dropped; switch to Enforce in Settings." : " The filter is holding it back."}
        </p>
      ) : null}
      {monitor ? (
        <p className="banner banner-warn" role="status">
          Monitor mode: traffic is counted against the limits but nothing is dropped or banned. Switch to Enforce in Settings once the numbers look right.
        </p>
      ) : null}
      {st.error ? <ErrorNotice title="The filter rules are not loaded" error={st.error} /> : null}
      <div className="status-strip">
        <div className="status-tile">
          <span className="label">Filter</span>
          <span className="value">
            <StatusBadge status={st.active ? (monitor ? "warning" : "ok") : "failed"} label={st.active ? (monitor ? "Monitoring" : "Enforcing") : "Not loaded"} />
          </span>
          <span className="meta">{st.devices?.length ? `On ${st.devices.join(", ")}` : "No uplink yet"}</span>
        </div>
        <div className="status-tile">
          <span className="label">Protected</span>
          <span className="value">{protectedCount}</span>
          <span className="meta">of {targets.length} targets</span>
        </div>
        <div className="status-tile">
          <span className="label">Traffic now</span>
          <span className="value">{n(inNow)}/s</span>
          <span className="meta">{n(dropNow)}/s dropped</span>
        </div>
        <div className="status-tile">
          <span className="label">Banned</span>
          <span className="value">{data.bans.length}</span>
          <span className="meta">{n(st.banned.packets + st.blocked.packets)} packets from bans and blocks</span>
        </div>
        <div className="status-tile">
          <span className="label">Connections</span>
          <span className="value">{n(st.conntrack.count)}</span>
          <span className="meta">of {n(st.conntrack.max)} tracked</span>
        </div>
      </div>
      {data.edges?.length ? (
        <div className="ddos-edges">
          {data.edges.map((e) => (
            <p key={e.node_id} className={e.error ? "banner banner-warn" : "meta"}>
              Also filtering on edge node <strong>{e.name}</strong>
              {e.targets.length ? ` for ${e.targets.join(", ")}` : ""}:{" "}
              {e.error ? e.error : e.active ? `${e.mode === "monitor" ? "monitoring" : "enforcing"}${e.devices?.length ? ` on ${e.devices.join(", ")}` : ""}` : "not loaded"}. Floods to
              its public address are dropped there, before they enter the tunnel.
            </p>
          ))}
        </div>
      ) : null}
      <div className="hub-tabs">
        <Segmented<Tab>
          ariaLabel="DDoS protection sections"
          value={tab}
          onChange={setTab}
          options={[
            { id: "targets", label: "Targets" },
            { id: "bans", label: `Bans${data.bans.length ? ` (${data.bans.length})` : ""}` },
            { id: "attacks", label: "Attacks" },
            { id: "settings", label: "Settings" },
          ]}
        />
      </div>
      {tab === "targets" ? (
        <>
          <div className="toolbar">
            <div className="toolbar-group">
              {canManage ? (
                <button className="btn btn-sm btn-primary" type="button" onClick={() => setEditing({ target: null })}>
                  Protect a service
                </button>
              ) : null}
            </div>
          </div>
          {targets.length === 0 ? (
            <EmptyState title="Nothing is protected yet">
              Game servers are protected automatically when Game Servers is on. Add a workload, a host service or your own destination with Protect a service.
            </EmptyState>
          ) : (
            <div className="table-wrap">
              <table className="data-table">
                <thead>
                  <tr>
                    <th>Target</th>
                    <th>Where</th>
                    <th>Profile</th>
                    <th>Last hour</th>
                    <th>Now</th>
                    <th>Dropped</th>
                    <th>Status</th>
                    <th className="cell-actions">
                      <span className="visually-hidden">Actions</span>
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {targets.map((t) => {
                    const s = targetStatus(t);
                    const href = refHref(t);
                    const prof = data.profiles.find((p) => p.id === t.profile);
                    const reasons = Object.entries(t.traffic.reasons ?? {}).sort((a, b) => b[1].packets - a[1].packets);
                    return (
                      <tr key={t.id}>
                        <td>
                          {href ? <Link href={href}>{t.name}</Link> : t.name}
                          <div className="meta">
                            {KIND_LABEL[t.kind]}
                            {t.auto ? " · protected by default" : ""}
                          </div>
                          {t.warnings?.map((w) => (
                            <div key={w} className="meta ddos-warning">
                              {w}
                            </div>
                          ))}
                        </td>
                        <td>{targetWhere(t)}</td>
                        <td>
                          {prof?.title ?? t.profile}
                          <div className="meta">{t.action === "ban" ? `Bans for ${duration(t.ban_seconds || 300)}` : "Drops the excess"}</div>
                        </td>
                        <td>
                          <Sparkline samples={t.history} />
                        </td>
                        <td>
                          {n(t.traffic.in_pps)}/s
                          <div className="meta">{n(t.traffic.drop_pps)}/s dropped</div>
                        </td>
                        <td title={reasons.map(([r, c]) => `${REASON_LABEL[r] ?? r}: ${n(c.packets)}`).join("\n")}>
                          {n(t.traffic.dropped_packets)}
                          <div className="meta">
                            {reasons.length ? `${REASON_LABEL[reasons[0][0]] ?? reasons[0][0]}` : formatBytes(t.traffic.dropped_bytes)}
                            {t.traffic.bans ? ` · ${t.traffic.bans} banned` : ""}
                          </div>
                        </td>
                        <td>
                          <StatusBadge status={s.status} label={s.label} />
                        </td>
                        <td className="cell-actions">
                          <ActionMenu items={targetItems(t)} />
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          )}
        </>
      ) : null}
      {tab === "bans" ? (
        <BansTab data={data} canManage={canManage} busy={busy} onBan={() => setBanning(true)} onUnban={(addr) => void act("Unbanning", () => unbanDDoS(addr), `${addr} can connect again.`)} />
      ) : null}
      {tab === "attacks" ? <AttacksTab events={events} /> : null}
      {tab === "settings" ? (
        <SettingsTab
          key={JSON.stringify(data.settings)}
          data={data}
          canManage={canManage}
          busy={busy}
          onSave={(body) => void act("Saving settings", () => putDDoSSettings(body), "Settings saved and applied.")}
        />
      ) : null}
      <p className="meta ddos-footnote">
        This filter runs on this node and on edge nodes that forward to it (<Link href="/network/edge">Edge forwarding</Link>), so it stops attacks their uplinks can carry. A flood bigger than your connection fills it before it gets here, and only your provider or a
        scrubbing service upstream can stop that. Web services can also be put behind <Link href="/network/cloudflare">Cloudflare</Link>.
      </p>
      <TargetDialog
        state={editing}
        profiles={data.profiles}
        targets={targets}
        onClose={() => setEditing(null)}
        onSaved={(res, msg) => {
          setData(res);
          setEditing(null);
          setNotice(msg);
          void reload();
        }}
      />
      <BanDialog
        open={banning}
        onClose={() => setBanning(false)}
        onSaved={(res, msg) => {
          setData(res);
          setBanning(false);
          setNotice(msg);
        }}
      />
    </section>
  );
}

function BansTab({ data, canManage, busy, onBan, onUnban }: { data: DDoSOverview; canManage: boolean; busy: string | null; onBan: () => void; onUnban: (addr: string) => void }) {
  return (
    <>
      <div className="toolbar">
        <div className="toolbar-group">
          {canManage ? (
            <button className="btn btn-sm btn-primary" type="button" onClick={onBan}>
              Ban an address
            </button>
          ) : null}
        </div>
      </div>
      {data.bans.length === 0 ? (
        <EmptyState title="No one is banned">Sources over a target's limit are banned from that target for a while. Bans you add here cover every target.</EmptyState>
      ) : (
        <div className="table-wrap">
          <table className="data-table">
            <thead>
              <tr>
                <th>Address</th>
                <th>Banned from</th>
                <th>Time left</th>
                <th>Why</th>
                <th className="cell-actions">
                  <span className="visually-hidden">Actions</span>
                </th>
              </tr>
            </thead>
            <tbody>
              {data.bans.map((b) => (
                <tr key={`${b.addr}/${b.target_id ?? ""}`}>
                  <td>
                    <code>{b.addr}</code>
                    {b.addr.endsWith("::") ? <div className="meta">The whole /64</div> : null}
                  </td>
                  <td>{b.manual ? "Every target" : b.target || "Removed target"}</td>
                  <td>{duration(b.seconds)}</td>
                  <td>{b.manual ? [b.reason || "Added by hand", b.by ? `by ${b.by}` : ""].filter(Boolean).join(" ") : "Over the per-source limit"}</td>
                  <td className="cell-actions">
                    {canManage ? (
                      <button className="btn btn-sm btn-secondary" type="button" disabled={busy != null} onClick={() => onUnban(b.addr)}>
                        Unban
                      </button>
                    ) : null}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </>
  );
}

function AttacksTab({ events }: { events: DDoSEvent[] }) {
  if (events.length === 0) {
    return <EmptyState title="No attacks recorded">An attack is recorded when a target drops more packets per second than the attack threshold in Settings.</EmptyState>;
  }
  return (
    <div className="table-wrap">
      <table className="data-table">
        <thead>
          <tr>
            <th>Target</th>
            <th>Started</th>
            <th>Lasted</th>
            <th>Peak</th>
            <th>Dropped</th>
            <th>Mostly</th>
            <th>Banned sources</th>
          </tr>
        </thead>
        <tbody>
          {events.map((e) => {
            const end = e.ended_at ? new Date(e.ended_at).getTime() : Date.now();
            return (
              <tr key={e.id}>
                <td>{e.target}</td>
                <td title={e.started_at}>{formatRelative(e.started_at)}</td>
                <td>{e.ended_at ? duration(Math.round((end - new Date(e.started_at).getTime()) / 1000)) : <StatusBadge status="critical" label="Ongoing" />}</td>
                <td>{n(e.peak_pps)}/s</td>
                <td>
                  {n(e.dropped_packets)}
                  <div className="meta">{formatBytes(e.dropped_bytes)}</div>
                </td>
                <td>{REASON_LABEL[e.top_reason] ?? (e.top_reason || "-")}</td>
                <td>{e.sources}</td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

function SettingsTab({ data, canManage, busy, onSave }: { data: DDoSOverview; canManage: boolean; busy: string | null; onSave: (body: Partial<DDoSSettings>) => void }) {
  const s = data.settings;
  const [mode, setMode] = useState(s.mode);
  const [auto, setAuto] = useState(s.auto_game_servers);
  const [trustLocal, setTrustLocal] = useState(s.trust_local);
  const [trusted, setTrusted] = useState((s.trusted ?? []).join("\n"));
  const [blocked, setBlocked] = useState((s.blocked ?? []).join("\n"));
  const [ifaces, setIfaces] = useState((s.interfaces ?? []).join(" "));
  const [invalid, setInvalid] = useState(s.drop_invalid_tcp);
  const [hardening, setHardening] = useState(s.hardening);
  const [attack, setAttack] = useState(String(s.attack_pps));
  const [notify, setNotify] = useState(s.notify);
  const off = !canManage;
  return (
    <form
      className="ddos-settings"
      onSubmit={(e) => {
        e.preventDefault();
        onSave({
          mode,
          auto_game_servers: auto,
          trust_local: trustLocal,
          trusted: lines(trusted),
          blocked: lines(blocked),
          interfaces: lines(ifaces),
          drop_invalid_tcp: invalid,
          hardening,
          attack_pps: Number(attack) || 1000,
          notify,
        });
      }}
    >
      <fieldset className="field" disabled={off}>
        <legend className="field-label">Mode</legend>
        <Segmented<"enforce" | "monitor">
          ariaLabel="Mode"
          value={mode}
          onChange={setMode}
          options={[
            { id: "enforce", label: "Enforce" },
            { id: "monitor", label: "Monitor only" },
          ]}
        />
        <p className="field-hint">Monitor counts what would be dropped without dropping it. Use it to check limits against real traffic first.</p>
      </fieldset>
      <label className="check-row">
        <input type="checkbox" checked={auto} disabled={off} onChange={(e) => setAuto(e.target.checked)} />
        <span>
          Protect every game server automatically
          <span className="field-hint">Each game server gets the profile for its game (Source engine for Garry's Mod) on its published ports.</span>
        </span>
      </label>
      <label className="check-row">
        <input type="checkbox" checked={trustLocal} disabled={off} onChange={(e) => setTrustLocal(e.target.checked)} />
        <span>
          Always trust this node's own networks
          <span className="field-hint">Your LAN is never limited or banned. If your router rewrites the source of port-forwarded traffic, every player looks local; turn this off then.</span>
        </span>
      </label>
      <div className="field">
        <label className="field-label" htmlFor="ddos-trusted">
          Trusted networks
        </label>
        <textarea id="ddos-trusted" className="field-input" rows={3} value={trusted} disabled={off} placeholder="One address or CIDR per line" onChange={(e) => setTrusted(e.target.value)} />
        <p className="field-hint">Always let through and never banned: your own IP, monitoring, a game panel provider.</p>
      </div>
      <div className="field">
        <label className="field-label" htmlFor="ddos-blocked">
          Blocked networks
        </label>
        <textarea id="ddos-blocked" className="field-input" rows={3} value={blocked} disabled={off} placeholder="One address or CIDR per line" onChange={(e) => setBlocked(e.target.value)} />
        <p className="field-hint">Dropped on every target, for good.</p>
      </div>
      <div className="field">
        <label className="field-label" htmlFor="ddos-ifaces">
          Filter on interfaces
        </label>
        <input id="ddos-ifaces" className="field-input" value={ifaces} disabled={off} placeholder={data.status.devices?.join(" ") || "Found automatically"} onChange={(e) => setIfaces(e.target.value)} />
        <p className="field-hint">Leave empty to use the uplink behind the default route{data.status.devices?.length ? ` (now ${data.status.devices.join(", ")})` : ""}. A bridge is filtered on its physical port.</p>
      </div>
      <label className="check-row">
        <input type="checkbox" checked={invalid} disabled={off} onChange={(e) => setInvalid(e.target.checked)} />
        <span>
          Drop impossible TCP packets
          <span className="field-hint">NULL, XMAS, SYN+FIN and SYN+RST scans and floods to protected targets.</span>
        </span>
      </label>
      <label className="check-row">
        <input type="checkbox" checked={hardening} disabled={off} onChange={(e) => setHardening(e.target.checked)} />
        <span>
          Harden the kernel against floods
          <span className="field-hint">SYN cookies, a bigger SYN backlog and connection table. Settings are only made stricter and are put back when this is off.</span>
        </span>
      </label>
      {data.status.hardening?.length ? (
        <dl className="definition-list compact">
          {data.status.hardening.map((h) => (
            <div key={h.key} title={h.reason}>
              <dt>{h.key}</dt>
              <dd>
                {h.value}
                {h.value !== h.want ? <span className="meta"> (wants {h.want})</span> : null}
              </dd>
            </div>
          ))}
        </dl>
      ) : null}
      <div className="field">
        <label className="field-label" htmlFor="ddos-attack">
          Attack threshold (dropped packets per second)
        </label>
        <input id="ddos-attack" className="field-input" type="number" min={10} value={attack} disabled={off} onChange={(e) => setAttack(e.target.value)} />
      </div>
      <label className="check-row">
        <input type="checkbox" checked={notify} disabled={off} onChange={(e) => setNotify(e.target.checked)} />
        <span>Notify me in the bell when an attack starts and ends</span>
      </label>
      {canManage ? (
        <div className="btn-row">
          <button className="btn btn-primary" type="submit" disabled={busy != null}>
            {busy === "Saving settings" ? "Saving" : "Save settings"}
          </button>
        </div>
      ) : null}
    </form>
  );
}

function inputFrom(t: DDoSTarget): DDoSTargetInput {
  return {
    name: t.name,
    kind: t.kind,
    ref_id: t.ref_id,
    enabled: t.enabled,
    profile: t.profile,
    protocol: t.protocol,
    ports: t.auto ? [] : (t.ports ?? []),
    dest: t.dest ?? [],
    limits: t.limits,
    action: t.action,
    ban_seconds: t.ban_seconds,
    allow: t.allow ?? [],
    block: t.block ?? [],
  };
}

const LIMIT_FIELDS: { key: keyof DDoSLimits; label: string; hint: string; tcp?: boolean; udp?: boolean }[] = [
  { key: "per_source_pps", label: "Packets per second per source", hint: "Over this, a source is dropped (and banned with Ban)." },
  { key: "per_source_burst", label: "Burst per source", hint: "Short spikes allowed over the rate. Empty is twice the rate." },
  { key: "query_per_source", label: "Server queries per second per source", hint: "Steam A2S queries and connect challenges.", udp: true },
  { key: "syn_per_source", label: "New connections per second per source", hint: "TCP SYN rate.", tcp: true },
  { key: "conns_per_source", label: "Open connections per source", hint: "TCP connections at once.", tcp: true },
  { key: "total_pps", label: "Total packets per second", hint: "Cap for everyone together. Last resort; real players over it are dropped too." },
  { key: "min_udp_payload", label: "Smallest UDP payload (bytes)", hint: "Drops empty and tiny UDP packets.", udp: true },
  { key: "max_packet", label: "Largest packet (bytes)", hint: "Drops IP packets over this size." },
];

function TargetDialog({
  state,
  profiles,
  targets,
  onClose,
  onSaved,
}: {
  state: { target: DDoSTarget | null } | null;
  profiles: DDoSProfile[];
  targets: DDoSTarget[];
  onClose: () => void;
  onSaved: (res: DDoSOverview, msg: string) => void;
}) {
  const t = state?.target ?? null;
  const [form, setForm] = useState<DDoSTargetInput>({ name: "", kind: "gameserver", profile: "source" });
  const [ports, setPorts] = useState("");
  const [dest, setDest] = useState("");
  const [allow, setAllow] = useState("");
  const [block, setBlock] = useState("");
  const [games, setGames] = useState<GameServer[]>([]);
  const [workloads, setWorkloads] = useState<{ id: string; name: string; kind: string }[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!state) {
      return;
    }
    const init = t ? inputFrom(t) : { name: "", kind: "gameserver" as DDoSTargetKind, profile: "source", enabled: true };
    if (!t) {
      const p = profiles.find((x) => x.id === init.profile);
      Object.assign(init, { protocol: p?.protocol, action: p?.action, limits: { ...p?.limits } });
    }
    setForm(init);
    setPorts((init.ports ?? []).join(", "));
    setDest((init.dest ?? []).join("\n"));
    setAllow((init.allow ?? []).join("\n"));
    setBlock((init.block ?? []).join("\n"));
    setError(null);
    void listGameServers()
      .then((r) => setGames(r.items))
      .catch(() => setGames([]));
    void listWorkloads()
      .then((r) => setWorkloads(r.items.map((w) => ({ id: w.id, name: w.name, kind: w.kind }))))
      .catch(() => setWorkloads([]));
  }, [state, t, profiles]);

  const set = (patch: Partial<DDoSTargetInput>) => setForm((f) => ({ ...f, ...patch }));
  const setLimit = (key: keyof DDoSLimits, v: number | boolean) => setForm((f) => ({ ...f, limits: { ...f.limits, [key]: v } }));
  const pickProfile = (id: string) => {
    const p = profiles.find((x) => x.id === id);
    set({ profile: id, protocol: p?.protocol ?? form.protocol, action: p?.action ?? form.action, limits: { ...p?.limits } });
  };
  const taken = new Set(targets.filter((x) => !x.auto && x.id !== t?.id).map((x) => `${x.kind}/${x.ref_id}`));
  const proto = form.protocol ?? "both";
  const udp = proto !== "tcp";
  const tcp = proto !== "udp";
  const profile = profiles.find((p) => p.id === form.profile);
  const needsRef = form.kind === "gameserver" || form.kind === "workload";

  async function save() {
    setBusy(true);
    setError(null);
    const body: DDoSTargetInput = { ...form, ports: lines(ports), dest: lines(dest), allow: lines(allow), block: lines(block) };
    if (!body.name.trim()) {
      body.name =
        (form.kind === "gameserver" ? games.find((g) => g.id === form.ref_id)?.name : form.kind === "workload" ? workloads.find((w) => w.id === form.ref_id)?.name : "") || KIND_LABEL[form.kind];
    }
    try {
      const res = t ? await updateDDoSTarget(t.id, body) : await createDDoSTarget(body);
      onSaved(res, t ? `${body.name} saved and applied.` : `${body.name} is protected.`);
    } catch (err) {
      setError(errText(err, "Saving failed"));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog
      open={state != null}
      wide
      title={t ? `Protection for ${t.name}` : "Protect a service"}
      onClose={onClose}
      footer={
        <div className="btn-row">
          <button className="btn btn-ghost" type="button" onClick={onClose}>
            Cancel
          </button>
          <button className="btn btn-primary" type="button" disabled={busy || (needsRef && !form.ref_id)} onClick={() => void save()}>
            {busy ? "Saving and applying" : "Save"}
          </button>
        </div>
      }
    >
      {error ? <ErrorNotice error={error} /> : null}
      <div className="field-row">
        <div className="field">
          <label className="field-label" htmlFor="ddos-kind">
            What to protect
          </label>
          <select id="ddos-kind" className="field-input" value={form.kind} disabled={Boolean(t)} onChange={(e) => set({ kind: e.target.value as DDoSTargetKind, ref_id: "" })}>
            {(Object.keys(KIND_LABEL) as DDoSTargetKind[]).map((k) => (
              <option key={k} value={k}>
                {KIND_LABEL[k]}
              </option>
            ))}
          </select>
        </div>
        {form.kind === "gameserver" ? (
          <div className="field">
            <label className="field-label" htmlFor="ddos-ref">
              Game server
            </label>
            <select id="ddos-ref" className="field-input" value={form.ref_id ?? ""} disabled={Boolean(t)} onChange={(e) => set({ ref_id: e.target.value })}>
              <option value="">Choose</option>
              {games.map((g) => (
                <option key={g.id} value={g.id} disabled={taken.has(`gameserver/${g.id}`)}>
                  {g.name} ({g.template_name || g.game})
                </option>
              ))}
            </select>
          </div>
        ) : null}
        {form.kind === "workload" ? (
          <div className="field">
            <label className="field-label" htmlFor="ddos-ref">
              Workload
            </label>
            <select id="ddos-ref" className="field-input" value={form.ref_id ?? ""} disabled={Boolean(t)} onChange={(e) => set({ ref_id: e.target.value })}>
              <option value="">Choose</option>
              {workloads.map((w) => (
                <option key={w.id} value={w.id} disabled={taken.has(`workload/${w.id}`)}>
                  {w.name}
                </option>
              ))}
            </select>
          </div>
        ) : null}
        <div className="field">
          <label className="field-label" htmlFor="ddos-name">
            Name
          </label>
          <input id="ddos-name" className="field-input" value={form.name} placeholder="Shown on this page and in alerts" onChange={(e) => set({ name: e.target.value })} />
        </div>
      </div>
      <div className="field">
        <label className="field-label" htmlFor="ddos-profile">
          Profile
        </label>
        <select id="ddos-profile" className="field-input" value={form.profile} onChange={(e) => pickProfile(e.target.value)}>
          {profiles.map((p) => (
            <option key={p.id} value={p.id}>
              {p.title}
            </option>
          ))}
        </select>
        {profile ? <p className="field-hint">{profile.summary} Choosing a profile fills in its limits; change any of them below.</p> : null}
      </div>
      <div className="field-row">
        <div className="field">
          <label className="field-label" htmlFor="ddos-proto">
            Protocol
          </label>
          <select id="ddos-proto" className="field-input" value={proto} onChange={(e) => set({ protocol: e.target.value as DDoSProtocol })}>
            {(Object.keys(PROTO_LABEL) as DDoSProtocol[]).map((p) => (
              <option key={p} value={p}>
                {PROTO_LABEL[p]}
              </option>
            ))}
          </select>
        </div>
        <div className="field">
          <label className="field-label" htmlFor="ddos-ports">
            Ports
          </label>
          <input
            id="ddos-ports"
            className="field-input"
            value={ports}
            placeholder={form.kind === "gameserver" ? "Its published ports" : form.kind === "host" ? "For example 8080, 443" : "Every port"}
            onChange={(e) => setPorts(e.target.value)}
          />
          <p className="field-hint">Ports or ranges like 27015-27020, separated by commas.</p>
        </div>
      </div>
      {form.kind === "custom" ? (
        <div className="field">
          <label className="field-label" htmlFor="ddos-dest">
            Destinations
          </label>
          <textarea id="ddos-dest" className="field-input" rows={3} value={dest} placeholder="One address or CIDR per line" onChange={(e) => setDest(e.target.value)} />
          <p className="field-hint">Traffic to these addresses, reaching this node's uplink, is filtered.</p>
        </div>
      ) : null}
      <div className="field-row">
        <div className="field">
          <label className="field-label" htmlFor="ddos-action">
            Over a per-source limit
          </label>
          <select id="ddos-action" className="field-input" value={form.action ?? "drop"} onChange={(e) => set({ action: e.target.value as "ban" | "drop" })}>
            <option value="ban">Ban the source from this target</option>
            <option value="drop">Drop only the excess</option>
          </select>
        </div>
        {form.action === "ban" ? (
          <div className="field">
            <label className="field-label" htmlFor="ddos-ban">
              Ban for (minutes)
            </label>
            <input
              id="ddos-ban"
              className="field-input"
              type="number"
              min={1}
              value={Math.round((form.ban_seconds || 300) / 60)}
              onChange={(e) => set({ ban_seconds: Math.max(1, Number(e.target.value) || 5) * 60 })}
            />
          </div>
        ) : null}
      </div>
      <p className="field-hint">
        Floods can fake a player's address. A ban only covers this target and ends by itself, so a faked ban cannot lock a player out of everything for long.
      </p>
      <fieldset className="ddos-limits">
        <legend className="field-label">Limits (0 or empty turns one off)</legend>
        <div className="ddos-limit-grid">
          {LIMIT_FIELDS.filter((f) => (!f.tcp || tcp) && (!f.udp || udp)).map((f) => (
            <div className="field" key={f.key}>
              <label className="field-label" htmlFor={`ddos-l-${f.key}`}>
                {f.label}
              </label>
              <input
                id={`ddos-l-${f.key}`}
                className="field-input"
                type="number"
                min={0}
                value={(form.limits?.[f.key] as number | undefined) || ""}
                onChange={(e) => setLimit(f.key, Math.max(0, Number(e.target.value) || 0))}
              />
              <p className="field-hint">{f.hint}</p>
            </div>
          ))}
        </div>
        {udp ? (
          <>
            <label className="check-row">
              <input type="checkbox" checked={Boolean(form.limits?.block_amplification)} onChange={(e) => setLimit("block_amplification", e.target.checked)} />
              <span>
                Drop reflection traffic
                <span className="field-hint">UDP from DNS, NTP, SSDP, memcached and other reflection ports. Game traffic never comes from them.</span>
              </span>
            </label>
            <label className="check-row">
              <input type="checkbox" checked={Boolean(form.limits?.drop_fragments)} onChange={(e) => setLimit("drop_fragments", e.target.checked)} />
              <span>
                Drop fragmented packets
                <span className="field-hint">Source engine splits its own packets, so IP fragments to it are attack traffic.</span>
              </span>
            </label>
          </>
        ) : null}
      </fieldset>
      <div className="field-row">
        <div className="field">
          <label className="field-label" htmlFor="ddos-allow">
            Always allow
          </label>
          <textarea id="ddos-allow" className="field-input" rows={2} value={allow} placeholder="Addresses or CIDRs" onChange={(e) => setAllow(e.target.value)} />
        </div>
        <div className="field">
          <label className="field-label" htmlFor="ddos-block">
            Always block
          </label>
          <textarea id="ddos-block" className="field-input" rows={2} value={block} placeholder="Addresses or CIDRs" onChange={(e) => setBlock(e.target.value)} />
        </div>
      </div>
      <label className="check-row">
        <input type="checkbox" checked={form.enabled !== false} onChange={(e) => set({ enabled: e.target.checked })} />
        <span>Protection on</span>
      </label>
    </Dialog>
  );
}

function BanDialog({ open, onClose, onSaved }: { open: boolean; onClose: () => void; onSaved: (res: DDoSOverview, msg: string) => void }) {
  const [addr, setAddr] = useState("");
  const [minutes, setMinutes] = useState("60");
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    if (open) {
      setAddr("");
      setReason("");
      setError(null);
    }
  }, [open]);
  return (
    <Dialog
      open={open}
      title="Ban an address"
      onClose={onClose}
      footer={
        <div className="btn-row">
          <button className="btn btn-ghost" type="button" onClick={onClose}>
            Cancel
          </button>
          <button
            className="btn btn-primary"
            type="button"
            disabled={busy || !addr.trim()}
            onClick={() => {
              setBusy(true);
              setError(null);
              banDDoS(addr.trim(), Number(minutes) || 60, reason)
                .then((res) => onSaved(res, `${addr.trim()} is banned from every target.`))
                .catch((err) => setError(errText(err, "Ban failed")))
                .finally(() => setBusy(false));
            }}
          >
            {busy ? "Banning" : "Ban"}
          </button>
        </div>
      }
    >
      {error ? <ErrorNotice error={error} /> : null}
      <div className="field">
        <label className="field-label" htmlFor="ddos-ban-addr">
          IP address
        </label>
        <input id="ddos-ban-addr" className="field-input" value={addr} placeholder="203.0.113.7" onChange={(e) => setAddr(e.target.value)} />
      </div>
      <div className="field">
        <label className="field-label" htmlFor="ddos-ban-min">
          Minutes
        </label>
        <input id="ddos-ban-min" className="field-input" type="number" min={1} max={10080} value={minutes} onChange={(e) => setMinutes(e.target.value)} />
        <p className="field-hint">Up to 7 days. Add the address to Blocked networks in Settings to block it for good.</p>
      </div>
      <div className="field">
        <label className="field-label" htmlFor="ddos-ban-why">
          Why
        </label>
        <input id="ddos-ban-why" className="field-input" value={reason} placeholder="Optional" onChange={(e) => setReason(e.target.value)} />
      </div>
    </Dialog>
  );
}
