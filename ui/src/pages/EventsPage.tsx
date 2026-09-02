import { useEffect, useMemo, useState } from "react";
import { listEvents } from "../api/client";
import type { EventItem } from "../api/phase2";
import { ErrorState, LoadingState } from "../components/EmptyState";
import { Icon } from "../components/Icon";
import { PageHeader } from "../components/PageHeader";
import { ResourceTable } from "../components/ResourceTable";
import { formatWhen } from "../format";
import { eventHeadline, payloadFacts } from "../humanize";

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
    return list.filter((item) => eventHeadline(item.type, item.payload).toLowerCase().includes(q));
  }, [filter, items]);

  return (
    <section className="page" aria-labelledby="events-heading">
      <PageHeader id="events-heading" title="Events" kicker="What changed on this appliance" />
      {error ? <ErrorState>{error}</ErrorState> : null}
      <div className="stack">
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
