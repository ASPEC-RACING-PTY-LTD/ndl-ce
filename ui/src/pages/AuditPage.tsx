import { useMemo, useState } from "react";
import { listAudit } from "../api/client";
import { EmptyState, LoadingState } from "../components/EmptyState";
import { Icon } from "../components/Icon";
import { PageHeader } from "../components/PageHeader";
import { StatusBadge } from "../components/StatusBadge";
import { formatWhen } from "../format";
import { auditActionLabel } from "../humanize";
import { useQuery } from "../query";
import { DataTable, type Column } from "../ui/DataTable";
import { Dialog } from "../ui/Dialog";
import { ErrorBanner } from "../ui/ErrorBanner";
import { RelativeTime } from "../ui/RelativeTime";
import { Segmented } from "../ui/Segmented";
import { SummaryCard } from "../ui/SummaryCard";

type AuditRow = {
  id: string;
  action: string;
  result: string;
  created_at: string;
  actor_user_id?: string;
  actor_username?: string;
  actor_kind?: string;
  actor_label?: string;
  resource_kind?: string;
  resource_name?: string;
  resource_id?: string;
  remote_addr?: string;
  detail?: Record<string, unknown>;
};

type ResultFilter = "all" | "ok" | "denied" | "warning";
type Range = "1h" | "24h" | "7d" | "all";

const RANGE_MS: Record<Range, number> = { "1h": 3_600_000, "24h": 86_400_000, "7d": 604_800_000, all: 0 };

function actorLabel(row: AuditRow): string {
  return row.actor_label || row.actor_username || (row.actor_kind === "system" ? "System" : "Unknown");
}

function resultTone(result: string): string {
  if (result === "ok") {
    return "ok";
  }
  if (result === "denied" || result === "failed" || result === "error") {
    return "failed";
  }
  return "warning";
}

function resultLabel(result: string): string {
  if (result === "ok") {
    return "Succeeded";
  }
  if (result === "denied") {
    return "Denied";
  }
  return result ? result[0].toUpperCase() + result.slice(1) : "Unknown";
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

function csvCell(value: string): string {
  return /[",\n]/.test(value) ? `"${value.replace(/"/g, '""')}"` : value;
}

function downloadCSV(rows: AuditRow[]) {
  const head = ["time", "actor", "action", "resource", "result", "source", "id"];
  const lines = rows.map((r) =>
    [r.created_at, actorLabel(r), r.action, r.resource_name || r.resource_id || "", r.result, r.remote_addr || "", r.id]
      .map(csvCell)
      .join(","),
  );
  const blob = new Blob([[head.join(","), ...lines].join("\n")], { type: "text/csv" });
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = `audit-${new Date().toISOString().slice(0, 19).replace(/[:T]/g, "-")}.csv`;
  a.click();
  URL.revokeObjectURL(url);
}

export function AuditPage() {
  const [limit, setLimit] = useState(200);
  const q = useQuery(`audit:${limit}`, () => listAudit(limit), 30000);
  const items = useMemo(() => (q.data?.items ?? []) as AuditRow[], [q.data]);
  const [query, setQuery] = useState("");
  const [result, setResult] = useState<ResultFilter>("all");
  const [range, setRange] = useState<Range>("all");
  const [actor, setActor] = useState("");
  const [open, setOpen] = useState<AuditRow | null>(null);

  const actors = useMemo(() => [...new Set(items.map(actorLabel))].sort(), [items]);
  const filtered = useMemo(() => {
    const needle = query.trim().toLowerCase();
    const since = RANGE_MS[range] ? Date.now() - RANGE_MS[range] : 0;
    return items.filter((item) => {
      if (result !== "all" && (result === "ok" ? item.result !== "ok" : result === "denied" ? item.result !== "denied" : item.result === "ok" || item.result === "denied")) {
        return false;
      }
      if (actor && actorLabel(item) !== actor) {
        return false;
      }
      if (since && Date.parse(item.created_at) < since) {
        return false;
      }
      if (!needle) {
        return true;
      }
      const hay = [actorLabel(item), auditActionLabel(item.action), item.action, item.resource_name, item.resource_id, item.result, item.remote_addr]
        .filter(Boolean)
        .join(" ")
        .toLowerCase();
      return hay.includes(needle);
    });
  }, [items, query, result, actor, range]);

  const stats = useMemo(() => {
    const day = Date.now() - RANGE_MS["24h"];
    const recent = items.filter((i) => Date.parse(i.created_at) >= day);
    return {
      day: recent.length,
      denied: recent.filter((i) => i.result === "denied").length,
      actors: new Set(recent.map(actorLabel)).size,
    };
  }, [items]);

  const columns: Column<AuditRow>[] = [
    {
      id: "time",
      header: "Time",
      width: "11rem",
      sortValue: (r) => Date.parse(r.created_at) || 0,
      cell: (r) => (
        <span title={formatWhen(r.created_at)}>
          <RelativeTime value={r.created_at} />
          <span className="cell-sub">{formatWhen(r.created_at)}</span>
        </span>
      ),
    },
    {
      id: "actor",
      header: "Actor",
      sortValue: (r) => actorLabel(r),
      cell: (r) => (
        <span className="audit-actor">
          <span className="account-initial" aria-hidden="true">
            {actorLabel(r).slice(0, 1).toUpperCase()}
          </span>
          <span>
            {actorLabel(r)}
            {r.actor_kind && r.actor_kind !== "person" ? <span className="cell-sub">{r.actor_kind}</span> : null}
          </span>
        </span>
      ),
    },
    {
      id: "action",
      header: "Action",
      sortValue: (r) => r.action,
      cell: (r) => (
        <span>
          {auditActionLabel(r.action)}
          <span className="cell-sub cell-mono">{r.action}</span>
        </span>
      ),
    },
    {
      id: "resource",
      header: "Resource",
      sortValue: (r) => r.resource_name || r.resource_id || "",
      cell: (r) =>
        r.resource_name || r.resource_id ? (
          <span>
            {r.resource_name || r.resource_id}
            {r.resource_kind ? <span className="cell-sub">{r.resource_kind}</span> : null}
          </span>
        ) : (
          <span className="muted">None</span>
        ),
    },
    {
      id: "result",
      header: "Result",
      width: "8rem",
      sortValue: (r) => r.result,
      cell: (r) => <StatusBadge status={resultTone(r.result)} label={resultLabel(r.result)} />,
    },
    {
      id: "source",
      header: "Source",
      width: "9rem",
      sortValue: (r) => r.remote_addr || "",
      cell: (r) => <span className="cell-mono">{r.remote_addr || "Not recorded"}</span>,
    },
  ];

  const parsed = q.error && !q.data ? friendlyError(q.error) : null;
  const filtering = Boolean(query.trim() || result !== "all" || actor || range !== "all");

  return (
    <section className="page page-wide" aria-labelledby="audit-heading">
      <PageHeader
        id="audit-heading"
        icon="events"
        title="Audit Log"
        kicker="Who changed what, and when. Passwords, token values, and MFA secrets are never stored here."
        actions={
          <div className="btn-row is-flush">
            <button className="btn btn-ghost" type="button" onClick={() => void q.reload()}>
              <Icon name="restart" size={14} />
              Refresh
            </button>
            <button className="btn btn-secondary" type="button" disabled={filtered.length === 0} onClick={() => downloadCSV(filtered)}>
              Export CSV
            </button>
          </div>
        }
      />
      {parsed ? (
        <ErrorBanner
          title={parsed.title}
          secondary={parsed.secondary}
          detail={parsed.detail}
          action={
            <button className="btn btn-secondary" type="button" onClick={() => void q.reload()}>
              Retry
            </button>
          }
        />
      ) : null}
      {q.loading && !q.data ? <LoadingState label="Loading audit events" /> : null}
      {!q.loading && items.length === 0 && !parsed ? (
        <EmptyState icon="events" title="No audit events">
          Administrative actions will appear here.
        </EmptyState>
      ) : null}
      {items.length > 0 ? (
        <>
          <div className="summary-grid">
            <SummaryCard label="Events, last 24 hours" value={String(stats.day)} meta={`${items.length} loaded`} />
            <SummaryCard
              label="Denied, last 24 hours"
              value={String(stats.denied)}
              meta={stats.denied ? "Review denied actions" : "No denied actions"}
              tone={stats.denied ? "warn" : undefined}
              onClick={stats.denied ? () => {
                setResult("denied");
                setRange("24h");
              } : undefined}
            />
            <SummaryCard label="Active actors, last 24 hours" value={String(stats.actors)} meta="People, services and system" />
          </div>
          <div className="toolbar">
            <div className="toolbar-group">
              <label className="search-field">
                <Icon name="search" size={14} />
                <input
                  className="field-input"
                  type="search"
                  value={query}
                  onChange={(e) => setQuery(e.target.value)}
                  placeholder="Actor, action, resource or address"
                  aria-label="Search audit events"
                />
              </label>
              <Segmented<ResultFilter>
                ariaLabel="Result"
                value={result}
                onChange={setResult}
                options={[
                  { id: "all", label: "All" },
                  { id: "ok", label: "Succeeded" },
                  { id: "denied", label: "Denied" },
                  { id: "warning", label: "Other" },
                ]}
              />
            </div>
            <div className="toolbar-group">
              <label className="field-label">
                Actor
                <select className="field-input" value={actor} onChange={(e) => setActor(e.target.value)}>
                  <option value="">Everyone</option>
                  {actors.map((name) => (
                    <option key={name} value={name}>
                      {name}
                    </option>
                  ))}
                </select>
              </label>
              <label className="field-label">
                Time
                <select className="field-input" value={range} onChange={(e) => setRange(e.target.value as Range)}>
                  <option value="1h">Last hour</option>
                  <option value="24h">Last 24 hours</option>
                  <option value="7d">Last 7 days</option>
                  <option value="all">All loaded</option>
                </select>
              </label>
              <label className="field-label">
                Load
                <select className="field-input" value={limit} onChange={(e) => setLimit(Number(e.target.value))}>
                  <option value={200}>Newest 200</option>
                  <option value={500}>Newest 500</option>
                  <option value={2000}>Newest 2000</option>
                </select>
              </label>
              {filtering ? (
                <button
                  className="btn btn-ghost btn-sm"
                  type="button"
                  onClick={() => {
                    setQuery("");
                    setResult("all");
                    setActor("");
                    setRange("all");
                  }}
                >
                  Clear filters
                </button>
              ) : null}
            </div>
          </div>
          <DataTable
            label="Audit events"
            columns={columns}
            rows={filtered}
            rowKey={(r) => r.id}
            initialSort={{ id: "time", dir: "desc" }}
            onRowClick={(r) => setOpen(r)}
            selectedKey={open?.id ?? null}
            pageSize={50}
            empty={<EmptyState title="No matching events">Try a different search, result, actor or time range.</EmptyState>}
          />
        </>
      ) : null}
      <Dialog open={Boolean(open)} drawer title={open ? auditActionLabel(open.action) : "Audit event"} onClose={() => setOpen(null)}>
        {open ? (
          <div className="stack">
            <StatusBadge status={resultTone(open.result)} label={resultLabel(open.result)} />
            <dl className="review-grid">
              <dt>Time</dt>
              <dd>{formatWhen(open.created_at)}</dd>
              <dt>Actor</dt>
              <dd>{actorLabel(open)}</dd>
              <dt>Action</dt>
              <dd>
                <code>{open.action}</code>
              </dd>
              {open.resource_name || open.resource_id ? (
                <>
                  <dt>Resource</dt>
                  <dd>
                    {open.resource_name || open.resource_id}
                    {open.resource_kind ? ` (${open.resource_kind})` : ""}
                  </dd>
                </>
              ) : null}
              {open.resource_id && open.resource_name ? (
                <>
                  <dt>Resource id</dt>
                  <dd>
                    <code>{open.resource_id}</code>
                  </dd>
                </>
              ) : null}
              <dt>Source</dt>
              <dd>{open.remote_addr || "Not recorded"}</dd>
              <dt>Event id</dt>
              <dd>
                <code>{open.id}</code>
              </dd>
            </dl>
            <div className="btn-row">
              <button
                className="btn btn-sm btn-secondary"
                type="button"
                onClick={() => {
                  setActor(actorLabel(open));
                  setOpen(null);
                }}
              >
                Show this actor's events
              </button>
              <button
                className="btn btn-sm btn-secondary"
                type="button"
                onClick={() => {
                  setQuery(open.action);
                  setOpen(null);
                }}
              >
                Show this action
              </button>
            </div>
            {open.detail && Object.keys(open.detail).length > 0 ? (
              <>
                <h3>Detail</h3>
                <pre className="activity-detail-raw">{JSON.stringify(open.detail, null, 2)}</pre>
              </>
            ) : null}
          </div>
        ) : null}
      </Dialog>
    </section>
  );
}
