import { useCallback, useEffect, useState } from "react";
import {
  applyEdge,
  createEdgeForward,
  deleteEdgeForward,
  getEdge,
  listWorkloads,
  putEdgeNode,
  updateEdgeForward,
  updateWorkerNode,
  type EdgeForward,
  type EdgeForwardInput,
  type EdgeKind,
  type EdgeNode,
  type EdgeOverview,
  type EdgePort,
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
import { usePoll } from "../query";
import { hasGrant } from "../rbac";
import { useSession } from "../session";
import { Dialog } from "../ui/Dialog";

// Edge forwarding. A service on a node with no public address (a home
// server behind a router) is published on a node that has one (a VPS in the
// cluster) through a WireGuard tunnel. Players connect to the edge and their
// own addresses reach the server.

const KIND_LABEL: Record<EdgeKind, string> = {
  gameserver: "Game server",
  workload: "Workload",
  host: "Host service",
};

const errText = (err: unknown, fallback: string) => (err instanceof Error ? err.message : fallback);
const num = new Intl.NumberFormat("en");

/** portText is "UDP 27015", "TCP 25565 to 25566" or "UDP 27000-27010". */
export function portText(p: EdgePort): string {
  const span = p.port_end && p.port_end > p.port ? `${p.port}-${p.port_end}` : `${p.port}`;
  const to = p.to_port && p.to_port !== p.port ? ` to ${p.to_port}` : "";
  return `${p.protocol.toUpperCase()} ${span}${to}`;
}

/** parsePorts reads one port per line: "udp 27015", "tcp 25565", "udp 27000-27010" or "udp 7777 to 27016". */
export function parsePorts(text: string): EdgePort[] {
  const out: EdgePort[] = [];
  for (const raw of text.split(/[\n,]+/)) {
    const line = raw.trim().toLowerCase();
    if (!line) {
      continue;
    }
    const m = /^(udp|tcp)\s+(\d{1,5})(?:\s*-\s*(\d{1,5}))?(?:\s*(?:to|->|>)\s*(\d{1,5}))?$/.exec(line);
    if (!m) {
      throw new Error(`"${raw.trim()}" is not a port. Write one per line, like udp 27015 or tcp 25565.`);
    }
    const p: EdgePort = { protocol: m[1] as "udp" | "tcp", port: Number(m[2]) };
    if (m[3]) {
      p.port_end = Number(m[3]);
    }
    if (m[4]) {
      p.to_port = Number(m[4]);
    }
    out.push(p);
  }
  return out;
}

/** connectAddress is where players join: the edge's address and the first UDP port (else the first port). */
export function connectAddress(f: EdgeForward): string {
  const p = f.resolved_ports.find((x) => x.protocol === "udp") ?? f.resolved_ports[0];
  return f.public_addr && p ? `${f.public_addr}:${p.port}` : "";
}

function forwardStatus(f: EdgeForward, data: EdgeOverview): { status: "ok" | "warning" | "failed" | "unknown"; label: string } {
  if (!f.enabled) {
    return { status: "unknown", label: "Off" };
  }
  if (!f.active) {
    return { status: "warning", label: "Not published" };
  }
  const t = data.tunnels.find((x) => x.id === f.tunnel_id);
  if (!t || !t.connected) {
    return { status: "failed", label: "Tunnel down" };
  }
  return { status: "ok", label: "Live" };
}

function refHref(f: EdgeForward): string | null {
  if (f.kind === "gameserver" && f.ref_id) {
    return `/game-servers/${f.ref_id}`;
  }
  if (f.kind === "workload" && f.ref_id) {
    return `/workloads/${f.ref_id}`;
  }
  return null;
}

/** EdgePage is the Edge forwarding section of the Network page. */
export function EdgePage({ embedded = false }: { embedded?: boolean } = {}) {
  const session = useSession();
  const user = session.status === "ready" ? session.user : null;
  const canManage = hasGrant(user, "network.apply");
  const canUpdate = hasGrant(user, "node.update");
  const [data, setData] = useState<EdgeOverview | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [editing, setEditing] = useState<{
    forward: EdgeForward | null;
  } | null>(null);

  const reload = useCallback(async () => {
    try {
      setData(await getEdge());
      setError(null);
    } catch (err) {
      setError(errText(err, "Edge forwarding could not be loaded"));
    }
  }, []);
  useEffect(() => {
    void reload();
  }, [reload]);
  usePoll(reload, 15000);

  async function act(label: string, fn: () => Promise<EdgeOverview | void>, done?: string) {
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

  if (!data) {
    return error ? <ErrorNotice error={error} /> : <LoadingState label="Loading edge forwarding" />;
  }
  const nodeName = (id?: string) => data.nodes.find((n) => n.id === id)?.name ?? "Unknown node";
  const oldAgents = data.nodes.filter((n) => n.error?.includes("too old"));

  const items = (f: EdgeForward): ActionItem[] => {
    if (!canManage) {
      return [];
    }
    return [
      { label: "Edit", onClick: () => setEditing({ forward: f }) },
      {
        label: f.enabled ? "Turn off" : "Turn on",
        onClick: () => void act(f.enabled ? "Turning off" : "Turning on", () => updateEdgeForward(f.id, { enabled: !f.enabled })),
      },
      {
        label: "Remove",
        danger: true,
        onClick: () => {
          if (confirmAction(`Stop publishing ${f.name} on ${f.edge_name || "the edge"}? Players using ${connectAddress(f) || "the edge address"} lose the server.`)) {
            void act("Removing", () => deleteEdgeForward(f.id), `${f.name} is no longer published on the edge.`);
          }
        },
      },
    ];
  };

  return (
    <section className={embedded ? "network-section" : "page page-wide"} id="edge" aria-labelledby="edge-heading">
      <PageHeader
        id="edge-heading"
        title="Edge forwarding"
        kicker="Publish a home service on a VPS in the cluster. Players connect to the VPS and reach the service through a tunnel with their own address."
        actions={
          <div className="btn-row is-flush">
            <button className="btn btn-secondary" type="button" onClick={() => void reload()}>
              Refresh
            </button>
            {canManage ? (
              <button className="btn btn-secondary" type="button" disabled={busy != null} onClick={() => void act("Applying", applyEdge, "Tunnels and forwards re-applied.")}>
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
      {oldAgents.map((n) => (
        <div key={n.id} className="banner banner-warn" role="status">
          {n.name} runs an older No-dal that cannot do edge forwarding. Update it to the same release as this node.{" "}
          {canUpdate && n.role === "worker" ? (
            <button
              className="btn btn-sm btn-secondary"
              type="button"
              disabled={busy != null}
              onClick={() => {
                if (confirmAction(`Update No-dal on ${n.name}? Its guests keep running; its agent restarts during the update.`)) {
                  void act("Updating", () => updateWorkerNode(n.id).then(() => undefined), `${n.name} is updating. This page picks it up when its agent is back.`);
                }
              }}
            >
              Update No-dal on {n.name}
            </button>
          ) : null}
        </div>
      ))}

      <div className="toolbar">
        <div className="toolbar-group">
          {canManage ? (
            <button className="btn btn-sm btn-primary" type="button" onClick={() => setEditing({ forward: null })}>
              Publish a service
            </button>
          ) : null}
        </div>
      </div>
      {data.forwards.length === 0 ? (
        <EmptyState title="Nothing is published through an edge">
          Add a VPS from <Link href="/network/cluster">Cluster</Link>, then publish a game server, a workload or a host service on it.
        </EmptyState>
      ) : (
        <div className="table-wrap">
          <table className="data-table">
            <thead>
              <tr>
                <th>Service</th>
                <th>Players connect to</th>
                <th>Runs on</th>
                <th>Forwarded</th>
                <th>Status</th>
                <th className="cell-actions">
                  <span className="visually-hidden">Actions</span>
                </th>
              </tr>
            </thead>
            <tbody>
              {data.forwards.map((f) => {
                const s = forwardStatus(f, data);
                const href = refHref(f);
                return (
                  <tr key={f.id}>
                    <td>
                      {href ? <Link href={href}>{f.name}</Link> : f.name}
                      <div className="meta">
                        {KIND_LABEL[f.kind]} · {f.kind === "workload" && !f.keep_address ? "player addresses hidden" : "player addresses passed on"} ·{" "}
                        {f.egress ? "outbound through the edge" : "outbound from its own node"}
                      </div>
                      {f.warnings?.map((w) => (
                        <div key={w} className="meta ddos-warning">
                          {w}
                        </div>
                      ))}
                    </td>
                    <td>
                      <code>{connectAddress(f) || "Not known yet"}</code>
                      <div className="meta">
                        {f.edge_name || nodeName(f.edge_node_id)} · {f.resolved_ports.map(portText).join(", ") || "No ports"}
                      </div>
                    </td>
                    <td>
                      {f.origin_name || nodeName(f.origin_node_id)}
                      {f.target ? <div className="meta">{f.target}</div> : null}
                    </td>
                    <td>
                      {num.format(f.packets)} packets
                      <div className="meta">{formatBytes(f.bytes)}</div>
                    </td>
                    <td>
                      <StatusBadge status={s.status} label={s.label} />
                    </td>
                    <td className="cell-actions">
                      <ActionMenu items={items(f)} />
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}

      {data.tunnels.length > 0 ? (
        <>
          <h2 className="section-title">Tunnels</h2>
          <div className="table-wrap">
            <table className="data-table">
              <thead>
                <tr>
                  <th>Edge</th>
                  <th>Origin</th>
                  <th>Last handshake</th>
                  <th>Traffic</th>
                  <th>Status</th>
                </tr>
              </thead>
              <tbody>
                {data.tunnels.map((t) => (
                  <tr key={t.id}>
                    <td>
                      {nodeName(t.edge_node_id)}
                      <div className="meta">
                        {t.edge_addr} · UDP {t.listen_port}
                      </div>
                    </td>
                    <td>
                      {nodeName(t.origin_node_id)}
                      <div className="meta">
                        {t.origin_addr}
                        {t.endpoint ? ` · from ${t.endpoint}` : ""}
                      </div>
                    </td>
                    <td>{t.handshake ? formatRelative(t.handshake) : "Never"}</td>
                    <td>
                      {formatBytes(t.rx_bytes)} in · {formatBytes(t.tx_bytes)} out
                      {t.egress?.length ? <div className="meta">Outbound from {t.egress.join(", ")}</div> : null}
                    </td>
                    <td>
                      <StatusBadge status={t.connected ? "ok" : "failed"} label={t.connected ? "Connected" : "Down"} />
                      {t.error ? <div className="meta ddos-warning">{t.error}</div> : null}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </>
      ) : null}

      <p className="meta ddos-footnote">
        Allow the forwarded ports and the tunnel's UDP port in to the edge, also in its provider's firewall. Point the service's DNS at the edge with a DNS only record. Edge public addresses are set on{" "}
        <Link href="/network/cluster">Cluster</Link>.
      </p>
      <ForwardDialog
        state={editing}
        data={data}
        onClose={() => setEditing(null)}
        onSaved={(res, msg) => {
          setData(res);
          setEditing(null);
          setNotice(msg);
          void reload();
        }}
      />
    </section>
  );
}

/** EdgeNodesSection lists the nodes that can be edges and their public addresses; it sits on the Cluster tab. */
export function EdgeNodesSection() {
  const session = useSession();
  const user = session.status === "ready" ? session.user : null;
  const canManage = hasGrant(user, "network.apply");
  const [data, setData] = useState<EdgeOverview | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const reload = useCallback(async () => {
    try {
      setData(await getEdge());
      setError(null);
    } catch (err) {
      setError(errText(err, "Edge addresses could not be loaded"));
    }
  }, []);
  useEffect(() => {
    void reload();
  }, [reload]);
  if (!data) {
    return error ? <ErrorNotice error={error} /> : null;
  }
  return (
    <section className="section-block" aria-labelledby="edge-nodes-heading">
      <h2 id="edge-nodes-heading" className="section-title">
        Edge addresses
      </h2>
      <p className="meta">The public address players reach each node on, used by Edge forwarding.</p>
      {error ? <ErrorNotice error={error} /> : null}
      <div className="table-wrap">
        <table className="data-table">
          <thead>
            <tr>
              <th>Node</th>
              <th>Public address</th>
              <th>Status</th>
            </tr>
          </thead>
          <tbody>
            {data.nodes.map((n) => (
              <NodeRow
                key={n.id}
                node={n}
                canManage={canManage}
                busy={busy}
                onSave={(addr) => {
                  setBusy(true);
                  void putEdgeNode(n.id, addr)
                    .then((res) => setData(res))
                    .catch((err: unknown) => setError(errText(err, "Saving failed")))
                    .finally(() => setBusy(false));
                }}
              />
            ))}
          </tbody>
        </table>
      </div>
    </section>
  );
}

function NodeRow({ node, canManage, busy, onSave }: { node: EdgeNode; canManage: boolean; busy: boolean; onSave: (addr: string) => void }) {
  const [addr, setAddr] = useState(node.set_addr ?? "");
  useEffect(() => setAddr(node.set_addr ?? ""), [node.set_addr]);
  return (
    <tr>
      <td>
        {node.name}
        <div className="meta">{node.local ? "This node" : node.role === "worker" ? "Worker" : node.role}</div>
      </td>
      <td>
        {canManage && !node.local ? (
          <div className="btn-row is-flush">
            <input
              className="field-input"
              aria-label={`Public address of ${node.name}`}
              value={addr}
              placeholder={node.public_addr || "Address players reach it on"}
              onChange={(e) => setAddr(e.target.value)}
            />
            <button className="btn btn-sm btn-secondary" type="button" disabled={busy || addr === (node.set_addr ?? "")} onClick={() => onSave(addr.trim())}>
              Save
            </button>
          </div>
        ) : (
          node.public_addr || <span className="muted">None</span>
        )}
        {!node.set_addr && node.public_addr ? <div className="meta">From its agent address</div> : null}
      </td>
      <td>
        {node.error ? (
          <span className="ddos-warning">{node.error}</span>
        ) : node.checked_at ? (
          <span className="muted">Checked {formatRelative(node.checked_at)}</span>
        ) : (
          <span className="muted">Nothing on it</span>
        )}
        {node.warnings?.map((w) => (
          <div key={w} className="meta ddos-warning">
            {w}
          </div>
        ))}
      </td>
    </tr>
  );
}

type Choice = { id: string; name: string; node?: string };

/** ForwardDialog publishes a service on an edge, or edits one. */
export function ForwardDialog({
  state,
  data,
  preset,
  onClose,
  onSaved,
}: {
  state: { forward: EdgeForward | null } | null;
  data: EdgeOverview;
  preset?: { kind: EdgeKind; ref_id: string };
  onClose: () => void;
  onSaved: (res: EdgeOverview, msg: string) => void;
}) {
  const f = state?.forward ?? null;
  const [kind, setKind] = useState<EdgeKind>("gameserver");
  const [refID, setRefID] = useState("");
  const [edgeID, setEdgeID] = useState("");
  const [originID, setOriginID] = useState("");
  const [ports, setPorts] = useState("");
  const [keep, setKeep] = useState(false);
  const [egress, setEgress] = useState(true);
  const [games, setGames] = useState<Choice[]>([]);
  const [workloads, setWorkloads] = useState<Choice[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const workers = data.nodes.filter((n) => !n.local);
  useEffect(() => {
    if (!state) {
      return;
    }
    setError(null);
    setKind(f?.kind ?? preset?.kind ?? "gameserver");
    setRefID(f?.ref_id ?? preset?.ref_id ?? "");
    setEdgeID(f?.edge_node_id ?? workers.find((n) => n.public_addr)?.id ?? workers[0]?.id ?? "");
    setOriginID(f?.origin_node_id ?? data.nodes.find((n) => n.local)?.id ?? "");
    setPorts((f?.ports ?? []).map(portText).join("\n").toLowerCase());
    setKeep(f?.keep_address ?? false);
    setEgress(f?.egress ?? (f?.kind ?? preset?.kind ?? "gameserver") === "gameserver");
    void listGameServers()
      .then((r) => setGames(r.items.map((g) => ({ id: g.id, name: g.name }))))
      .catch(() => setGames([]));
    void listWorkloads()
      .then((r) => setWorkloads(r.items.map((w) => ({ id: w.id, name: w.name }))))
      .catch(() => setWorkloads([]));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [state]);

  function save() {
    let parsed: EdgePort[];
    try {
      parsed = parsePorts(ports);
    } catch (err) {
      setError(errText(err, "Ports are not valid"));
      return;
    }
    const body: EdgeForwardInput = {
      edge_node_id: edgeID,
      ports: parsed,
      keep_address: keep,
      egress,
    };
    if (!f) {
      body.kind = kind;
      if (kind === "host") {
        body.origin_node_id = originID;
        body.name = `Host ports on ${data.nodes.find((n) => n.id === originID)?.name ?? "node"}`;
      } else {
        body.ref_id = refID;
      }
    } else if (kind === "host") {
      body.origin_node_id = originID;
    }
    setBusy(true);
    setError(null);
    const call = f ? updateEdgeForward(f.id, body) : createEdgeForward(body);
    call
      .then((res) => {
        const saved = res.forwards.find((x) => (f ? x.id === f.id : x.kind === kind && (kind === "host" || x.ref_id === refID) && x.edge_node_id === edgeID));
        const where = saved ? connectAddress(saved) : "";
        onSaved(res, where ? `Published. Players connect to ${where}.` : "Saved.");
      })
      .catch((err) => setError(errText(err, "Saving failed")))
      .finally(() => setBusy(false));
  }

  const choices = kind === "gameserver" ? games : workloads;
  return (
    <Dialog
      open={state != null}
      title={f ? `Edit ${f.name}` : "Publish a service on an edge"}
      onClose={onClose}
      footer={
        <div className="btn-row">
          <button className="btn btn-ghost" type="button" onClick={onClose}>
            Cancel
          </button>
          <button className="btn btn-primary" type="button" disabled={busy || !edgeID || (!f && kind !== "host" && !refID)} onClick={save}>
            {busy ? "Saving" : f ? "Save" : "Publish"}
          </button>
        </div>
      }
    >
      {error ? <ErrorNotice error={error} /> : null}
      {workers.length === 0 ? <p className="banner banner-warn">There is no other node to publish on. Add your VPS from Cluster first.</p> : null}
      {!f && !preset ? (
        <div className="field">
          <label className="field-label" htmlFor="edge-kind">
            What to publish
          </label>
          <select id="edge-kind" className="field-input" value={kind} onChange={(e) => setKind(e.target.value as EdgeKind)}>
            <option value="gameserver">A game server</option>
            <option value="workload">A workload</option>
            <option value="host">Ports on a node</option>
          </select>
        </div>
      ) : null}
      {!f && !preset && kind !== "host" ? (
        <div className="field">
          <label className="field-label" htmlFor="edge-ref">
            {kind === "gameserver" ? "Game server" : "Workload"}
          </label>
          <select id="edge-ref" className="field-input" value={refID} onChange={(e) => setRefID(e.target.value)}>
            <option value="">Choose</option>
            {choices.map((c) => (
              <option key={c.id} value={c.id}>
                {c.name}
              </option>
            ))}
          </select>
        </div>
      ) : null}
      {kind === "host" ? (
        <div className="field">
          <label className="field-label" htmlFor="edge-origin">
            Node the service runs on
          </label>
          <select id="edge-origin" className="field-input" value={originID} onChange={(e) => setOriginID(e.target.value)}>
            {data.nodes.map((n) => (
              <option key={n.id} value={n.id}>
                {n.name}
              </option>
            ))}
          </select>
        </div>
      ) : null}
      <div className="field">
        <label className="field-label" htmlFor="edge-node">
          Publish on
        </label>
        <select id="edge-node" className="field-input" value={edgeID} onChange={(e) => setEdgeID(e.target.value)}>
          {workers.map((n) => (
            <option key={n.id} value={n.id}>
              {n.name}
              {n.public_addr ? ` (${n.public_addr})` : ""}
            </option>
          ))}
        </select>
        <p className="field-hint">The node players connect to. It needs a public address.</p>
      </div>
      <div className="field">
        <label className="field-label" htmlFor="edge-ports">
          Ports
        </label>
        <textarea
          id="edge-ports"
          className="field-input"
          rows={3}
          value={ports}
          placeholder={kind === "gameserver" ? "Leave empty to use the server's ports" : "udp 27015\ntcp 25565"}
          onChange={(e) => setPorts(e.target.value)}
        />
        <p className="field-hint">
          One per line: udp 27015, tcp 25565, udp 27000-27010, or udp 7777 to 27016 for a different port on the edge.
          {kind === "gameserver" ? " Left empty, every published port is used except a Source server's TCP port, which is only RCON and stays off the internet." : ""}
        </p>
      </div>
      {kind !== "host" ? (
        <label className="check-row">
          <input type="checkbox" checked={egress} onChange={(e) => setEgress(e.target.checked)} />
          <span>
            Send its own internet traffic out through the edge too
            <span className="field-hint">
              {" "}
              Steam then lists the server at the edge's address, and your home address is never seen. If the tunnel goes down, it gets no internet rather than falling back.
            </span>
          </span>
        </label>
      ) : null}
      {kind === "workload" ? (
        <label className="check-row">
          <input type="checkbox" checked={keep} onChange={(e) => setKeep(e.target.checked)} />
          <span>
            Pass players' addresses on
            <span className="field-hint">
              {" "}
              Only when the workload's traffic goes through its node (a NAT network). On a LAN bridge it answers through your router, so leave this off.
            </span>
          </span>
        </label>
      ) : null}
    </Dialog>
  );
}
