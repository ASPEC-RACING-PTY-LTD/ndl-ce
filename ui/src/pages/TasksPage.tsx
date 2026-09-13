import { useMemo, useState } from "react";
import { listTasks } from "../api/client";
import { ActivityDetail, fieldsFromRecord } from "../components/ActivityDetail";
import { ErrorState, LoadingState } from "../components/EmptyState";
import { PageHeader } from "../components/PageHeader";
import { StatusBadge } from "../components/StatusBadge";
import { humanTaskMessage, taskIntentTitle, taskStageFriendly } from "../humanize";
import { useQuery } from "../query";
import { Segmented } from "../ui/Segmented";
import { RelativeTime } from "../ui/RelativeTime";

type Filter = "all" | "running" | "failed" | "completed";

export function TasksPage() {
  const { data, error, loading } = useQuery("tasks-page", () => listTasks(), 5000);
  const [openId, setOpenId] = useState<string | null>(null);
  const [filter, setFilter] = useState<Filter>("all");
  const items = useMemo(() => data ?? [], [data]);
  const selected = items.find((item) => item.id === openId) ?? null;
  const filtered = useMemo(() => {
    return items.filter((item) => {
      if (filter === "running") {
        return item.state === "running";
      }
      if (filter === "failed") {
        return item.state === "failed";
      }
      if (filter === "completed") {
        return item.state === "succeeded" || item.state === "completed";
      }
      return true;
    });
  }, [filter, items]);

  return (
    <section className="page" aria-labelledby="tasks-heading">
      <PageHeader
        id="tasks-heading"
        title="Tasks"
        kicker="What No-DAL is doing right now, in operator language."
        actions={
          <Segmented
            ariaLabel="Task status"
            value={filter}
            onChange={setFilter}
            options={[
              { id: "all", label: "All" },
              { id: "running", label: "Running" },
              { id: "failed", label: "Failed" },
              { id: "completed", label: "Completed" },
            ]}
          />
        }
      />
      {error ? <ErrorState>{error}</ErrorState> : null}
      {loading && !data ? (
        <LoadingState />
      ) : filtered.length === 0 ? (
        <p className="lede">No tasks in this filter.</p>
      ) : (
        <div className="stack">
          {filtered.map((item) => {
            const open = openId === item.id;
            const showProgress =
              item.state === "running" && item.progress != null && item.progress > 0 && item.progress < 100;
            return (
              <div key={item.id}>
                <button
                  type="button"
                  className={"activity-row" + (item.state === "running" ? " is-running" : "")}
                  onClick={() => setOpenId(open ? null : item.id)}
                  aria-expanded={open}
                >
                  <span>
                    <span className="activity-title">{taskIntentTitle(item)}</span>
                    <span className="activity-meta">
                      {" "}
                      {taskStageFriendly(item.stage)}
                      {humanTaskMessage(item.message) ? ` · ${humanTaskMessage(item.message)}` : ""}
                    </span>
                  </span>
                  <span className="activity-meta">
                    <StatusBadge status={item.state} /> <RelativeTime value={item.updated_at || item.created_at} />
                  </span>
                </button>
                {showProgress ? (
                  <span className="progress">
                    <span className="progress-track">
                      <span className="progress-fill" style={{ width: `${item.progress}%` }} />
                    </span>
                    {item.progress}%
                  </span>
                ) : null}
              </div>
            );
          })}
        </div>
      )}
      {selected ? (
        <ActivityDetail
          title={taskIntentTitle(selected)}
          fields={fieldsFromRecord(selected as unknown as Record<string, unknown>, [
            { label: "Friendly title", value: taskIntentTitle(selected) },
            { label: "Status", value: selected.state },
            { label: "Stage", value: selected.stage || "Not reported" },
            { label: "Created", value: selected.created_at || "Not reported" },
            { label: "Updated", value: selected.updated_at || "Not reported" },
            { label: "Error", value: humanTaskMessage(selected.message) || selected.message || "None" },
          ])}
          raw={selected}
          onClose={() => setOpenId(null)}
        />
      ) : null}
    </section>
  );
}
