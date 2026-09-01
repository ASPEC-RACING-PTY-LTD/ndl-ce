import { useEffect, useState } from "react";
import { ApiError, addGroupMember, bindGroupRole, createGroup, listGroups } from "../api/client";
import type { Group } from "../generated/openapi";

export function GroupsPage() {
  const [items, setItems] = useState<Group[]>([]);
  const [name, setName] = useState("");
  const [memberGroup, setMemberGroup] = useState("");
  const [memberUser, setMemberUser] = useState("");
  const [roleGroup, setRoleGroup] = useState("");
  const [role, setRole] = useState("operator");
  const [error, setError] = useState<string | null>(null);

  async function reload() {
    const body = await listGroups();
    setItems(body.items ?? []);
  }

  useEffect(() => {
    void reload().catch((err) => setError(err instanceof Error ? err.message : "Unavailable"));
  }, []);

  return (
    <section className="page" aria-labelledby="groups-heading">
      <header className="page-header">
        <h1 id="groups-heading">Groups</h1>
        <p className="page-kicker">
          Groups receive operator or viewer role bindings. Admin cannot be granted through a group.
        </p>
      </header>
      {error ? (
        <p className="banner banner-error" role="alert">
          {error}
        </p>
      ) : null}
      <form
        className="form"
        onSubmit={(event) => {
          event.preventDefault();
          void createGroup(name)
            .then(() => {
              setName("");
              return reload();
            })
            .catch((err) => setError(err instanceof ApiError ? err.message : "Create failed"));
        }}
      >
        <label htmlFor="group-name">Name</label>
        <input id="group-name" value={name} onChange={(e) => setName(e.target.value)} />
        <button className="btn btn-primary" type="submit">
          Add group
        </button>
      </form>
      {items.length === 0 ? (
        <p>Not configured</p>
      ) : (
        <>
          <ul>
            {items.map((g) => (
              <li key={g.id}>
                {g.name} ({g.id})
              </li>
            ))}
          </ul>
          <form
            className="form"
            onSubmit={(event) => {
              event.preventDefault();
              void addGroupMember(memberGroup, memberUser)
                .then(() => {
                  setMemberUser("");
                  return reload();
                })
                .catch((err) => setError(err instanceof ApiError ? err.message : "Add member failed"));
            }}
          >
            <label htmlFor="member-group">Group id</label>
            <input id="member-group" value={memberGroup} onChange={(e) => setMemberGroup(e.target.value)} />
            <label htmlFor="member-user">User id</label>
            <input id="member-user" value={memberUser} onChange={(e) => setMemberUser(e.target.value)} />
            <button className="btn" type="submit">
              Add member
            </button>
          </form>
          <form
            className="form"
            onSubmit={(event) => {
              event.preventDefault();
              void bindGroupRole(roleGroup, role)
                .then(() => reload())
                .catch((err) => setError(err instanceof ApiError ? err.message : "Bind role failed"));
            }}
          >
            <label htmlFor="role-group">Group id</label>
            <input id="role-group" value={roleGroup} onChange={(e) => setRoleGroup(e.target.value)} />
            <label htmlFor="group-role">Role</label>
            <select id="group-role" value={role} onChange={(e) => setRole(e.target.value)}>
              <option value="operator">operator</option>
              <option value="viewer">viewer</option>
            </select>
            <button className="btn" type="submit">
              Bind role
            </button>
          </form>
        </>
      )}
    </section>
  );
}
