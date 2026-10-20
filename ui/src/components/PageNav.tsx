import { useEffect, useRef, useState, type ReactNode } from "react";
import { usePref } from "../prefs";
import { navigate, usePath } from "../router";
import { ContextMenu, useContextMenu } from "./ContextMenu";
import { Icon, type IconName } from "./Icon";
import { Link } from "./Link";

/** A page below a main button, shown in its dropdown. */
export type PageNavChild = {
  id: string;
  label: string;
  href: string;
  match?: (path: string) => boolean;
};

/** A main button on a page bar. children appear in a dropdown on hover. */
export type PageNavItem = {
  id: string;
  label: string;
  href: string;
  icon?: IconName;
  match?: (path: string) => boolean;
  children?: PageNavChild[];
};

const orderKey = (id: string) => `pagenav.${id}`;

/** applyOrder sorts items in the saved order; new items keep their place after it. */
export function applyOrder<T extends { id: string }>(items: T[], order: string[] | null | undefined): T[] {
  if (!Array.isArray(order) || order.length === 0) {
    return items;
  }
  const rank = new Map(order.map((id, i) => [id, i]));
  return items
    .map((item, i) => ({ item, i }))
    .sort((a, b) => (rank.get(a.item.id) ?? order.length + a.i) - (rank.get(b.item.id) ?? order.length + b.i))
    .map((x) => x.item);
}

/** moveId moves id to before (or after) target. */
export function moveId(ids: string[], id: string, target: string, after: boolean): string[] {
  if (id === target) {
    return ids;
  }
  const list = ids.filter((x) => x !== id);
  let at = list.indexOf(target);
  if (at < 0) {
    return ids;
  }
  if (after) {
    at++;
  }
  list.splice(at, 0, id);
  return list;
}

/** usePageNavOrder is a bar's items in the person's saved order. */
export function usePageNavOrder<T extends { id: string }>(barId: string, items: T[]): [T[], (ids: string[] | undefined) => void] {
  const [order, setOrder] = usePref<string[] | null>(orderKey(barId), null);
  return [applyOrder(items, order), (ids) => setOrder(ids ?? null)];
}

function itemActive(item: PageNavItem | PageNavChild, path: string): boolean {
  if (item.match) {
    return item.match(path);
  }
  const base = item.href.split(/[?#]/)[0];
  return path === base || path.startsWith(base + "/");
}

/**
 * PageNav is the bar of main buttons right under the header. A button with
 * children opens a dropdown on hover or ArrowDown; clicking the button
 * opens its own page. Buttons can be dragged (or moved from the right-click
 * menu) and the order is saved per person.
 */
export function PageNav({
  id,
  label,
  icon,
  items,
  actions,
  className,
}: {
  id: string;
  label: string;
  icon?: IconName;
  items: PageNavItem[];
  actions?: ReactNode;
  className?: string;
}) {
  const path = usePath();
  const [ordered, setOrder] = usePageNavOrder(id, items);
  const ids = ordered.map((i) => i.id);
  const [open, setOpen] = useState<string | null>(null);
  const [drag, setDrag] = useState<string | null>(null);
  const [dropAt, setDropAt] = useState<{ id: string; after: boolean } | null>(null);
  const timer = useRef<number | null>(null);
  const menu = useContextMenu();
  const barRef = useRef<HTMLElement>(null);

  useEffect(() => setOpen(null), [path]);
  useEffect(
    () => () => {
      if (timer.current) {
        window.clearTimeout(timer.current);
      }
    },
    [],
  );

  function hoverOpen(next: string | null) {
    if (timer.current) {
      window.clearTimeout(timer.current);
    }
    // A short delay keeps menus from flashing open as the pointer passes.
    timer.current = window.setTimeout(() => setOpen(next), next ? 90 : 160);
  }

  function moveBy(itemId: string, delta: number) {
    const i = ids.indexOf(itemId);
    const j = i + delta;
    if (i < 0 || j < 0 || j >= ids.length) {
      return;
    }
    setOrder(moveId(ids, itemId, ids[j], delta > 0));
  }

  // The current page's main button: the one whose page or children match,
  // longest match first so /iam/access beats /iam.
  const current = ordered
    .map((item) => {
      const hits = [item, ...(item.children ?? [])].filter((x) => itemActive(x, path)).map((x) => x.href.length);
      return { id: item.id, score: hits.length ? Math.max(...hits) : -1 };
    })
    .sort((a, b) => b.score - a.score)[0];
  const activeId = current && current.score >= 0 ? current.id : null;

  return (
    <nav
      ref={barRef}
      className={"page-nav" + (className ? " " + className : "")}
      aria-label={label}
      onKeyDown={(e) => {
        if (e.key === "Escape") {
          setOpen(null);
        }
      }}
    >
      <span className="page-nav-title">
        {icon ? <Icon name={icon} size={16} /> : null}
        {label}
      </span>
      <ul className="page-nav-list" role="list">
        {ordered.map((item) => {
          const active = item.id === activeId;
          const hasMenu = (item.children?.length ?? 0) > 0;
          const isOpen = open === item.id;
          return (
            <li
              key={item.id}
              className={
                "page-nav-item" +
                (drag === item.id ? " is-dragging" : "") +
                (dropAt?.id === item.id ? (dropAt.after ? " is-drop-after" : " is-drop-before") : "")
              }
              onMouseEnter={() => hasMenu && hoverOpen(item.id)}
              onMouseLeave={() => hasMenu && hoverOpen(null)}
              onDragOver={(e) => {
                if (!drag) {
                  return;
                }
                e.preventDefault();
                const r = e.currentTarget.getBoundingClientRect();
                const after = e.clientX > r.left + r.width / 2;
                if (dropAt?.id !== item.id || dropAt.after !== after) {
                  setDropAt({ id: item.id, after });
                }
              }}
              onDrop={(e) => {
                e.preventDefault();
                if (drag && dropAt) {
                  setOrder(moveId(ids, drag, item.id, dropAt.after));
                }
                setDrag(null);
                setDropAt(null);
              }}
            >
              <Link
                href={item.href}
                className="page-nav-link"
                aria-current={active ? "page" : undefined}
                aria-haspopup={hasMenu ? "menu" : undefined}
                aria-expanded={hasMenu ? isOpen : undefined}
                aria-keyshortcuts="Alt+ArrowLeft Alt+ArrowRight"
                draggable
                onDragStart={(e) => {
                  e.dataTransfer.effectAllowed = "move";
                  e.dataTransfer.setData("text/plain", item.label);
                  setDrag(item.id);
                  setOpen(null);
                }}
                onDragEnd={() => {
                  setDrag(null);
                  setDropAt(null);
                }}
                onFocus={() => hasMenu && setOpen((cur) => (cur && cur !== item.id ? null : cur))}
                onKeyDown={(e) => {
                  if (e.altKey && (e.key === "ArrowLeft" || e.key === "ArrowRight")) {
                    e.preventDefault();
                    moveBy(item.id, e.key === "ArrowLeft" ? -1 : 1);
                  } else if (hasMenu && e.key === "ArrowDown") {
                    e.preventDefault();
                    setOpen(item.id);
                    window.requestAnimationFrame(() =>
                      barRef.current?.querySelector<HTMLAnchorElement>(`[data-menu="${item.id}"] a`)?.focus(),
                    );
                  }
                }}
                onContextMenu={(e) =>
                  menu.openAt(e, item.label, [
                    { label: "Open", onClick: () => navigate(item.href) },
                    { label: "Open in new tab", onClick: () => window.open(item.href, "_blank", "noopener") },
                    "sep",
                    { label: "Move left", disabled: ids[0] === item.id, onClick: () => moveBy(item.id, -1) },
                    { label: "Move right", disabled: ids[ids.length - 1] === item.id, onClick: () => moveBy(item.id, 1) },
                    { label: "Reset order", onClick: () => setOrder(undefined) },
                  ])
                }
              >
                {item.icon ? <Icon name={item.icon} size={14} /> : null}
                <span>{item.label}</span>
                {hasMenu ? <Icon name="chevron-down" size={12} className="page-nav-caret" /> : null}
              </Link>
              {hasMenu && isOpen ? (
                <div className="page-nav-menu" role="menu" data-menu={item.id}>
                  {item.children?.map((c) => (
                    <Link
                      key={c.id}
                      href={c.href}
                      role="menuitem"
                      className="page-nav-menu-link"
                      aria-current={itemActive(c, path) ? "page" : undefined}
                      onKeyDown={(e) => {
                        const links = Array.from(e.currentTarget.parentElement?.querySelectorAll<HTMLAnchorElement>("a") ?? []);
                        const at = links.indexOf(e.currentTarget);
                        if (e.key === "ArrowDown") {
                          e.preventDefault();
                          links[(at + 1) % links.length]?.focus();
                        } else if (e.key === "ArrowUp") {
                          e.preventDefault();
                          links[(at - 1 + links.length) % links.length]?.focus();
                        }
                      }}
                    >
                      {c.label}
                    </Link>
                  ))}
                </div>
              ) : null}
            </li>
          );
        })}
      </ul>
      {actions ? <div className="page-nav-actions">{actions}</div> : null}
      <ContextMenu menu={menu.menu} onClose={menu.close} />
    </nav>
  );
}
