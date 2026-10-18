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
            key={`${i}-${item.label}`}
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

// --- Site-wide right-click menu --------------------------------------------
//
// No-dal adds its own right-click menu only where it saves real work: in
// terminals (copy, paste, clear) and on rows that open their own menus (file
// managers, the sidebar). Everywhere else the browser's menu shows.

type XtermLike = {
  element?: HTMLElement;
  getSelection(): string;
  hasSelection(): boolean;
  paste(text: string): void;
  selectAll(): void;
  clear(): void;
  focus(): void;
};
const terminals = new WeakMap<Element, XtermLike>();

/** registerTerminal lets the site menu copy from and paste into an xterm terminal. */
export function registerTerminal(term: XtermLike): void {
  if (term.element) {
    terminals.set(term.element, term);
  }
}

/** OPEN_PALETTE asks the shell to open the command palette. */
export const OPEN_PALETTE = "ndl:open-palette";

function copyText(text: string): void {
  void navigator.clipboard?.writeText(text).catch(() => document.execCommand("copy"));
}

function terminalItems(t: XtermLike): ContextItem[] {
  return [
    { label: "Copy", disabled: !t.hasSelection(), onClick: () => copyText(t.getSelection()) },
    {
      label: "Paste",
      disabled: !navigator.clipboard?.readText,
      onClick: () => void navigator.clipboard.readText().then((text) => (t.focus(), t.paste(text))),
    },
    "sep",
    { label: "Select all", onClick: () => t.selectAll() },
    { label: "Clear screen", onClick: () => t.clear() },
  ];
}

/** siteMenuFor builds the site menu for a right-click target, or null to leave the browser's. Exported for tests. */
export function siteMenuFor(target: Element | null): { title: string; items: ContextItem[] } | null {
  const term = target?.closest(".xterm");
  const t = term ? terminals.get(term) : undefined;
  return t ? { title: "Terminal", items: terminalItems(t) } : null;
}

/** GlobalContextMenu adds the terminal menu; other spots keep the browser's menu. */
export function GlobalContextMenu() {
  const [menu, setMenu] = useState<ContextMenuState>(null);
  useEffect(() => {
    function onContext(event: MouseEvent) {
      // Monaco, noVNC and row menus handle their own right-clicks.
      if (event.defaultPrevented) {
        return;
      }
      const target = event.target instanceof Element ? event.target : null;
      // Pages that opt out (the Terminal workspace) keep the browser's own menu.
      if (target?.closest("[data-native-contextmenu]")) {
        return;
      }
      const built = siteMenuFor(target);
      if (!built) {
        return;
      }
      event.preventDefault();
      let { clientX: x, clientY: y } = event;
      if (x === 0 && y === 0 && target) {
        // Opened from the keyboard (Shift+F10 or the menu key).
        const r = target.getBoundingClientRect();
        x = r.left + 8;
        y = r.bottom;
      }
      setMenu({ x, y, title: built.title, items: built.items });
    }
    window.addEventListener("contextmenu", onContext);
    return () => window.removeEventListener("contextmenu", onContext);
  }, []);
  return <ContextMenu menu={menu} onClose={() => setMenu(null)} />;
}
