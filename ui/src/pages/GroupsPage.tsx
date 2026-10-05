import { useEffect, useMemo, useState } from "react";
import { ApiError, addGroupMember, bindGroupRole, createGroup, listGroups, listManagedUsers, type ManagedUser } from "../api/client";
import { DeleteButton } from "../components/DeleteButton";
import { EmptyState, ErrorState, LoadingState } from "../components/EmptyState";
import { PageHeader } from "../components/PageHeader";
import type { Group } from "../generated/openapi";
import { Dialog } from "../ui/Dialog";

type Action = { kind: "member" | "role"; group: Group } | null;

export function GroupsPage() {
  const [items, setItems] = useState<Group[]>([]);
  const [users, setUsers] = useState<ManagedUser[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);
  const [name, setName] = useState("");
  const [action, setAction] = useState<Action>(null);
  const [memberUser, setMemberUser] = useState("");
  const [role, setRole] = useState("operator");
  const [busy, setBusy] = useState(false);

  async function reload() {
    const body = await listGroups();
    setItems(body.items ?? []);
  }

  useEffect(() => {
    void Promise.all([reload(), listManagedUsers({}).then((b) => setUsers(b.items ?? [])).catch(() => setUsers([]))])
      .catch((err) => setError(err instanceof Error ? err.message : "Unavailable"))
      .finally(() => setLoading(false));
  }, []);

  const userName = useMemo(() => new Map(users.map((u) => [u.id, u.display_name || u.username])), [users]);

  async function run(fn: () => Promise<unknown>, fallback: string) {
    setBusy(true);
    setError(null);
    try {
      await fn();
      setAction(null);
      setCreating(false);
      setName("");
      setMemberUser("");
      await reload();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : fallback);
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="page" aria-labelledby="groups-heading">
      <PageHeader
        id="groups-heading"
        title="Groups"
        kicker="Groups receive operator or viewer role bindings. Admin cannot be granted through a group."
        actions={
          <button className="btn btn-primary" type="button" onClick={() => setCreating(true)}>
            Add group
          </button>
        }
      />
      {error ? <ErrorState>{error}</ErrorState> : null}
      {loading ? <LoadingState label="Loading groups" /> : null}
      {!loading && items.length === 0 ? (
        <EmptyState icon="users" title="No groups yet">
          Create a group to grant a role to several accounts at once.
        </EmptyState>
      ) : null}
      {items.length > 0 ? (
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>Group</th>
                <th>Members</th>
                <th className="col-tools">
                  <span className="visually-hidden">Actions</span>
                </th>
              </tr>
            </thead>
            <tbody>
              {items.map((g) => (
                <tr key={g.id}>
                  <td>
                    <strong>{g.name}</strong>
                    <span className="cell-sub cell-mono">{g.id}</span>
                  </td>
                  <td>
                    {g.member_ids?.length
                      ? g.member_ids.map((id) => userName.get(id) ?? id.slice(0, 8)).join(", ")
                      : <span className="muted">No members</span>}
                  </td>
                  <td className="col-tools">
                    <div className="btn-row is-flush">
                      <button className="btn btn-sm btn-secondary" type="button" onClick={() => setAction({ kind: "member", group: g })}>
                        Add member
                      </button>
                      <button className="btn btn-sm btn-secondary" type="button" onClick={() => setAction({ kind: "role", group: g })}>
                        Bind role
                      </button>
                      <DeleteButton
                        path={`/groups/${g.id}`}
                        name={g.name}
                        noun="group"
                        description={<p>Members lose the roles this group gave them. Their accounts and own roles stay.</p>}
                        onDeleted={() => reload()}
                      />
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : null}
      <Dialog
        open={creating}
        title="Add group"
        unsaved={Boolean(name)}
        onClose={() => setCreating(false)}
        footer={
          <button className="btn btn-primary" type="submit" form="group-create" disabled={busy || !name.trim()}>
            Add group
          </button>
        }
      >
        <form
          id="group-create"
          className="form"
          onSubmit={(event) => {
            event.preventDefault();
            void run(() => createGroup(name.trim()), "Create failed");
          }}
        >
          <label className="field" htmlFor="group-name">
            <span className="field-label">Name</span>
            <input id="group-name" className="field-input" value={name} onChange={(e) => setName(e.target.value)} />
          </label>
        </form>
      </Dialog>
      <Dialog
        open={Boolean(action)}
        title={action ? `${action.kind === "member" ? "Add member to" : "Bind role to"} ${action.group.name}` : "Group"}
        onClose={() => setAction(null)}
        footer={
          <button
            className="btn btn-primary"
            type="button"
            disabled={busy || (action?.kind === "member" && !memberUser)}
            onClick={() => {
              if (!action) {
                return;
              }
              void run(
                () => (action.kind === "member" ? addGroupMember(action.group.id, memberUser) : bindGroupRole(action.group.id, role)),
                action.kind === "member" ? "Add member failed" : "Bind role failed",
              );
            }}
          >
            {action?.kind === "member" ? "Add member" : "Bind role"}
          </button>
        }
      >
        {action?.kind === "member" ? (
          <label className="field" htmlFor="member-user">
            <span className="field-label">Account</span>
            <select id="member-user" className="field-input" value={memberUser} onChange={(e) => setMemberUser(e.target.value)}>
              <option value="">Choose an account</option>
              {users
                .filter((u) => !action.group.member_ids?.includes(u.id))
                .map((u) => (
                  <option key={u.id} value={u.id}>
                    {u.display_name || u.username}
                  </option>
                ))}
            </select>
          </label>
        ) : (
          <label className="field" htmlFor="group-role">
            <span className="field-label">Role</span>
            <select id="group-role" className="field-input" value={role} onChange={(e) => setRole(e.target.value)}>
              <option value="operator">Admin (operator)</option>
              <option value="viewer">User (viewer)</option>
            </select>
          </label>
        )}
      </Dialog>
    </section>
  );
}
