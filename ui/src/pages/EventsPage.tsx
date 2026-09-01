import { useEffect, useState } from "react";
import { listEvents } from "../api/client";
import type { EventItem } from "../api/phase2";
import { formatWhen } from "../format";

export function EventsPage() {
  const [items, setItems] = useState<EventItem[] | null>(null);

  useEffect(() => {
    let cancelled = false;
    void listEvents()
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
    const stream = new EventSource("/api/v1/events/stream", { withCredentials: true });
    stream.onmessage = (msg) => {
      try {
        const item = JSON.parse(msg.data) as EventItem;
        setItems((cur) => {
          const list = cur ?? [];
          if (list.some((e) => e.id === item.id)) {
            return list;
          }
          return [item, ...list].slice(0, 50);
        });
      } catch {
        // Ignore a malformed stream frame.
      }
    };
    return () => {
      cancelled = true;
      stream.close();
    };
  }, []);

  return (
    <section className="page page-wide" aria-labelledby="events-heading">
      <header className="page-header">
        <h1 id="events-heading">Events</h1>
        <p className="page-kicker">Platform events. Audit history is a separate record.</p>
      </header>
      <article className="panel">
        {items == null ? (
          <p>Collecting</p>
        ) : items.length === 0 ? (
          <p>No events yet.</p>
        ) : (
          <ul className="plain-list">
            {items.map((item) => (
              <li key={item.id}>
                <strong>{item.type}</strong>{" "}
                <span className="muted">{formatWhen(item.created_at)}</span>
              </li>
            ))}
          </ul>
        )}
      </article>
    </section>
  );
}
