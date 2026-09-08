import { useEffect, useMemo, useState } from "react";
import { getTimeline, listEvents } from "../api/client";
import type { EventItem } from "../api/phase2";
import { ActivityDetail, fieldsFromRecord } from "../components/ActivityDetail";
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
  const [openTimeline, setOpenTimeline] = useState<string | null>(null);
  const [openEvent, setOpenEvent] = useState<string | null>(null);

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
            {timeline.map((item) => {
              const key = item.kind + item.id;
              return (
                <li key={key} className={openTimeline === key ? "is-open" : undefined}>
                  <button type="button" className="activity-toggle" onClick={() => setOpenTimeline(openTimeline === key ? null : key)}>
                    <span>
                      <strong>{item.kind}</strong> {item.title}
                      {item.result ? ` ${item.result}` : ""}
                      {item.state ? ` ${item.state}` : ""}
                    </span>
                    <span className="muted">{formatWhen(item.created_at)}</span>
                  </button>
                  {openTimeline === key ? (
                    <ActivityDetail
                      title={`${item.kind} ${item.title}`.trim()}
                      fields={fieldsFromRecord(item as unknown as Record<string, unknown>, [
                        { label: "id", value: item.id },
                        { label: "kind", value: item.kind },
                        { label: "created", value: item.created_at },
                      ])}
                      raw={item}
                      onClose={() => setOpenTimeline(null)}
                    />
                  ) : null}
                </li>
              );
            })}
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
          <>
            <ResourceTable
              headers={["Event", "Detail", "When"]}
              empty={<p>No events yet.</p>}
              selected={filtered.findIndex((item) => item.id === openEvent)}
              onRowClick={(index) => {
                const item = filtered[index];
                setOpenEvent(item && openEvent === item.id ? null : item?.id ?? null);
              }}
              rows={filtered.map((item) => {
                const facts = payloadFacts(item.payload);
                return [
                  eventHeadline(item.type, item.payload),
                  facts.length ? facts.map((f) => `${f.label} ${f.value}`).join(" · ") : "No extra detail",
                  formatWhen(item.created_at),
                ];
              })}
            />
            {openEvent
              ? (() => {
                  const item = filtered.find((row) => row.id === openEvent);
                  if (!item) {
                    return null;
                  }
                  return (
                    <ActivityDetail
                      title={eventHeadline(item.type, item.payload)}
                      fields={fieldsFromRecord(item.payload, [
                        { label: "event id", value: item.id },
                        { label: "type", value: item.type },
                        { label: "created", value: item.created_at },
                        ...(item.node_id ? [{ label: "node id", value: item.node_id }] : []),
                      ])}
                      raw={item}
                      onClose={() => setOpenEvent(null)}
                    />
                  );
                })()
              : null}
          </>
        )}
      </div>
    </section>
  );
}
