import { useEffect, useState } from "react";
import { listRoles, type RoleInfo } from "../api/client";
import { EmptyState, ErrorState, LoadingState } from "../components/EmptyState";
import { PageHeader } from "../components/PageHeader";
import { StatusBadge } from "../components/StatusBadge";

export function RolesPage() {
  const [items, setItems] = useState<RoleInfo[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    void listRoles()
      .then((body) => {
        setItems(body.items ?? []);
        setError(null);
      })
      .catch((err) => setError(err instanceof Error ? err.message : "Unavailable"))
      .finally(() => setLoading(false));
  }, []);

  return (
    <section className="page page-wide" aria-labelledby="roles-heading">
      <PageHeader
        id="roles-heading"
        title="Roles & Permissions"
        kicker="Built-in roles are immutable. Custom roles are not assigned at request time, so they are not offered here."
      />
      {error ? <ErrorState>{error}</ErrorState> : null}
      {loading ? <LoadingState label="Loading roles" /> : null}
      {!loading && items.length === 0 ? <EmptyState title="No roles">The catalog did not return built-in roles.</EmptyState> : null}
      <div className="stack">
        {items.map((role) => (
          <article className="panel stack" key={role.name}>
            <header className="section-head">
              <h2>
                {role.title} <span className="field-hint">({role.name})</span>
              </h2>
              <StatusBadge status={role.immutable ? "ok" : "warning"} label={role.immutable ? "Built-in" : "Custom"} />
            </header>
            <p className="lede">{role.summary}</p>
            <dl className="definition-list">
              <div>
                <dt>Assigned accounts</dt>
                <dd>{role.user_count}</dd>
              </div>
              <div>
                <dt>Sign-in role</dt>
                <dd>{role.login ? "Yes" : "No"}</dd>
              </div>
            </dl>
            <h3 className="field-label">Effective permissions</h3>
            <ul className="plain-list">
              {(role.permissions ?? []).map((perm) => (
                <li key={perm}>
                  <code>{perm}</code>
                </li>
              ))}
            </ul>
          </article>
        ))}
      </div>
    </section>
  );
}
