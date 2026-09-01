import { useEffect, useState } from "react";
import { listTasks } from "../api/client";
import type { TaskItem } from "../api/phase2";
import { formatWhen, honestStatus } from "../format";

export function TasksPage() {
  const [items, setItems] = useState<TaskItem[] | null>(null);

  useEffect(() => {
    let cancelled = false;
    void listTasks()
      .then((value) => {
        if (!cancelled) {
          setItems(value);
        }
      })
      .catch(() => {
        if (!cancelled) {
          setItems([]);
        }
      });
    return () => {
      cancelled = true;
    };
  }, []);

  return (
    <section className="page page-wide" aria-labelledby="tasks-heading">
      <header className="page-header">
        <h1 id="tasks-heading">Tasks</h1>
        <p className="page-kicker">Operations reported by the control plane. Progress is not invented.</p>
      </header>
      <article className="panel">
        {items == null ? (
          <p>Collecting</p>
        ) : items.length === 0 ? (
          <p>No tasks yet.</p>
        ) : (
          <div className="table-wrap">
            <table>
              <thead>
                <tr>
                  <th>Kind</th>
                  <th>State</th>
                  <th>Stage</th>
                  <th>Progress</th>
                  <th>Updated</th>
                </tr>
              </thead>
              <tbody>
                {items.map((item) => (
                  <tr key={item.id}>
                    <td>{item.kind}</td>
                    <td>{honestStatus(item.state)}</td>
                    <td>{item.stage || "Not reported"}</td>
                    <td>{item.progress == null ? "Not reported" : `${item.progress}%`}</td>
                    <td>{formatWhen(item.updated_at)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </article>
    </section>
  );
}
