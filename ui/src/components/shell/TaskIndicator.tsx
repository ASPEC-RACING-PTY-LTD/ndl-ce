import { useState } from "react";
import { listTasks } from "../../api/client";
import { formatWhen } from "../../format";
import { taskKindLabel } from "../../labels";
import { useQuery } from "../../query";
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
  const items = data ?? [];
  const running = items.filter((t) => isActive(t.state));
  const failed = items.find((t) => isFailed(t.state));
  const label =
    running.length > 0 ? `${running.length} running` : failed ? "Task failed" : "No running tasks";

  return (
    <div className="menu">
      <button
        className="btn btn-ghost btn-sm"
        type="button"
        aria-expanded={open}
        aria-haspopup="true"
        onClick={() => setOpen((v) => !v)}
      >
        {label}
      </button>
      {open ? (
        <div className="task-panel" role="dialog" aria-label="Recent tasks">
          {items.length === 0 ? (
            <p>No tasks yet.</p>
          ) : (
            <ul className="plain-list">
              {items.slice(0, 6).map((task) => (
                <li key={task.id}>
                  <StatusBadge status={task.state} /> {taskKindLabel(task.kind)}
                  {task.message ? ` ${task.message}` : ""}
                  <span className="muted"> {formatWhen(task.updated_at)}</span>
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
