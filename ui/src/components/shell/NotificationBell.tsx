import { useEffect, useRef, useState } from "react";
import { getUnreadNotifications, listNotifications, listTasks, markNotificationsRead, type AppNotification } from "../../api/client";
import { useNotifyDisplay } from "../../appearance/state";
import { formatWhen } from "../../format";
import { humanTaskMessage, taskStageLabel } from "../../humanize";
import { taskKindLabel } from "../../labels";
import { useQuery } from "../../query";
import { navigate } from "../../router";
import { Icon } from "../Icon";
import { Link } from "../Link";
import { StatusBadge } from "../StatusBadge";

/** The badge text for an unread count: 1 to 99, then 99+. */
export function badgeText(n: number): string {
  if (n <= 0) {
    return "";
  }
  return n > 99 ? "99+" : String(n);
}

function isActive(state?: string): boolean {
  return state === "running" || state === "pending" || state === "starting";
}

/** isRecentFailure reports a task that failed within the last hour. */
export function isRecentFailure(task: { state?: string; updated_at?: string }, now = Date.now()): boolean {
  if (task.state !== "failed" || !task.updated_at) {
    return false;
  }
  const t = Date.parse(task.updated_at);
  return !Number.isNaN(t) && now - t < 60 * 60 * 1000;
}

/** newSince picks notifications created after since, oldest first, for toasts. */
export function newSince(items: AppNotification[], since: string): AppNotification[] {
  const t = Date.parse(since);
  return items.filter((n) => !n.read && Date.parse(n.created_at) > t).sort((a, b) => Date.parse(a.created_at) - Date.parse(b.created_at));
}

type Toast = { n: AppNotification; leaving: boolean };

/**
 * NotificationBell is the one header button for notifications and tasks.
 * It shows the unread count (and a ring while tasks run), opens a panel
 * sized to its content and the window, and pops new notifications down
 * from the top, as each person chose on the Theme page.
 */
export function NotificationBell() {
  const [display] = useNotifyDisplay();
  const unread = useQuery("notifications-unread", () => getUnreadNotifications(), 15000);
  const tasks = useQuery("tasks", () => listTasks(), 5000);
  const [open, setOpen] = useState(false);
  const [tab, setTab] = useState<"notifications" | "tasks">("notifications");
  const [items, setItems] = useState<AppNotification[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [toasts, setToasts] = useState<Toast[]>([]);
  const root = useRef<HTMLDivElement>(null);
  const seen = useRef<string>(new Date().toISOString());
  const lastCount = useRef<number | null>(null);
  const count = unread.data?.unread ?? 0;
  const taskList = tasks.data ?? [];
  const running = taskList.filter((t) => isActive(t.state));
  const failed = taskList.find((t) => isRecentFailure(t));
  const label =
    (count > 0 ? `${count} unread notifications` : "Notifications") + (running.length > 0 ? `, ${running.length} tasks running` : "");

  // New notifications become toasts. The first count read only sets the
  // starting point, so signing in does not replay old ones.
  useEffect(() => {
    if (unread.data === undefined) {
      return;
    }
    const prev = lastCount.current;
    lastCount.current = count;
    if (prev === null || count <= prev || !display.toast) {
      if (!display.toast) {
        seen.current = new Date().toISOString();
      }
      return;
    }
    let live = true;
    listNotifications({ limit: 5, unread: true })
      .then((res) => {
        if (!live) {
          return;
        }
        const fresh = newSince(res.items, seen.current);
        if (fresh.length) {
          seen.current = fresh[fresh.length - 1].created_at;
          setToasts((cur) => [...cur, ...fresh.slice(-3).map((n) => ({ n, leaving: false }))].slice(-4));
        }
      })
      .catch(() => undefined);
    return () => {
      live = false;
    };
  }, [count, unread.data, display.toast]);

  useEffect(() => {
    if (!open) {
      return;
    }
    let live = true;
    listNotifications({ limit: 20 })
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
      lastCount.current = null;
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

  function dismiss(id: string) {
    setToasts((cur) => cur.map((t) => (t.n.id === id ? { ...t, leaving: true } : t)));
    window.setTimeout(() => setToasts((cur) => cur.filter((t) => t.n.id !== id)), 260);
  }

  return (
    <div className="menu" ref={root}>
      <button
        className={"activity-chip bell-chip" + (running.length > 0 ? " is-busy" : "") + (failed && running.length === 0 ? " is-failed" : "")}
        type="button"
        aria-expanded={display.panel ? open : undefined}
        aria-haspopup={display.panel ? "dialog" : undefined}
        aria-label={label}
        title={label}
        onClick={() => (display.panel ? setOpen((v) => !v) : navigate("/notifications"))}
      >
        <Icon name="bell" size={15} />
        {count > 0 ? (
          <span className="bell-badge" aria-hidden="true">
            {badgeText(count)}
          </span>
        ) : null}
      </button>
      {open ? (
        <div className="notif-panel" role="dialog" aria-label="Notifications and tasks">
          <div className="notif-tabs" role="tablist">
            <button type="button" role="tab" aria-selected={tab === "notifications"} className="chip" onClick={() => setTab("notifications")}>
              Notifications{count > 0 ? ` (${badgeText(count)})` : ""}
            </button>
            <button type="button" role="tab" aria-selected={tab === "tasks"} className="chip" onClick={() => setTab("tasks")}>
              Tasks{running.length > 0 ? ` (${running.length} running)` : ""}
            </button>
            <span className="theme-spacer" />
            {tab === "notifications" ? (
              <button className="btn btn-ghost btn-sm" type="button" disabled={count === 0} onClick={() => void markRead([])}>
                Mark all read
              </button>
            ) : null}
          </div>
          <div className="notif-body">
            {tab === "notifications" ? (
              <>
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
              </>
            ) : taskList.length === 0 ? (
              <p className="muted">No tasks yet.</p>
            ) : (
              <ul className="activity-list">
                {taskList.slice(0, 12).map((task) => (
                  <li key={task.id}>
                    <span className="activity-toggle">
                      <StatusBadge status={task.state} />
                      <span>
                        {taskKindLabel(task.kind)}
                        {humanTaskMessage(task.message) ? ` ${humanTaskMessage(task.message)}` : ""}
                        {task.stage ? <span className="muted"> · {taskStageLabel(task.stage)}</span> : null}
                      </span>
                      <span className="muted">{formatWhen(task.updated_at)}</span>
                    </span>
                  </li>
                ))}
              </ul>
            )}
          </div>
          <div className="notif-foot">
            <Link href={tab === "notifications" ? "/notifications" : "/tasks"} onClick={() => setOpen(false)}>
              View all
            </Link>
            <Link href="/theme/notifications" onClick={() => setOpen(false)}>
              Notification settings
            </Link>
          </div>
        </div>
      ) : null}
      {toasts.length > 0 ? (
        <div className="toast-stack" aria-live="polite">
          {toasts.map((t) => (
            <ToastCard key={t.n.id} toast={t} seconds={display.seconds} onDismiss={dismiss} onOpen={openItem} />
          ))}
        </div>
      ) : null}
    </div>
  );
}

function ToastCard({
  toast,
  seconds,
  onDismiss,
  onOpen,
}: {
  toast: Toast;
  seconds: number;
  onDismiss: (id: string) => void;
  onOpen: (n: AppNotification) => void;
}) {
  const [paused, setPaused] = useState(false);
  useEffect(() => {
    if (paused || toast.leaving) {
      return;
    }
    const id = window.setTimeout(() => onDismiss(toast.n.id), seconds * 1000);
    return () => window.clearTimeout(id);
  }, [paused, toast.leaving, toast.n.id, seconds, onDismiss]);
  return (
    <div
      className={"toast" + (toast.leaving ? " is-leaving" : "")}
      role="status"
      onMouseEnter={() => setPaused(true)}
      onMouseLeave={() => setPaused(false)}
    >
      <button type="button" className="toast-main" onClick={() => onOpen(toast.n)}>
        <Icon name="bell" size={14} />
        <span className="toast-text">
          <strong>{toast.n.title}</strong>
          {toast.n.body ? <span>{toast.n.body}</span> : null}
        </span>
      </button>
      <button type="button" className="btn btn-ghost btn-sm btn-icon" aria-label="Dismiss" onClick={() => onDismiss(toast.n.id)}>
        ×
      </button>
    </div>
  );
}
