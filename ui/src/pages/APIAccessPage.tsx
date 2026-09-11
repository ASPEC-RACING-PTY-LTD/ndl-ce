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
import { ConfirmDialog } from "../components/ConfirmDialog";
import { EmptyState } from "../components/EmptyState";
import { Field } from "../components/Field";
import { PageHeader } from "../components/PageHeader";
import { SelectField } from "../components/form/SelectField";
import { formatWhen } from "../format";
import { hasGrant } from "../rbac";
import { useSession } from "../session";
import { Dialog } from "../ui/Dialog";
import { Tabs } from "../ui/Tabs";
import { RelativeTime } from "../ui/RelativeTime";

type Reveal = { label: string; token: string };
type Tab = "tokens" | "accounts";

function expiryLabel(tok: APITokenItem): string {
  if (tok.expired) {
    return "Expired";
  }
  if (!tok.expires_at) {
    return "No expiry";
  }
  return formatWhen(tok.expires_at);
}

export function APIAccessPage() {
  const session = useSession();
  const user = session.status === "ready" ? session.user : null;
  const mutate = hasGrant(user, "api_access.manage") || hasGrant(user, "identity.token.create");
  const admin = hasGrant(user, "identity.service");
  const [tab, setTab] = useState<Tab>("tokens");
  const [tokens, setTokens] = useState<APITokenItem[]>([]);
  const [principals, setPrincipals] = useState<ServicePrincipalItem[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [reveal, setReveal] = useState<Reveal | null>(null);
  const [copied, setCopied] = useState(false);
  const [createOpen, setCreateOpen] = useState(false);
  const [serviceOpen, setServiceOpen] = useState(false);
  const [helpOpen, setHelpOpen] = useState(false);
  const [revokeId, setRevokeId] = useState<string | null>(null);
  const [name, setName] = useState("diagnostic");
  const [preset, setPreset] = useState("readonly-debug");
  const [ttl, setTtl] = useState("72");
  const [spName, setSpName] = useState("diag-agent");
  const [spPreset, setSpPreset] = useState("readonly-debug");
  const [spTtl, setSpTtl] = useState("168");
  const [query, setQuery] = useState("");

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
  }, [admin]);

  function created(label: string, body: CreatedAPIToken | { token: string }) {
    setReveal({ label, token: body.token });
    setCreateOpen(false);
    setServiceOpen(false);
    return reload();
  }

  const shownTokens = tokens.filter((tok) => {
    const q = query.trim().toLowerCase();
    if (!q) {
      return true;
    }
    return [tok.name, tok.username, tok.prefix, tok.user_id].join(" ").toLowerCase().includes(q);
  });

  return (
    <section className="page page-wide" aria-labelledby="api-access-heading">
      <PageHeader
        id="api-access-heading"
        title="API Access"
        kicker="REST tokens and service accounts for scripts and diagnostic tools."
        actions={
          <div className="btn-row is-flush">
            <button className="btn btn-ghost" type="button" onClick={() => setHelpOpen(true)}>
              API usage
            </button>
            {mutate ? (
              <button className="btn btn-primary" type="button" onClick={() => setCreateOpen(true)}>
                Create token
              </button>
            ) : null}
            {admin ? (
              <button className="btn btn-secondary" type="button" onClick={() => setServiceOpen(true)}>
                Create service account
              </button>
            ) : null}
          </div>
        }
      />
      {error ? (
        <p className="banner banner-error" role="alert">
          {error}
        </p>
      ) : null}
      {reveal ? (
        <p className="banner banner-warn" role="status">
          {reveal.label} token (shown once): <code className="token-reveal">{reveal.token}</code>{" "}
          <button
            type="button"
            className="btn btn-ghost"
            onClick={() => {
              void navigator.clipboard?.writeText(reveal.token).then(() => setCopied(true));
            }}
          >
            {copied ? "Copied" : "Copy token"}
          </button>
        </p>
      ) : null}
      <Tabs
        ariaLabel="API access sections"
        value={tab}
        onChange={setTab}
        options={[
          { id: "tokens", label: "Tokens" },
          { id: "accounts", label: "Service accounts" },
        ]}
      />
      {tab === "tokens" ? (
        <div className="stack">
          <Field id="token-filter" label="Search tokens" value={query} onChange={(e) => setQuery(e.target.value)} />
          {shownTokens.length === 0 ? (
            <EmptyState
              title="No API tokens"
              action={
                mutate ? (
                  <button className="btn btn-primary" type="button" onClick={() => setCreateOpen(true)}>
                    Create token
                  </button>
                ) : undefined
              }
            >
              Create a token for scripts, integrations or diagnostic tools.
            </EmptyState>
          ) : (
            <div className="data-list">
              <table>
                <thead>
                  <tr>
                    <th>Name</th>
                    <th>Owner</th>
                    <th>Scope</th>
                    <th>Expiry</th>
                    <th>Last used</th>
                    <th>Status</th>
                    <th>Actions</th>
                  </tr>
                </thead>
                <tbody>
                  {shownTokens.map((tok) => (
                    <tr key={tok.id}>
                      <td>{tok.name}</td>
                      <td>
                        {tok.username || tok.user_id}
                        {tok.user_kind === "service" ? " (service)" : ""}
                      </td>
                      <td>{tok.preset || ((tok.permissions ?? []).length ? "Custom" : "Role grants")}</td>
                      <td>{expiryLabel(tok)}</td>
                      <td>{tok.last_used_at ? <RelativeTime value={tok.last_used_at} /> : "Never"}</td>
                      <td>{tok.disabled ? "Revoked" : tok.expired ? "Expired" : "Active"}</td>
                      <td>
                        {mutate && !tok.disabled ? (
                          <button type="button" className="btn btn-ghost" onClick={() => setRevokeId(tok.id)}>
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
        </div>
      ) : (
        <div className="stack">
          {principals.length === 0 ? (
            <EmptyState
              title="No service accounts"
              action={
                admin ? (
                  <button className="btn btn-primary" type="button" onClick={() => setServiceOpen(true)}>
                    Create service account
                  </button>
                ) : undefined
              }
            >
              A service account is a non-login identity with its own scoped token.
            </EmptyState>
          ) : (
            <ul className="plain-list">
              {principals.map((sp) => (
                <li key={sp.id}>
                  {sp.name}
                </li>
              ))}
            </ul>
          )}
        </div>
      )}

      <Dialog open={createOpen} title="Create token" onClose={() => setCreateOpen(false)}>
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
          <Field id="token-name" label="Token name" value={name} onChange={(e) => setName(e.target.value)} required />
          <SelectField id="token-preset" label="Scope" value={preset} onChange={(e) => setPreset(e.target.value)}>
            <option value="readonly-debug">Read-only debug</option>
            {admin ? <option value="full-audit">Full audit</option> : null}
            <option value="">Inherit my role</option>
          </SelectField>
          <Field
            id="token-ttl"
            label="TTL"
            type="number"
            min={0}
            max={8760}
            value={ttl}
            onChange={(e) => setTtl(e.target.value)}
            suffix="hours"
            hint="0 means no expiry."
          />
          <div className="btn-row">
            <button className="btn btn-ghost" type="button" onClick={() => setCreateOpen(false)}>
              Cancel
            </button>
            <button className="btn btn-primary" type="submit" disabled={!mutate}>
              Create token
            </button>
          </div>
        </form>
      </Dialog>

      <Dialog open={serviceOpen} title="Create service account" onClose={() => setServiceOpen(false)}>
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
          <Field id="sp-name" label="Service account name" value={spName} onChange={(e) => setSpName(e.target.value)} required />
          <SelectField id="sp-preset" label="Scope" value={spPreset} onChange={(e) => setSpPreset(e.target.value)}>
            <option value="readonly-debug">Read-only debug</option>
            <option value="full-audit">Full audit</option>
            <option value="">Full operator token</option>
          </SelectField>
          <Field id="sp-ttl" label="TTL" type="number" min={0} max={8760} value={spTtl} onChange={(e) => setSpTtl(e.target.value)} suffix="hours" />
          <div className="btn-row">
            <button className="btn btn-ghost" type="button" onClick={() => setServiceOpen(false)}>
              Cancel
            </button>
            <button className="btn btn-primary" type="submit" disabled={!admin}>
              Create service account
            </button>
          </div>
        </form>
      </Dialog>

      <Dialog open={helpOpen} title="How to call the REST API" onClose={() => setHelpOpen(false)}>
        <p>
          Send <code>Authorization: Bearer ndl_…</code> to <code>/api/v1/…</code>. A read-only debug token can inspect
          migration jobs, events, logs, workloads, storage, networking, and health without opening this UI.
        </p>
        <p className="field-hint">
          Presets are scoped by RBAC. Full audit adds audit.read and can be issued only by an administrator. Token
          create, revoke, and service-account create are written to the audit log. This is not MCP and not account
          profile settings.
        </p>
      </Dialog>

      <ConfirmDialog
        open={Boolean(revokeId)}
        title="Revoke token"
        danger
        confirmLabel="Revoke"
        onClose={() => setRevokeId(null)}
        onConfirm={() => {
          if (!revokeId) {
            return;
          }
          void revokeAPIToken(revokeId)
            .then(() => {
              setRevokeId(null);
              return reload();
            })
            .catch((err) => setError(err instanceof ApiError ? err.message : "Revoke failed"));
        }}
      >
        <p>The token stops working immediately. Its secret cannot be recovered.</p>
      </ConfirmDialog>
    </section>
  );
}
