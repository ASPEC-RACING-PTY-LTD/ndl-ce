import { useEffect, useMemo, useState } from "react";
import { listEvents } from "../api/client";
import type { EventItem } from "../api/phase2";
import { ErrorState, LoadingState } from "../components/EmptyState";
import { PageHeader } from "../components/PageHeader";
import { ResourceTable } from "../components/ResourceTable";
import { formatWhen } from "../format";
import { eventTypeLabel } from "../labels";

export function EventsPage() {
  const [items, setItems] = useState<EventItem[] | null>(null);
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
    return list.filter((item) => eventTypeLabel(item.type).toLowerCase().includes(q));
  }, [filter, items]);

  return (
    <section className="page" aria-labelledby="events-heading">
      <PageHeader id="events-heading" title="Events" kicker="Platform events for this appliance." />
      {error ? <ErrorState>{error}</ErrorState> : null}
      <article className="panel stack">
        <input
          className="field-input"
          type="search"
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
          placeholder="Filter by type"
          aria-label="Filter events"
        />
        {items == null ? (
          <LoadingState />
        ) : (
          <ResourceTable
            headers={["Type", "When"]}
            empty={<p>No events yet.</p>}
            rows={filtered.map((item) => [eventTypeLabel(item.type), formatWhen(item.created_at)])}
          />
        )}
      </article>
    </section>
  );
}
