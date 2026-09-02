import { listTasks } from "../api/client";
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
          rows={(data ?? []).map((item) => [
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
    </section>
  );
}
