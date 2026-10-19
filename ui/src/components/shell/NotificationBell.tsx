import { useEffect, useRef, useState } from "react";
import { getUnreadNotifications, listNotifications, markNotificationsRead, type AppNotification } from "../../api/client";
import { formatWhen } from "../../format";
import { useQuery } from "../../query";
import { navigate } from "../../router";
import { Icon } from "../Icon";
import { Link } from "../Link";

/** The badge text for an unread count: 1 to 99, then 99+. */
export function badgeText(n: number): string {
  if (n <= 0) {
    return "";
  }
  return n > 99 ? "99+" : String(n);
}

/**
 * NotificationBell sits top right in the header: a red badge with the
 * unread count, and a dropdown of the newest notifications.
 */
export function NotificationBell() {
  const unread = useQuery("notifications-unread", () => getUnreadNotifications(), 30000);
  const [open, setOpen] = useState(false);
  const [items, setItems] = useState<AppNotification[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const root = useRef<HTMLDivElement>(null);
  const count = unread.data?.unread ?? 0;
  const label = count > 0 ? `${count} unread notifications` : "Notifications";

  useEffect(() => {
    if (!open) {
      return;
    }
    let live = true;
    listNotifications({ limit: 8 })
      .then((res) => {
        if (live) {
          setItems(res.items);
          setError(null);
        }
      })
      .catch(() => live && setError("Notifications are unavailable"));
    function onDoc(event: MouseEvent) {
      if (root.current && !root.current.contains(event.target as Node)) {
        setOpen(false);
      }
    }
    function onKey(event: KeyboardEvent) {
      if (event.key === "Escape") {
        setOpen(false);
      }
    }
    document.addEventListener("mousedown", onDoc);
    document.addEventListener("keydown", onKey);
    return () => {
      live = false;
      document.removeEventListener("mousedown", onDoc);
      document.removeEventListener("keydown", onKey);
    };
  }, [open]);

  async function markRead(ids: string[]) {
    try {
      await markNotificationsRead(ids);
      setItems((cur) => (cur ?? []).map((n) => (ids.length === 0 || ids.includes(n.id) ? { ...n, read: true } : n)));
      await unread.reload();
    } catch {
      setError("Could not mark notifications read");
    }
  }

  function openItem(n: AppNotification) {
    if (!n.read) {
      void markRead([n.id]);
    }
    setOpen(false);
    if (n.link) {
      navigate(n.link);
    }
  }

  return (
    <div className="menu" ref={root}>
      <button
        className="activity-chip bell-chip"
        type="button"
        aria-expanded={open}
        aria-haspopup="true"
        aria-label={label}
        title={label}
        onClick={() => setOpen((v) => !v)}
      >
        <Icon name="bell" size={15} />
        {count > 0 ? (
          <span className="bell-badge" aria-hidden="true">
            {badgeText(count)}
          </span>
        ) : null}
      </button>
      {open ? (
        <div className="task-panel bell-panel" role="dialog" aria-label="Notifications">
          <div className="bell-head">
            <strong>Notifications</strong>
            <button className="btn btn-ghost btn-sm" type="button" disabled={count === 0} onClick={() => void markRead([])}>
              Mark all read
            </button>
          </div>
          {error ? <p className="muted">{error}</p> : null}
          {items === null && !error ? <p className="muted">Loading…</p> : null}
          {items && items.length === 0 ? <p className="muted">No notifications yet.</p> : null}
          {items && items.length > 0 ? (
            <ul className="bell-list">
              {items.map((n) => (
                <li key={n.id} className={n.read ? undefined : "is-unread"}>
                  <button type="button" className="bell-item" onClick={() => openItem(n)}>
                    <span className="bell-title">{n.title}</span>
                    {n.body ? <span className="bell-body">{n.body}</span> : null}
                    <span className="muted bell-when">{formatWhen(n.created_at)}</span>
                  </button>
                  {!n.read ? (
                    <button className="btn btn-ghost btn-sm btn-icon" type="button" aria-label="Mark read" title="Mark read" onClick={() => void markRead([n.id])}>
                      <Icon name="check" size={14} />
                    </button>
                  ) : null}
                </li>
              ))}
            </ul>
          ) : null}
          <Link href="/notifications" onClick={() => setOpen(false)}>
            View all
          </Link>
        </div>
      ) : null}
    </div>
  );
}
