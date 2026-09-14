import { useEffect, useMemo, useState } from "react";
import { listAudit } from "../api/client";
import { EmptyState, LoadingState } from "../components/EmptyState";
import { Field } from "../components/Field";
import { PageHeader } from "../components/PageHeader";
import { StatusBadge } from "../components/StatusBadge";
import { auditActionLabel } from "../humanize";
import { SelectField } from "../components/form/SelectField";
import { ErrorBanner } from "../ui/ErrorBanner";
import { RelativeTime } from "../ui/RelativeTime";

type AuditRow = {
  id: string;
  action: string;
  result: string;
  created_at: string;
  actor_user_id?: string;
  actor_username?: string;
  actor_kind?: string;
  actor_label?: string;
  resource_name?: string;
  resource_id?: string;
  detail?: Record<string, unknown>;
};

function actorLabel(row: AuditRow): string {
  return row.actor_label || row.actor_username || (row.actor_kind === "system" ? "System" : "Unknown");
}

function friendlyError(raw: string): { title: string; secondary: string; detail: string } {
  if (/invalid input syntax for type uuid/i.test(raw) || /SQLSTATE 22P02/i.test(raw)) {
    return {
      title: "Audit log couldn't be loaded",
      secondary: "The server rejected an invalid actor identifier.",
      detail: raw,
    };
  }
  return {
    title: "Audit log couldn't be loaded",
    secondary: "The control plane could not return audit events.",
    detail: raw,
  };
}

export function AuditPage() {
  const [items, setItems] = useState<AuditRow[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [query, setQuery] = useState("");
  const [result, setResult] = useState("all");
  const [openId, setOpenId] = useState<string | null>(null);

  function reload() {
    setLoading(true);
    void listAudit()
      .then((body) => {
        setItems((body.items ?? []) as AuditRow[]);
        setError(null);
      })
      .catch((err) => setError(err instanceof Error ? err.message : "Unavailable"))
      .finally(() => setLoading(false));
  }

  useEffect(() => {
    reload();
  }, []);

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    return items.filter((item) => {
      if (result !== "all" && item.result !== result) {
        return false;
      }
      if (!q) {
        return true;
      }
      const hay = [actorLabel(item), auditActionLabel(item.action), item.action, item.resource_name, item.result]
        .filter(Boolean)
        .join(" ")
        .toLowerCase();
      return hay.includes(q);
    });
  }, [items, query, result]);

  const parsed = error ? friendlyError(error) : null;

  return (
    <section className="page page-wide" aria-labelledby="audit-heading">
      <PageHeader
        id="audit-heading"
        title="Audit Log"
        kicker="Who changed what. Passwords, token values, and MFA secrets are never stored here."
      />
      {parsed ? (
        <ErrorBanner
          title={parsed.title}
          secondary={parsed.secondary}
          detail={parsed.detail}
          action={
            <button className="btn btn-secondary" type="button" onClick={() => reload()}>
              Retry
            </button>
          }
        />
      ) : null}
      {loading ? <LoadingState label="Loading audit events" /> : null}
      {!loading && items.length === 0 && !error ? (
        <EmptyState title="No audit events">Administrative actions will appear here.</EmptyState>
      ) : null}
      {items.length > 0 ? (
        <>
          <div className="btn-row">
            <Field
              id="audit-q"
              label="Search"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder="Actor, action, or resource"
            />
            <SelectField id="audit-result" label="Result" value={result} onChange={(e) => setResult(e.target.value)}>
              <option value="all">All results</option>
              <option value="ok">Succeeded</option>
              <option value="denied">Denied</option>
              <option value="warning">Warning</option>
            </SelectField>
          </div>
          <div className="stack">
            {filtered.map((row) => {
              const open = openId === row.id;
              return (
                <div key={row.id}>
                  <button
                    type="button"
                    className="activity-row"
                    onClick={() => setOpenId(open ? null : row.id)}
                    aria-expanded={open}
                  >
                    <span>
                      <span className="activity-title">{actorLabel(row)}</span>
                      <span className="activity-meta">
                        {" "}
                        {auditActionLabel(row.action)}
                        {row.resource_name ? ` “${row.resource_name}”` : ""}
                        <span className="field-hint"> {row.action}</span>
                      </span>
                    </span>
                    <span className="activity-meta">
                      <StatusBadge status={row.result} /> <RelativeTime value={row.created_at} />
                    </span>
                  </button>
                  {open ? (
                    <div className="activity-detail">
                      <p className="field-hint">
                        {row.action}
                        {row.resource_id ? ` · ${row.resource_id}` : ""}
                      </p>
                      {row.detail ? <pre className="activity-detail-raw">{JSON.stringify(row.detail, null, 2)}</pre> : null}
                    </div>
                  ) : null}
                </div>
              );
            })}
          </div>
        </>
      ) : null}
    </section>
  );
}
