import { useCallback, useEffect, useState } from "react";
import { ApiError, listNotifications, markNotificationsRead, type AppNotification } from "../api/client";
import { LoadingState } from "../components/EmptyState";
import { ErrorNotice } from "../components/ErrorNotice";
import { PageHeader } from "../components/PageHeader";
import { ApprovalButtons, isApprovalAsk } from "../components/shell/ApprovalButtons";
import { formatWhen } from "../format";
import { invalidateQueries } from "../query";
import { navigate } from "../router";

/** NotificationsPage lists every notification kept for you (the newest 500). */
export function NotificationsPage() {
  const [items, setItems] = useState<AppNotification[] | null>(null);
  const [unread, setUnread] = useState(0);
  const [unreadOnly, setUnreadOnly] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    try {
      const res = await listNotifications({ unread: unreadOnly, limit: 500 });
      setItems(res.items);
      setUnread(res.unread);
      setError(null);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Notifications are unavailable");
    }
  }, [unreadOnly]);

  useEffect(() => {
    void load();
  }, [load]);

  async function markRead(ids: string[]) {
    try {
      await markNotificationsRead(ids);
      invalidateQueries("notifications-unread");
      await load();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Could not mark notifications read");
    }
  }

  return (
    <section className="page" aria-labelledby="notifications-heading">
      <PageHeader
        id="notifications-heading"
        title="Notifications"
        icon="bell"
        kicker="Approvals, security changes on your account and automations that were turned off."
        actions={
          <button className="btn btn-sm btn-secondary" type="button" disabled={unread === 0} onClick={() => void markRead([])}>
            Mark all read
          </button>
        }
      />
      {error ? <ErrorNotice error={error} /> : null}
      <label className="check-row">
        <input type="checkbox" checked={unreadOnly} onChange={(e) => setUnreadOnly(e.target.checked)} />
        <span>Unread only ({unread})</span>
      </label>
      {!items && !error ? <LoadingState label="Loading notifications" /> : null}
      {items && items.length === 0 ? <p className="muted">No notifications.</p> : null}
      {items && items.length > 0 ? (
        <ul className="bell-list notifications-list">
          {items.map((n) => (
            <li key={n.id} className={n.read ? undefined : "is-unread"}>
              <button
                type="button"
                className="bell-item"
                onClick={() => {
                  if (!n.read) void markRead([n.id]);
                  if (n.link) navigate(n.link);
                }}
              >
                <span className="bell-title">{n.title}</span>
                {n.body ? <span className="bell-body">{n.body}</span> : null}
                <span className="muted bell-when">{formatWhen(n.created_at)}</span>
              </button>
              <ApprovalButtons
                n={n}
                onDecided={(id, state) => {
                  setItems((cur) => (cur ?? []).map((x) => (x.id === id ? { ...x, approval: state, read: true } : x)));
                  setUnread((u) => Math.max(0, u - (n.read ? 0 : 1)));
                  invalidateQueries("notifications-unread");
                }}
              />
              {!n.read && !isApprovalAsk(n) ? (
                <button className="btn btn-ghost btn-sm" type="button" onClick={() => void markRead([n.id])}>
                  Mark read
                </button>
              ) : null}
            </li>
          ))}
        </ul>
      ) : null}
    </section>
  );
}
