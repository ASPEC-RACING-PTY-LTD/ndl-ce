import { useCallback, useEffect, useState } from "react";
import { getEdge, type EdgeOverview } from "../api/client";
import { Link } from "../components/Link";
import { StatusBadge } from "../components/StatusBadge";
import { connectAddress, ForwardDialog, portText } from "../pages/EdgePage";
import { hasGrant } from "../rbac";
import { useSession } from "../session";

/** GameEdgePane shows where players connect when a game server is published through an edge node (Network > Edge forwarding). */
export function GameEdgePane({ id }: { id: string }) {
  const session = useSession();
  const user = session.status === "ready" ? session.user : null;
  const canManage = hasGrant(user, "network.apply");
  const [data, setData] = useState<EdgeOverview | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [publishing, setPublishing] = useState(false);
  const [notice, setNotice] = useState<string | null>(null);
  const load = useCallback(() => {
    void getEdge()
      .then((d) => {
        setData(d);
        setError(null);
      })
      .catch((err) => setError(err instanceof Error ? err.message : "Edge forwarding could not be loaded"));
  }, []);
  const canRead = hasGrant(user, "network.read");
  useEffect(() => {
    if (canRead) {
      load();
    }
  }, [load, id, canRead]);
  if (!canRead) {
    return <p className="muted">Where players connect is managed on Network, which your account cannot see.</p>;
  }
  if (error) {
    return <p className="muted">{error}</p>;
  }
  if (!data) {
    return <p className="muted">Loading</p>;
  }
  const forwards = data.forwards.filter((f) => f.kind === "gameserver" && f.ref_id === id);
  const dialog = (
    <ForwardDialog
      state={publishing ? { forward: null } : null}
      data={data}
      preset={{ kind: "gameserver", ref_id: id }}
      onClose={() => setPublishing(false)}
      onSaved={(res, msg) => {
        setData(res);
        setPublishing(false);
        setNotice(msg);
      }}
    />
  );
  if (forwards.length === 0) {
    return (
      <div className="gs-form">
        <p className="muted">
          Players connect to this node directly. To give the server the public address of another node, such as a VPS, and keep your home address private, publish it through that
          node.
        </p>
        {notice ? <p className="banner banner-ok">{notice}</p> : null}
        {canManage && data.nodes.some((n) => !n.local) ? (
          <button className="btn btn-sm btn-primary" type="button" onClick={() => setPublishing(true)}>
            Publish through another node
          </button>
        ) : null}
        {dialog}
      </div>
    );
  }
  return (
    <div className="gs-form">
      {notice ? <p className="banner banner-ok">{notice}</p> : null}
      {forwards.map((f) => {
        const tunnel = data.tunnels.find((t) => t.id === f.tunnel_id);
        const live = f.enabled && f.active && tunnel?.connected;
        return (
          <dl key={f.id} className="definition-list compact">
            <div>
              <dt>Players connect to</dt>
              <dd>
                <code>{connectAddress(f) || "Not known yet"}</code>
              </dd>
            </div>
            <div>
              <dt>Through</dt>
              <dd>{f.edge_name}</dd>
            </div>
            <div>
              <dt>Status</dt>
              <dd>
                <StatusBadge status={live ? "ok" : f.enabled ? "failed" : "unknown"} label={live ? "Live" : !f.enabled ? "Off" : !f.active ? "Not published" : "Tunnel down"} />
              </dd>
            </div>
            <div>
              <dt>Ports</dt>
              <dd>{f.resolved_ports.map(portText).join(", ")}</dd>
            </div>
            <div>
              <dt>Player addresses</dt>
              <dd>Passed on to the server</dd>
            </div>
            <div>
              <dt>Server's own traffic</dt>
              <dd>{f.egress ? "Leaves through the edge (Steam lists the edge's address)" : "Leaves from this node"}</dd>
            </div>
            {f.warnings?.map((w) => (
              <div key={w}>
                <dt>Warning</dt>
                <dd>{w}</dd>
              </div>
            ))}
            {tunnel?.error ? (
              <div>
                <dt>Tunnel</dt>
                <dd>{tunnel.error}</dd>
              </div>
            ) : null}
          </dl>
        );
      })}
      <p className="meta">
        Ports, the edge node and the tunnel are managed on <Link href="/network/edge">Edge forwarding</Link>.
      </p>
    </div>
  );
}
