import { useEffect, useLayoutEffect, useRef, useState, type MouseEvent as ReactMouseEvent } from "react";

export type ContextItem =
  | { label: string; onClick: () => void; danger?: boolean; disabled?: boolean }
  | "sep";

export type ContextMenuState = { x: number; y: number; title: string; items: ContextItem[] } | null;

/** useContextMenu holds one open right-click menu for a page. */
export function useContextMenu() {
  const [menu, setMenu] = useState<ContextMenuState>(null);
  function openAt(event: ReactMouseEvent, title: string, items: ContextItem[]) {
    event.preventDefault();
    event.stopPropagation();
    setMenu({ x: event.clientX, y: event.clientY, title, items });
  }
  return { menu, openAt, close: () => setMenu(null) };
}

/** ContextMenu is a right-click menu that stays inside the window. */
export function ContextMenu({ menu, onClose }: { menu: ContextMenuState; onClose: () => void }) {
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!menu) {
      return;
    }
    function onDoc() {
      onClose();
    }
    function onKey(event: KeyboardEvent) {
      if (event.key === "Escape") {
        onClose();
      }
    }
    document.addEventListener("mousedown", onDoc);
    document.addEventListener("keydown", onKey);
    window.addEventListener("blur", onDoc);
    window.addEventListener("resize", onDoc);
    return () => {
      document.removeEventListener("mousedown", onDoc);
      document.removeEventListener("keydown", onKey);
      window.removeEventListener("blur", onDoc);
      window.removeEventListener("resize", onDoc);
    };
  }, [menu, onClose]);

  useLayoutEffect(() => {
    const el = ref.current;
    if (!menu || !el) {
      return;
    }
    const margin = 8;
    const rect = el.getBoundingClientRect();
    el.style.left = `${Math.max(margin, Math.min(menu.x, window.innerWidth - rect.width - margin))}px`;
    el.style.top = `${Math.max(margin, Math.min(menu.y, window.innerHeight - rect.height - margin))}px`;
    el.querySelector<HTMLButtonElement>("button:not(:disabled)")?.focus();
  }, [menu]);

  if (!menu) {
    return null;
  }
  return (
    <div
      ref={ref}
      className="menu-panel term-ctx"
      role="menu"
      aria-label={`${menu.title} actions`}
      style={{ left: menu.x, top: menu.y }}
      onMouseDown={(event) => event.stopPropagation()}
      onContextMenu={(event) => event.preventDefault()}
    >
      <p className="ctx-title">{menu.title}</p>
      {menu.items.map((item, i) =>
        item === "sep" ? (
          <hr key={`sep-${i}`} />
        ) : (
          <button
            key={item.label}
            type="button"
            role="menuitem"
            className={item.danger ? "is-danger" : undefined}
            disabled={item.disabled}
            onClick={() => {
              onClose();
              item.onClick();
            }}
          >
            {item.label}
          </button>
        ),
      )}
    </div>
  );
}
