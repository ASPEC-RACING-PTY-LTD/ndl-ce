import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { Icon } from "./Icon";

export type ActionItem = {
  label: string;
  onClick: () => void;
  danger?: boolean;
};

/**
 * ActionMenu is the "…" menu. Its panel floats above the page (rendered on
 * document.body), so a table or card that scrolls or clips never cuts it
 * off. It opens downwards, or upwards when there is no room below.
 */
export function ActionMenu({
  label = "More actions",
  items,
}: {
  label?: string;
  items: ActionItem[];
}) {
  const [open, setOpen] = useState(false);
  const root = useRef<HTMLDivElement>(null);
  const button = useRef<HTMLButtonElement>(null);
  const panel = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) {
      return;
    }
    function onDoc(event: MouseEvent) {
      const t = event.target as Node;
      if (!root.current?.contains(t) && !panel.current?.contains(t)) {
        setOpen(false);
      }
    }
    function onKey(event: KeyboardEvent) {
      if (event.key === "Escape") {
        setOpen(false);
      }
    }
    function onMove(event: Event) {
      // Scrolling inside the menu itself is fine; anything else closes it.
      if (!panel.current?.contains(event.target as Node)) {
        setOpen(false);
      }
    }
    document.addEventListener("mousedown", onDoc);
    document.addEventListener("keydown", onKey);
    window.addEventListener("scroll", onMove, true);
    window.addEventListener("resize", onMove);
    return () => {
      document.removeEventListener("mousedown", onDoc);
      document.removeEventListener("keydown", onKey);
      window.removeEventListener("scroll", onMove, true);
      window.removeEventListener("resize", onMove);
    };
  }, [open]);

  useLayoutEffect(() => {
    const b = button.current;
    const p = panel.current;
    if (!open || !b || !p) {
      return;
    }
    const margin = 8;
    const r = b.getBoundingClientRect();
    const h = p.offsetHeight;
    const w = p.offsetWidth;
    const below = window.innerHeight - r.bottom - margin;
    const top = below >= h || r.top - margin < h ? Math.min(r.bottom + 4, window.innerHeight - h - margin) : r.top - h - 4;
    const left = Math.min(Math.max(margin, r.right - w), window.innerWidth - w - margin);
    p.style.top = `${Math.max(margin, top)}px`;
    p.style.left = `${left}px`;
  }, [open]);

  if (items.length === 0) {
    return null;
  }

  return (
    <div className="menu" ref={root}>
      <button
        ref={button}
        className="btn btn-ghost btn-sm btn-icon"
        type="button"
        aria-label={label}
        aria-expanded={open}
        aria-haspopup="true"
        title={label}
        onClick={() => setOpen((v) => !v)}
      >
        <Icon name="more" />
      </button>
      {open
        ? createPortal(
            <div ref={panel} className="menu-panel is-floating" role="menu" aria-label={label}>
              {items.map((item) => (
                <button
                  key={item.label}
                  type="button"
                  role="menuitem"
                  className={item.danger ? "is-danger" : undefined}
                  onClick={() => {
                    setOpen(false);
                    item.onClick();
                  }}
                >
                  {item.label}
                </button>
              ))}
            </div>,
            document.body,
          )
        : null}
    </div>
  );
}
