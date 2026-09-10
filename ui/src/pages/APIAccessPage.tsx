import { useEffect, useState } from "react";
import {
  ApiError,
  createAPIToken,
  createServicePrincipal,
  listAPITokens,
  listServicePrincipals,
  revokeAPIToken,
  type APITokenItem,
  type CreatedAPIToken,
  type ServicePrincipalItem,
} from "../api/client";
import { PageHeader } from "../components/PageHeader";
import { formatWhen } from "../format";
import { useSession } from "../session";
import { hasGrant } from "../rbac";

type Reveal = { label: string; token: string };

export function APIAccessPage() {
  const session = useSession();
  const user = session.status === "ready" ? session.user : null;
  const mutate = hasGrant(user, "api_access.manage") || hasGrant(user, "identity.token.create");
  const admin = hasGrant(user, "identity.service");
  const [tokens, setTokens] = useState<APITokenItem[]>([]);
  const [principals, setPrincipals] = useState<ServicePrincipalItem[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [reveal, setReveal] = useState<Reveal | null>(null);
  const [name, setName] = useState("diagnostic");
  const [preset, setPreset] = useState("readonly-debug");
  const [ttl, setTtl] = useState("72");
  const [spName, setSpName] = useState("diag-agent");
  const [spPreset, setSpPreset] = useState("readonly-debug");
  const [spTtl, setSpTtl] = useState("168");

  async function reload() {
    const listed = await listAPITokens(true);
    setTokens(listed.items ?? []);
    if (admin) {
      const sps = await listServicePrincipals();
      setPrincipals(sps.items ?? []);
    }
  }

  useEffect(() => {
    void reload().catch((err) => setError(err instanceof Error ? err.message : "Unavailable"));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [admin]);

  function created(label: string, body: CreatedAPIToken | { token: string }) {
    setReveal({ label, token: body.token });
    return reload();
  }

  return (
    <section className="page" aria-labelledby="api-access-heading">
      <PageHeader
        id="api-access-heading"
        title="API Access"
        kicker="REST tokens and service accounts for integrations and diagnostic agents. This is not MCP and not account profile settings."
      />
      {error ? (
        <p className="banner banner-error" role="alert">
          {error}
        </p>
      ) : null}
      <section className="section">
        <h2>How to call the REST API</h2>
        <p>
          Send <code>Authorization: Bearer ndl_…</code> to <code>/api/v1/…</code>. A read-only debug token can inspect
          migration jobs, events, logs, workloads, storage, networking, and health without opening this UI.
        </p>
        <p className="field-hint">
          Presets are scoped by RBAC. Full audit adds audit.read and can be issued only by an administrator. Token
          create, revoke, and service-account create are written to the audit log.
        </p>
      </section>
      {reveal ? (
        <p className="banner banner-warn" role="status">
          {reveal.label} token (shown once): <code className="token-reveal">{reveal.token}</code>{" "}
          <button
            type="button"
            className="btn btn-ghost"
            onClick={() => {
              void navigator.clipboard?.writeText(reveal.token);
            }}
          >
            Copy token
          </button>
        </p>
      ) : null}
      {mutate ? (
        <form
          className="form"
          onSubmit={(event) => {
            event.preventDefault();
            const hours = Number(ttl);
            void createAPIToken({
              name,
              preset: preset || undefined,
              ttl_hours: Number.isFinite(hours) && hours > 0 ? hours : undefined,
            })
              .then((body) => created("Personal", body))
              .catch((err) => setError(err instanceof ApiError ? err.message : "Create failed"));
          }}
        >
          <h2>Create token</h2>
          <label htmlFor="token-name">Token name</label>
          <input id="token-name" value={name} onChange={(e) => setName(e.target.value)} required />
          <label htmlFor="token-preset">Scope</label>
          <select id="token-preset" value={preset} onChange={(e) => setPreset(e.target.value)}>
            <option value="readonly-debug">Read-only debug (jobs, events, logs, workloads, storage, network, health)</option>
            {admin ? <option value="full-audit">Full audit (read-only debug plus audit.read)</option> : null}
            <option value="">Inherit my role</option>
          </select>
          <label htmlFor="token-ttl">TTL hours (blank or 0 means no expiry)</label>
          <input id="token-ttl" type="number" min={0} max={8760} value={ttl} onChange={(e) => setTtl(e.target.value)} />
          <button className="btn btn-primary" type="submit">
            Create token
          </button>
        </form>
      ) : (
        <p>Your role cannot create API tokens.</p>
      )}
      <section className="section">
        <h2>Tokens</h2>
        {tokens.length === 0 ? (
          <p>No tokens yet.</p>
        ) : (
          <div className="table-wrap">
            <table>
              <thead>
                <tr>
                  <th>Name</th>
                  <th>Prefix</th>
                  <th>Owner</th>
                  <th>Scope</th>
                  <th>Expires</th>
                  <th>Last used</th>
                  <th>Status</th>
                  <th>Actions</th>
                </tr>
              </thead>
              <tbody>
                {tokens.map((tok) => (
                  <tr key={tok.id}>
                    <td>{tok.name}</td>
                    <td>
                      <code>{tok.prefix}</code>
                    </td>
                    <td>
                      {tok.username || tok.user_id}
                      {tok.user_kind === "service" ? " (service)" : ""}
                    </td>
                    <td>{(tok.permissions ?? []).length ? (tok.permissions ?? []).join(", ") : "Role grants"}</td>
                    <td>{tok.expires_at ? formatWhen(tok.expires_at) : "None"}</td>
                    <td>{tok.last_used_at ? formatWhen(tok.last_used_at) : "Never"}</td>
                    <td>{tok.disabled ? "Revoked" : tok.expired ? "Expired" : "Active"}</td>
                    <td>
                      {mutate && !tok.disabled ? (
                        <button
                          type="button"
                          className="btn btn-ghost"
                          onClick={() => {
                            void revokeAPIToken(tok.id)
                              .then(() => reload())
                              .catch((err) => setError(err instanceof ApiError ? err.message : "Revoke failed"));
                          }}
                        >
                          Revoke
                        </button>
                      ) : null}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>
      {admin ? (
        <section className="section">
          <h2>Service accounts</h2>
          <p className="field-hint">
            A service principal is a non-login identity with its own scoped bearer token. Use this for an authorized
            diagnostic agent.
          </p>
          <form
            className="form"
            onSubmit={(event) => {
              event.preventDefault();
              const hours = Number(spTtl);
              void createServicePrincipal({
                name: spName,
                preset: spPreset || undefined,
                ttl_hours: Number.isFinite(hours) && hours > 0 ? hours : undefined,
              })
                .then((body) => created("Service account", body))
                .catch((err) => setError(err instanceof ApiError ? err.message : "Service account create failed"));
            }}
          >
            <label htmlFor="sp-name">Service account name</label>
            <input id="sp-name" value={spName} onChange={(e) => setSpName(e.target.value)} required />
            <label htmlFor="sp-preset">Scope</label>
            <select id="sp-preset" value={spPreset} onChange={(e) => setSpPreset(e.target.value)}>
              <option value="readonly-debug">Read-only debug</option>
              <option value="full-audit">Full audit</option>
              <option value="">Full operator token</option>
            </select>
            <label htmlFor="sp-ttl">TTL hours</label>
            <input id="sp-ttl" type="number" min={0} max={8760} value={spTtl} onChange={(e) => setSpTtl(e.target.value)} />
            <button className="btn btn-primary" type="submit">
              Create service account
            </button>
          </form>
          {principals.length === 0 ? (
            <p>No service accounts yet.</p>
          ) : (
            <ul className="plain-list">
              {principals.map((sp) => (
                <li key={sp.id}>
                  {sp.name} · {sp.user_id}
                </li>
              ))}
            </ul>
          )}
        </section>
      ) : null}
    </section>
  );
}
