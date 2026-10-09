import { useMemo, useState } from "react";
import { ApiError, clearActivityLog, listAudit, listTasks } from "../api/client";
import type { TaskItem } from "../api/phase2";
import { ConfirmDialog } from "../components/ConfirmDialog";
import { EmptyState, LoadingState } from "../components/EmptyState";
import { ErrorNotice } from "../components/ErrorNotice";
import { Icon } from "../components/Icon";
import { PageHeader } from "../components/PageHeader";
import { StatusBadge } from "../components/StatusBadge";
import { formatWhen } from "../format";
import { auditActionLabel, humanTaskMessage, taskIntentTitle, taskStageFriendly } from "../humanize";
import { useQuery } from "../query";
import { isAdmin } from "../rbac";
import { useSession } from "../session";
import { DataTable, type Column } from "../ui/DataTable";
import { Dialog } from "../ui/Dialog";
import { ErrorBanner } from "../ui/ErrorBanner";
import { RelativeTime } from "../ui/RelativeTime";
import { Segmented } from "../ui/Segmented";
import { SummaryCard } from "../ui/SummaryCard";

// The Audit Log shows two feeds as one list: audit events (who changed
// what) and tasks (what No-dal did, including work still running).
type AuditRow = {
  id: string;
  /** audit or task. */
  source?: "audit" | "task";
  /** Display title for the action column. */
  label?: string;
  stage?: string;
  message?: string;
  progress?: number;
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
type SourceFilter = "all" | "audit" | "task";

function taskRow(t: TaskItem): AuditRow {
  const state = (t.state || "").toLowerCase();
  const result = state === "succeeded" || state === "completed" ? "ok" : state;
  return {
    id: `task:${t.id}`,
    source: "task",
    label: taskIntentTitle(t),
    action: t.kind,
    result,
    created_at: t.updated_at || t.created_at || "",
    actor_kind: "system",
    actor_label: "No-dal",
    resource_name: t.resource_name,
    stage: t.stage,
    message: humanTaskMessage(t.message),
    progress: t.progress,
  };
}

function rowLabel(r: AuditRow): string {
  return r.label || auditActionLabel(r.action);
}

function daysAgoInput(days: number): string {
  const d = new Date(Date.now() - days * 86_400_000);
  return d.toISOString().slice(0, 10);
}
type Range = "1h" | "24h" | "7d" | "all";

const RANGE_MS: Record<Range, number> = { "1h": 3_600_000, "24h": 86_400_000, "7d": 604_800_000, all: 0 };

function actorLabel(row: AuditRow): string {
  return row.actor_label || row.actor_username || (row.actor_kind === "system" ? "System" : "Unknown");
}

function resultTone(result: string): string {
  if (result === "ok") {
    return "ok";
  }
  if (result === "running") {
    return "running";
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
  if (result === "running") {
    return "Running";
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
  const head = ["time", "kind", "actor", "action", "resource", "result", "source", "id"];
  const lines = rows.map((r) =>
    [r.created_at, r.source || "audit", actorLabel(r), r.action, r.resource_name || r.resource_id || "", r.result, r.remote_addr || "", r.id]
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

export function AuditPage({ initialSource = "all" }: { initialSource?: SourceFilter }) {
  const session = useSession();
  const admin = isAdmin(session.status === "ready" ? session.user?.roles : undefined);
  const [limit, setLimit] = useState(200);
  const q = useQuery(`audit:${limit}`, () => listAudit(limit), 30000);
  const tq = useQuery(`tasks:${limit}`, () => listTasks(limit), 10000);
  // Operators can read tasks but not audit events; the page then shows tasks.
  const auditForbidden = Boolean(q.error && !q.data && /forbidden|403/i.test(q.error));
  const [source, setSource] = useState<SourceFilter>(initialSource);
  const items = useMemo(() => {
    const audit = auditForbidden ? [] : ((q.data?.items ?? []) as AuditRow[]).map((r) => ({ ...r, source: "audit" as const }));
    const tasks = (tq.data ?? []).map(taskRow);
    if (source === "audit") {
      return audit;
    }
    if (source === "task") {
      return tasks;
    }
    return [...audit, ...tasks];
  }, [q.data, tq.data, source, auditForbidden]);
  const [clearing, setClearing] = useState(false);
  const [clearAll, setClearAll] = useState(false);
  const [clearBefore, setClearBefore] = useState(daysAgoInput(30));
  const [clearAudit, setClearAudit] = useState(true);
  const [clearTasks, setClearTasks] = useState(true);
  const [notice, setNotice] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);

  async function onClear() {
    setClearing(false);
    setActionError(null);
    try {
      const res = await clearActivityLog({
        all: clearAll,
        before: clearAll ? undefined : new Date(`${clearBefore}T00:00:00`).toISOString(),
        audit: clearAudit,
        tasks: clearTasks,
      });
      setNotice(`Cleared ${res.audit_deleted ?? 0} audit event(s) and ${res.tasks_deleted ?? 0} task(s).`);
      await Promise.all([q.reload(), tq.reload()]);
    } catch (err) {
      setActionError(err instanceof ApiError ? err.message : err instanceof Error ? err.message : "Clear failed");
    }
  }
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
      const bad = item.result === "denied" || item.result === "failed" || item.result === "error";
      if (result !== "all" && (result === "ok" ? item.result !== "ok" : result === "denied" ? !bad : item.result === "ok" || bad)) {
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
      const hay = [actorLabel(item), rowLabel(item), item.action, item.resource_name, item.resource_id, item.result, item.remote_addr, item.message, item.stage]
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
          {rowLabel(r)}
          {r.source === "task" ? <span className="tag-inline">Task</span> : null}
          <span className="cell-sub">
            {r.source === "task" ? [taskStageFriendly(r.stage), r.message].filter(Boolean).join(" · ") || r.action : r.action}
          </span>
          {r.source === "task" && r.result === "running" && r.progress && r.progress > 0 && r.progress < 100 ? (
            <span className="progress">
              <span className="progress-track">
                <span className="progress-fill" style={{ width: `${r.progress}%` }} />
              </span>
              {r.progress}%
            </span>
          ) : null}
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

  const parsed = q.error && !q.data && !auditForbidden ? friendlyError(q.error) : null;
  const filtering = Boolean(query.trim() || result !== "all" || actor || range !== "all");
  const loading = (q.loading && !q.data) || (tq.loading && !tq.data);

  return (
    <section className="page page-wide" aria-labelledby="audit-heading">
      <PageHeader
        id="audit-heading"
        icon="events"
        title="Audit Log"
        kicker="Who changed what, and what No-dal did, and when. Tasks still running are listed here too. Passwords, token values and MFA secrets are never stored."
        actions={
          <div className="btn-row is-flush">
            <button
              className="btn btn-ghost"
              type="button"
              onClick={() => {
                void q.reload();
                void tq.reload();
              }}
            >
              <Icon name="restart" size={14} />
              Refresh
            </button>
            {admin ? (
              <button className="btn btn-ghost btn-danger-text" type="button" onClick={() => setClearing(true)}>
                Clear
              </button>
            ) : null}
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
      {actionError ? <ErrorNotice error={actionError} /> : null}
      {notice ? (
        <p className="banner banner-ok" role="status">
          {notice}
        </p>
      ) : null}
      {loading ? <LoadingState label="Loading the log" /> : null}
      {!loading && items.length === 0 && !parsed ? (
        <EmptyState icon="events" title="Nothing logged">
          Administrative actions and No-dal tasks will appear here.
        </EmptyState>
      ) : null}
      {items.length > 0 || source !== "all" ? (
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
              <Segmented<SourceFilter>
                ariaLabel="Source"
                value={source}
                onChange={setSource}
                options={[
                  { id: "all", label: "Everything" },
                  ...(auditForbidden ? [] : [{ id: "audit" as const, label: "Audit" }]),
                  { id: "task", label: "Tasks" },
                ]}
              />
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
                  { id: "denied", label: "Denied or failed" },
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
            label="Audit events and tasks"
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
      <ConfirmDialog
        open={clearing}
        title="Clear the log"
        confirmLabel="Clear"
        danger
        confirmDisabled={(!clearAudit && !clearTasks) || (!clearAll && !clearBefore)}
        onClose={() => setClearing(false)}
        onConfirm={() => void onClear()}
      >
        <p>Deleted entries cannot be brought back. Running tasks are never removed. The clear itself is recorded as a new audit event.</p>
        <fieldset className="stack">
          <label className="check-row">
            <input type="radio" name="clear-range" checked={!clearAll} onChange={() => setClearAll(false)} />
            <span>Entries older than</span>
            <input
              className="field-input"
              type="date"
              aria-label="Clear entries older than"
              value={clearBefore}
              max={daysAgoInput(0)}
              disabled={clearAll}
              onChange={(e) => setClearBefore(e.target.value)}
            />
          </label>
          <label className="check-row">
            <input type="radio" name="clear-range" checked={clearAll} onChange={() => setClearAll(true)} />
            <span>Everything</span>
          </label>
        </fieldset>
        <fieldset className="stack">
          <label className="check-row">
            <input type="checkbox" checked={clearAudit} onChange={(e) => setClearAudit(e.target.checked)} />
            <span>Audit events</span>
          </label>
          <label className="check-row">
            <input type="checkbox" checked={clearTasks} onChange={(e) => setClearTasks(e.target.checked)} />
            <span>Finished tasks</span>
          </label>
        </fieldset>
      </ConfirmDialog>
      <Dialog open={Boolean(open)} drawer title={open ? rowLabel(open) : "Audit event"} onClose={() => setOpen(null)}>
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
              {open.source === "task" ? (
                <>
                  <dt>Stage</dt>
                  <dd>{taskStageFriendly(open.stage) || "Not reported"}</dd>
                  <dt>Message</dt>
                  <dd>{open.message || "None"}</dd>
                </>
              ) : null}
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
