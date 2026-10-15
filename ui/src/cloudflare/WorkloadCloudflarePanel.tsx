import { useEffect, useState } from "react";
import { getCfInventory, getWorkloadCloudflare, syncCfRouteIP, type CfInventory, type CfWorkloadInfo } from "../api/client";
import { ErrorNotice } from "../components/ErrorNotice";
import { Link } from "../components/Link";
import { StatusBadge } from "../components/StatusBadge";
import { hasGrant } from "../rbac";
import { useSession } from "../session";
import { RouteDialog } from "./RouteDialog";

/** WorkloadCloudflarePanel shows the public hostnames that reach a workload and publishes new ones. */
export function WorkloadCloudflarePanel({ workload }: { workload: { id: string; name: string } }) {
  const session = useSession();
  const user = session.status === "ready" ? session.user : null;
  const canRead = hasGrant(user, "cloudflare.read");
  const canManage = hasGrant(user, "cloudflare.manage");
  const [info, setInfo] = useState<CfWorkloadInfo | null>(null);
  const [inv, setInv] = useState<CfInventory | null>(null);
  const [open, setOpen] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  async function reload() {
    try {
      setInfo(await getWorkloadCloudflare(workload.id));
      setError(null);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Cloudflare is unavailable");
    }
  }

  useEffect(() => {
    if (canRead) {
      void reload();
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [workload.id, canRead]);

  if (!canRead || !info) {
    return null;
  }
  if (!info.connected && info.routes.length === 0) {
    return (
      <article className="panel" aria-label="Cloudflare">
        <h2>Cloudflare</h2>
        <p className="meta">
          Not published. <Link href="/cloudflare">Connect a Cloudflare account</Link> to give this workload a public hostname.
        </p>
      </article>
    );
  }
  return (
    <article className="panel" aria-label="Cloudflare">
      <div className="btn-row is-flush">
        <h2>Cloudflare</h2>
        {canManage ? (
          <button
            className="btn btn-sm btn-secondary"
            type="button"
            onClick={() => {
              void getCfInventory()
                .then((i) => {
                  setInv(i);
                  setOpen(true);
                })
                .catch((err) => setError(err instanceof Error ? err.message : "Cloudflare is unavailable"));
            }}
          >
            Publish
          </button>
        ) : null}
      </div>
      {error ? <ErrorNotice error={error} /> : null}
      {notice ? <p className="banner banner-ok">{notice}</p> : null}
      {info.routes.length === 0 ? (
        <p className="meta">No public hostname reaches this workload. Nothing is published unless you choose Publish.</p>
      ) : (
        <ul className="plain-list" aria-label="Public hostnames">
          {info.routes.map((r) => (
            <li key={r.key}>
              <StatusBadge status={r.enabled ? (r.dns?.action === "ok" ? "healthy" : "warning") : "unknown"} label={r.enabled ? "On" : "Off"} />{" "}
              <a href={`https://${r.hostname}${r.path ?? ""}`} target="_blank" rel="noopener noreferrer">
                {r.hostname}
                {r.path ?? ""}
              </a>{" "}
              <span className="meta">
                → <code>{r.service}</code> via {r.tunnel_name} ({r.account_name})
              </span>
              {r.ip_changed && canManage ? (
                <div className="banner banner-warn">
                  This workload's address changed to {r.workload?.current_ip}.{" "}
                  <button
                    className="btn btn-sm btn-secondary"
                    type="button"
                    onClick={() =>
                      void syncCfRouteIP({ account_id: r.account_id, tunnel_id: r.tunnel_id, hostname: r.hostname, path: r.path })
                        .then((res) => {
                          setNotice(`${res.hostname} now goes to ${res.service}.`);
                          return reload();
                        })
                        .catch((err) => setError(err instanceof Error ? err.message : "Update failed"))
                    }
                  >
                    Update the route
                  </button>
                </div>
              ) : null}
            </li>
          ))}
        </ul>
      )}
      {inv ? (
        <RouteDialog
          open={open}
          accounts={inv.accounts ?? []}
          tunnels={inv.tunnels ?? []}
          workload={{ id: workload.id, name: workload.name, suggestions: info.suggestions, preferred: info.preferred }}
          onClose={() => setOpen(false)}
          onSaved={(res) => {
            setOpen(false);
            setNotice(`${res.hostname} published to ${res.service}.`);
            void reload();
          }}
        />
      ) : null}
    </article>
  );
}
