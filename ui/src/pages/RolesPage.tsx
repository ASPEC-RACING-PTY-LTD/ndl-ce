import { useEffect, useMemo, useState } from "react";
import { listManagedUsers, listRoles, type RoleInfo } from "../api/client";
import { EmptyState, ErrorState, LoadingState } from "../components/EmptyState";
import { PageHeader } from "../components/PageHeader";
import { StatusBadge } from "../components/StatusBadge";
import { permissionGroup, permissionLabel, roleLabel } from "../labels";

type Section = "roles" | "permissions" | "assignments";

function groupedPermissions(perms: string[]): { group: string; items: string[] }[] {
  const map = new Map<string, string[]>();
  for (const perm of perms) {
    const group = permissionGroup(perm);
    const list = map.get(group) ?? [];
    list.push(perm);
    map.set(group, list);
  }
  return [...map.entries()].map(([group, items]) => ({ group, items }));
}

export function RolesPage() {
  const [items, setItems] = useState<RoleInfo[]>([]);
  const [assignments, setAssignments] = useState<{ username: string; roles: string[] }[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [section, setSection] = useState<Section>("roles");
  const [selected, setSelected] = useState<string>("");

  useEffect(() => {
    void Promise.all([listRoles(), listManagedUsers().catch(() => ({ items: [] }))])
      .then(([roles, users]) => {
        setItems(roles.items ?? []);
        setAssignments(
          (users.items ?? []).map((u) => ({ username: u.username, roles: u.roles ?? [] })),
        );
        setSelected((roles.items ?? [])[0]?.name ?? "");
        setError(null);
      })
      .catch((err) => setError(err instanceof Error ? err.message : "Unavailable"))
      .finally(() => setLoading(false));
  }, []);

  const role = items.find((item) => item.name === selected) ?? items[0] ?? null;
  const groups = useMemo(() => groupedPermissions(role?.permissions ?? []), [role]);

  return (
    <section className="page page-wide" aria-labelledby="roles-heading">
      <PageHeader
        id="roles-heading"
        title="Roles & Permissions"
        kicker="Built-in roles are immutable. Authorization stays on the control plane."
      />
      {error ? <ErrorState>{error}</ErrorState> : null}
      {loading ? <LoadingState label="Loading roles" /> : null}
      {!loading && items.length === 0 ? <EmptyState title="No roles">The catalog did not return built-in roles.</EmptyState> : null}
      {items.length > 0 ? (
        <div className="ctx-page">
          <nav className="ctx-page-nav" aria-label="Roles sections">
            <button type="button" aria-current={section === "roles" ? "page" : undefined} onClick={() => setSection("roles")}>
              Roles
            </button>
            <button
              type="button"
              aria-current={section === "permissions" ? "page" : undefined}
              onClick={() => setSection("permissions")}
            >
              Permissions
            </button>
            <button
              type="button"
              aria-current={section === "assignments" ? "page" : undefined}
              onClick={() => setSection("assignments")}
            >
              Assignments
            </button>
          </nav>
          <div className="stack">
            {section === "roles" ? (
              <div className="content-grid">
                {items.map((item) => (
                  <button
                    key={item.name}
                    type="button"
                    className={"selection-card" + (item.name === role?.name ? " is-selected" : "")}
                    onClick={() => {
                      setSelected(item.name);
                      setSection("permissions");
                    }}
                  >
                    <span className="title">{item.title}</span>
                    <span className="desc">{item.summary}</span>
                    <span className="desc">
                      {item.user_count} assigned · {item.login ? "Sign-in allowed" : "No sign-in"}
                    </span>
                    <StatusBadge status={item.immutable ? "ok" : "warning"} label={item.immutable ? "Built-in" : "Custom"} />
                  </button>
                ))}
              </div>
            ) : null}
            {section === "permissions" && role ? (
              <article className="compact-card stack">
                <header className="section-head">
                  <h2>{role.title}</h2>
                  <StatusBadge status={role.immutable ? "ok" : "warning"} label={role.immutable ? "Built-in" : "Custom"} />
                </header>
                <p className="lede">{role.summary}</p>
                <p className="field-hint">
                  {role.user_count} assigned accounts · {role.login ? "Sign-in allowed" : "Not a sign-in role"}
                  {role.immutable ? " · Immutable" : ""}
                </p>
                {groups.map((group) => (
                  <section key={group.group} className="stack">
                    <h3 className="field-label">{group.group}</h3>
                    <ul className="plain-list">
                      {group.items.map((perm) => (
                        <li key={perm}>
                          {permissionLabel(perm)}
                          <span className="field-hint"> {perm}</span>
                        </li>
                      ))}
                    </ul>
                  </section>
                ))}
              </article>
            ) : null}
            {section === "assignments" ? (
              <div className="data-list">
                <table>
                  <thead>
                    <tr>
                      <th>Account</th>
                      <th>Roles</th>
                    </tr>
                  </thead>
                  <tbody>
                    {assignments.map((row) => (
                      <tr key={row.username}>
                        <td>{row.username}</td>
                        <td>{row.roles.map((name) => roleLabel(name)).join(", ") || "None"}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            ) : null}
          </div>
        </div>
      ) : null}
    </section>
  );
}
