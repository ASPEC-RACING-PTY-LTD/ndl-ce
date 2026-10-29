import { confirmAction } from "../confirm";
import { memo, useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  connectCodehold,
  detectCodehold,
  disconnectCodehold,
  getCodehold,
  listNetworks,
  listPools,
  listWorkloads,
  retryCodeholdRunner,
  saveCodeholdSettings,
  type CodeholdInstance,
  type CodeholdRunner,
  type CodeholdSettings,
  type CodeholdStatus,
} from "../api/client";
import { ActionMenu } from "../components/ActionMenu";
import type { ContextItem } from "../components/ContextMenu";
import { EmptyState, LoadingState } from "../components/EmptyState";
import { ErrorNotice } from "../components/ErrorNotice";
import { Link } from "../components/Link";
import { PageHeader } from "../components/PageHeader";
import { CodeholdCLIDialog } from "../components/CodeholdCLIDialog";
import { StatusBadge } from "../components/StatusBadge";
import { formatRelative, formatWhen } from "../format";
import { usePoll } from "../query";
import { canMutate, hasGrant } from "../rbac";
import { navigate } from "../router";
import { useSession } from "../session";
import { storageGet, storageSet } from "../storage";
import { Dialog } from "../ui/Dialog";
import { ColumnEditor, SortHeader, useTablePrefs, type TableColumn } from "../ui/tablePrefs";
import { nodeLabel } from "../nodeNames";

// CodeHold runners: No-dal is a CodeHold host controller. Runners are created,
// started, stopped and removed in CodeHold; this page shows the connection,
// each runner's container and the settings for new ones.

const CACHE = "ndl-codehold";
const errText = (err: unknown, fallback: string) => (err instanceof Error ? err.message : fallback);
const actionItems = (items: ContextItem[]) => items.filter((i): i is Exclude<ContextItem, "sep"> => i !== "sep" && !i.disabled);

function stateBadge(r: CodeholdRunner): { status: string; label: string } {
  if (r.detached) {
    return { status: "stopped", label: r.delete_after ? "Deleting soon" : "Kept, not synced" };
  }
  if (r.setting_up) {
    return { status: "pending", label: "Setting up" };
  }
  switch (r.state) {
    case "running":
      return { status: r.busy > 0 ? "running" : "healthy", label: r.busy > 0 ? `Running ${r.busy} job${r.busy === 1 ? "" : "s"}` : "Ready" };
    case "provisioning":
      return { status: "pending", label: "Registering" };
    case "stopped":
      return { status: "stopped", label: "Stopped" };
    case "removed":
      return { status: "unknown", label: "Removed" };
    case "error":
      return { status: "critical", label: "Error" };
  }
  return { status: "unknown", label: r.state };
}

const COLUMNS: TableColumn<CodeholdRunner>[] = [
  { id: "name", label: "Runner", required: true, sortValue: (r) => r.name },
  { id: "state", label: "State", sortValue: (r) => stateBadge(r).label },
  { id: "desired", label: "CodeHold wants", sortValue: (r) => r.desired },
  { id: "limits", label: "Per job", sortValue: (r) => (r.limits.cpus ?? 0) * 100000 + (r.limits.memory_mb ?? 0) },
  { id: "container", label: "Container", sortValue: (r) => r.workload_name ?? "" },
  { id: "address", label: "Address", defaultHidden: true, sortValue: (r) => r.workload_ip ?? "" },
];

const RunnerRow = memo(function RunnerRow({ r, cols, itemsFor }: { r: CodeholdRunner; cols: string[]; itemsFor: (r: CodeholdRunner) => ContextItem[] }) {
  const st = stateBadge(r);
  return (
    <tr className={r.detached ? "is-muted" : undefined}>
      {cols.map((c) => {
        switch (c) {
          case "name":
            return (
              <td key={c}>
                <strong>{r.name}</strong>
                {r.error ? <div className="tunnel-reason">{r.error}</div> : null}
              </td>
            );
          case "state":
            return (
              <td key={c}>
                <StatusBadge status={st.status} label={st.label} />
                {r.delete_after ? <div className="meta">deleted {formatWhen(r.delete_after)}</div> : null}
              </td>
            );
          case "desired":
            return <td key={c}>{r.desired || "-"}</td>;
          case "limits":
            return (
              <td key={c} className="meta">
                {r.limits.concurrency ? `${r.limits.concurrency} at a time` : "-"}
                {r.limits.cpus ? ` · ${r.limits.cpus} CPU` : ""}
                {r.limits.memory_mb ? ` · ${r.limits.memory_mb} MB` : ""}
              </td>
            );
          case "container":
            return (
              <td key={c}>
                {r.workload_id && r.workload_name ? (
                  <>
                    <Link href={`/workloads/${r.workload_id}`}>{r.workload_name}</Link>
                    {r.power ? <span className="meta"> {r.power}</span> : null}
                  </>
                ) : (
                  <span className="meta">{r.setting_up ? "being created" : "-"}</span>
                )}
              </td>
            );
          case "address":
            return <td key={c}>{r.workload_ip ? <code>{r.workload_ip}</code> : <span className="meta">-</span>}</td>;
        }
        return null;
      })}
      <td className="cell-actions">
        <ActionMenu label={`${r.name} actions`} items={actionItems(itemsFor(r))} />
      </td>
    </tr>
  );
});

export function CodeholdPage() {
  const session = useSession();
  const user = session.status === "ready" ? session.user : null;
  const canManage = hasGrant(user, "feature.manage");
  const [data, setData] = useState<CodeholdStatus | null>(() => {
    try {
      const raw = storageGet(CACHE);
      return raw ? (JSON.parse(raw) as CodeholdStatus) : null;
    } catch {
      return null;
    }
  });
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [connectOpen, setConnectOpen] = useState(false);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const table = useTablePrefs<CodeholdRunner>("codehold.runners", COLUMNS, { id: "name", dir: "asc" });

  const reload = useCallback(async () => {
    try {
      const next = await getCodehold();
      setData(next);
      storageSet(CACHE, JSON.stringify(next));
      setError(null);
    } catch (err) {
      setError(errText(err, "CodeHold runners could not be read"));
    }
  }, []);

  useEffect(() => {
    void reload();
  }, [reload]);
  const settingUp = (data?.runners ?? []).some((r) => r.setting_up || r.state === "provisioning");
  usePoll(() => reload(), settingUp ? 5000 : 20000);

  const latest = useRef({ canManage, data });
  latest.current = { canManage, data };
  const runnerItems = useCallback(
    (r: CodeholdRunner): ContextItem[] => {
      const items: ContextItem[] = [];
      if (r.workload_id) {
        items.push({ label: "Open container", onClick: () => navigate(`/workloads/${r.workload_id}`) });
        items.push({ label: "Open terminal", onClick: () => navigate(`/workloads/${r.workload_id}/terminal`) });
      }
      if (latest.current.canManage && r.state === "error") {
        items.push({
          label: "Retry setup now",
          onClick: () =>
            void retryCodeholdRunner(r.id)
              .then(() => setNotice(`${r.name} will be set up again on the next sync.`))
              .catch((err) => setError(errText(err, "Retry failed"))),
        });
      }
      items.push("sep", { label: "Copy runner ID", onClick: () => void navigator.clipboard?.writeText(r.id) });
      return items;
    },
    [],
  );

  const pageItems: ContextItem[] = [];
  if (canManage) {
    pageItems.push(
      { label: data?.connected ? "Reconnect with a new token" : "Connect CodeHold", onClick: () => setConnectOpen(true) },
      { label: "Runner settings", disabled: !data?.connected, onClick: () => setSettingsOpen(true) },
    );
  }
  pageItems.push({ label: "Refresh", onClick: () => void reload() });

  const rows = useMemo(() => table.sortRows(data?.runners ?? []),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [data, table.sort?.id, table.sort?.dir]);

  if (!data) {
    return (
      <section className="page">
        {error ? <ErrorNotice error={error} /> : <LoadingState label="Reading CodeHold runners" />}
      </section>
    );
  }
  const c = data.controller;
  const active = data.runners.filter((r) => !r.detached);
  const ready = active.filter((r) => r.state === "running").length;
  const cols = table.visible.map((x) => x.id);

  return (
    <section className="page page-wide" aria-labelledby="ch-heading">
      <PageHeader
        id="ch-heading"
        title="CodeHold"
        kicker="Actions runners this node hosts for CodeHold, and the ch client in system containers. Create and remove runners in CodeHold; each gets its own container here."
        actions={
          <div className="btn-row is-flush">
            <button className="btn btn-secondary" type="button" onClick={() => void reload()}>
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
      {!data.connected ? (
        <EmptyState title="Not connected to CodeHold">
          Connect with a one-time enrollment token from CodeHold (Administration, Actions, Host controllers). A CodeHold running on this node is
          found automatically.{" "}
          {canManage ? (
            <button className="btn btn-sm btn-primary" type="button" onClick={() => setConnectOpen(true)}>
              Connect CodeHold
            </button>
          ) : null}
        </EmptyState>
      ) : (
        <>
          {c && c.status !== "connected" ? (
            <div className="banner banner-warn" role="alert">
              <strong>{c.status === "rejected" ? "CodeHold rejected this node's credential" : "CodeHold cannot be reached"}</strong>
              <p>{c.status_reason}</p>
              {c.status === "rejected" && canManage ? (
                <button className="btn btn-sm btn-primary" type="button" onClick={() => setConnectOpen(true)}>
                  Reconnect with a new token
                </button>
              ) : null}
            </div>
          ) : null}
          <div className="status-strip">
            <div className="status-tile">
              <span className="label">CodeHold</span>
              <span className="value">
                <StatusBadge status={c?.status === "connected" ? "healthy" : "critical"} label={c?.status ?? "unknown"} />
              </span>
              <span className="meta">{c?.last_sync_at ? `synced ${formatRelative(c.last_sync_at)}` : "not synced yet"}</span>
            </div>
            <div className="status-tile">
              <span className="label">Runners</span>
              <span className="value">
                {ready}/{active.length}
              </span>
              <span className="meta">ready</span>
            </div>
            <div className="status-tile">
              <span className="label">Each runner</span>
              <span className="value">
                {data.settings.cpus} vCPU · {Math.round(data.settings.memory_mb / 1024)} GB
              </span>
              <span className="meta">{data.settings.disk_gb} GB disk</span>
            </div>
            <div className="status-tile">
              <span className="label">Offered to CodeHold</span>
              <span className="value">{data.capacity.cpus} CPU</span>
              <span className="meta">{Math.round(data.capacity.memory_mb / 1024)} GB memory</span>
            </div>
          </div>
          <div className="toolbar">
            <div className="toolbar-group">
              <span className="meta">
                {c?.name} at{" "}
                <a href={c?.url} target="_blank" rel="noopener noreferrer">
                  {c?.url}
                </a>
                {c?.runner_url && c.runner_url !== c.url ? ` · runners use ${c.runner_url}` : ""}
                {c?.cloudflare_access ? " · Cloudflare Access" : ""}
                {c?.credential_expires_at ? ` · credential renews by ${formatWhen(c.credential_expires_at)}` : ""}
              </span>
              {canManage ? (
                <>
                  <button className="btn btn-sm btn-secondary" type="button" onClick={() => setSettingsOpen(true)}>
                    Runner settings
                  </button>
                  <button className="btn btn-sm btn-ghost" type="button" onClick={() => setConnectOpen(true)}>
                    Reconnect
                  </button>
                  <button
                    className="btn btn-sm btn-ghost btn-danger-text"
                    type="button"
                    onClick={() => {
                      if (confirmAction("Disconnect from CodeHold? Runner containers must be gone first; CodeHold will show this controller as offline.")) {
                        void disconnectCodehold()
                          .then(() => {
                            setNotice("Disconnected from CodeHold.");
                            return reload();
                          })
                          .catch((err) => setError(errText(err, "Disconnect failed")));
                      }
                    }}
                  >
                    Disconnect
                  </button>
                </>
              ) : null}
            </div>
          </div>
          {data.runners.length === 0 ? (
            <EmptyState title="No runners yet">
              Add a local runner on this host controller in CodeHold (Administration, Actions, Runners). Its container appears here within a few seconds.
            </EmptyState>
          ) : (
            <div className="table-wrap">
              <table aria-label="CodeHold runners">
                <thead>
                  <tr>
                    {table.visible.map((col) => (
                      <SortHeader key={col.id} column={col} prefs={table} />
                    ))}
                    <th className="cell-actions">
                      <ColumnEditor prefs={table} label="Edit runner columns" />
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {rows.map((r) => (
                    <RunnerRow key={r.id} r={r} cols={cols} itemsFor={runnerItems} />
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </>
      )}
      <ConnectDialog
        open={connectOpen}
        current={c?.url}
        instances={data.instances ?? []}
        onClose={() => setConnectOpen(false)}
        onDone={(msg) => {
          setConnectOpen(false);
          setNotice(msg);
          void reload();
        }}
      />
      <SettingsDialog
        open={settingsOpen}
        settings={data.settings}
        instances={data.instances ?? []}
        capacity={data.capacity}
        onClose={() => setSettingsOpen(false)}
        onSaved={() => {
          setSettingsOpen(false);
          setNotice("Runner settings saved. They apply to runners created from now on.");
          void reload();
        }}
      />
      <CodeholdCLIDialog open asPage canChange={canMutate(user?.roles)} onClose={() => undefined} />
    </section>
  );
}

function ConnectDialog({ open, current, instances, onClose, onDone }: { open: boolean; current?: string; instances: CodeholdInstance[]; onClose: () => void; onDone: (msg: string) => void }) {
  const [found, setFound] = useState<CodeholdInstance[]>(instances);
  const [looking, setLooking] = useState(false);
  const [url, setURL] = useState("");
  const [runnerURL, setRunnerURL] = useState("");
  const [token, setToken] = useState("");
  const [name, setName] = useState("");
  const [cfOn, setCfOn] = useState(false);
  const [cfID, setCfID] = useState("");
  const [cfSecret, setCfSecret] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!open) {
      return;
    }
    setToken("");
    setError(null);
    setCfID("");
    setCfSecret("");
    setURL(current ?? "");
    setRunnerURL("");
    setLooking(true);
    void detectCodehold()
      .then((res) => {
        const list = res.instances ?? [];
        setFound(list);
        // A CodeHold on this node is used over the local network.
        const local = list.find((i) => i.url && i.has_runner);
        if (local?.url && !current) {
          setURL(local.url);
        }
      })
      .catch(() => undefined)
      .finally(() => setLooking(false));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  return (
    <Dialog
      open={open}
      title={current ? "Reconnect CodeHold" : "Connect CodeHold"}
      onClose={onClose}
      footer={
        <div className="btn-row">
          <button className="btn btn-ghost" type="button" onClick={onClose}>
            Cancel
          </button>
          <button
            className="btn btn-primary"
            type="button"
            disabled={busy || !url.trim() || !token.trim() || (cfOn && (!cfID.trim() || !cfSecret.trim()))}
            onClick={() => {
              setBusy(true);
              setError(null);
              connectCodehold({
                url: url.trim(),
                runner_url: runnerURL.trim() || undefined,
                enrollment_token: token.trim(),
                name: name.trim() || undefined,
                cf_client_id: cfOn ? cfID.trim() : undefined,
                cf_client_secret: cfOn ? cfSecret.trim() : undefined,
              })
                .then((res) => onDone(`Connected to CodeHold as ${res.name}.`))
                .catch((err) => setError(errText(err, "Connecting failed")))
                .finally(() => setBusy(false));
            }}
          >
            {busy ? "Enrolling" : "Connect"}
          </button>
        </div>
      }
    >
      {error ? <ErrorNotice error={error} /> : null}
      {looking ? <p className="meta">Looking for CodeHold on this node…</p> : null}
      {found.length > 0 ? (
        <div className="field">
          <span className="field-label">Found on this node</span>
          <ul className="plain-list">
            {found.map((i) => (
              <li key={i.container_id}>
                <strong>{i.container_name}</strong> <span className="meta">on {nodeLabel(i.machine_id, i.machine_name)}</span>{" "}
                {i.url ? <code>{i.url}</code> : <span className="meta">no published HTTP port</span>}
                {!i.has_runner ? <span className="tunnel-reason"> has no runner downloads</span> : null}{" "}
                {i.url ? (
                  <button type="button" className="btn btn-sm btn-ghost" onClick={() => setURL(i.url ?? "")}>
                    Use
                  </button>
                ) : null}
              </li>
            ))}
          </ul>
        </div>
      ) : null}
      <div className="field">
        <label className="field-label" htmlFor="ch-url">
          CodeHold address
        </label>
        <input id="ch-url" className="field-input" value={url} placeholder="http://192.168.2.196:8085 or https://git.example.com" onChange={(e) => setURL(e.target.value)} />
        <p className="field-hint">A CodeHold on this node is reached directly over the local network, without public routing.</p>
      </div>
      <div className="field">
        <label className="field-label" htmlFor="ch-runner-url">
          Address runners use (optional)
        </label>
        <input id="ch-runner-url" className="field-input" value={runnerURL} placeholder="Same as above" onChange={(e) => setRunnerURL(e.target.value)} />
      </div>
      <div className="field">
        <label className="field-label" htmlFor="ch-token">
          Enrollment token
        </label>
        <input id="ch-token" className="field-input" type="password" autoComplete="off" value={token} placeholder="chce_…" onChange={(e) => setToken(e.target.value)} />
        <p className="field-hint">
          In CodeHold: Administration, Actions, Host controllers, then create an enrollment token. It works once; the credential it gives this node is
          encrypted here and never shown.
        </p>
      </div>
      <div className="field">
        <label className="field-label" htmlFor="ch-name">
          Controller name (optional)
        </label>
        <input id="ch-name" className="field-input" value={name} placeholder="This node's name" onChange={(e) => setName(e.target.value)} />
      </div>
      <label className="check-row">
        <input type="checkbox" checked={cfOn} onChange={(e) => setCfOn(e.target.checked)} />
        <span>CodeHold is behind Cloudflare Access</span>
      </label>
      {cfOn ? (
        <>
          <div className="field">
            <label className="field-label" htmlFor="ch-cf-id">
              Access client ID
            </label>
            <input id="ch-cf-id" className="field-input" autoComplete="off" value={cfID} onChange={(e) => setCfID(e.target.value)} />
          </div>
          <div className="field">
            <label className="field-label" htmlFor="ch-cf-secret">
              Access client secret
            </label>
            <input id="ch-cf-secret" className="field-input" type="password" autoComplete="off" value={cfSecret} onChange={(e) => setCfSecret(e.target.value)} />
          </div>
        </>
      ) : null}
    </Dialog>
  );
}

function SettingsDialog({
  open,
  settings,
  instances,
  capacity,
  onClose,
  onSaved,
}: {
  open: boolean;
  settings: CodeholdSettings;
  instances: CodeholdInstance[];
  capacity: { cpus: number; memory_mb: number };
  onClose: () => void;
  onSaved: () => void;
}) {
  const [s, setS] = useState<CodeholdSettings>(settings);
  const [pools, setPools] = useState<{ id: string; name: string }[]>([]);
  const [nets, setNets] = useState<{ id: string; name: string }[]>([]);
  const [pins, setPins] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!open) {
      return;
    }
    setS(settings);
    setError(null);
    void Promise.all([listPools(), listNetworks(), listWorkloads()])
      .then(([p, n, w]) => {
        setPools((p.items ?? []).filter((x) => x.status === "available" || x.status === "warning").map((x) => ({ id: x.id, name: x.name })));
        setNets((n.items ?? []).filter((x) => x.status === "available" || x.status === "warning").map((x) => ({ id: x.id, name: x.name })));
        setPins(w.image_pins ?? []);
      })
      .catch(() => undefined);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  const num = (v: string) => (v.trim() === "" ? 0 : Number(v));
  const usable = instances.filter((i) => i.has_runner);

  return (
    <Dialog
      open={open}
      title="Runner settings"
      wide
      onClose={onClose}
      footer={
        <div className="btn-row">
          <button className="btn btn-ghost" type="button" onClick={onClose}>
            Cancel
          </button>
          <button
            className="btn btn-primary"
            type="button"
            disabled={busy}
            onClick={() => {
              setBusy(true);
              setError(null);
              saveCodeholdSettings(s)
                .then(onSaved)
                .catch((err) => setError(errText(err, "Saving failed")))
                .finally(() => setBusy(false));
            }}
          >
            Save
          </button>
        </div>
      }
    >
      {error ? <ErrorNotice error={error} /> : null}
      <h3>Each runner's container</h3>
      <div className="form-grid">
        <div className="field">
          <label className="field-label" htmlFor="chs-cpus">
            vCPU
          </label>
          <input id="chs-cpus" className="field-input" type="number" min={1} value={s.cpus} onChange={(e) => setS({ ...s, cpus: num(e.target.value) })} />
        </div>
        <div className="field">
          <label className="field-label" htmlFor="chs-mem">
            Memory (MB)
          </label>
          <input id="chs-mem" className="field-input" type="number" min={512} step={256} value={s.memory_mb} onChange={(e) => setS({ ...s, memory_mb: num(e.target.value) })} />
        </div>
        <div className="field">
          <label className="field-label" htmlFor="chs-disk">
            Disk (GB)
          </label>
          <input id="chs-disk" className="field-input" type="number" min={10} value={s.disk_gb} onChange={(e) => setS({ ...s, disk_gb: num(e.target.value) })} />
        </div>
        <div className="field">
          <label className="field-label" htmlFor="chs-pin">
            Image
          </label>
          <select id="chs-pin" className="field-input" value={s.image_pin} onChange={(e) => setS({ ...s, image_pin: e.target.value })}>
            {(pins.length ? pins : [s.image_pin]).map((p) => (
              <option key={p} value={p}>
                {p}
              </option>
            ))}
          </select>
        </div>
        <div className="field">
          <label className="field-label" htmlFor="chs-pool">
            Storage pool
          </label>
          <select id="chs-pool" className="field-input" value={s.pool_id ?? ""} onChange={(e) => setS({ ...s, pool_id: e.target.value || undefined })}>
            <option value="">Automatic</option>
            {pools.map((p) => (
              <option key={p.id} value={p.id}>
                {p.name}
              </option>
            ))}
          </select>
        </div>
        <div className="field">
          <label className="field-label" htmlFor="chs-net">
            Network
          </label>
          <select id="chs-net" className="field-input" value={s.network_id ?? ""} onChange={(e) => setS({ ...s, network_id: e.target.value || undefined })}>
            <option value="">Automatic</option>
            {nets.map((n) => (
              <option key={n.id} value={n.id}>
                {n.name}
              </option>
            ))}
          </select>
        </div>
      </div>
      <p className="field-hint">Docker is installed in every runner's container for its jobs. Changes apply to runners created from now on.</p>
      <h3>What CodeHold may use on this node</h3>
      <div className="form-grid">
        <div className="field">
          <label className="field-label" htmlFor="chs-ccpu">
            CPUs
          </label>
          <input
            id="chs-ccpu"
            className="field-input"
            type="number"
            min={0}
            placeholder={`Automatic (${capacity.cpus})`}
            value={s.capacity_cpus || ""}
            onChange={(e) => setS({ ...s, capacity_cpus: num(e.target.value) })}
          />
        </div>
        <div className="field">
          <label className="field-label" htmlFor="chs-cmem">
            Memory (MB)
          </label>
          <input
            id="chs-cmem"
            className="field-input"
            type="number"
            min={0}
            placeholder={`Automatic (${capacity.memory_mb})`}
            value={s.capacity_memory_mb || ""}
            onChange={(e) => setS({ ...s, capacity_memory_mb: num(e.target.value) })}
          />
        </div>
      </div>
      <p className="field-hint">CodeHold reserves each runner's per-job limits times its jobs at a time from this, and refuses runners that do not fit.</p>
      <h3>Where the runner program comes from</h3>
      <label className="check-row">
        <input type="radio" name="chs-source" checked={s.source === "local"} onChange={() => setS({ ...s, source: "local" })} />
        <span>From CodeHold on this node (copied directly, no network download)</span>
      </label>
      {s.source === "local" ? (
        usable.length ? (
          <div className="field">
            <select
              className="field-input"
              aria-label="CodeHold instance"
              value={s.local_container ? `${s.local_machine_id}|${s.local_container}` : ""}
              onChange={(e) => {
                const [m, c] = e.target.value.split("|");
                setS({ ...s, local_machine_id: m || undefined, local_container: c || undefined });
              }}
            >
              <option value="">Any CodeHold found on this node</option>
              {usable.map((i) => (
                <option key={i.container_id} value={`${i.machine_id}|${i.container_name}`}>
                  {i.container_name} on {nodeLabel(i.machine_id, i.machine_name)}
                </option>
              ))}
            </select>
          </div>
        ) : (
          <p className="banner banner-warn">No CodeHold with runner downloads was found on this node. Start it, or use an HTTPS address below.</p>
        )
      ) : null}
      <label className="check-row">
        <input type="radio" name="chs-source" checked={s.source === "url"} onChange={() => setS({ ...s, source: "url" })} />
        <span>Download over HTTPS</span>
      </label>
      {s.source === "url" ? (
        <>
          <div className="field">
            <label className="field-label" htmlFor="chs-url">
              Address
            </label>
            <input
              id="chs-url"
              className="field-input"
              value={s.download_url ?? ""}
              placeholder="https://git.example.com/ (the download path is added)"
              onChange={(e) => setS({ ...s, download_url: e.target.value })}
            />
          </div>
          <label className="check-row">
            <input type="checkbox" checked={s.checksum_required} onChange={(e) => setS({ ...s, checksum_required: e.target.checked })} />
            <span>Require a SHA-256 checksum</span>
          </label>
          {s.checksum_required ? (
            <div className="field">
              <label className="field-label" htmlFor="chs-sha">
                SHA-256
              </label>
              <input id="chs-sha" className="field-input" value={s.sha256 ?? ""} placeholder="64 hexadecimal characters" onChange={(e) => setS({ ...s, sha256: e.target.value })} />
            </div>
          ) : (
            <p className="field-hint">Without a checksum the program is trusted because it comes from that HTTPS address.</p>
          )}
        </>
      ) : null}
    </Dialog>
  );
}
