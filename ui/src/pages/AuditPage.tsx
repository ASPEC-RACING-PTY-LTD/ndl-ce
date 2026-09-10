import { useEffect, useState } from "react";
import { listAudit } from "../api/client";
import type { AuditEvent } from "../generated/openapi";
import { EmptyState, ErrorState, LoadingState } from "../components/EmptyState";
import { PageHeader } from "../components/PageHeader";
import { formatWhen } from "../format";

export function AuditPage() {
  const [items, setItems] = useState<AuditEvent[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    void listAudit()
      .then((body) => {
        setItems(body.items ?? []);
        setError(null);
      })
      .catch((err) => setError(err instanceof Error ? err.message : "Unavailable"))
      .finally(() => setLoading(false));
  }, []);

  return (
    <section className="page page-wide" aria-labelledby="audit-heading">
      <PageHeader
        id="audit-heading"
        title="Audit Log"
        kicker="Administrative actions on this appliance. Passwords, token values, and MFA secrets are never stored here."
      />
      {error ? <ErrorState>{error}</ErrorState> : null}
      {loading ? <LoadingState label="Loading audit events" /> : null}
      {!loading && items.length === 0 && !error ? (
        <EmptyState title="No audit events">Administrative actions will appear here.</EmptyState>
      ) : null}
      {items.length > 0 ? (
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>When</th>
                <th>Actor</th>
                <th>Action</th>
                <th>Result</th>
              </tr>
            </thead>
            <tbody>
              {items.map((e) => (
                <tr key={e.id}>
                  <td>{formatWhen(e.created_at)}</td>
                  <td>{e.actor_username || e.actor_user_id || "System"}</td>
                  <td>{e.action}</td>
                  <td>{e.result}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : null}
    </section>
  );
}
