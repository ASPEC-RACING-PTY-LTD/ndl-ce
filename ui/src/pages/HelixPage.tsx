import { useCallback, useEffect, useMemo, useState } from "react";
import { confirmAction } from "../confirm";
import { EmptyState, LoadingState } from "../components/EmptyState";
import { ErrorNotice } from "../components/ErrorNotice";
import { PageHeader } from "../components/PageHeader";
import { StatusBadge } from "../components/StatusBadge";
import { formatWhen } from "../format";
import {
  HELIX_CAPS,
  createHelixCredential,
  decideHelixApproval,
  engageHelixKill,
  getHelix,
  getHelixActivity,
  getHelixKill,
  putHelixMachines,
  putHelixSettings,
  releaseHelixKill,
  revokeHelixCredential,
  type HelixActivity,
  type HelixCap,
  type HelixGrant,
  type HelixKill,
  type HelixView,
} from "../helix/api";
import { hasGrant } from "../rbac";
import { useSession } from "../session";

const errText = (err: unknown, fallback: string) => (err instanceof Error ? err.message : fallback);

const capLabel: Record<HelixCap, { title: string; help: string }> = {
  read: { title: "Read", help: "Status, resources, network, containers, services" },
  logs: { title: "Logs", help: "Container logs and service journals" },
  write: { title: "Write", help: "Start, stop and restart containers and services" },
  execute: { title: "Execute", help: "Run commands" },
  deploy: { title: "Deploy", help: "Deploy and roll back projects" },
  power: { title: "Power", help: "Start, stop and restart the machine" },
};

// What VMs and app containers support: Helix reaches inside system
// containers (LXC) only.
const vmCaps: HelixCap[] = ["read", "power"];

/**
 * HelixPage configures the Helix integration: an external AI operator that
 * reaches only the machines and actions granted here. Admins who are not
 * SuperAdmins see the kill switch only.
 */
export function HelixPage() {
  const session = useSession();
  const user = session.status === "ready" ? session.user : null;
  const canManage = hasGrant(user, "helix.manage");
  if (!canManage) {
    return (
      <section className="page" aria-labelledby="hx-heading">
        <PageHeader id="hx-heading" title="Helix" kicker="Helix is an AI operator that manages the machines a SuperAdmin grants it. You can stop it at once here." />
        <KillOnly />
      </section>
    );
  }
  return <HelixManage />;
}

function KillOnly() {
  const [state, setState] = useState<{ enabled: boolean; kill: HelixKill; running: number } | null>(null);
  const [error, setError] = useState<string | null>(null);
  const reload = useCallback(async () => {
    try {
      setState(await getHelixKill());
    } catch (err) {
      setError(errText(err, "The kill switch could not be read"));
    }
  }, []);
  useEffect(() => {
    void reload();
  }, [reload]);
  if (!state) {
    return error ? <ErrorNotice error={error} /> : <LoadingState label="Reading Helix" />;
  }
  return <KillPanel kill={state.kill} running={state.running} canRelease={false} onChange={reload} />;
}

function HelixManage() {
  const [data, setData] = useState<HelixView | null>(null);
  const [activity, setActivity] = useState<HelixActivity[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  const reload = useCallback(async () => {
    try {
      setData(await getHelix());
      setActivity((await getHelixActivity()).items);
      setError(null);
    } catch (err) {
      setError(errText(err, "Helix settings could not be loaded"));
    }
  }, []);
  useEffect(() => {
    void reload();
    const timer = window.setInterval(() => void reload(), 15000);
    return () => window.clearInterval(timer);
  }, [reload]);

  const run = async (fn: () => Promise<unknown>, done?: string) => {
    setError(null);
    setNotice(null);
    try {
      await fn();
      if (done) {
        setNotice(done);
      }
      await reload();
    } catch (err) {
      setError(errText(err, "That did not work"));
    }
  };

  if (!data) {
    return <section className="page">{error ? <ErrorNotice error={error} /> : <LoadingState label="Reading Helix" />}</section>;
  }
  const granted = data.machines.filter((m) => m.grant && m.grant.caps.length > 0).length;
  const activeCreds = data.credentials.filter((c) => c.active).length;
  return (
    <section className="page page-wide" aria-labelledby="hx-heading">
      <PageHeader
        id="hx-heading"
        title="Helix"
        kicker="Helix is an AI operator that runs on its own machine and manages your projects. It reaches only the machines and actions you grant here, NDL-CE checks every request, and the kill switch stops it at once."
        actions={
          <button className="btn btn-secondary" type="button" onClick={() => void reload()}>
            Refresh
          </button>
        }
      />
      {error ? <ErrorNotice error={error} /> : null}
      {notice ? (
        <p className="banner banner-ok" role="status">
          {notice}
        </p>
      ) : null}
      <div className="status-strip">
        <div className="status-tile">
          <span className="label">Integration</span>
          <span className="value">
            <StatusBadge
              status={data.kill.engaged ? "critical" : data.enabled ? "healthy" : "unknown"}
              label={data.kill.engaged ? "stopped by the kill switch" : data.enabled ? "on" : "off"}
            />
          </span>
          <span className="meta">
            <label className="check-row">
              <input
                type="checkbox"
                checked={data.enabled}
                onChange={(e) => void run(() => putHelixSettings({ enabled: e.target.checked }), e.target.checked ? "Helix is on." : "Helix is off.")}
              />
              Allow Helix to connect
            </label>
          </span>
        </div>
        <div className="status-tile">
          <span className="label">Machines</span>
          <span className="value">{granted}</span>
          <span className="meta">shared with Helix</span>
        </div>
        <div className="status-tile">
          <span className="label">Credentials</span>
          <span className="value">{activeCreds}</span>
          <span className="meta">active</span>
        </div>
        <div className="status-tile">
          <span className="label">Waiting for approval</span>
          <span className="value">{data.pending_approvals}</span>
          <span className="meta">{data.running.length} running now</span>
        </div>
      </div>

      <KillPanel kill={data.kill} running={data.running.length} canRelease onChange={reload} />
      <ApprovalsPanel data={data} run={run} />
      <CredentialsPanel data={data} run={run} />
      <MachinesPanel data={data} run={run} />
      <SettingsPanel data={data} run={run} />
      {data.running.length > 0 ? (
        <div className="panel sys-card">
          <h2>Running now</h2>
          <ul className="plain-list">
            {data.running.map((o) => (
              <li key={o.id}>
                <strong>{o.op}</strong> <span className="meta">since {formatWhen(o.started_at)}</span>
                <div className="muted">{o.summary}</div>
              </li>
            ))}
          </ul>
        </div>
      ) : null}
      <ActivityPanel items={activity} />
    </section>
  );
}

function KillPanel({ kill, running, canRelease, onChange }: { kill: HelixKill; running: number; canRelease: boolean; onChange: () => Promise<void> | void }) {
  const [reason, setReason] = useState("");
  const [revoke, setRevoke] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [result, setResult] = useState<string | null>(null);
  const engage = async () => {
    if (!confirmAction("Stop Helix now? Every Helix request will be refused and its running commands and deployments cancelled.")) {
      return;
    }
    setError(null);
    try {
      const r = await engageHelixKill(reason.trim(), revoke);
      setResult(
        `Helix is stopped: ${r.stopped_operations} operation(s) and ${r.cancelled_deployments} deployment(s) cancelled` +
          (revoke ? `, ${r.revoked_credentials} credential(s) revoked.` : "."),
      );
      setReason("");
      await onChange();
    } catch (err) {
      setError(errText(err, "The kill switch did not engage"));
    }
  };
  const release = async () => {
    if (!confirmAction("Release the kill switch? Helix can connect again with any credential that is still active.")) {
      return;
    }
    try {
      await releaseHelixKill();
      setResult("The kill switch is released.");
      await onChange();
    } catch (err) {
      setError(errText(err, "The kill switch was not released"));
    }
  };
  return (
    <div className={kill.engaged ? "panel sys-card hx-kill is-engaged" : "panel sys-card hx-kill"}>
      <h2>Kill switch</h2>
      {error ? <ErrorNotice error={error} /> : null}
      {result ? (
        <p className="banner banner-ok" role="status">
          {result}
        </p>
      ) : null}
      {kill.engaged ? (
        <>
          <p>
            <StatusBadge status="critical" label="engaged" /> by {kill.by || "someone"}
            {kill.at ? ` ${formatWhen(kill.at)}` : ""}
            {kill.reason ? `: ${kill.reason}` : ""}
          </p>
          <p className="muted">Helix cannot do anything until a SuperAdmin releases it.</p>
          {canRelease ? (
            <div className="btn-row">
              <button className="btn btn-secondary" type="button" onClick={() => void release()}>
                Release
              </button>
            </div>
          ) : null}
        </>
      ) : (
        <>
          <p className="muted">
            Refuses every Helix request at once and cancels its running commands ({running} now) and its deployments. Only a SuperAdmin can release it.
          </p>
          <div className="field">
            <label className="field-label" htmlFor="hx-kill-reason">
              Reason (optional)
            </label>
            <input id="hx-kill-reason" className="field-input" value={reason} maxLength={300} onChange={(e) => setReason(e.target.value)} />
          </div>
          <label className="check-row">
            <input type="checkbox" checked={revoke} onChange={(e) => setRevoke(e.target.checked)} />
            Also revoke every credential
          </label>
          <div className="btn-row">
            <button className="btn btn-danger" type="button" onClick={() => void engage()}>
              Stop Helix now
            </button>
          </div>
        </>
      )}
    </div>
  );
}

type Run = (fn: () => Promise<unknown>, done?: string) => Promise<void>;

function ApprovalsPanel({ data, run }: { data: HelixView; run: Run }) {
  const [notes, setNotes] = useState<Record<string, string>>({});
  const pending = data.approvals.filter((a) => a.status === "pending");
  const recent = data.approvals.filter((a) => a.status !== "pending").slice(0, 10);
  return (
    <div className="panel sys-card">
      <h2>Approvals</h2>
      <p className="muted">
        Destructive operations, and every change on a protected machine, wait here. An approval lets Helix run that exact request once within an hour.
      </p>
      {pending.length === 0 ? <p className="muted">Nothing is waiting.</p> : null}
      <ul className="plain-list">
        {pending.map((a) => (
          <li key={a.id} className="hx-approval">
            <strong>{a.machine_name}</strong> <span className="meta">{a.op}</span>
            <pre className="code-block">{a.summary}</pre>
            <ul className="muted">
              {a.reasons.map((r) => (
                <li key={r}>{r}</li>
              ))}
            </ul>
            <span className="meta">
              Asked {formatWhen(a.requested_at)} with {a.requested_by}; expires {formatWhen(a.expires_at)}
            </span>
            <div className="btn-row">
              <input
                className="field-input"
                aria-label="Note"
                placeholder="Note (optional)"
                value={notes[a.id] ?? ""}
                onChange={(e) => setNotes({ ...notes, [a.id]: e.target.value })}
              />
              <button className="btn btn-primary btn-sm" type="button" onClick={() => void run(() => decideHelixApproval(a.id, "approve", notes[a.id] ?? ""), "Approved.")}>
                Approve
              </button>
              <button className="btn btn-secondary btn-sm" type="button" onClick={() => void run(() => decideHelixApproval(a.id, "deny", notes[a.id] ?? ""), "Denied.")}>
                Deny
              </button>
            </div>
          </li>
        ))}
      </ul>
      {recent.length > 0 ? (
        <details>
          <summary>Recent decisions</summary>
          <ul className="plain-list">
            {recent.map((a) => (
              <li key={a.id}>
                <strong>{a.machine_name}</strong> {a.summary} <span className="meta">{a.status}</span>
                {a.decided_by ? <span className="meta"> by {a.decided_by}</span> : null}
              </li>
            ))}
          </ul>
        </details>
      ) : null}
    </div>
  );
}

function CredentialsPanel({ data, run }: { data: HelixView; run: Run }) {
  const [name, setName] = useState("helix");
  const [days, setDays] = useState(90);
  const [token, setToken] = useState<string | null>(null);
  const create = () =>
    run(async () => {
      const r = await createHelixCredential(name.trim(), days);
      setToken(r.token);
    }, "Credential created. Copy it now: it is not shown again.");
  return (
    <div className="panel sys-card">
      <h2>Credentials</h2>
      <p className="muted">
        Helix sends one as <code>Authorization: Bearer hlx_…</code>. It works only on the Helix API ({data.api_base}), never on the rest of NDL-CE. Revoking one takes
        effect on Helix&apos;s next request.
      </p>
      {token ? (
        <div className="banner banner-ok" role="status">
          <p>Give this to Helix. It is shown once.</p>
          <pre className="code-block">{token}</pre>
          <div className="btn-row">
            <button className="btn btn-secondary btn-sm" type="button" onClick={() => void navigator.clipboard?.writeText(token)}>
              Copy
            </button>
            <button className="btn btn-ghost btn-sm" type="button" onClick={() => setToken(null)}>
              Done
            </button>
          </div>
        </div>
      ) : null}
      {data.credentials.length === 0 ? <EmptyState title="No credentials yet">Create one and give it to Helix.</EmptyState> : null}
      {data.credentials.length > 0 ? (
        <table className="data-table">
          <thead>
            <tr>
              <th>Name</th>
              <th>Key</th>
              <th>Last used</th>
              <th>Expires</th>
              <th>State</th>
              <th aria-label="Actions" />
            </tr>
          </thead>
          <tbody>
            {data.credentials.map((c) => (
              <tr key={c.id}>
                <td>
                  {c.name}
                  <div className="meta">by {c.created_by}</div>
                </td>
                <td>
                  <code>{c.prefix}…</code>
                </td>
                <td>{c.last_used_at ? `${formatWhen(c.last_used_at)} from ${c.last_used_ip}` : "never"}</td>
                <td>{c.expires_at ? formatWhen(c.expires_at) : "never"}</td>
                <td>{c.revoked_at ? `revoked by ${c.revoked_by}` : c.active ? "active" : "expired"}</td>
                <td>
                  {c.active ? (
                    <button
                      className="btn btn-ghost btn-sm"
                      type="button"
                      onClick={() => confirmAction(`Revoke ${c.name}? Helix stops working with it at once.`) && void run(() => revokeHelixCredential(c.id), "Revoked.")}
                    >
                      Revoke
                    </button>
                  ) : null}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      ) : null}
      <div className="hx-form-row">
        <div className="field">
          <label className="field-label" htmlFor="hx-cred-name">
            Name
          </label>
          <input id="hx-cred-name" className="field-input" value={name} maxLength={64} onChange={(e) => setName(e.target.value)} />
        </div>
        <div className="field">
          <label className="field-label" htmlFor="hx-cred-days">
            Expires after (days, 0 never)
          </label>
          <input id="hx-cred-days" className="field-input" type="number" min={0} max={365} value={days} onChange={(e) => setDays(Number(e.target.value))} />
        </div>
        <div className="btn-row">
          <button className="btn btn-primary" type="button" disabled={!name.trim()} onClick={() => void create()}>
            Create credential
          </button>
        </div>
      </div>
    </div>
  );
}

function MachinesPanel({ data, run }: { data: HelixView; run: Run }) {
  const initial = useMemo(() => {
    const out: Record<string, HelixGrant> = {};
    for (const m of data.machines) {
      out[m.id] = m.grant ? { ...m.grant, exec_users: m.grant.exec_users ?? [] } : { machine_id: m.id, caps: [], run_as: "root", exec_users: [] };
    }
    return out;
  }, [data.machines]);
  const [grants, setGrants] = useState(initial);
  useEffect(() => setGrants(initial), [initial]);
  const dirty = JSON.stringify(grants) !== JSON.stringify(initial);
  const set = (id: string, change: Partial<HelixGrant>) => setGrants({ ...grants, [id]: { ...grants[id], ...change } });
  const toggle = (id: string, cap: HelixCap, on: boolean) => {
    const caps = new Set(grants[id].caps);
    if (on) {
      caps.add(cap);
      caps.add("read");
    } else {
      caps.delete(cap);
      if (cap === "read") {
        caps.clear();
      }
    }
    set(id, { caps: HELIX_CAPS.filter((c) => caps.has(c)) });
  };
  const save = () => run(() => putHelixMachines(Object.values(grants).filter((g) => g.caps.length > 0)), "Machine access saved.");
  return (
    <div className="panel sys-card sys-span-all">
      <h2>Machines</h2>
      <p className="muted">
        Helix sees only the machines ticked here, and does only what each one allows. Commands, Docker and services reach system containers (LXC); virtual machines
        offer read and power. <strong>Protected</strong> marks critical machines: every change on them waits for approval. Destructive operations (stopping or
        restarting the machine, stopping a container or service, rollbacks, and commands that delete data, cut access or run hidden code) wait for approval unless
        you allow them unattended.
      </p>
      {data.machines.length === 0 ? <EmptyState title="No machines">Create a system container or VM first.</EmptyState> : null}
      {data.machines.length > 0 ? (
        <div className="hx-scroll">
          <table className="data-table hx-grants">
            <thead>
              <tr>
                <th>Machine</th>
                {HELIX_CAPS.map((c) => (
                  <th key={c} title={capLabel[c].help}>
                    {capLabel[c].title}
                  </th>
                ))}
                <th title="Every change waits for approval">Protected</th>
                <th title="Destructive operations run without approval">Destructive unattended</th>
                <th title="The account commands run as, and any others Helix may ask for">Run as</th>
              </tr>
            </thead>
            <tbody>
              {data.machines.map((m) => {
                const g = grants[m.id];
                if (!g) {
                  return null;
                }
                return (
                  <tr key={m.id}>
                    <td>
                      {m.name}
                      <div className="meta">
                        {m.type} · {m.status}
                      </div>
                    </td>
                    {HELIX_CAPS.map((c) => {
                      const supported = m.exec_capable || vmCaps.includes(c);
                      return (
                        <td key={c}>
                          <input
                            type="checkbox"
                            aria-label={`${capLabel[c].title} on ${m.name}`}
                            disabled={!supported}
                            checked={g.caps.includes(c)}
                            onChange={(e) => toggle(m.id, c, e.target.checked)}
                          />
                        </td>
                      );
                    })}
                    <td>
                      <input
                        type="checkbox"
                        aria-label={`Protected: ${m.name}`}
                        checked={Boolean(g.protected)}
                        onChange={(e) => set(m.id, { protected: e.target.checked, destructive: e.target.checked ? false : g.destructive })}
                      />
                    </td>
                    <td>
                      <input
                        type="checkbox"
                        aria-label={`Destructive unattended: ${m.name}`}
                        disabled={Boolean(g.protected)}
                        checked={Boolean(g.destructive)}
                        onChange={(e) => set(m.id, { destructive: e.target.checked })}
                      />
                    </td>
                    <td>
                      {m.exec_capable ? (
                        <input
                          className="field-input hx-runas"
                          aria-label={`Run as on ${m.name}`}
                          value={[g.run_as || "root", ...(g.exec_users ?? []).filter((u) => u !== g.run_as)].join(", ")}
                          onChange={(e) => {
                            const users = e.target.value
                              .split(",")
                              .map((u) => u.trim())
                              .filter(Boolean);
                            set(m.id, { run_as: users[0] ?? "", exec_users: users.slice(1) });
                          }}
                        />
                      ) : (
                        <span className="muted">n/a</span>
                      )}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      ) : null}
      <div className="btn-row">
        <button className="btn btn-primary" type="button" disabled={!dirty} onClick={() => void save()}>
          Save machine access
        </button>
        {dirty ? (
          <button className="btn btn-ghost" type="button" onClick={() => setGrants(initial)}>
            Undo changes
          </button>
        ) : null}
      </div>
    </div>
  );
}

function SettingsPanel({ data, run }: { data: HelixView; run: Run }) {
  const [ips, setIps] = useState(data.allowed_ips.join("\n"));
  const [global, setGlobal] = useState(data.global_events);
  const [cpu, setCpu] = useState(data.thresholds.cpu_percent);
  const [mem, setMem] = useState(data.thresholds.memory_percent);
  useEffect(() => {
    setIps(data.allowed_ips.join("\n"));
    setGlobal(data.global_events);
    setCpu(data.thresholds.cpu_percent);
    setMem(data.thresholds.memory_percent);
  }, [data.allowed_ips, data.global_events, data.thresholds]);
  const save = () =>
    run(
      () =>
        putHelixSettings({
          allowed_ips: ips
            .split(/[\s,]+/)
            .map((x) => x.trim())
            .filter(Boolean),
          global_events: global,
          thresholds: { cpu_percent: cpu, memory_percent: mem },
        }),
      "Settings saved.",
    );
  return (
    <div className="panel sys-card">
      <h2>Connection and events</h2>
      <div className="field">
        <label className="field-label" htmlFor="hx-ips">
          Allowed addresses
        </label>
        <textarea id="hx-ips" className="field-input" rows={3} value={ips} placeholder="Any address" onChange={(e) => setIps(e.target.value)} />
        <span className="muted">Helix&apos;s address or range, one per line, for example 10.0.3.15. Empty allows any address.</span>
      </div>
      <label className="check-row">
        <input type="checkbox" checked={global} onChange={(e) => setGlobal(e.target.checked)} />
        Send events that are not about one machine (sign-in failures, uptime monitors, alerts, storage)
      </label>
      <div className="hx-form-row">
        <div className="field">
          <label className="field-label" htmlFor="hx-cpu">
            CPU alert at (%)
          </label>
          <input id="hx-cpu" className="field-input" type="number" min={10} max={100} value={cpu} onChange={(e) => setCpu(Number(e.target.value))} />
        </div>
        <div className="field">
          <label className="field-label" htmlFor="hx-mem">
            Memory alert at (%)
          </label>
          <input id="hx-mem" className="field-input" type="number" min={10} max={100} value={mem} onChange={(e) => setMem(Number(e.target.value))} />
        </div>
      </div>
      <div className="btn-row">
        <button className="btn btn-primary" type="button" onClick={() => void save()}>
          Save
        </button>
      </div>
    </div>
  );
}

function ActivityPanel({ items }: { items: HelixActivity[] }) {
  return (
    <div className="panel sys-card sys-span-all">
      <h2>Activity</h2>
      <p className="muted">Everything Helix did or tried, and every change to these settings. The full history is in the audit log.</p>
      {items.length === 0 ? (
        <p className="muted">Nothing yet.</p>
      ) : (
        <div className="hx-scroll">
          <table className="data-table">
            <thead>
              <tr>
                <th>When</th>
                <th>Action</th>
                <th>Result</th>
                <th>By</th>
                <th>Detail</th>
              </tr>
            </thead>
            <tbody>
              {items.map((a) => (
                <tr key={a.id}>
                  <td>{formatWhen(a.at)}</td>
                  <td>{a.action}</td>
                  <td>{a.result}</td>
                  <td>
                    {a.actor}
                    {a.detail?.credential ? <div className="meta">{a.detail.credential}</div> : null}
                  </td>
                  <td className="hx-detail">{a.detail?.detail}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
