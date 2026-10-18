import { useCallback, useEffect, useMemo, useState } from "react";
import {
  ApiError,
  createAPIToken,
  createServicePrincipal,
  deleteAPIToken,
  getAPITokenUsage,
  listAPITokens,
  listServicePrincipals,
  rollAPIToken,
  updateAPIToken,
  type APITokenItem,
  type CreatedAPIToken,
  type ServicePrincipalItem,
  type TokenUsageItem,
} from "../api/client";
import { confirmAction } from "../confirm";
import { EmptyState } from "../components/EmptyState";
import { ErrorNotice } from "../components/ErrorNotice";
import { Field } from "../components/Field";
import { SelectField } from "../components/form/SelectField";
import { formatWhen } from "../format";
import { hasGrant } from "../rbac";
import { useSession } from "../session";
import { Dialog } from "../ui/Dialog";
import { RelativeTime } from "../ui/RelativeTime";
import { ColumnEditor, SortHeader, useTablePrefs, type TableColumn } from "../ui/tablePrefs";

// API Access: personal tokens and service-account tokens in one table. A
// deleted or expired token is gone (its history is in the audit log), so the
// table only ever lists live tokens.

type Row = {
  key: string;
  token?: APITokenItem;
  account?: ServicePrincipalItem;
  name: string;
  owner: string;
  type: "token" | "service";
  expires?: string;
  lastUsed?: string;
  status: "Active" | "Expiring" | "No token";
};

const DAY = 24 * 60 * 60 * 1000;

function statusOf(tok: APITokenItem): Row["status"] {
  if (tok.expires_at && Date.parse(tok.expires_at) - Date.now() < 3 * DAY) {
    return "Expiring";
  }
  return "Active";
}

const COLUMNS: TableColumn<Row>[] = [
  { id: "name", label: "Name", sortValue: (r) => r.name.toLowerCase(), required: true },
  { id: "owner", label: "Created by", sortValue: (r) => r.owner.toLowerCase() },
  { id: "type", label: "Type", sortValue: (r) => r.type, defaultHidden: true },
  { id: "expires", label: "Expiry", sortValue: (r) => (r.expires ? Date.parse(r.expires) : Number.MAX_SAFE_INTEGER) },
  { id: "used", label: "Last used", sortValue: (r) => (r.lastUsed ? Date.parse(r.lastUsed) : 0) },
  { id: "status", label: "Status", sortValue: (r) => r.status },
];

export function APIAccessPage() {
  const session = useSession();
  const user = session.status === "ready" ? session.user : null;
  const mutate = hasGrant(user, "api_access.manage") || hasGrant(user, "identity.token.create");
  const admin = hasGrant(user, "identity.service");
  const [tokens, setTokens] = useState<APITokenItem[]>([]);
  const [principals, setPrincipals] = useState<ServicePrincipalItem[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [reveal, setReveal] = useState<{ label: string; token: string } | null>(null);
  const [copied, setCopied] = useState(false);
  const [createOpen, setCreateOpen] = useState(false);
  const [serviceOpen, setServiceOpen] = useState(false);
  const [helpOpen, setHelpOpen] = useState(false);
  const [openKey, setOpenKey] = useState<string | null>(null);
  const [bulkEdit, setBulkEdit] = useState(false);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [query, setQuery] = useState("");
  const [typeFilter, setTypeFilter] = useState("all");
  const [statusFilter, setStatusFilter] = useState("all");
  const [name, setName] = useState("diagnostic");
  const [preset, setPreset] = useState("readonly-debug");
  const [ttl, setTtl] = useState("72");
  const [spName, setSpName] = useState("diag-agent");
  const [spPreset, setSpPreset] = useState("readonly-debug");
  const [spTtl, setSpTtl] = useState("168");
  const table = useTablePrefs<Row>("iam.api-access", COLUMNS, { id: "name", dir: "asc" });

  const reload = useCallback(async () => {
    const listed = await listAPITokens();
    setTokens(listed.items ?? []);
    if (admin) {
      const sps = await listServicePrincipals();
      setPrincipals(sps.items ?? []);
    }
  }, [admin]);

  useEffect(() => {
    void reload().catch((err) => setError(err instanceof Error ? err.message : "Unavailable"));
  }, [reload]);

  const rows = useMemo<Row[]>(() => {
    const out: Row[] = tokens
      .filter((t) => !t.disabled && !t.expired)
      .map((t) => ({
        key: t.id,
        token: t,
        name: t.name,
        owner: t.username || t.user_id,
        type: t.user_kind === "service" ? "service" : "token",
        expires: t.expires_at,
        lastUsed: t.last_used_at,
        status: statusOf(t),
      }));
    // Service accounts whose token was deleted still show, so they can be removed or given a new token.
    for (const sp of principals) {
      if (!tokens.some((t) => t.user_id === sp.user_id && !t.disabled && !t.expired)) {
        out.push({ key: `sp:${sp.id}`, account: sp, name: sp.name, owner: sp.name, type: "service", status: "No token" });
      }
    }
    return out;
  }, [tokens, principals]);

  const q = query.trim().toLowerCase();
  const shown = table.sortRows(
    rows.filter(
      (r) =>
        (typeFilter === "all" || r.type === typeFilter) &&
        (statusFilter === "all" || r.status === statusFilter) &&
        (!q || [r.name, r.owner, r.token?.prefix ?? ""].join(" ").toLowerCase().includes(q)),
    ),
  );
  const selectable = shown.filter((r) => r.token);
  const chosen = selectable.filter((r) => selected.has(r.key));
  const allChosen = selectable.length > 0 && chosen.length === selectable.length;
  const open = rows.find((r) => r.key === openKey) ?? null;

  function toggle(key: string, on: boolean) {
    setSelected((cur) => {
      const next = new Set(cur);
      if (on) {
        next.add(key);
      } else {
        next.delete(key);
      }
      return next;
    });
  }

  async function run(label: string, fn: () => Promise<unknown>) {
    setError(null);
    try {
      await fn();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : `${label} failed`);
    }
    await reload().catch(() => undefined);
  }

  function bulkDelete() {
    if (!confirmAction(`Delete ${chosen.length} token(s)? They stop working now. Their history stays in the audit log.`)) {
      return;
    }
    void run("Delete", async () => {
      for (const r of chosen) {
        await deleteAPIToken(r.key);
      }
      setSelected(new Set());
    });
  }

  function bulkRoll() {
    if (!confirmAction(`Roll the secret of ${chosen.length} token(s)? Every client using them must get the new secret.`)) {
      return;
    }
    void run("Roll", async () => {
      const lines: string[] = [];
      for (const r of chosen) {
        const res = await rollAPIToken(r.key);
        lines.push(`${r.name}: ${res.token}`);
      }
      setReveal({ label: "New secrets", token: lines.join("   ") });
    });
  }

  function created(label: string, body: CreatedAPIToken | { token: string }) {
    setReveal({ label, token: body.token });
    setCreateOpen(false);
    setServiceOpen(false);
    return reload();
  }

  const cell = (r: Row, id: string) => {
    switch (id) {
      case "name":
        return (
          <span className="iam-cell-name">
            <strong>{r.name}</strong>
            {r.token ? <span className="meta"> {r.token.prefix}…</span> : null}
          </span>
        );
      case "owner":
        return r.owner;
      case "type":
        return r.type === "service" ? "Service account" : "Personal";
      case "expires":
        return r.expires ? formatWhen(r.expires) : r.token ? "No expiry" : "—";
      case "used":
        return r.lastUsed ? <RelativeTime value={r.lastUsed} /> : "Never";
      case "status":
        return <span className={"iam-status is-" + r.status.toLowerCase().replace(" ", "-")}>{r.status}</span>;
    }
    return null;
  };

  return (
    <div className="iam-section" aria-labelledby="api-access-heading">
      <div className="iam-toolbar">
        <h2 id="api-access-heading" className="iam-section-title">
          API Access <span className="meta">{rows.length}</span>
        </h2>
        <input
          className="field-input iam-search"
          type="search"
          aria-label="Search tokens"
          placeholder="Search name, owner, prefix"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
        />
        <select className="field-input" aria-label="Type" value={typeFilter} onChange={(e) => setTypeFilter(e.target.value)}>
          <option value="all">All types</option>
          <option value="token">Personal tokens</option>
          <option value="service">Service accounts</option>
        </select>
        <select className="field-input" aria-label="Status" value={statusFilter} onChange={(e) => setStatusFilter(e.target.value)}>
          <option value="all">Any status</option>
          <option value="Active">Active</option>
          <option value="Expiring">Expiring in 3 days</option>
          <option value="No token">No token</option>
        </select>
        <div className="btn-row is-flush iam-toolbar-actions">
          {mutate ? (
            <>
              <button className="btn btn-sm btn-secondary" type="button" disabled={chosen.length === 0} onClick={() => setBulkEdit(true)}>
                Edit
              </button>
              <button className="btn btn-sm btn-secondary" type="button" disabled={chosen.length === 0} onClick={bulkRoll}>
                Roll
              </button>
              <button className="btn btn-sm btn-danger" type="button" disabled={chosen.length === 0} onClick={bulkDelete}>
                Delete{chosen.length ? ` (${chosen.length})` : ""}
              </button>
            </>
          ) : null}
          <button className="btn btn-sm btn-ghost" type="button" onClick={() => setHelpOpen(true)}>
            API usage
          </button>
          {mutate ? (
            <button className="btn btn-sm btn-primary" type="button" onClick={() => setCreateOpen(true)}>
              Create token
            </button>
          ) : null}
          {admin ? (
            <button className="btn btn-sm btn-secondary" type="button" onClick={() => setServiceOpen(true)}>
              Create service account
            </button>
          ) : null}
        </div>
      </div>
      {error ? <ErrorNotice error={error} /> : null}
      {reveal ? (
        <p className="banner banner-warn" role="status">
          {reveal.label} (shown once): <code className="token-reveal">{reveal.token}</code>{" "}
          <button
            type="button"
            className="btn btn-ghost btn-sm"
            onClick={() => {
              void navigator.clipboard?.writeText(reveal.token).then(() => setCopied(true));
            }}
          >
            {copied ? "Copied" : "Copy"}
          </button>
          <button type="button" className="btn btn-ghost btn-sm" onClick={() => setReveal(null)}>
            Dismiss
          </button>
        </p>
      ) : null}
      {rows.length === 0 ? (
        <EmptyState title="No API tokens">Create a token for scripts, integrations or diagnostic tools.</EmptyState>
      ) : (
        <div className="table-wrap iam-table">
          <table aria-label="API tokens and service accounts">
            <thead>
              <tr>
                <th className="cell-check">
                  <input
                    type="checkbox"
                    aria-label="Select every token"
                    checked={allChosen}
                    onChange={(e) => setSelected(e.target.checked ? new Set(selectable.map((r) => r.key)) : new Set())}
                  />
                </th>
                {table.visible.map((col) => (
                  <SortHeader key={col.id} column={col} prefs={table} />
                ))}
                <th className="cell-actions">
                  <ColumnEditor prefs={table} label="Edit token columns" />
                </th>
              </tr>
            </thead>
            <tbody>
              {shown.map((r) => (
                <tr key={r.key} className="is-clickable" onClick={() => setOpenKey(r.key)}>
                  <td className="cell-check" onClick={(e) => e.stopPropagation()}>
                    {r.token ? (
                      <input type="checkbox" aria-label={`Select ${r.name}`} checked={selected.has(r.key)} onChange={(e) => toggle(r.key, e.target.checked)} />
                    ) : null}
                  </td>
                  {table.visible.map((col) => (
                    <td key={col.id}>{cell(r, col.id)}</td>
                  ))}
                  <td />
                </tr>
              ))}
              {shown.length === 0 ? (
                <tr>
                  <td colSpan={table.visible.length + 2} className="muted">
                    No tokens match the current filters.
                  </td>
                </tr>
              ) : null}
            </tbody>
          </table>
        </div>
      )}

      {open ? (
        <TokenDialog
          row={open}
          canManage={mutate}
          onClose={() => setOpenKey(null)}
          onRolled={(token) => setReveal({ label: `New secret for ${open.name}`, token })}
          onChanged={() => void reload()}
          onError={setError}
        />
      ) : null}

      <Dialog open={bulkEdit} title={`Edit ${chosen.length} token(s)`} onClose={() => setBulkEdit(false)}>
        <BulkEditForm
          onCancel={() => setBulkEdit(false)}
          onSave={(hours) => {
            setBulkEdit(false);
            void run("Edit", async () => {
              for (const r of chosen) {
                await updateAPIToken(r.key, { ttl_hours: hours });
              }
            });
          }}
        />
      </Dialog>

      <Dialog open={createOpen} title="Create token" onClose={() => setCreateOpen(false)}>
        <form
          className="form"
          onSubmit={(event) => {
            event.preventDefault();
            const hours = Number(ttl);
            void createAPIToken({ name, preset: preset || undefined, ttl_hours: Number.isFinite(hours) && hours > 0 ? hours : undefined })
              .then((body) => created("Personal token", body))
              .catch((err) => setError(err instanceof ApiError ? err.message : "Create failed"));
          }}
        >
          <Field id="token-name" label="Token name" value={name} onChange={(e) => setName(e.target.value)} required />
          <SelectField id="token-preset" label="Scope" value={preset} onChange={(e) => setPreset(e.target.value)}>
            <option value="readonly-debug">Read-only debug</option>
            {admin ? <option value="full-audit">Full audit</option> : null}
            <option value="">Inherit my role</option>
          </SelectField>
          <Field id="token-ttl" label="TTL" type="number" min={0} max={8760} value={ttl} onChange={(e) => setTtl(e.target.value)} suffix="hours" hint="0 means no expiry." />
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
            void createServicePrincipal({ name: spName, preset: spPreset || undefined, ttl_hours: Number.isFinite(hours) && hours > 0 ? hours : undefined })
              .then((body) => created("Service account token", body))
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
          Send <code>Authorization: Bearer ndl_…</code> to <code>/api/v1/…</code>. A read-only debug token can inspect migration jobs, events,
          logs, workloads, storage, networking, and health without opening this UI.
        </p>
        <p className="field-hint">
          Deleted and expired tokens are removed completely. The audit log keeps each token's creation, the IP addresses that used it and when,
          and when it ended.
        </p>
      </Dialog>
    </div>
  );
}

function BulkEditForm({ onSave, onCancel }: { onSave: (hours: number) => void; onCancel: () => void }) {
  const [hours, setHours] = useState("168");
  return (
    <form
      className="form"
      onSubmit={(e) => {
        e.preventDefault();
        onSave(Math.max(0, Number(hours) || 0));
      }}
    >
      <Field id="bulk-ttl" label="New expiry from now" type="number" min={0} max={8760} value={hours} onChange={(e) => setHours(e.target.value)} suffix="hours" hint="0 means no expiry." />
      <div className="btn-row">
        <button className="btn btn-ghost" type="button" onClick={onCancel}>
          Cancel
        </button>
        <button className="btn btn-primary" type="submit">
          Save
        </button>
      </div>
    </form>
  );
}

function TokenDialog({
  row,
  canManage,
  onClose,
  onRolled,
  onChanged,
  onError,
}: {
  row: Row;
  canManage: boolean;
  onClose: () => void;
  onRolled: (token: string) => void;
  onChanged: () => void;
  onError: (msg: string) => void;
}) {
  const tok = row.token;
  const [usage, setUsage] = useState<TokenUsageItem[] | null>(null);
  const [name, setName] = useState(row.name);
  const [hours, setHours] = useState("");
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (tok) {
      void getAPITokenUsage(tok.id)
        .then((r) => setUsage(r.items ?? []))
        .catch(() => setUsage([]));
    }
  }, [tok]);

  async function act(fn: () => Promise<void>) {
    setBusy(true);
    try {
      await fn();
      onChanged();
    } catch (err) {
      onError(err instanceof ApiError ? err.message : "Action failed");
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog open wide title={row.name} onClose={onClose}>
      {tok ? (
        <div className="stack">
          <dl className="iam-facts">
            <dt>Created by</dt>
            <dd>{row.owner}</dd>
            <dt>Created</dt>
            <dd>{tok.created_at ? formatWhen(tok.created_at) : "Unknown"}</dd>
            <dt>Expiry</dt>
            <dd>{tok.expires_at ? formatWhen(tok.expires_at) : "No expiry"}</dd>
            <dt>Scope</dt>
            <dd>{tok.preset || ((tok.permissions ?? []).length ? `${tok.permissions!.length} permissions` : "Owner's role")}</dd>
            <dt>Prefix</dt>
            <dd>
              <code>{tok.prefix}…</code>
            </dd>
          </dl>
          {canManage ? (
            <form
              className="iam-inline-form"
              onSubmit={(e) => {
                e.preventDefault();
                const h = hours.trim() === "" ? undefined : Math.max(0, Number(hours) || 0);
                void act(async () => {
                  await updateAPIToken(tok.id, { name: name.trim(), ttl_hours: h });
                });
              }}
            >
              <Field id="tok-name" label="Name" value={name} onChange={(e) => setName(e.target.value)} />
              <Field id="tok-ttl" label="New expiry from now" type="number" min={0} max={8760} value={hours} onChange={(e) => setHours(e.target.value)} suffix="hours" hint="Leave empty to keep it. 0 means no expiry." />
              <button className="btn btn-secondary" type="submit" disabled={busy}>
                Save
              </button>
            </form>
          ) : null}
          <section>
            <h3 className="iam-subhead">Used from</h3>
            {usage === null ? (
              <p className="muted">Loading</p>
            ) : usage.length === 0 ? (
              <p className="muted">Not used yet.</p>
            ) : (
              <div className="table-wrap">
                <table aria-label="IP addresses that used this token">
                  <thead>
                    <tr>
                      <th>IP address</th>
                      <th>First used</th>
                      <th>Last used</th>
                      <th className="num">Requests</th>
                    </tr>
                  </thead>
                  <tbody>
                    {usage.map((u) => (
                      <tr key={u.ip}>
                        <td>
                          <code>{u.ip}</code>
                        </td>
                        <td>{formatWhen(u.first_at)}</td>
                        <td>
                          <RelativeTime value={u.last_at} />
                        </td>
                        <td className="num">{u.count}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </section>
          {canManage ? (
            <div className="btn-row iam-dialog-actions">
              <button
                className="btn btn-secondary"
                type="button"
                disabled={busy}
                title="Issues a new secret; every client using the old one is cut off at once"
                onClick={() => {
                  if (confirmAction("Roll the secret? Every client using the current secret is cut off immediately.")) {
                    void act(async () => {
                      const res = await rollAPIToken(tok.id);
                      onRolled(res.token);
                    });
                  }
                }}
              >
                Roll secret and cut off clients
              </button>
              <button
                className="btn btn-danger"
                type="button"
                disabled={busy}
                onClick={() => {
                  if (confirmAction(`Delete ${row.name}? It stops working now. Its history stays in the audit log.`)) {
                    void act(async () => {
                      await deleteAPIToken(tok.id);
                      onClose();
                    });
                  }
                }}
              >
                Delete token
              </button>
            </div>
          ) : null}
        </div>
      ) : (
        <p className="muted">This service account has no token. Create a new service account to issue one.</p>
      )}
    </Dialog>
  );
}
