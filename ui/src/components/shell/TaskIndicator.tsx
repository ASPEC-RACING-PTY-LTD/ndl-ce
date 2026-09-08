import { useEffect, useRef, useState } from "react";
import { listTasks } from "../../api/client";
import { formatWhen } from "../../format";
import { humanTaskMessage, taskStageLabel } from "../../humanize";
import { taskKindLabel } from "../../labels";
import { useQuery } from "../../query";
import { ActivityDetail, fieldsFromRecord } from "../ActivityDetail";
import { Icon } from "../Icon";
import { Link } from "../Link";
import { StatusBadge } from "../StatusBadge";

function isActive(state?: string): boolean {
  return state === "running" || state === "pending" || state === "starting";
}

function isFailed(state?: string): boolean {
  return state === "failed";
}

export function TaskIndicator() {
  const { data } = useQuery("tasks", () => listTasks(), 5000);
  const [open, setOpen] = useState(false);
  const [detailId, setDetailId] = useState<string | null>(null);
  const root = useRef<HTMLDivElement>(null);
  const items = data ?? [];
  const running = items.filter((t) => isActive(t.state));
  const failed = items.find((t) => isFailed(t.state));
  const label =
    running.length > 0 ? `${running.length} running` : failed ? "Task failed" : "No running tasks";

  useEffect(() => {
    if (!open) {
      return;
    }
    function onDoc(event: MouseEvent) {
      if (root.current && !root.current.contains(event.target as Node)) {
        setOpen(false);
      }
    }
    function onKey(event: KeyboardEvent) {
      if (event.key === "Escape") {
        setOpen(false);
      }
    }
    document.addEventListener("mousedown", onDoc);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onDoc);
      document.removeEventListener("keydown", onKey);
    };
  }, [open]);

  return (
    <div className="menu" ref={root}>
      <button
        className="activity-chip"
        type="button"
        aria-expanded={open}
        aria-haspopup="true"
        aria-label={label}
        title={label}
        onClick={() => setOpen((v) => !v)}
      >
        <Icon name="activity" size={14} />
        {running.length > 0 ? (
          <span className="activity-count">{running.length}</span>
        ) : failed ? (
          <span className="activity-count is-bad">!</span>
        ) : (
          <span className="visually-hidden">{label}</span>
        )}
        <span className="visually-hidden">{label}</span>
      </button>
      {open ? (
        <div className="task-panel" role="dialog" aria-label="Recent tasks">
          {items.length === 0 ? (
            <p>No tasks yet.</p>
          ) : (
            <ul className="activity-list">
              {items.slice(0, 6).map((task) => (
                <li key={task.id} className={detailId === task.id ? "is-open" : undefined}>
                  <button type="button" className="activity-toggle" onClick={() => setDetailId(detailId === task.id ? null : task.id)}>
                    <StatusBadge status={task.state} />
                    <span>
                      {taskKindLabel(task.kind)}
                      {humanTaskMessage(task.message) ? ` ${humanTaskMessage(task.message)}` : ""}
                      {task.stage ? <span className="muted"> · {taskStageLabel(task.stage)}</span> : null}
                    </span>
                    <span className="muted">{formatWhen(task.updated_at)}</span>
                  </button>
                  {detailId === task.id ? (
                    <ActivityDetail
                      title={taskKindLabel(task.kind)}
                      fields={fieldsFromRecord(task as unknown as Record<string, unknown>, [
                        { label: "job id", value: task.id },
                        { label: "stage", value: task.stage || "Not reported" },
                        { label: "status", value: task.state },
                        { label: "updated", value: task.updated_at || "Not reported" },
                        { label: "error", value: humanTaskMessage(task.message) || task.message || "None" },
                      ])}
                      raw={task}
                      onClose={() => setDetailId(null)}
                    />
                  ) : null}
                </li>
              ))}
            </ul>
          )}
          <Link href="/tasks" onClick={() => setOpen(false)}>
            View all
          </Link>
        </div>
      ) : null}
    </div>
  );
}
