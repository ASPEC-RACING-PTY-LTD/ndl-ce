import { confirmAction } from "../confirm";
import { useCallback, useEffect, useRef, useState } from "react";
import { NodeGroupsPanel } from "../components/ConfigPanels";
import {
  ApiError,
  checkNode,
  createJoinToken,
  fenceClusterHA,
  getClusterHA,
  getJoinSettings,
  getClusterUpdate,
  listNodes,
  promoteClusterHA,
  putJoinSettings,
  revokeClusterNode,
  runClusterUpdate,
  setNodeAgentAddress,
  type ClusterJoinSettings,
  type JoinTokenCreateResult,
  type WorkerLinkCheck,
} from "../api/client";
import type { NodeSummary } from "../api/phase2";
import type { HAStatus, RollingUpdatePreview } from "../generated/openapi";
import { useSession } from "../session";
import { DeleteButton } from "../components/DeleteButton";
import { ErrorNotice } from "../components/ErrorNotice";
import { EmptyState } from "../components/EmptyState";
import { Field } from "../components/Field";
import { PageHeader } from "../components/PageHeader";
import { StatusBadge } from "../components/StatusBadge";
import { Dialog } from "../ui/Dialog";
import { RelativeTime } from "../ui/RelativeTime";
import { Segmented } from "../ui/Segmented";
import { formatBytes } from "../format";

const INSTALL_CMD =
  "curl -fsSL https://raw.githubusercontent.com/ASPEC-RACING-PTY-LTD/ndl-ce/main/packaging/bootstrap/get-nodal.sh | sudo sh";

function canJoin(roles: string[] | undefined): boolean {
  return Boolean(roles?.includes("superadmin") || roles?.includes("admin"));
}

function canPromote(roles: string[] | undefined): boolean {
  return Boolean(roles?.includes("superadmin"));
}

function message(err: unknown, fallback: string): string {
  return err instanceof ApiError ? err.message : err instanceof Error ? err.message : fallback;
}

/** shellQuote wraps a value for a POSIX shell when it needs it. */
function shellQuote(v: string): string {
  return /^[A-Za-z0-9_./:@%+=,-]+$/.test(v) ? v : `'${v.replace(/'/g, `'\\''`)}'`;
}

/** joinCommand is the one line an admin runs on the new machine. */
export function joinCommand(
  url: string,
  token: string,
  fingerprint?: string,
  advertise?: string,
  access?: { id?: string; secret?: string },
): string {
  const parts = ["sudo nodalctl cluster join", `--url ${shellQuote(url.trim())}`, `--token ${token}`];
  if (fingerprint) {
    parts.push(`--fingerprint ${fingerprint}`);
  }
  if (advertise?.trim()) {
    parts.push(`--advertise ${shellQuote(advertise.trim())}`);
  }
  if (access?.id && access.secret) {
    parts.push(`--access-client-id ${shellQuote(access.id)}`, `--access-client-secret ${shellQuote(access.secret)}`);
  }
  return parts.join(" ");
}

/**
 * JoinSettingsSection keeps how new nodes reach this control node: the
 * join URL, and a Cloudflare Access service token when the control node is
 * behind Access. Add node puts both into the join command.
 */
function JoinSettingsSection() {
  const [settings, setSettings] = useState<ClusterJoinSettings | null>(null);
  const [url, setUrl] = useState("");
  const [clientId, setClientId] = useState("");
  const [secret, setSecret] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);
  const [busy, setBusy] = useState(false);

  function apply(next: ClusterJoinSettings) {
    setSettings(next);
    setUrl(next.join_url);
    setClientId(next.access_client_id);
    setSecret("");
  }

  useEffect(() => {
    void getJoinSettings()
      .then(apply)
      .catch((err) => setError(message(err, "Unavailable")));
  }, []);

  async function save(clear = false) {
    setBusy(true);
    setError(null);
    setSaved(false);
    try {
      const next = await putJoinSettings(
        clear
          ? { join_url: url.trim(), clear_access: true }
          : { join_url: url.trim(), access_client_id: clientId.trim(), access_client_secret: secret.trim() },
      );
      apply(next);
      setSaved(true);
    } catch (err) {
      setError(message(err, "Could not save"));
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="section-block" aria-labelledby="cluster-join-heading">
      <h2 id="cluster-join-heading">Join settings</h2>
      <p className="lede">
        How new nodes reach this control node. Behind Cloudflare Access, create a service token in Zero Trust (Access,
        Service Auth), add a Service Auth policy for it to this application, and save it here. Add node then puts it in
        the join command.
      </p>
      {error ? <ErrorNotice error={error} /> : null}
      {settings === null && !error ? (
        <p className="muted">Loading</p>
      ) : (
        <>
          <Field
            id="join-settings-url"
            label="Join URL"
            hint="The address new nodes post their join to, such as https://nodal.example.com. Leave blank to use the address you open this page on."
            placeholder={window.location.origin}
            value={url}
            onChange={(e) => setUrl(e.target.value)}
          />
          <div className="field-row">
            <Field
              id="join-settings-client-id"
              label="Cloudflare Access client ID"
              placeholder="xxxxxxxx.access"
              autoComplete="off"
              value={clientId}
              onChange={(e) => setClientId(e.target.value)}
            />
            <Field
              id="join-settings-client-secret"
              label="Cloudflare Access client secret"
              type="password"
              autoComplete="new-password"
              hint={settings?.access_secret_set ? "Saved. Leave blank to keep it." : undefined}
              value={secret}
              onChange={(e) => setSecret(e.target.value)}
            />
          </div>
          <div className="btn-row">
            <button className="btn btn-primary" type="button" disabled={busy} onClick={() => void save()}>
              Save join settings
            </button>
            {settings?.access_secret_set ? (
              <button className="btn btn-ghost" type="button" disabled={busy} onClick={() => void save(true)}>
                Remove service token
              </button>
            ) : null}
            {saved ? (
              <span className="muted" role="status">
                Saved
              </span>
            ) : null}
          </div>
        </>
      )}
    </section>
  );
}

function isWorker(n: NodeSummary): boolean {
  return n.role === "worker";
}

function linkTone(n: NodeSummary): { status: string; label: string } {
  if (!isWorker(n)) {
    return { status: "ok", label: "This host" };
  }
  if (!n.agent?.listen_addr) {
    return { status: "warning", label: "No address" };
  }
  return n.agent.status === "Ready" ? { status: "ok", label: "Connected" } : { status: "failed", label: "Not reachable" };
}

function hostSummary(n: NodeSummary): string {
  const bits: string[] = [];
  if (n.host_os) {
    bits.push(n.host_os);
  }
  if (n.cpu_threads || n.cpu_cores) {
    bits.push(`${n.cpu_threads || n.cpu_cores} CPU`);
  }
  if (n.memory_bytes) {
    bits.push(formatBytes(n.memory_bytes));
  }
  return bits.join(" · ");
}

function CopyLine({ label, text }: { label: string; text: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <div className="join-cmd">
      <div className="join-cmd-head">
        <span>{label}</span>
        <button
          type="button"
          className="btn btn-ghost btn-sm"
          onClick={() => {
            void navigator.clipboard?.writeText(text).then(() => setCopied(true));
          }}
        >
          {copied ? "Copied" : "Copy"}
        </button>
      </div>
      <pre className="code-block" tabIndex={0}>
        {text}
      </pre>
    </div>
  );
}

type Where = "lan" | "internet";

/**
 * AddNodeDialog walks an admin through adding a machine: install No-DAL on
 * it, run one join command, open the agent port, then watch it connect.
 * The worker agent link is mutual TLS, so a VPS joins over the internet
 * with no tunnel.
 */
function AddNodeDialog({
  open,
  known,
  onClose,
  onChanged,
}: {
  open: boolean;
  known: Set<string>;
  onClose: () => void;
  onChanged: () => Promise<NodeSummary[]>;
}) {
  const [where, setWhere] = useState<Where>("internet");
  const [url, setUrl] = useState("");
  const [advertise, setAdvertise] = useState("");
  const [token, setToken] = useState<JoinTokenCreateResult | null>(null);
  const [joined, setJoined] = useState<NodeSummary | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const knownAtOpen = useRef<Set<string>>(new Set());

  useEffect(() => {
    if (open) {
      knownAtOpen.current = new Set(known);
      setToken(null);
      setJoined(null);
      setError(null);
      setAdvertise("");
      void getJoinSettings()
        .then((st) => setUrl(st.join_url))
        .catch(() => undefined);
    }
    // Only reset when the dialog opens.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  // Once a command is shown, watch for the new node and its link.
  useEffect(() => {
    if (!open || !token) {
      return;
    }
    const timer = window.setInterval(() => {
      void onChanged()
        .then((nodes) => {
          const fresh = nodes.find((n) => isWorker(n) && !knownAtOpen.current.has(n.id));
          if (fresh) {
            setJoined(fresh);
          }
        })
        .catch(() => undefined);
    }, 4000);
    return () => window.clearInterval(timer);
  }, [open, token, onChanged]);

  async function onGenerate() {
    setBusy(true);
    setError(null);
    try {
      const created = await createJoinToken();
      setToken(created);
      if (!url) {
        setUrl(created.join_url ?? window.location.origin);
      }
    } catch (err) {
      setError(message(err, "Could not create a join token"));
    } finally {
      setBusy(false);
    }
  }

  const joinUrl = url || token?.join_url || window.location.origin;
  const plainHttp = joinUrl.startsWith("http://");
  const port = token?.agent_port || "9444";
  const ready = joined?.agent?.status === "Ready";

  return (
    <Dialog
      open={open}
      title="Add a node"
      wide
      onClose={onClose}
      footer={
        <div className="btn-row is-flush">
          {token ? null : (
            <button className="btn btn-primary" type="button" disabled={busy} onClick={() => void onGenerate()}>
              Create join command
            </button>
          )}
          <button className="btn" type="button" onClick={onClose}>
            {ready ? "Done" : "Close"}
          </button>
        </div>
      }
    >
      <p className="lede">
        Any Debian 13 amd64 machine can join: a spare box on your network or a VPS anywhere. It becomes a worker that
        runs workloads, managed from here. The link to its agent is mutual TLS with this cluster&apos;s certificates, so
        no VPN or tunnel is needed.
      </p>
      {error ? <ErrorNotice error={error} /> : null}
      <div className="field">
        <span className="field-label">Where is the machine?</span>
        <Segmented<Where>
          value={where}
          ariaLabel="Where the new node is"
          options={[
            { id: "internet", label: "Over the internet (VPS)" },
            { id: "lan", label: "On this network" },
          ]}
          onChange={setWhere}
        />
      </div>
      <Field
        id="join-url"
        label="Address the node uses to reach this control node"
        hint={
          where === "internet"
            ? "Must be reachable from the VPS: your public HTTPS address, such as a Cloudflare Tunnel hostname or a forwarded port."
            : "The LAN address of this control node."
        }
        value={joinUrl}
        onChange={(e) => setUrl(e.target.value)}
      />
      <Field
        id="join-advertise"
        label={where === "internet" ? "Node's public IP or hostname (optional)" : "Node's LAN address (optional)"}
        hint={`This control node dials the node's agent on TCP ${port}. Leave blank to use the address the join comes from.`}
        placeholder={where === "internet" ? "203.0.113.10" : "192.168.1.50"}
        value={advertise}
        onChange={(e) => setAdvertise(e.target.value)}
      />
      {where === "internet" && plainHttp ? (
        <p className="banner banner-error">
          This address is plain HTTP. The join token would cross the internet unencrypted, so nodalctl refuses it for a
          public address. Use this control node&apos;s HTTPS address.
        </p>
      ) : null}

      <ol className="join-steps">
        <li>
          <h3>Install No-DAL on the new machine</h3>
          <CopyLine label="On the new machine" text={INSTALL_CMD} />
        </li>
        <li>
          <h3>Join it to this cluster</h3>
          {token ? (
            <>
              <CopyLine label="On the new machine" text={joinCommand(joinUrl, token.token, token.control_fingerprint, advertise, {
                  id: token.access_client_id,
                  secret: token.access_client_secret,
                })} />
              <p className="muted">
                Single use, shown once. Expires <RelativeTime value={token.expires_at} />.
                {token.control_fingerprint ? " The fingerprint pins this control node's certificate." : ""}
                {token.access_client_id ? " It includes your Cloudflare Access service token, so keep it private." : ""}
              </p>
            </>
          ) : (
            <p className="muted">Create the join command, then run it on the new machine.</p>
          )}
        </li>
        <li>
          <h3>Let this control node reach the agent</h3>
          <p>
            Allow inbound TCP <code>{port}</code> on the new machine from this control node
            {where === "internet" ? " in the VPS provider's firewall or security group" : ""}. Only this cluster&apos;s
            control certificate is accepted on that port.
          </p>
        </li>
      </ol>

      {token ? (
        <div className="join-status" role="status" aria-live="polite">
          {joined ? (
            ready ? (
              <p>
                <StatusBadge status="ok" label="Connected" /> <strong>{joined.name}</strong> joined
                {hostSummary(joined) ? `: ${hostSummary(joined)}` : ""}.
              </p>
            ) : (
              <p>
                <StatusBadge status="pending" label="Joined" /> <strong>{joined.name}</strong> joined. Waiting for its
                agent at <code>{joined.agent?.listen_addr || "an address you set"}</code>
                {joined.agent?.reason ? `: ${joined.agent.reason}` : ""}.
              </p>
            )
          ) : (
            <p>
              <StatusBadge status="pending" label="Waiting" /> Waiting for the node to join.
            </p>
          )}
        </div>
      ) : null}
    </Dialog>
  );
}

function AddressDialog({
  node,
  onClose,
  onSaved,
}: {
  node: NodeSummary | null;
  onClose: () => void;
  onSaved: (id: string) => Promise<void>;
}) {
  const [addr, setAddr] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    setAddr(node?.agent?.listen_addr ?? "");
    setError(null);
  }, [node]);

  async function save() {
    if (!node) {
      return;
    }
    let value = addr.trim();
    if (value && !/:\d+$/.test(value)) {
      value = value.includes(":") && !value.startsWith("[") ? `[${value}]:9444` : `${value}:9444`;
    }
    setBusy(true);
    setError(null);
    try {
      await setNodeAgentAddress(node.id, value);
      await onSaved(node.id);
      onClose();
    } catch (err) {
      setError(message(err, "Could not save the address"));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog
      open={node !== null}
      title={`Agent address for ${node?.name ?? "node"}`}
      onClose={onClose}
      footer={
        <div className="btn-row is-flush">
          <button className="btn btn-primary" type="button" disabled={busy || !addr.trim()} onClick={() => void save()}>
            Save and check
          </button>
          <button className="btn" type="button" onClick={onClose}>
            Cancel
          </button>
        </div>
      }
    >
      {error ? <ErrorNotice error={error} /> : null}
      <Field
        id="agent-addr"
        label="Host and port"
        hint="Where this control node dials the node's agent. The port defaults to 9444."
        placeholder="203.0.113.10:9444"
        value={addr}
        onChange={(e) => setAddr(e.target.value)}
      />
    </Dialog>
  );
}

export function ClusterPage() {
  const session = useSession();
  const roles = session.status === "ready" ? session.user?.roles : undefined;
  const mutate = canJoin(roles);
  const promote = canPromote(roles);
  const [nodes, setNodes] = useState<NodeSummary[] | null>(null);
  const [ha, setHa] = useState<HAStatus | null>(null);
  const [rolling, setRolling] = useState<RollingUpdatePreview | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [adding, setAdding] = useState(false);
  const [editing, setEditing] = useState<NodeSummary | null>(null);
  const [checks, setChecks] = useState<Record<string, WorkerLinkCheck | "checking">>({});

  const reloadNodes = useCallback(async () => {
    const next = await listNodes();
    setNodes(next);
    return next;
  }, []);

  const reload = useCallback(async () => {
    const [, status, plan] = await Promise.all([reloadNodes(), getClusterHA(), getClusterUpdate()]);
    setHa(status);
    setRolling(plan);
  }, [reloadNodes]);

  useEffect(() => {
    let cancelled = false;
    void reload().catch((err) => {
      if (!cancelled) {
        setError(message(err, "Unavailable"));
      }
    });
    // Worker links are checked by the control node every 15 seconds.
    const timer = window.setInterval(() => void reloadNodes().catch(() => undefined), 15000);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [reload, reloadNodes]);

  async function onCheck(id: string) {
    setChecks((c) => ({ ...c, [id]: "checking" }));
    try {
      const res = await checkNode(id);
      setChecks((c) => ({ ...c, [id]: res }));
      await reloadNodes();
    } catch (err) {
      setChecks((c) => ({
        ...c,
        [id]: { reachable: false, listen_addr: "", checked_at: "", error: message(err, "Check failed") },
      }));
    }
  }

  async function onRevoke(n: NodeSummary) {
    if (
      !confirmAction(
        `Revoke ${n.name}? Its certificate stops working and this control node stops managing it. Workloads on it keep running there.`,
      )
    ) {
      return;
    }
    setBusy(true);
    setError(null);
    try {
      await revokeClusterNode(n.id);
      await reload();
    } catch (err) {
      setError(message(err, "Unavailable"));
    } finally {
      setBusy(false);
    }
  }

  async function onFence() {
    if (!confirmAction("Fence records that the old writer is isolated. STONITH is not implemented.")) {
      return;
    }
    setBusy(true);
    setError(null);
    try {
      await fenceClusterHA();
      await reload();
    } catch (err) {
      setError(message(err, "Fence failed"));
    } finally {
      setBusy(false);
    }
  }

  async function onPromote() {
    if (!confirmAction("Promote this process to the single writer after fence or lease expiry?")) {
      return;
    }
    setBusy(true);
    setError(null);
    try {
      await promoteClusterHA();
      await reload();
    } catch (err) {
      setError(message(err, "Promote failed"));
    } finally {
      setBusy(false);
    }
  }

  async function onRolling() {
    if (
      !confirmAction(
        "Roll one node at a time: drain, then apply the package update on this control node. Guests are not stopped.",
      )
    ) {
      return;
    }
    setBusy(true);
    setError(null);
    try {
      await runClusterUpdate();
      await reload();
    } catch (err) {
      setError(message(err, "Rolling update failed"));
    } finally {
      setBusy(false);
    }
  }

  const list = nodes ?? [];
  const workers = list.filter(isWorker);
  const connected = workers.filter((n) => n.agent?.status === "Ready").length;
  const replicaLabel = ha?.replica_status === "unavailable" ? "Unavailable" : "Not configured";

  return (
    <section className="page page-wide" aria-labelledby="cluster-heading">
      <PageHeader
        id="cluster-heading"
        title="Cluster"
        kicker={
          workers.length === 0
            ? "Add machines on your network or VPS servers anywhere as worker nodes, all managed from here."
            : `${list.length} nodes. ${connected} of ${workers.length} workers connected.`
        }
        actions={
          mutate ? (
            <div className="btn-row is-flush">
              <button className="btn btn-primary" type="button" onClick={() => setAdding(true)}>
                Add node
              </button>
            </div>
          ) : null
        }
      />
      {error ? <ErrorNotice error={error} /> : null}

      <section className="section-block" aria-labelledby="cluster-nodes-heading">
        <h2 id="cluster-nodes-heading">Nodes</h2>
        {nodes === null ? (
          <p className="muted">Loading</p>
        ) : list.length === 0 ? (
          <EmptyState title="No nodes yet">This host appears here once setup has finished.</EmptyState>
        ) : (
          <div className="table-wrap">
            <table aria-label="Cluster nodes">
              <thead>
                <tr>
                  <th>Node</th>
                  <th>Link</th>
                  <th>Host</th>
                  <th>Last seen</th>
                  <th className="cell-actions">
                    <span className="visually-hidden">Actions</span>
                  </th>
                </tr>
              </thead>
              <tbody>
                {list.map((n) => {
                  const link = linkTone(n);
                  const check = checks[n.id];
                  return (
                    <tr key={n.id}>
                      <td>
                        <strong>{n.name}</strong>
                        <div className="muted">{isWorker(n) ? "Worker" : "Control"}</div>
                      </td>
                      <td>
                        <StatusBadge status={link.status} label={link.label} />
                        {isWorker(n) && n.agent?.listen_addr ? (
                          <div className="muted">
                            <code>{n.agent.listen_addr}</code>
                          </div>
                        ) : null}
                        {isWorker(n) && n.agent?.status !== "Ready" && n.agent?.reason ? (
                          <div className="muted">{n.agent.reason}</div>
                        ) : null}
                        {check && check !== "checking" ? (
                          <div className="muted" role="status">
                            {check.reachable ? `Reached agent ${check.agent_version ?? ""}`.trim() : check.error}
                          </div>
                        ) : null}
                      </td>
                      <td>{hostSummary(n) || <span className="muted">Not observed yet</span>}</td>
                      <td>
                        {isWorker(n) ? (
                          n.agent?.last_seen_at ? (
                            <RelativeTime value={n.agent.last_seen_at} />
                          ) : (
                            <span className="muted">Never</span>
                          )
                        ) : (
                          <RelativeTime value={n.observed_at} />
                        )}
                      </td>
                      <td className="cell-actions">
                        {isWorker(n) ? (
                          <div className="btn-row is-flush">
                            <button
                              className="btn btn-ghost btn-sm"
                              type="button"
                              disabled={check === "checking"}
                              onClick={() => void onCheck(n.id)}
                            >
                              {check === "checking" ? "Checking" : "Check"}
                            </button>
                            {mutate ? (
                              <>
                                <button className="btn btn-ghost btn-sm" type="button" onClick={() => setEditing(n)}>
                                  Address
                                </button>
                                <button
                                  className="btn btn-ghost btn-sm"
                                  type="button"
                                  disabled={busy}
                                  onClick={() => void onRevoke(n)}
                                >
                                  Revoke
                                </button>
                                <DeleteButton
                                  path={`/nodes/${encodeURIComponent(n.id)}`}
                                  name={n.name || n.id}
                                  noun="node"
                                  description="This removes the node record and the storage pools and networks recorded for it. Nodes that still have workloads are refused."
                                  onDeleted={reload}
                                />
                              </>
                            ) : null}
                          </div>
                        ) : null}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
      </section>

      <section className="section-block" aria-labelledby="cluster-ha-heading">
        <h2 id="cluster-ha-heading">High availability</h2>
        <p className="lede">
          One writer holds the lease. Fence records that the old writer is isolated, then promote takes the lease.
          Guests keep running when the control plane moves.
        </p>
        {ha ? (
          <p>
            Mode {ha.mode}. Writer {ha.writer ? "yes" : "no"}. Replica {replicaLabel}. Fencing {ha.fencing_mode}.
            {ha.lease_holder ? ` Lease ${ha.lease_holder}.` : ""}
          </p>
        ) : (
          <p className="muted">Collecting</p>
        )}
        {promote ? (
          <div className="btn-row">
            <button className="btn" type="button" disabled={busy} onClick={() => void onFence()}>
              Fence old writer
            </button>
            <button className="btn btn-primary" type="button" disabled={busy} onClick={() => void onPromote()}>
              Promote this writer
            </button>
          </div>
        ) : null}
      </section>

      <section className="section-block" aria-labelledby="cluster-rolling-heading">
        <h2 id="cluster-rolling-heading">Rolling update</h2>
        <p className="lede">Drain one node, apply the package update, then the next. Guests are not stopped.</p>
        {rolling?.preview && rolling.preview.length > 0 ? (
          <ol>
            {rolling.preview.map((step) => (
              <li key={`${step.node_id}-${step.ordinal}`}>
                {step.action} {step.name || step.node_id}
                {step.status ? ` (${step.status})` : ""}
              </li>
            ))}
          </ol>
        ) : (
          <p className="muted">No nodes in the rolling preview yet.</p>
        )}
        {mutate ? (
          <button className="btn" type="button" disabled={busy} onClick={() => void onRolling()}>
            Run rolling update
          </button>
        ) : null}
      </section>

      {mutate ? <JoinSettingsSection /> : null}

      <NodeGroupsPanel />

      <AddNodeDialog
        open={adding}
        known={new Set(list.map((n) => n.id))}
        onClose={() => setAdding(false)}
        onChanged={reloadNodes}
      />
      <AddressDialog node={editing} onClose={() => setEditing(null)} onSaved={onCheck} />
    </section>
  );
}
