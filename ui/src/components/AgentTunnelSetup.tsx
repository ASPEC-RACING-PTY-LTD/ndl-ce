import { useEffect, useState } from "react";
import {
  ApiError,
  createAgentTunnel,
  getCfInventory,
  type AgentTunnel,
  type CfAccount,
} from "../api/client";
import { RelativeTime } from "../ui/RelativeTime";
import { ErrorNotice } from "./ErrorNotice";

export const CLOUDFLARE_AGENT_PREFIX = "cloudflare:";

/** tunnelHost is the hostname of a cloudflare:<hostname> agent address. */
export function tunnelHost(addr: string | undefined): string | null {
  const v = (addr ?? "").trim();
  return v.toLowerCase().startsWith(CLOUDFLARE_AGENT_PREFIX)
    ? v.slice(CLOUDFLARE_AGENT_PREFIX.length).trim()
    : null;
}

/**
 * AgentTunnelSetup reaches a worker's agent through a Cloudflare tunnel:
 * either a tunnel you already made (its hostname has a TCP route to
 * localhost:9444 on the worker), or one No-dal makes in a linked account,
 * with a one-time command that installs the connector on the worker.
 */
export function AgentTunnelSetup({
  hostname,
  onHostname,
  onCreated,
}: {
  hostname: string;
  onHostname: (h: string) => void;
  onCreated: (t: AgentTunnel) => void;
}) {
  const [mode, setMode] = useState<"existing" | "create">("existing");
  const [accounts, setAccounts] = useState<CfAccount[] | null>(null);
  const [account, setAccount] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [created, setCreated] = useState<AgentTunnel | null>(null);

  useEffect(() => {
    if (mode !== "create" || accounts) {
      return;
    }
    getCfInventory()
      .then((inv) => {
        const list = inv.accounts ?? [];
        setAccounts(list);
        setAccount((cur) => cur || list[0]?.id || "");
      })
      .catch(() => setAccounts([]));
  }, [mode, accounts]);

  async function create() {
    setBusy(true);
    setError(null);
    try {
      const t = await createAgentTunnel(account, hostname.trim());
      setCreated(t);
      onCreated(t);
    } catch (err) {
      setError(
        err instanceof ApiError || err instanceof Error
          ? err.message
          : "Could not set up the tunnel",
      );
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="agent-tunnel stack">
      <div className="btn-row is-flush" role="radiogroup" aria-label="Tunnel">
        <label className="check">
          <input
            type="radio"
            name="agent-tunnel-mode"
            checked={mode === "existing"}
            onChange={() => setMode("existing")}
          />{" "}
          I already made the tunnel
        </label>
        <label className="check">
          <input
            type="radio"
            name="agent-tunnel-mode"
            checked={mode === "create"}
            onChange={() => setMode("create")}
          />{" "}
          Make it for me
        </label>
      </div>
      <label className="field">
        <span className="field-label">Tunnel hostname</span>
        <input
          className="field-input"
          value={hostname}
          placeholder="agent-node1.example.com"
          onChange={(e) => {
            onHostname(e.target.value);
            setCreated(null);
          }}
        />
        <span className="field-hint">
          {mode === "existing"
            ? "A public hostname of the node's tunnel, with service type TCP and URL localhost:9444."
            : "A hostname in one of the account's zones. No-dal routes it to the node's agent port and adds its DNS record."}
        </span>
      </label>
      {mode === "create" ? (
        accounts && accounts.length === 0 ? (
          <p className="banner banner-warn">
            No Cloudflare account is linked. Add one under Network, Cloudflare,
            Manage accounts.
          </p>
        ) : (
          <>
            <label className="field">
              <span className="field-label">Cloudflare account</span>
              <select
                className="field-input"
                value={account}
                onChange={(e) => setAccount(e.target.value)}
              >
                {(accounts ?? []).map((a) => (
                  <option key={a.id} value={a.id}>
                    {a.name || a.id}
                  </option>
                ))}
              </select>
            </label>
            <div className="btn-row is-flush">
              <button
                className="btn btn-secondary"
                type="button"
                disabled={busy || !account || !hostname.trim()}
                onClick={() => void create()}
              >
                {created ? "Set up again" : "Set up tunnel"}
              </button>
            </div>
          </>
        )
      ) : null}
      {error ? <ErrorNotice error={error} /> : null}
      {created ? (
        <div className="stack">
          <p>
            Tunnel <strong>{created.tunnel_name}</strong> routes{" "}
            <code>{created.hostname}</code> to the node&apos;s agent. Run this
            once on the node to start its connector:
          </p>
          <pre className="code-block">
            <code>{created.install_command}</code>
          </pre>
          <p className="meta">
            Single use, expires <RelativeTime value={created.expires_at} />. It
            installs cloudflared from Cloudflare&apos;s repository if needed and
            runs it as cloudflared-ndl-agent.
          </p>
          {created.warnings?.map((w) => (
            <p key={w} className="banner banner-warn">
              {w}
            </p>
          ))}
        </div>
      ) : null}
      <p className="meta">
        The control node connects with cloudflared access, so cloudflared must
        be installed here too. Mutual TLS still runs inside the tunnel. If the
        hostname is behind Cloudflare Access, the service token saved under Join
        settings is used.
      </p>
    </div>
  );
}
