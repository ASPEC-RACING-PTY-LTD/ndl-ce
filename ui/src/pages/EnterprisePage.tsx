import { useEffect, useState, type FormEvent } from "react";
import { ApiError, enterpriseJSON, getLicense } from "../api/client";
import type { LicenseStatus } from "../generated/openapi";
import { Field } from "../components/Field";
import { Link } from "../components/Link";

type Provider = {
  id?: string;
  kind?: string;
  name?: string;
  issuer?: string;
  client_id?: string;
  redirect_url?: string;
  enabled?: boolean;
  sso_url?: string;
  certificate?: string;
  bind_dn?: string;
  base_dn?: string;
  user_filter?: string;
  start_tls?: boolean;
  audience?: string;
};

type ExportTarget = { id?: string; name?: string; url?: string; dialect?: string; enabled?: boolean };
type FleetCluster = {
  id?: string;
  organization?: string;
  edition?: string;
  status?: string;
  last_seen?: string;
  node_count?: number;
  workload_count?: number;
};
type Policy = {
  require_sso?: boolean;
  deny_local_password?: boolean;
  require_mfa?: boolean;
  require_audit_export?: boolean;
  max_session_hours?: number;
  admin_cidrs?: string[];
};
type AdminSummary = {
  organization?: string;
  subscription_id?: string;
  update_channel?: string;
  status?: string;
};

export function EnterprisePage() {
  const [license, setLicense] = useState<LicenseStatus | null>(null);
  const [admin, setAdmin] = useState<AdminSummary>({});
  const [providers, setProviders] = useState<Provider[]>([]);
  const [exports, setExports] = useState<ExportTarget[]>([]);
  const [fleet, setFleet] = useState<FleetCluster[]>([]);
  const [policy, setPolicy] = useState<Policy>({});
  const [error, setError] = useState<string | null>(null);
  const [kind, setKind] = useState("oidc");
  const [name, setName] = useState("");
  const [issuer, setIssuer] = useState("");
  const [clientId, setClientId] = useState("");
  const [clientSecret, setClientSecret] = useState("");
  const [redirect, setRedirect] = useState("");
  const [audience, setAudience] = useState("");
  const [ssoURL, setSSOURL] = useState("");
  const [certificate, setCertificate] = useState("");
  const [bindDN, setBindDN] = useState("");
  const [baseDN, setBaseDN] = useState("");
  const [userFilter, setUserFilter] = useState("");
  const [startTLS, setStartTLS] = useState(false);
  const [exportName, setExportName] = useState("");
  const [exportURL, setExportURL] = useState("");
  const [exportToken, setExportToken] = useState("");
  const [dialect, setDialect] = useState("webhook");
  const [cidrs, setCidrs] = useState("");

  async function reload() {
    const lic = await getLicense();
    setLicense(lic);
    if (lic.edition !== "ee") {
      return;
    }
    const [p, e, f, pol, adm] = await Promise.all([
      enterpriseJSON<{ items?: Provider[] }>("/enterprise/identity/providers").catch(() => ({ items: [] })),
      enterpriseJSON<{ items?: ExportTarget[] }>("/enterprise/compliance/exports").catch(() => ({ items: [] })),
      enterpriseJSON<{ items?: FleetCluster[] }>("/enterprise/fleet/clusters").catch(() => ({ items: [] })),
      enterpriseJSON<Policy>("/enterprise/policy").catch(() => ({} as Policy)),
      enterpriseJSON<AdminSummary>("/enterprise/admin").catch(() => ({} as AdminSummary)),
    ]);
    setProviders(p.items ?? []);
    setExports(e.items ?? []);
    setFleet(f.items ?? []);
    setPolicy(pol);
    setAdmin(adm);
    setCidrs((pol.admin_cidrs ?? []).join(", "));
  }

  useEffect(() => {
    void reload().catch((err) => setError(err instanceof Error ? err.message : "Unavailable"));
  }, []);

  const entitled = license?.edition === "ee";

  async function addProvider(event: FormEvent) {
    event.preventDefault();
    setError(null);
    try {
      await enterpriseJSON("/enterprise/identity/providers", {
        method: "POST",
        body: JSON.stringify({
          kind,
          name,
          issuer,
          client_id: clientId,
          client_secret: clientSecret || undefined,
          redirect_url: redirect,
          audience: audience || undefined,
          sso_url: ssoURL || undefined,
          certificate: certificate || undefined,
          bind_dn: bindDN || undefined,
          base_dn: baseDN || undefined,
          user_filter: userFilter || undefined,
          start_tls: kind === "ldap" ? startTLS : undefined,
          enabled: true,
        }),
      });
      setName("");
      setClientSecret("");
      setCertificate("");
      await reload();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Could not store provider");
    }
  }

  async function deleteProvider(id: string) {
    setError(null);
    try {
      await enterpriseJSON(`/enterprise/identity/providers/${id}`, { method: "DELETE" });
      await reload();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Could not delete provider");
    }
  }

  async function addExport(event: FormEvent) {
    event.preventDefault();
    setError(null);
    try {
      await enterpriseJSON("/enterprise/compliance/exports", {
        method: "POST",
        body: JSON.stringify({
          name: exportName,
          url: exportURL,
          dialect,
          token: exportToken || undefined,
          enabled: true,
        }),
      });
      setExportName("");
      setExportURL("");
      setExportToken("");
      await reload();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Could not store export target");
    }
  }

  async function runExport(id: string) {
    setError(null);
    try {
      await enterpriseJSON(`/enterprise/compliance/exports/${id}/run`, { method: "POST", body: "{}" });
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Could not run export");
    }
  }

  async function deleteExport(id: string) {
    setError(null);
    try {
      await enterpriseJSON(`/enterprise/compliance/exports/${id}`, { method: "DELETE" });
      await reload();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Could not delete export target");
    }
  }

  async function savePolicy(event: FormEvent) {
    event.preventDefault();
    setError(null);
    try {
      const admin_cidrs = cidrs
        .split(",")
        .map((v) => v.trim())
        .filter(Boolean);
      await enterpriseJSON("/enterprise/policy", {
        method: "POST",
        body: JSON.stringify({ ...policy, admin_cidrs }),
      });
      await reload();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Could not store policy");
    }
  }

  return (
    <section className="page" aria-labelledby="enterprise-heading">
      <header className="page-header">
        <h1 id="enterprise-heading">Enterprise</h1>
        <p className="lede">
          Identity, audit export, fleet inventory, and advanced policy live in the Enterprise sidecar. Community Edition
          login and running workloads stay available if the sidecar or license is absent. See{" "}
          <Link href="/settings/license">License</Link>.
        </p>
      </header>
      {error ? (
        <p className="banner banner-error" role="alert">
          {error}
        </p>
      ) : null}
      {!entitled ? (
        <article className="panel">
          <p>Enterprise capabilities are not entitled on this install.</p>
        </article>
      ) : (
        <>
          <article className="panel">
            <h2>Subscription</h2>
            <p>
              {admin.organization || license?.organization || "Organization not set"} · {admin.status || license?.status} ·{" "}
              {admin.update_channel || license?.update_channel || "channel not set"}
            </p>
          </article>
          <article className="panel">
            <h2>Identity providers</h2>
            <form className="form" onSubmit={(ev) => void addProvider(ev)}>
              <label htmlFor="idp-kind">Kind</label>
              <select id="idp-kind" className="field-input" value={kind} onChange={(e) => setKind(e.target.value)}>
                <option value="oidc">OIDC</option>
                <option value="saml">SAML</option>
                <option value="ldap">LDAP</option>
              </select>
              <Field id="idp-name" label="Name" value={name} onChange={(e) => setName(e.target.value)} required />
              <Field
                id="idp-issuer"
                label={kind === "ldap" ? "Directory URL" : "Issuer"}
                value={issuer}
                onChange={(e) => setIssuer(e.target.value)}
                hint={kind === "ldap" ? "ldap:// or ldaps:// host" : undefined}
              />
              {kind === "oidc" ? (
                <>
                  <Field id="idp-client" label="Client id" value={clientId} onChange={(e) => setClientId(e.target.value)} />
                  <Field
                    id="idp-secret"
                    label="Client secret"
                    type="password"
                    value={clientSecret}
                    onChange={(e) => setClientSecret(e.target.value)}
                  />
                  <Field
                    id="idp-redirect"
                    label="Redirect URL"
                    value={redirect}
                    onChange={(e) => setRedirect(e.target.value)}
                  />
                  <Field id="idp-aud" label="Audience" value={audience} onChange={(e) => setAudience(e.target.value)} />
                </>
              ) : null}
              {kind === "saml" ? (
                <>
                  <Field id="idp-sso" label="SSO URL" value={ssoURL} onChange={(e) => setSSOURL(e.target.value)} />
                  <Field
                    id="idp-redirect"
                    label="Assertion consumer URL"
                    value={redirect}
                    onChange={(e) => setRedirect(e.target.value)}
                  />
                  <Field id="idp-aud" label="SP entity ID" value={audience} onChange={(e) => setAudience(e.target.value)} />
                  <label className="field-label" htmlFor="idp-cert">
                    IdP signing certificate
                  </label>
                  <textarea
                    id="idp-cert"
                    className="field-input"
                    rows={6}
                    value={certificate}
                    onChange={(e) => setCertificate(e.target.value)}
                  />
                </>
              ) : null}
              {kind === "ldap" ? (
                <>
                  <Field
                    id="idp-bind"
                    label="Bind DN"
                    value={bindDN}
                    onChange={(e) => setBindDN(e.target.value)}
                    hint="Use {username} for simple bind, or a service DN with the secret below"
                  />
                  <Field
                    id="idp-secret"
                    label="Service bind password"
                    type="password"
                    value={clientSecret}
                    onChange={(e) => setClientSecret(e.target.value)}
                  />
                  <Field id="idp-base" label="Base DN" value={baseDN} onChange={(e) => setBaseDN(e.target.value)} />
                  <Field
                    id="idp-filter"
                    label="User filter"
                    value={userFilter}
                    onChange={(e) => setUserFilter(e.target.value)}
                    hint="Example: (uid={username})"
                  />
                  <label>
                    <input type="checkbox" checked={startTLS} onChange={(e) => setStartTLS(e.target.checked)} /> StartTLS
                  </label>
                </>
              ) : null}
              <button className="btn btn-primary" type="submit">
                Store provider
              </button>
            </form>
            {providers.length === 0 ? (
              <p>Not configured</p>
            ) : (
              <ul>
                {providers.map((p) => (
                  <li key={p.id}>
                    {p.name} ({p.kind}) {p.enabled ? "enabled" : "disabled"}{" "}
                    {p.id ? (
                      <button className="btn" type="button" onClick={() => void deleteProvider(p.id as string)}>
                        Delete
                      </button>
                    ) : null}
                  </li>
                ))}
              </ul>
            )}
          </article>
          <article className="panel">
            <h2>Audit export</h2>
            <form className="form" onSubmit={(ev) => void addExport(ev)}>
              <Field id="ex-name" label="Name" value={exportName} onChange={(e) => setExportName(e.target.value)} />
              <Field id="ex-url" label="URL" value={exportURL} onChange={(e) => setExportURL(e.target.value)} />
              <Field
                id="ex-token"
                label="Token"
                type="password"
                value={exportToken}
                onChange={(e) => setExportToken(e.target.value)}
              />
              <label htmlFor="ex-dialect">Dialect</label>
              <select id="ex-dialect" className="field-input" value={dialect} onChange={(e) => setDialect(e.target.value)}>
                <option value="webhook">webhook</option>
                <option value="splunk_hec">splunk_hec</option>
                <option value="elasticsearch">elasticsearch</option>
                <option value="cef">cef</option>
                <option value="syslog_rfc5424">syslog_rfc5424</option>
              </select>
              <button className="btn btn-primary" type="submit">
                Store target
              </button>
            </form>
            {exports.length === 0 ? (
              <p>Not configured</p>
            ) : (
              <ul>
                {exports.map((t) => (
                  <li key={t.id}>
                    {t.name} {t.dialect} {t.url}{" "}
                    {t.id ? (
                      <>
                        <button className="btn" type="button" onClick={() => void runExport(t.id as string)}>
                          Run
                        </button>{" "}
                        <button className="btn" type="button" onClick={() => void deleteExport(t.id as string)}>
                          Delete
                        </button>
                      </>
                    ) : null}
                  </li>
                ))}
              </ul>
            )}
          </article>
          <article className="panel">
            <h2>Fleet</h2>
            {fleet.length === 0 ? (
              <p>No heartbeats yet</p>
            ) : (
              <ul>
                {fleet.map((c) => (
                  <li key={c.id}>
                    {c.id} {c.organization} {c.edition} {c.status} {c.last_seen} nodes={c.node_count ?? 0} workloads=
                    {c.workload_count ?? 0}
                  </li>
                ))}
              </ul>
            )}
          </article>
          <article className="panel">
            <h2>Advanced policy</h2>
            <p>These rules apply only while Enterprise is entitled and the sidecar is present.</p>
            <form className="form" onSubmit={(ev) => void savePolicy(ev)}>
              <label>
                <input
                  type="checkbox"
                  checked={Boolean(policy.require_sso)}
                  onChange={(e) => setPolicy({ ...policy, require_sso: e.target.checked })}
                />{" "}
                Require SSO
              </label>
              <label>
                <input
                  type="checkbox"
                  checked={Boolean(policy.deny_local_password)}
                  onChange={(e) => setPolicy({ ...policy, deny_local_password: e.target.checked })}
                />{" "}
                Deny local password
              </label>
              <label>
                <input
                  type="checkbox"
                  checked={Boolean(policy.require_mfa)}
                  onChange={(e) => setPolicy({ ...policy, require_mfa: e.target.checked })}
                />{" "}
                Require MFA
              </label>
              <label>
                <input
                  type="checkbox"
                  checked={Boolean(policy.require_audit_export)}
                  onChange={(e) => setPolicy({ ...policy, require_audit_export: e.target.checked })}
                />{" "}
                Require audit export
              </label>
              <Field
                id="pol-hours"
                label="Max session hours"
                type="number"
                value={String(policy.max_session_hours ?? 0)}
                onChange={(e) => setPolicy({ ...policy, max_session_hours: Number(e.target.value) })}
              />
              <Field id="pol-cidrs" label="Admin CIDRs" value={cidrs} onChange={(e) => setCidrs(e.target.value)} />
              <button className="btn btn-primary" type="submit">
                Save policy
              </button>
            </form>
          </article>
        </>
      )}
    </section>
  );
}
