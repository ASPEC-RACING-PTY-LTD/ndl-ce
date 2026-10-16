import { useEffect, useId, useLayoutEffect, useRef, useState, type MouseEvent as ReactMouseEvent } from "react";

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
// Every right-click on the site opens one of No-dal's menus, never the
// browser's. Rows, cards and tabs open their own menus (they call
// preventDefault); anywhere else this menu opens with what fits the spot:
// clipboard actions in text fields and terminals, link actions on links, the
// page's own actions (see usePageContextItems) and navigation.

type PageEntry = { title: string; items: ContextItem[] };
const pageEntries = new Map<string, PageEntry>();

/** usePageContextItems adds page actions (such as "Add hostname") to right-clicks on the page. */
export function usePageContextItems(title: string, items: ContextItem[]): void {
  const id = useId();
  useEffect(() => {
    pageEntries.delete(id);
    pageEntries.set(id, { title, items });
  });
  useEffect(() => () => void pageEntries.delete(id), [id]);
}

function currentPageEntry(): PageEntry | null {
  let last: PageEntry | null = null;
  for (const e of pageEntries.values()) {
    last = e;
  }
  return last;
}

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

function textField(el: Element | null): HTMLInputElement | HTMLTextAreaElement | HTMLElement | null {
  const f = el?.closest("input, textarea, [contenteditable=''], [contenteditable='true']") as HTMLElement | null;
  if (!f) {
    return null;
  }
  if (f instanceof HTMLInputElement && !["text", "search", "url", "email", "password", "number", "tel", ""].includes(f.type)) {
    return null;
  }
  return f;
}

function copyText(text: string): void {
  void navigator.clipboard?.writeText(text).catch(() => document.execCommand("copy"));
}

function fieldItems(f: HTMLElement): ContextItem[] {
  const input = f instanceof HTMLInputElement || f instanceof HTMLTextAreaElement ? f : null;
  const secret = input instanceof HTMLInputElement && input.type === "password";
  const readOnly = Boolean(input?.readOnly || input?.disabled);
  const selected = input ? input.value.slice(input.selectionStart ?? 0, input.selectionEnd ?? 0) : window.getSelection()?.toString() ?? "";
  return [
    {
      label: "Cut",
      disabled: secret || readOnly || !selected,
      onClick: () => {
        copyText(selected);
        f.focus();
        document.execCommand("insertText", false, "");
      },
    },
    { label: "Copy", disabled: secret || !selected, onClick: () => copyText(selected) },
    {
      label: "Paste",
      disabled: readOnly || !navigator.clipboard?.readText,
      onClick: () => {
        void navigator.clipboard.readText().then((text) => {
          f.focus();
          // insertText keeps React-controlled inputs and the undo history in step.
          document.execCommand("insertText", false, text);
        });
      },
    },
    "sep",
    {
      label: "Select all",
      onClick: () => {
        f.focus();
        if (input) {
          input.select();
        } else {
          document.execCommand("selectAll");
        }
      },
    },
  ];
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

function linkItems(a: HTMLAnchorElement): ContextItem[] {
  const href = a.href;
  const internal = a.origin === window.location.origin && a.target !== "_blank";
  return [
    { label: "Open", onClick: () => (internal ? a.click() : window.open(href, a.target || "_self", "noopener")) },
    { label: "Open in new tab", onClick: () => window.open(href, "_blank", "noopener") },
    { label: "Copy link address", onClick: () => copyText(href) },
  ];
}

function navItems(): ContextItem[] {
  return [
    { label: "Back", disabled: window.history.length <= 1, onClick: () => window.history.back() },
    { label: "Forward", onClick: () => window.history.forward() },
    { label: "Reload", onClick: () => window.location.reload() },
    "sep",
    { label: "Search No-dal…", onClick: () => window.dispatchEvent(new Event(OPEN_PALETTE)) },
    { label: "Copy page link", onClick: () => copyText(window.location.href) },
  ];
}

/** siteMenuFor builds the site menu for a right-click target. Exported for tests. */
export function siteMenuFor(target: Element | null): { title: string; items: ContextItem[] } {
  const join = (...groups: ContextItem[][]) => groups.filter((g) => g.length).flatMap((g, i) => (i ? (["sep", ...g] as ContextItem[]) : g));
  const term = target?.closest(".xterm");
  const t = term ? terminals.get(term) : undefined;
  if (t) {
    return { title: "Terminal", items: terminalItems(t) };
  }
  const field = textField(target);
  if (field) {
    return { title: "Text", items: fieldItems(field) };
  }
  const selection = window.getSelection()?.toString() ?? "";
  const copy: ContextItem[] = selection.trim() ? [{ label: "Copy", onClick: () => copyText(selection) }] : [];
  const link = target?.closest("a[href]") as HTMLAnchorElement | null;
  const page = currentPageEntry();
  if (link) {
    return { title: link.textContent?.trim().slice(0, 60) || "Link", items: join(copy, linkItems(link), page?.items ?? []) };
  }
  return { title: page?.title ?? document.title.replace(/ ?[-|·].*$/, "") ?? "No-dal", items: join(copy, page?.items ?? [], navItems()) };
}

/** GlobalContextMenu replaces the browser's right-click menu everywhere on the site. */
export function GlobalContextMenu() {
  const [menu, setMenu] = useState<ContextMenuState>(null);
  useEffect(() => {
    function onContext(event: MouseEvent) {
      // Monaco, noVNC and row menus handle their own right-clicks.
      if (event.defaultPrevented) {
        return;
      }
      event.preventDefault();
      const target = event.target instanceof Element ? event.target : null;
      const built = siteMenuFor(target);
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
