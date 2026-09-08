import { useState } from "react";
import { listTasks } from "../api/client";
import { ActivityDetail, fieldsFromRecord } from "../components/ActivityDetail";
import { ErrorState, LoadingState } from "../components/EmptyState";
import { PageHeader } from "../components/PageHeader";
import { ResourceTable } from "../components/ResourceTable";
import { StatusBadge } from "../components/StatusBadge";
import { formatWhen } from "../format";
import { humanTaskMessage, taskStageLabel } from "../humanize";
import { taskKindLabel } from "../labels";
import { useQuery } from "../query";

export function TasksPage() {
  const { data, error, loading } = useQuery("tasks-page", () => listTasks(), 5000);
  const [openId, setOpenId] = useState<string | null>(null);
  const items = data ?? [];
  const selected = items.find((item) => item.id === openId) ?? null;

  return (
    <section className="page" aria-labelledby="tasks-heading">
      <PageHeader id="tasks-heading" title="Tasks" kicker="In-flight and completed operations" />
      {error ? <ErrorState>{error}</ErrorState> : null}
      {loading && !data ? (
        <LoadingState />
      ) : (
        <ResourceTable
          headers={["Operation", "State", "Stage", "Progress", "Message", "Updated"]}
          numeric={[3]}
          empty={<p>No tasks yet.</p>}
          selected={items.findIndex((item) => item.id === openId)}
          onRowClick={(index) => {
            const item = items[index];
            setOpenId(item && openId === item.id ? null : item?.id ?? null);
          }}
          rows={items.map((item) => [
            taskKindLabel(item.kind),
            <StatusBadge key={item.id} status={item.state} />,
            taskStageLabel(item.stage),
            item.progress == null ? (
              "Not reported"
            ) : (
              <span className="progress" key="pg">
                <span className="progress-track">
                  <span className="progress-fill" style={{ width: `${item.progress}%` }} />
                </span>
                {item.progress}%
              </span>
            ),
            humanTaskMessage(item.message) || "Not reported",
            formatWhen(item.updated_at),
          ])}
        />
      )}
      {selected ? (
        <ActivityDetail
          title={taskKindLabel(selected.kind)}
          fields={fieldsFromRecord(selected as unknown as Record<string, unknown>, [
            { label: "job id", value: selected.id },
            { label: "stage", value: selected.stage || "Not reported" },
            { label: "status", value: selected.state },
            { label: "created", value: selected.created_at || "Not reported" },
            { label: "updated", value: selected.updated_at || "Not reported" },
            { label: "error", value: humanTaskMessage(selected.message) || selected.message || "None" },
          ])}
          raw={selected}
          onClose={() => setOpenId(null)}
        />
      ) : null}
    </section>
  );
}
