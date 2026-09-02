import { listTasks } from "../api/client";
import { ErrorState, LoadingState } from "../components/EmptyState";
import { PageHeader } from "../components/PageHeader";
import { ResourceTable } from "../components/ResourceTable";
import { StatusBadge } from "../components/StatusBadge";
import { formatWhen } from "../format";
import { taskKindLabel } from "../labels";
import { useQuery } from "../query";

export function TasksPage() {
  const { data, error, loading } = useQuery("tasks-page", () => listTasks(), 5000);

  return (
    <section className="page" aria-labelledby="tasks-heading">
      <PageHeader id="tasks-heading" title="Tasks" kicker="Operations reported by the control plane." />
      {error ? <ErrorState>{error}</ErrorState> : null}
      <article className="panel">
        {loading && !data ? (
          <LoadingState />
        ) : (
          <ResourceTable
            headers={["Kind", "State", "Stage", "Progress", "Message", "Updated"]}
            empty={<p>No tasks yet.</p>}
            rows={(data ?? []).map((item) => [
              taskKindLabel(item.kind),
              <StatusBadge key={item.id} status={item.state} />,
              item.stage || "Not reported",
              item.progress == null ? "Not reported" : `${item.progress}%`,
              item.message || "Not reported",
              formatWhen(item.updated_at),
            ])}
          />
        )}
      </article>
    </section>
  );
}
