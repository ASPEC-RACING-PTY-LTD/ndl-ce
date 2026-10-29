import { useEffect, useState } from "react";
import {
  ApiError,
  deleteManagedUser,
  getUserAllowlist,
  listAudit,
  listResourceAccess,
  patchManagedUser,
  putUserAllowlist,
  resetManagedUserPassword,
  resetUserMfa,
  revokeManagedUserSessions,
  revokeManagedUserTokens,
  unlockUser,
  type ManagedUser,
} from "../api/client";
import type { AuditEvent, IPAllowlist } from "../generated/openapi";
import { formatWhen } from "../format";
import { roleLabel } from "../labels";
import { hasGrant } from "../rbac";
import { useSession } from "../session";
import { Dialog } from "../ui/Dialog";
import { Segmented } from "../ui/Segmented";
import { Avatar } from "./Avatar";
import { ErrorNotice } from "./ErrorNotice";
import { LoadingState } from "./EmptyState";
import { StatusBadge } from "./StatusBadge";

type Tab = "profile" | "access" | "security" | "activity";

function errText(err: unknown, fallback: string): string {
  return err instanceof ApiError ? err.message : err instanceof Error ? err.message : fallback;
}

function primaryRole(u: ManagedUser): string {
  return u.roles?.includes("superadmin") ? "superadmin" : u.roles?.includes("admin") ? "admin" : u.roles?.[0] || "none";
}

/**
 * UserDetailDialog is everything about one account in IAM: profile, access,
 * security and recent activity, with every management action. The server
 * enforces the role hierarchy; actions the viewer cannot take are hidden.
 */
export function UserDetailDialog({
  user,
  onClose,
  onChanged,
}: {
  user: ManagedUser | null;
  onClose: () => void;
  onChanged: () => Promise<void> | void;
}) {
  const session = useSession();
  const me = session.status === "ready" ? session.user : null;
  const [tab, setTab] = useState<Tab>("profile");
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [username, setUsername] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [email, setEmail] = useState("");
  const [email2, setEmail2] = useState("");
  const [role, setRole] = useState("user");
  const [newPassword, setNewPassword] = useState("");
  const [deleteText, setDeleteText] = useState("");
  const [allow, setAllow] = useState<IPAllowlist | null>(null);
  const [allowRows, setAllowRows] = useState<{ cidr: string; note: string }[]>([]);
  const [allowOn, setAllowOn] = useState(false);
  const [activity, setActivity] = useState<AuditEvent[] | null>(null);
  const [activityError, setActivityError] = useState<string | null>(null);
  const [itemCount, setItemCount] = useState<number | null>(null);

  useEffect(() => {
    if (!user) {
      return;
    }
    setTab("profile");
    setError(null);
    setNotice(null);
    setUsername(user.username);
    setDisplayName(user.display_name ?? "");
    setEmail(user.email ?? "");
    setEmail2(user.email_secondary ?? "");
    setRole(primaryRole(user));
    setNewPassword("");
    setDeleteText("");
    setAllow(null);
    setActivity(null);
    setActivityError(null);
    setItemCount(null);
  }, [user]);

  // Root is managed only by itself (or the host console).
  const locked = Boolean(user?.is_root && me?.user_id !== user?.id);
  const self = me?.user_id === user?.id;
  const canUpdate = hasGrant(me, "users.update") && !locked;
  const canRoles = hasGrant(me, "users.roles.manage") && !locked && !self;
  const canRevoke = hasGrant(me, "users.sessions.revoke") && !locked;
  const canDelete = hasGrant(me, "users.delete") && !locked && !self && !user?.protected;
  const canItemAccess = hasGrant(me, "users.roles.manage");

  useEffect(() => {
    if (!user || tab !== "access" || !canItemAccess || itemCount !== null) {
      return;
    }
    let cancelled = false;
    void listResourceAccess()
      .then((res) => {
        if (!cancelled) {
          setItemCount((res.grants ?? []).filter((g) => g.subject_kind === "user" && g.subject_id === user.id).length);
        }
      })
      .catch(() => undefined);
    return () => {
      cancelled = true;
    };
  }, [user, tab, canItemAccess, itemCount]);

  useEffect(() => {
    if (!user || tab !== "security" || allow || locked) {
      return;
    }
    void getUserAllowlist(user.id)
      .then((a) => {
        setAllow(a);
        setAllowRows(a.entries.map((e) => ({ cidr: e.cidr, note: e.note ?? "" })));
        setAllowOn(a.enabled);
      })
      .catch((err) => setError(errText(err, "Allowlist is unavailable")));
  }, [user, tab, allow, locked]);

  useEffect(() => {
    if (!user || tab !== "activity" || activity) {
      return;
    }
    void listAudit(500)
      .then((res) => {
        const id = user.id;
        setActivity(
          (res.items ?? [])
            .filter((e) => e.actor_user_id === id || e.resource_id === id || JSON.stringify(e.detail ?? {}).includes(id))
            .slice(0, 100),
        );
      })
      .catch((err) => setActivityError(errText(err, "Activity is unavailable")));
  }, [user, tab, activity]);

  async function act(fn: () => Promise<unknown>, done: string, fallback: string, close = false) {
    setBusy(true);
    setError(null);
    setNotice(null);
    try {
      await fn();
      setNotice(done);
      await onChanged();
      if (close) {
        onClose();
      }
    } catch (err) {
      setError(errText(err, fallback));
    } finally {
      setBusy(false);
    }
  }

  if (!user) {
    return null;
  }

  const profileDirty =
    username.trim() !== user.username ||
    displayName.trim() !== (user.display_name ?? "") ||
    email.trim() !== (user.email ?? "") ||
    email2.trim() !== (user.email_secondary ?? "");

  return (
    <Dialog open={Boolean(user)} wide title={user.display_name || user.username} onClose={onClose} unsaved={profileDirty}>
      <div className="user-detail-head">
        <Avatar userID={user.id} name={user.display_name || user.username} updatedAt={user.avatar_updated_at} size={48} />
        <div>
          <strong>{user.username}</strong>
          <div className="field-hint">
            {roleLabel(primaryRole(user))}
            {user.is_root ? " · root account" : ""}
            {user.kind === "service" ? " · service" : ""}
          </div>
        </div>
        <StatusBadge status={user.status === "active" ? "ok" : "stopped"} label={user.status === "active" ? "Active" : "Disabled"} />
      </div>
      {locked ? (
        <p className="banner banner-warn" role="status">
          This is the root account. Only its owner can change it, from their own Account page or the host console.
        </p>
      ) : null}
      {error ? <ErrorNotice error={error} /> : null}
      {notice ? (
        <p className="banner banner-ok" role="status">
          {notice}
        </p>
      ) : null}
      <div className="hub-tabs">
        <Segmented<Tab>
          ariaLabel="User sections"
          value={tab}
          onChange={setTab}
          options={[
            { id: "profile", label: "Profile" },
            { id: "access", label: "Access" },
            { id: "security", label: "Security" },
            { id: "activity", label: "Activity" },
          ]}
        />
      </div>

      {tab === "profile" ? (
        <div className="form">
          <label htmlFor="ud-username">Username</label>
          <input id="ud-username" className="field-input" value={username} disabled={!canUpdate} onChange={(e) => setUsername(e.target.value)} />
          <p className="field-hint">Renaming changes how they sign in. Audit history keeps the old name in each entry.</p>
          <label htmlFor="ud-display">Display name</label>
          <input id="ud-display" className="field-input" value={displayName} disabled={!canUpdate} onChange={(e) => setDisplayName(e.target.value)} />
          <label htmlFor="ud-email">Email</label>
          <input id="ud-email" className="field-input" type="email" value={email} disabled={!canUpdate} onChange={(e) => setEmail(e.target.value)} />
          <label htmlFor="ud-email2">Secondary email</label>
          <input id="ud-email2" className="field-input" type="email" value={email2} disabled={!canUpdate} onChange={(e) => setEmail2(e.target.value)} />
          <dl className="definition-list">
            <div>
              <dt>User ID</dt>
              <dd>
                <code>{user.id}</code>
              </dd>
            </div>
            <div>
              <dt>Created</dt>
              <dd>{user.created_at ? formatWhen(user.created_at) : "Not reported"}</dd>
            </div>
            <div>
              <dt>Last sign-in</dt>
              <dd>{user.last_login_at ? formatWhen(user.last_login_at) : "Never"}</dd>
            </div>
            <div>
              <dt>API tokens</dt>
              <dd>{user.api_token_count ?? 0}</dd>
            </div>
          </dl>
          {canUpdate ? (
            <div className="btn-row is-flush">
              <button
                className="btn btn-primary"
                type="button"
                disabled={busy || !profileDirty}
                onClick={() =>
                  void act(
                    () =>
                      patchManagedUser(user.id, {
                        username: username.trim(),
                        display_name: displayName.trim(),
                        email: email.trim(),
                        email_secondary: email2.trim(),
                      }),
                    "Profile saved.",
                    "Save failed",
                  )
                }
              >
                Save profile
              </button>
            </div>
          ) : null}
        </div>
      ) : null}

      {tab === "access" ? (
        <div className="form">
          <label htmlFor="ud-role">Role</label>
          <select id="ud-role" className="field-input" value={role} disabled={!canRoles} onChange={(e) => setRole(e.target.value)}>
            <option value="none">No role (item access only)</option>
            <option value="user">User</option>
            <option value="admin">Admin</option>
            <option value="superadmin">SuperAdmin</option>
          </select>
          <p className="field-hint">
            You can only assign roles below your own. The last SuperAdmin cannot be demoted. With no role, the account sees only
            the items it is given access to.
          </p>
          {itemCount !== null ? (
            <p className="field-hint">
              {itemCount === 0 ? "No access to single items." : `Has access to ${itemCount} item${itemCount === 1 ? "" : "s"}.`}{" "}
              {document.getElementById("item-access") ? (
                <button
                  type="button"
                  className="linkish"
                  onClick={() => {
                    onClose();
                    setTimeout(() => document.getElementById("item-access")?.scrollIntoView({ block: "start" }), 50);
                  }}
                >
                  Manage item access
                </button>
              ) : null}
            </p>
          ) : null}
          {canRoles ? (
            <div className="btn-row is-flush">
              <button
                className="btn btn-primary"
                type="button"
                disabled={busy || role === primaryRole(user)}
                onClick={() => void act(() => patchManagedUser(user.id, { role }), "Role changed.", "Role change failed")}
              >
                Change role
              </button>
            </div>
          ) : null}
          <label className="check-row">
            <input
              type="checkbox"
              checked={Boolean(user.mfa_required)}
              disabled={!canUpdate || busy}
              onChange={(e) =>
                void act(() => patchManagedUser(user.id, { mfa_required: e.target.checked }), "MFA requirement saved.", "Update failed")
              }
            />{" "}
            Require MFA for this user
          </label>
          <p className="field-hint">MFA: {user.mfa_status === "enabled" ? "enrolled" : user.mfa_status === "required" ? "required, not enrolled" : "not enrolled"}.</p>
        </div>
      ) : null}

      {tab === "security" ? (
        <div className="stack">
          {locked ? null : (
            <>
              <article className="panel stack">
                <h3>IP allowlist</h3>
                {!allow ? (
                  <LoadingState />
                ) : (
                  <>
                    {!allow.mfa_enabled ? <p className="field-hint">This user has no MFA, so the allowlist can only be off.</p> : null}
                    {allowRows.length ? (
                      <ul className="allowlist-rows">
                        {allowRows.map((r, i) => (
                          <li key={i}>
                            <input
                              className="field-input"
                              aria-label={`Address ${i + 1}`}
                              value={r.cidr}
                              disabled={!canUpdate}
                              onChange={(e) => setAllowRows((cur) => cur.map((x, j) => (j === i ? { ...x, cidr: e.target.value } : x)))}
                            />
                            <input
                              className="field-input"
                              aria-label={`Note ${i + 1}`}
                              value={r.note}
                              disabled={!canUpdate}
                              onChange={(e) => setAllowRows((cur) => cur.map((x, j) => (j === i ? { ...x, note: e.target.value } : x)))}
                            />
                            {canUpdate ? (
                              <button className="btn btn-ghost btn-sm" type="button" onClick={() => setAllowRows((cur) => cur.filter((_, j) => j !== i))}>
                                Remove
                              </button>
                            ) : null}
                          </li>
                        ))}
                      </ul>
                    ) : (
                      <p className="muted">No addresses.</p>
                    )}
                    {canUpdate ? (
                      <>
                        <div className="btn-row is-flush">
                          <button className="btn btn-ghost btn-sm" type="button" onClick={() => setAllowRows((cur) => [...cur, { cidr: "", note: "" }])}>
                            Add address
                          </button>
                        </div>
                        <label className="check-row">
                          <input type="checkbox" checked={allowOn} disabled={!allow.mfa_enabled && !allowOn} onChange={(e) => setAllowOn(e.target.checked)} /> Only
                          allow these addresses
                        </label>
                        <div className="btn-row is-flush">
                          <button
                            className="btn btn-primary"
                            type="button"
                            disabled={busy}
                            onClick={() =>
                              void act(
                                async () => {
                                  const res = await putUserAllowlist(user.id, {
                                    enabled: allowOn,
                                    entries: allowRows.filter((r) => r.cidr.trim()).map((r) => ({ cidr: r.cidr.trim(), note: r.note.trim() })),
                                  });
                                  setAllow(res);
                                  setAllowRows(res.entries.map((e) => ({ cidr: e.cidr, note: e.note ?? "" })));
                                  setAllowOn(res.enabled);
                                },
                                "Allowlist saved.",
                                "Save failed",
                              )
                            }
                          >
                            Save allowlist
                          </button>
                        </div>
                      </>
                    ) : null}
                  </>
                )}
              </article>
              {canUpdate && !self ? (
                <article className="panel stack">
                  <h3>Password</h3>
                  <label htmlFor="ud-newpw">New password</label>
                  <input id="ud-newpw" className="field-input" type="password" minLength={8} value={newPassword} onChange={(e) => setNewPassword(e.target.value)} />
                  <div className="btn-row is-flush">
                    <button
                      className="btn"
                      type="button"
                      disabled={busy || newPassword.length < 8}
                      onClick={() =>
                        void act(async () => {
                          await resetManagedUserPassword(user.id, newPassword);
                          setNewPassword("");
                        }, "Password reset. Their sessions were signed out.", "Reset failed")
                      }
                    >
                      Reset password
                    </button>
                  </div>
                </article>
              ) : null}
              <article className="panel stack">
                <h3>Access recovery</h3>
                <div className="btn-row is-flush">
                  {canUpdate ? (
                    <button className="btn" type="button" disabled={busy} onClick={() => void act(() => unlockUser(user.id), "Login lockout cleared.", "Unlock failed")}>
                      Clear login lockout
                    </button>
                  ) : null}
                  {canUpdate && !self && user.mfa_enabled ? (
                    <button
                      className="btn"
                      type="button"
                      disabled={busy}
                      onClick={() => void act(() => resetUserMfa(user.id), "MFA reset. They can enrol again; their IP allowlist is off.", "Reset failed")}
                    >
                      Reset MFA
                    </button>
                  ) : null}
                  {canRevoke ? (
                    <>
                      <button className="btn" type="button" disabled={busy} onClick={() => void act(() => revokeManagedUserSessions(user.id), "Sessions revoked.", "Revoke failed")}>
                        Revoke sessions
                      </button>
                      <button className="btn" type="button" disabled={busy} onClick={() => void act(() => revokeManagedUserTokens(user.id), "API tokens revoked.", "Revoke failed")}>
                        Revoke API tokens
                      </button>
                    </>
                  ) : null}
                </div>
              </article>
              {(canUpdate && !self && !user.protected) || canDelete ? (
                <article className="panel stack">
                  <h3>Account</h3>
                  {canUpdate && !self && !user.protected ? (
                    <div className="btn-row is-flush">
                      <button
                        className={user.status === "disabled" ? "btn" : "btn btn-danger"}
                        type="button"
                        disabled={busy}
                        onClick={() =>
                          void act(
                            () => patchManagedUser(user.id, { disabled: user.status !== "disabled" }, "disable-user"),
                            user.status === "disabled" ? "Account enabled." : "Account disabled; sessions and tokens revoked.",
                            "Update failed",
                          )
                        }
                      >
                        {user.status === "disabled" ? "Enable account" : "Disable account"}
                      </button>
                    </div>
                  ) : null}
                  {canDelete ? (
                    <>
                      <label htmlFor="ud-delete">
                        Type <code>{user.username}</code> to delete this account
                      </label>
                      <input id="ud-delete" className="field-input" value={deleteText} onChange={(e) => setDeleteText(e.target.value)} />
                      <div className="btn-row is-flush">
                        <button
                          className="btn btn-danger"
                          type="button"
                          disabled={busy || deleteText !== user.username}
                          onClick={() => void act(() => deleteManagedUser(user.id), "Account deleted.", "Delete failed", true)}
                        >
                          Delete account
                        </button>
                      </div>
                    </>
                  ) : null}
                </article>
              ) : null}
            </>
          )}
        </div>
      ) : null}

      {tab === "activity" ? (
        activityError ? (
          <ErrorNotice error={activityError} />
        ) : !activity ? (
          <LoadingState label="Loading activity" />
        ) : activity.length === 0 ? (
          <p className="muted">No recent activity for this account.</p>
        ) : (
          <ul className="user-activity">
            {activity.map((e) => (
              <li key={e.id}>
                <span className="muted">{formatWhen(e.created_at)}</span>
                <code>{e.action}</code>
                <span>{e.result}</span>
                <span className="muted">{e.actor_label || e.actor_username || ""}</span>
              </li>
            ))}
          </ul>
        )
      ) : null}
    </Dialog>
  );
}
