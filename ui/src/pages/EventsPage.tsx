import { useEffect, useState } from "react";
import { getTimeline, listEvents } from "../api/client";
import type { EventItem } from "../api/phase2";
import { formatWhen } from "../format";

type TimelineItem = {
  kind: string;
  id: string;
  title: string;
  created_at: string;
  result?: string;
  state?: string;
  message?: string;
};

export function EventsPage() {
  const [items, setItems] = useState<EventItem[] | null>(null);
  const [timeline, setTimeline] = useState<TimelineItem[]>([]);

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
    void getTimeline()
      .then((body) => {
        if (!cancelled) {
          setTimeline(body.items ?? []);
        }
      })
      .catch(() => undefined);
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
        <p className="page-kicker">Platform events plus a change timeline from events, tasks, and audit.</p>
      </header>
      <article className="panel">
        <h2>What changed</h2>
        {timeline.length === 0 ? (
          <p>No timeline entries in this window.</p>
        ) : (
          <ul className="plain-list">
            {timeline.map((item) => (
              <li key={item.kind + item.id}>
                <strong>{item.kind}</strong> {item.title}{" "}
                <span className="muted">{formatWhen(item.created_at)}</span>
                {item.result ? ` ${item.result}` : ""}
                {item.state ? ` ${item.state}` : ""}
              </li>
            ))}
          </ul>
        )}
      </article>
      <article className="panel">
        <h2>Live events</h2>
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
