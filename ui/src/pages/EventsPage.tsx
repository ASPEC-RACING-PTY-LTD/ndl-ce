import { useEffect, useMemo, useState } from "react";
import { getTimeline, listEvents } from "../api/client";
import type { EventItem } from "../api/phase2";
import { ErrorState, LoadingState } from "../components/EmptyState";
import { Icon } from "../components/Icon";
import { PageHeader } from "../components/PageHeader";
import { ResourceTable } from "../components/ResourceTable";
import { formatWhen } from "../format";
import { eventHeadline, payloadFacts } from "../humanize";

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
  const [error, setError] = useState<string | null>(null);
  const [filter, setFilter] = useState("");

  useEffect(() => {
    let cancelled = false;
    void listEvents()
      .then((value) => {
        if (!cancelled) {
          setItems(value);
        }
      })
      .catch((err) => {
        if (!cancelled) {
          setError(err instanceof Error ? err.message : "Unavailable");
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
    if (typeof EventSource === "undefined") {
      return () => {
        cancelled = true;
      };
    }
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

  const filtered = useMemo(() => {
    const list = items ?? [];
    const q = filter.trim().toLowerCase();
    if (!q) {
      return list;
    }
    return list.filter((item) => eventHeadline(item.type, item.payload).toLowerCase().includes(q));
  }, [filter, items]);

  return (
    <section className="page" aria-labelledby="events-heading">
      <PageHeader
        id="events-heading"
        title="Events"
        kicker="Platform events plus a change timeline from events, tasks, and audit."
      />
      {error ? <ErrorState>{error}</ErrorState> : null}
      <section className="section">
        <h2>What changed</h2>
        {timeline.length === 0 ? (
          <p>No timeline entries in this window.</p>
        ) : (
          <ul className="activity-list">
            {timeline.map((item) => (
              <li key={item.kind + item.id}>
                <span>
                  <strong>{item.kind}</strong> {item.title}
                  {item.result ? ` ${item.result}` : ""}
                  {item.state ? ` ${item.state}` : ""}
                </span>
                <span className="muted">{formatWhen(item.created_at)}</span>
              </li>
            ))}
          </ul>
        )}
      </section>
      <div className="stack">
        <h2>Live events</h2>
        <label className="search-field">
          <Icon name="search" size={14} />
          <input
            className="field-input"
            type="search"
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            placeholder="Filter by type"
            aria-label="Filter events"
          />
        </label>
        {items == null ? (
          <LoadingState />
        ) : (
          <ResourceTable
            headers={["Event", "Detail", "When"]}
            empty={<p>No events yet.</p>}
            rows={filtered.map((item) => {
              const facts = payloadFacts(item.payload);
              return [
                eventHeadline(item.type, item.payload),
                facts.length ? facts.map((f) => `${f.label} ${f.value}`).join(" · ") : "No extra detail",
                formatWhen(item.created_at),
              ];
            })}
          />
        )}
      </div>
    </section>
  );
}
