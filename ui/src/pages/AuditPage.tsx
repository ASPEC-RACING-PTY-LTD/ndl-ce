import { useEffect, useState } from "react";
import { listAudit } from "../api/client";
import type { AuditEvent } from "../generated/openapi";
import { formatWhen } from "../format";

export function AuditPage() {
  const [items, setItems] = useState<AuditEvent[]>([]);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    void listAudit()
      .then((body) => setItems(body.items ?? []))
      .catch((err) => setError(err instanceof Error ? err.message : "Unavailable"));
  }, []);

  return (
    <section className="page page-wide" aria-labelledby="audit-heading">
      <header className="page-header">
        <h1 id="audit-heading">Audit</h1>
        <p className="page-kicker">Security audit events. Viewers cannot read this log.</p>
      </header>
      {error ? (
        <p className="banner banner-error" role="alert">
          {error}
        </p>
      ) : null}
      {items.length === 0 && !error ? <p>Not configured</p> : (
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>When</th>
                <th>Action</th>
                <th>Result</th>
              </tr>
            </thead>
            <tbody>
              {items.map((e) => (
                <tr key={e.id}>
                  <td>{formatWhen(e.created_at)}</td>
                  <td>{e.action}</td>
                  <td>{e.result}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}
