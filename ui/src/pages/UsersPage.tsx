import { useEffect, useMemo, useState } from "react";
import {
  ApiError,
  createManagedUser,
  deleteManagedUser,
  listManagedUsers,
  patchManagedUser,
  resetManagedUserPassword,
  revokeManagedUserSessions,
  revokeManagedUserTokens,
  type ManagedUser,
} from "../api/client";
import { ActionMenu } from "../components/ActionMenu";
import { ConfirmDialog } from "../components/ConfirmDialog";
import { EmptyState, ErrorState, LoadingState } from "../components/EmptyState";
import { Icon } from "../components/Icon";
import { PageHeader } from "../components/PageHeader";
import { ResourceTable } from "../components/ResourceTable";
import { StatusBadge } from "../components/StatusBadge";
import { formatWhen } from "../format";
import { roleLabel } from "../labels";
import { hasGrant } from "../rbac";
import { useSession } from "../session";
import { Field } from "../components/Field";
import { SelectField } from "../components/form/SelectField";
import { Dialog } from "../ui/Dialog";

type ConfirmKind = "delete" | "disable" | "reset" | "sessions" | "tokens" | "mfa" | null;

export function UsersPage() {
  const session = useSession();
  const user = session.status === "ready" ? session.user : null;
  const canCreate = hasGrant(user, "users.create");
  const canUpdate = hasGrant(user, "users.update");
  const canDelete = hasGrant(user, "users.delete");
  const canRoles = hasGrant(user, "users.roles.manage");
  const canRevoke = hasGrant(user, "users.sessions.revoke");
  const [items, setItems] = useState<ManagedUser[]>([]);
  const [total, setTotal] = useState(0);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [query, setQuery] = useState("");
  const [role, setRole] = useState("");
  const [status, setStatus] = useState("");
  const [sort, setSort] = useState("username");
  const [creating, setCreating] = useState(false);
  const [editing, setEditing] = useState<ManagedUser | null>(null);
  const [username, setUsername] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [password, setPassword] = useState("");
  const [newRole, setNewRole] = useState("viewer");
  const [target, setTarget] = useState<ManagedUser | null>(null);
  const [confirm, setConfirm] = useState<ConfirmKind>(null);
  const [resetPassword, setResetPassword] = useState("");
  const [busy, setBusy] = useState(false);

  async function reload() {
    const body = await listManagedUsers({ q: query, role, status, sort });
    setItems(body.items ?? []);
    setTotal(body.total ?? 0);
  }

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    void listManagedUsers({ q: query, role, status, sort })
      .then((body) => {
        if (!cancelled) {
          setItems(body.items ?? []);
          setTotal(body.total ?? 0);
          setError(null);
        }
      })
      .catch((err) => {
        if (!cancelled) {
          setError(err instanceof Error ? err.message : "Unavailable");
          setItems([]);
        }
      })
      .finally(() => {
        if (!cancelled) {
          setLoading(false);
        }
      });
    return () => {
      cancelled = true;
    };
  }, [query, role, sort, status]);

  const headers = useMemo(
    () => ["User", "Role", "Status", "MFA", "API tokens", "Created", "Last login", ""],
    [],
  );

  async function onCreate() {
    setBusy(true);
    setError(null);
    try {
      await createManagedUser({ username, password, display_name: displayName, role: newRole });
      setUsername("");
      setDisplayName("");
      setPassword("");
      setNewRole("viewer");
      setCreating(false);
      await reload();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Create failed");
    } finally {
      setBusy(false);
    }
  }

  async function onConfirm() {
    if (!target || !confirm) {
      return;
    }
    setBusy(true);
    setError(null);
    try {
      if (confirm === "delete") {
        await deleteManagedUser(target.id);
      } else if (confirm === "disable") {
        await patchManagedUser(target.id, { disabled: target.status !== "disabled" }, "disable-user");
      } else if (confirm === "reset") {
        await resetManagedUserPassword(target.id, resetPassword);
        setResetPassword("");
      } else if (confirm === "sessions") {
        await revokeManagedUserSessions(target.id);
      } else if (confirm === "tokens") {
        await revokeManagedUserTokens(target.id);
      } else if (confirm === "mfa") {
        await patchManagedUser(target.id, { mfa_required: !target.mfa_required });
      }
      setConfirm(null);
      setTarget(null);
      await reload();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Action failed");
    } finally {
      setBusy(false);
    }
  }

  async function onSaveDisplayName() {
    if (!editing) {
      return;
    }
    setBusy(true);
    setError(null);
    try {
      await patchManagedUser(editing.id, { display_name: displayName });
      setEditing(null);
      setDisplayName("");
      await reload();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Update failed");
    } finally {
      setBusy(false);
    }
  }

  async function onChangeRole(row: ManagedUser, next: string) {
    setBusy(true);
    setError(null);
    try {
      await patchManagedUser(row.id, { role: next });
      await reload();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Role change failed");
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="page page-wide" aria-labelledby="users-heading">
      <PageHeader
        id="users-heading"
        title="Users"
        kicker="People and service identities on this appliance. The last Owner cannot be removed, disabled, or demoted."
        actions={
          canCreate ? (
            <button
              className="btn btn-primary"
              type="button"
              onClick={() => {
                setEditing(null);
                setDisplayName("");
                setCreating(true);
              }}
            >
              Create user
            </button>
          ) : null
        }
      />
      {error ? <ErrorState>{error}</ErrorState> : null}
      <Dialog
        open={creating && canCreate}
        title="Create user"
        unsaved={Boolean(username || password || displayName)}
        onClose={() => setCreating(false)}
        footer={
          <div className="btn-row">
            <button className="btn btn-primary" type="submit" form="create-user-form" disabled={busy}>
              Create
            </button>
          </div>
        }
      >
        <form
          id="create-user-form"
          className="form"
          onSubmit={(event) => {
            event.preventDefault();
            void onCreate();
          }}
        >
          <Field id="new-username" label="Username" value={username} onChange={(e) => setUsername(e.target.value)} required />
          <Field id="new-display" label="Display name" value={displayName} onChange={(e) => setDisplayName(e.target.value)} />
          <Field
            id="new-password"
            label="Password"
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            required
            minLength={8}
          />
          <SelectField id="new-role" label="Role" value={newRole} onChange={(e) => setNewRole(e.target.value)}>
            <option value="viewer">User</option>
            <option value="operator">Admin</option>
            {canRoles ? <option value="admin">Owner</option> : null}
          </SelectField>
        </form>
      </Dialog>
      <Dialog
        open={Boolean(editing) && canUpdate}
        title={editing ? `Edit ${editing.username}` : "Edit user"}
        unsaved={Boolean(editing && displayName !== (editing.display_name || ""))}
        onClose={() => {
          setEditing(null);
          setDisplayName("");
        }}
        footer={
          <div className="btn-row">
            <button className="btn btn-primary" type="submit" form="edit-user-form" disabled={busy}>
              Save
            </button>
          </div>
        }
      >
        <form
          id="edit-user-form"
          className="form"
          onSubmit={(event) => {
            event.preventDefault();
            void onSaveDisplayName();
          }}
        >
          <p className="field-hint">Username and password hashes stay unchanged. Use reset password for a new credential.</p>
          <Field id="edit-display" label="Display name" value={displayName} onChange={(e) => setDisplayName(e.target.value)} />
        </form>
      </Dialog>
      <div className="stack">
        <div className="toolbar">
          <label className="search-field">
            <Icon name="search" size={14} />
            <input
              className="field-input"
              type="search"
              placeholder="Search users"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              aria-label="Search users"
            />
          </label>
          <label className="field-label">
            Role
            <select className="field-input" value={role} onChange={(e) => setRole(e.target.value)}>
              <option value="">All roles</option>
              <option value="admin">Owner</option>
              <option value="operator">Admin</option>
              <option value="viewer">User</option>
            </select>
          </label>
          <label className="field-label">
            Status
            <select className="field-input" value={status} onChange={(e) => setStatus(e.target.value)}>
              <option value="">All statuses</option>
              <option value="active">Active</option>
              <option value="disabled">Disabled</option>
            </select>
          </label>
          <label className="field-label">
            Sort
            <select className="field-input" value={sort} onChange={(e) => setSort(e.target.value)}>
              <option value="username">Username</option>
              <option value="created_desc">Newest</option>
              <option value="last_login">Last login</option>
              <option value="role">Role</option>
            </select>
          </label>
        </div>
        {loading ? <LoadingState label="Loading users" /> : null}
        <ResourceTable
          headers={headers}
          empty={<EmptyState title="No users match">{total === 0 ? "No accounts are visible." : "Try a different search or filter."}</EmptyState>}
          rows={items.map((row) => {
            const primary = row.roles?.includes("admin") ? "admin" : row.roles?.includes("operator") ? "operator" : row.roles?.[0] || "";
            const actions = [];
            if (canUpdate) {
              actions.push({
                label: "Edit",
                onClick: () => {
                  setCreating(false);
                  setEditing(row);
                  setDisplayName(row.display_name || "");
                },
              });
              actions.push({
                label: row.mfa_required ? "Stop requiring MFA" : "Require MFA",
                onClick: () => {
                  setTarget(row);
                  setConfirm("mfa");
                },
              });
            }
            if (canUpdate && !row.protected) {
              actions.push({
                label: row.status === "disabled" ? "Enable" : "Disable",
                onClick: () => {
                  setTarget(row);
                  setConfirm("disable");
                },
              });
              actions.push({
                label: "Reset password",
                onClick: () => {
                  setTarget(row);
                  setConfirm("reset");
                },
              });
            }
            if (canUpdate && canRoles && !row.protected) {
              actions.push({
                label: "Make User",
                onClick: () => void onChangeRole(row, "viewer"),
              });
              actions.push({
                label: "Make Admin",
                onClick: () => void onChangeRole(row, "operator"),
              });
              actions.push({
                label: "Make Owner",
                onClick: () => void onChangeRole(row, "admin"),
              });
            }
            if (canRevoke) {
              actions.push({
                label: "Revoke sessions",
                onClick: () => {
                  setTarget(row);
                  setConfirm("sessions");
                },
              });
              actions.push({
                label: "Revoke API tokens",
                onClick: () => {
                  setTarget(row);
                  setConfirm("tokens");
                },
              });
            }
            if (canDelete && !row.protected) {
              actions.push({
                label: "Delete",
                danger: true,
                onClick: () => {
                  setTarget(row);
                  setConfirm("delete");
                },
              });
            }
            return [
              <div key="user">
                <strong>{row.display_name || row.username}</strong>
                <div className="field-hint">
                  {row.username}
                  {row.kind === "service" ? " · service" : ""}
                  {row.protected ? " · last owner" : ""}
                </div>
              </div>,
              roleLabel(primary),
              <StatusBadge
                key="st"
                status={row.status === "active" ? "ok" : "stopped"}
                label={row.status === "active" ? "Active" : "Disabled"}
              />,
              row.mfa_status === "enabled" ? "Enrolled" : row.mfa_status === "required" ? "Required" : "Not enrolled",
              String(row.api_token_count ?? 0),
              row.created_at ? formatWhen(row.created_at) : "Not reported",
              row.last_login_at ? formatWhen(row.last_login_at) : "Never",
              actions.length ? <ActionMenu key="act" items={actions} /> : null,
            ];
          })}
        />
        <p className="field-hint">{total} account{total === 1 ? "" : "s"}</p>
      </div>
      <ConfirmDialog
        open={Boolean(confirm && target)}
        title={
          confirm === "delete"
            ? `Delete ${target?.username ?? "user"}`
            : confirm === "disable"
              ? `${target?.status === "disabled" ? "Enable" : "Disable"} ${target?.username ?? "user"}`
              : confirm === "reset"
                ? `Reset password for ${target?.username ?? "user"}`
                : confirm === "sessions"
                  ? `Revoke sessions for ${target?.username ?? "user"}`
                  : confirm === "mfa"
                    ? `${target?.mfa_required ? "Stop requiring MFA for" : "Require MFA for"} ${target?.username ?? "user"}`
                    : `Revoke API tokens for ${target?.username ?? "user"}`
        }
        danger={confirm === "delete" || confirm === "disable"}
        confirmLabel={confirm === "delete" ? "Delete user" : "Confirm"}
        onClose={() => {
          setConfirm(null);
          setTarget(null);
        }}
        onConfirm={() => void onConfirm()}
      >
        {confirm === "delete" ? (
          <p>This removes the account, sessions, and API tokens. Workloads are not changed. This cannot be undone.</p>
        ) : null}
        {confirm === "disable" ? (
          <p>A disabled account cannot sign in. Existing sessions and API tokens are revoked.</p>
        ) : null}
        {confirm === "reset" ? (
          <>
            <p>Set a new password. The current password is never shown. Active sessions are revoked.</p>
            <label className="field-label" htmlFor="reset-password">
              New password
            </label>
            <input
              id="reset-password"
              className="field-input"
              type="password"
              minLength={8}
              value={resetPassword}
              onChange={(e) => setResetPassword(e.target.value)}
            />
          </>
        ) : null}
        {confirm === "sessions" ? <p>Every browser session for this user will have to sign in again.</p> : null}
        {confirm === "tokens" ? <p>Every API token owned by this user will be revoked. Secrets are never displayed.</p> : null}
        {confirm === "mfa" ? (
          <p>
            This sets an enrollment requirement. Authenticator secrets are never shown. Personal enrollment stays on
            Account.
          </p>
        ) : null}
      </ConfirmDialog>
    </section>
  );
}
