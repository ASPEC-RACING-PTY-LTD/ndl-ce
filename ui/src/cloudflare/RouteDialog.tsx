import { useEffect, useMemo, useState } from "react";
import {
  planCfRoute,
  publishWorkload,
  upsertCfRoute,
  type CfAccount,
  type CfDNSPlan,
  type CfOriginOptions,
  type CfRoute,
  type CfRouteResult,
  type CfTunnel,
} from "../api/client";
import { ErrorNotice } from "../components/ErrorNotice";
import { StatusBadge } from "../components/StatusBadge";
import { Dialog } from "../ui/Dialog";

const PROTOCOLS = ["http", "https", "tcp", "ssh", "rdp", "smb"] as const;
const DEFAULT_PORT: Record<string, number> = { http: 80, https: 443, ssh: 22, rdp: 3389, smb: 445, tcp: 0 };

export type RouteDialogProps = {
  open: boolean;
  onClose: () => void;
  onSaved: (res: CfRouteResult) => void;
  accounts: CfAccount[];
  tunnels: CfTunnel[];
  /** Route to edit; omitted to add one. */
  route?: CfRoute | null;
  /** Publishing a workload: its address and suggested origins. */
  workload?: { id: string; name: string; suggestions: { label: string; host: string; port?: number }[]; preferred?: { account_id: string; tunnel_id: string } };
};

function dnsText(plan: CfDNSPlan | null): { status: string; text: string } | null {
  if (!plan) {
    return null;
  }
  switch (plan.action) {
    case "create":
      return { status: "ok", text: "A proxied CNAME record will be created for this hostname." };
    case "ok":
      return { status: "ok", text: "DNS already points at this tunnel." };
    case "proxy":
      return { status: "warning", text: "The record points at this tunnel but is not proxied; it will be switched to proxied." };
    case "retarget":
      return { status: "warning", text: plan.reason || "The hostname points at another tunnel." };
    case "conflict":
      return { status: "failed", text: plan.reason || "Another record uses this name." };
  }
  return { status: "unknown", text: plan.reason || plan.action };
}

/** RouteDialog adds or edits a public hostname route, or publishes a workload. */
export function RouteDialog({ open, onClose, onSaved, accounts, tunnels, route, workload }: RouteDialogProps) {
  const editable = tunnels.filter((t) => t.remote_managed);
  const [accountID, setAccountID] = useState("");
  const [tunnelID, setTunnelID] = useState("");
  const [hostname, setHostname] = useState("");
  const [path, setPath] = useState("");
  const [protocol, setProtocol] = useState("http");
  const [host, setHost] = useState("");
  const [port, setPort] = useState("");
  const [opts, setOpts] = useState<CfOriginOptions>({});
  const [dns, setDNS] = useState(true);
  const [replaceDNS, setReplaceDNS] = useState(false);
  const [plan, setPlan] = useState<CfDNSPlan | null>(null);
  const [planErr, setPlanErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!open) {
      return;
    }
    const pref = workload?.preferred;
    const firstTunnel = editable.find((t) => (pref ? t.id === pref.tunnel_id : true)) ?? editable[0];
    setAccountID(route?.account_id ?? pref?.account_id ?? firstTunnel?.account_tag ?? accounts[0]?.id ?? "");
    setTunnelID(route?.tunnel_id ?? (pref?.tunnel_id || firstTunnel?.id) ?? "");
    setHostname(route?.hostname ?? "");
    setPath(route?.path ?? "");
    setProtocol(route?.origin?.protocol ?? "http");
    const sug = workload?.suggestions[0];
    setHost(route?.origin?.host ?? sug?.host ?? "");
    setPort(String(route?.origin?.port ?? sug?.port ?? ""));
    setOpts(route?.options ?? {});
    setDNS(true);
    setReplaceDNS(false);
    setPlan(null);
    setError(null);
    // Reset only when the dialog opens.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  const accountTunnels = useMemo(() => editable.filter((t) => t.account_tag === accountID), [editable, accountID]);
  const hostOK = /^(\*\.)?([a-z0-9]([a-z0-9-]*[a-z0-9])?\.)+[a-z]{2,}$/i.test(hostname.trim());

  useEffect(() => {
    if (!open || !dns || !hostOK || !tunnelID || !accountID) {
      setPlan(null);
      setPlanErr(null);
      return;
    }
    const timer = window.setTimeout(() => {
      planCfRoute({ account_id: accountID, tunnel_id: tunnelID, hostname: hostname.trim() })
        .then((res) => {
          setPlan(res.plan);
          setPlanErr(null);
        })
        .catch((err) => {
          setPlan(null);
          setPlanErr(err instanceof Error ? err.message : "Could not check DNS");
        });
    }, 400);
    return () => window.clearTimeout(timer);
  }, [open, dns, hostOK, hostname, tunnelID, accountID]);

  const dnsNote = dnsText(plan);
  const blocked = dns && (plan?.action === "conflict" || (plan?.action === "retarget" && !replaceDNS) || !!planErr);

  async function save() {
    setBusy(true);
    setError(null);
    const body = {
      account_id: accountID,
      tunnel_id: tunnelID,
      hostname: hostname.trim(),
      path: path.trim() || undefined,
      protocol,
      host: host.trim(),
      port: Number(port) || DEFAULT_PORT[protocol] || 0,
      options: opts,
      dns,
      replace_dns: replaceDNS,
      ...(route && (route.hostname !== hostname.trim() || (route.path ?? "") !== path.trim())
        ? { original: { account_id: route.account_id, tunnel_id: route.tunnel_id, hostname: route.hostname, path: route.path } }
        : {}),
    };
    try {
      const res = workload ? await publishWorkload(workload.id, body) : await upsertCfRoute(body);
      onSaved(res);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not save the route");
    } finally {
      setBusy(false);
    }
  }

  const title = route ? `Edit ${route.hostname}` : workload ? `Publish ${workload.name}` : "Add public hostname";
  return (
    <Dialog
      open={open}
      title={title}
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
            disabled={busy || !hostOK || !tunnelID || !host.trim() || blocked}
            onClick={() => void save()}
          >
            {busy ? "Saving" : route ? "Save" : workload ? "Publish" : "Add hostname"}
          </button>
        </div>
      }
    >
      {editable.length === 0 ? (
        <p className="banner banner-warn">No dashboard-managed tunnel is available. Create a tunnel first, or add a token that can edit one.</p>
      ) : null}
      {error ? <ErrorNotice error={error} /> : null}
      <div className="oci-grid">
        <div className="field">
          <label className="field-label" htmlFor="cf-route-account">
            Account
          </label>
          <select
            id="cf-route-account"
            className="field-input"
            value={accountID}
            disabled={!!route}
            onChange={(e) => {
              setAccountID(e.target.value);
              setTunnelID(editable.find((t) => t.account_tag === e.target.value)?.id ?? "");
            }}
          >
            {accounts.map((a) => (
              <option key={a.id} value={a.id}>
                {a.name}
              </option>
            ))}
          </select>
        </div>
        <div className="field">
          <label className="field-label" htmlFor="cf-route-tunnel">
            Tunnel
          </label>
          <select id="cf-route-tunnel" className="field-input" value={tunnelID} disabled={!!route} onChange={(e) => setTunnelID(e.target.value)}>
            {accountTunnels.length === 0 ? <option value="">No editable tunnel in this account</option> : null}
            {accountTunnels.map((t) => (
              <option key={t.id} value={t.id}>
                {t.name} ({t.status})
              </option>
            ))}
          </select>
          {route ? <p className="field-hint">Use Move to change a route's tunnel.</p> : null}
        </div>
        <div className="field">
          <label className="field-label" htmlFor="cf-route-host">
            Public hostname
          </label>
          <input id="cf-route-host" className="field-input" value={hostname} placeholder="app.example.com" onChange={(e) => setHostname(e.target.value)} />
        </div>
        <div className="field">
          <label className="field-label" htmlFor="cf-route-path">
            Path (optional)
          </label>
          <input id="cf-route-path" className="field-input" value={path} placeholder="^/api" onChange={(e) => setPath(e.target.value)} />
        </div>
      </div>
      <fieldset className="field">
        <legend className="field-label">Origin (where the connector sends traffic)</legend>
        {workload && workload.suggestions.length > 0 ? (
          <div className="btn-row is-flush">
            {workload.suggestions.map((s) => (
              <button
                key={s.label}
                className="btn btn-sm btn-ghost"
                type="button"
                onClick={() => {
                  setHost(s.host);
                  if (s.port) {
                    setPort(String(s.port));
                  }
                }}
              >
                Use {s.label}
              </button>
            ))}
          </div>
        ) : null}
        <div className="oci-grid">
          <div className="field">
            <label className="field-label" htmlFor="cf-route-proto">
              Protocol
            </label>
            <select id="cf-route-proto" className="field-input" value={protocol} onChange={(e) => setProtocol(e.target.value)}>
              {PROTOCOLS.map((p) => (
                <option key={p} value={p}>
                  {p.toUpperCase()}
                </option>
              ))}
            </select>
          </div>
          <div className="field">
            <label className="field-label" htmlFor="cf-route-origin">
              Address
            </label>
            <input id="cf-route-origin" className="field-input" value={host} placeholder="192.168.2.38 or localhost" onChange={(e) => setHost(e.target.value)} />
          </div>
          <div className="field">
            <label className="field-label" htmlFor="cf-route-port">
              Port
            </label>
            <input
              id="cf-route-port"
              className="field-input"
              inputMode="numeric"
              value={port}
              placeholder={String(DEFAULT_PORT[protocol] || "")}
              onChange={(e) => setPort(e.target.value.replace(/[^0-9]/g, ""))}
            />
          </div>
        </div>
        <p className="field-hint">localhost means the machine the tunnel's connector runs on, not this browser.</p>
      </fieldset>
      <details>
        <summary>Origin and TLS options</summary>
        <div className="stack">
          <label className="check-row">
            <input type="checkbox" checked={!!opts.no_tls_verify} onChange={(e) => setOpts({ ...opts, no_tls_verify: e.target.checked })} />
            <span>Do not verify the origin's TLS certificate (self-signed origins)</span>
          </label>
          <label className="check-row">
            <input type="checkbox" checked={!!opts.http2_origin} onChange={(e) => setOpts({ ...opts, http2_origin: e.target.checked })} />
            <span>Connect to the origin over HTTP/2</span>
          </label>
          <label className="check-row">
            <input type="checkbox" checked={!!opts.disable_chunked_encoding} onChange={(e) => setOpts({ ...opts, disable_chunked_encoding: e.target.checked })} />
            <span>Disable chunked transfer encoding</span>
          </label>
          <div className="oci-grid">
            <div className="field">
              <label className="field-label" htmlFor="cf-route-sni">
                Origin server name (TLS)
              </label>
              <input id="cf-route-sni" className="field-input" value={opts.origin_server_name ?? ""} onChange={(e) => setOpts({ ...opts, origin_server_name: e.target.value })} />
            </div>
            <div className="field">
              <label className="field-label" htmlFor="cf-route-hosthdr">
                Host header sent to the origin
              </label>
              <input id="cf-route-hosthdr" className="field-input" value={opts.http_host_header ?? ""} onChange={(e) => setOpts({ ...opts, http_host_header: e.target.value })} />
            </div>
            <div className="field">
              <label className="field-label" htmlFor="cf-route-timeout">
                Connect timeout (seconds)
              </label>
              <input
                id="cf-route-timeout"
                className="field-input"
                inputMode="numeric"
                value={opts.connect_timeout_seconds ? String(opts.connect_timeout_seconds) : ""}
                onChange={(e) => setOpts({ ...opts, connect_timeout_seconds: Number(e.target.value.replace(/[^0-9]/g, "")) || undefined })}
              />
            </div>
          </div>
        </div>
      </details>
      <label className="check-row">
        <input type="checkbox" checked={dns} onChange={(e) => setDNS(e.target.checked)} />
        <span>Create or fix the DNS record for this hostname</span>
      </label>
      {dns && dnsNote ? (
        <p className={"banner" + (dnsNote.status === "failed" ? " banner-error" : dnsNote.status === "warning" ? " banner-warn" : " banner-ok")} role="status">
          <StatusBadge status={dnsNote.status} label="DNS" /> {dnsNote.text}
        </p>
      ) : null}
      {dns && planErr ? <ErrorNotice error={planErr} /> : null}
      {dns && plan?.action === "retarget" ? (
        <label className="check-row">
          <input type="checkbox" checked={replaceDNS} onChange={(e) => setReplaceDNS(e.target.checked)} />
          <span>Repoint the record at this tunnel (the other tunnel stops serving this hostname)</span>
        </label>
      ) : null}
    </Dialog>
  );
}
