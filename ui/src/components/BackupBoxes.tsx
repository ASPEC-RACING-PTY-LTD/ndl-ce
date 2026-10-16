import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";

const STORE_KEY = "ndl.backups.minimized";

export type BoxInfo = { id: string; short: string };

type Boxes = {
  minimized: (id: string) => boolean;
  minimize: (id: string) => void;
  restore: (id: string) => void;
  /** The box a restore just opened, so it can scroll into view. */
  opened: string | null;
};

const BoxesContext = createContext<Boxes | null>(null);

function readMinimized(defaults: string[]): string[] {
  try {
    const raw = window.localStorage.getItem(STORE_KEY);
    if (raw) {
      const parsed = JSON.parse(raw) as unknown;
      if (Array.isArray(parsed)) {
        return parsed.filter((x): x is string => typeof x === "string");
      }
    }
  } catch {
    // Storage is a convenience; fall back to the defaults.
  }
  return defaults;
}

function writeMinimized(ids: string[]) {
  try {
    window.localStorage.setItem(STORE_KEY, JSON.stringify(ids));
  } catch {
    // Ignore: the layout still works for this visit.
  }
}

/**
 * useBoxes keeps which Backups boxes are minimized, per viewer. A
 * minimized box leaves the page and lives as a button in the page bar.
 */
export function useBoxes(defaultMinimized: string[]) {
  const [ids, setIds] = useState<string[]>(() => readMinimized(defaultMinimized));
  const [opened, setOpened] = useState<string | null>(null);
  const minimize = useCallback((id: string) => {
    setIds((cur) => {
      const next = cur.includes(id) ? cur : [...cur, id];
      writeMinimized(next);
      return next;
    });
  }, []);
  const restore = useCallback((id: string) => {
    setIds((cur) => {
      const next = cur.filter((x) => x !== id);
      writeMinimized(next);
      return next;
    });
    setOpened(id);
  }, []);
  const value = useMemo<Boxes>(() => ({ minimized: (id) => ids.includes(id), minimize, restore, opened }), [ids, minimize, restore, opened]);
  return { value, ids };
}

export function BoxesProvider({ value, children }: { value: Boxes; children: ReactNode }) {
  return <BoxesContext.Provider value={value}>{children}</BoxesContext.Provider>;
}

function useBoxesContext(): Boxes {
  const ctx = useContext(BoxesContext);
  if (!ctx) {
    throw new Error("Box must be inside BoxesProvider");
  }
  return ctx;
}

const SHRINK_MS = 250;
const FLY_MS = 650;

function reducedMotion(): boolean {
  return typeof window.matchMedia === "function" && window.matchMedia("(prefers-reduced-motion: reduce)").matches;
}

/**
 * flyToDock animates a minimize: the box shrinks fast, then a button with
 * its short name travels to the page bar. Only transform and opacity
 * change, on the compositor, so it stays smooth on large pages. The whole
 * move takes under a second; done runs when it lands.
 */
function flyToDock(el: HTMLElement, short: string, done: () => void) {
  if (typeof el.animate !== "function" || reducedMotion()) {
    done();
    return;
  }
  const from = el.getBoundingClientRect();
  const dock = document.querySelector(".bk-dock") ?? document.querySelector(".bk-bar");
  const to = dock?.getBoundingClientRect();
  el.style.willChange = "transform, opacity";
  const shrink = el.animate(
    [
      { transform: "scale(1)", opacity: 1 },
      { transform: "scale(0.18)", opacity: 0 },
    ],
    { duration: SHRINK_MS, easing: "cubic-bezier(0.4, 0, 1, 1)", fill: "forwards" },
  );
  shrink.onfinish = () => {
    const ghost = document.createElement("div");
    ghost.className = "bk-chip bk-ghost";
    ghost.textContent = short;
    document.body.appendChild(ghost);
    const g = ghost.getBoundingClientRect();
    const startX = from.left + from.width / 2 - g.width / 2;
    const startY = from.top + from.height / 2 - g.height / 2;
    const endX = to ? (dock?.classList.contains("bk-dock") ? to.right + 6 : to.left + 8) : startX;
    const endY = to ? to.top + (to.height - g.height) / 2 : 0;
    const fly = ghost.animate(
      [
        { transform: `translate(${startX}px, ${startY}px) scale(0.6)`, opacity: 0.4 },
        { transform: `translate(${startX}px, ${startY}px) scale(1)`, opacity: 1, offset: 0.15 },
        { transform: `translate(${endX}px, ${endY}px) scale(1)`, opacity: 1 },
      ],
      { duration: FLY_MS, easing: "cubic-bezier(0.22, 1, 0.36, 1)", fill: "forwards" },
    );
    const finish = () => {
      ghost.remove();
      el.style.willChange = "";
      done();
    };
    fly.onfinish = finish;
    fly.oncancel = finish;
  };
  shrink.oncancel = done;
}

/**
 * Box wraps one content box of the Backups page with a minimize control.
 * Minimizing shrinks it and sends it to the page bar as a button.
 */
export function Box({ id, title, short, children, className = "" }: { id: string; title: string; short: string; children: ReactNode; className?: string }) {
  const boxes = useBoxesContext();
  const [leaving, setLeaving] = useState(false);
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (boxes.opened === id && ref.current && typeof ref.current.scrollIntoView === "function") {
      ref.current.scrollIntoView({ behavior: reducedMotion() ? "auto" : "smooth", block: "nearest" });
    }
  }, [boxes.opened, id]);

  if (boxes.minimized(id)) {
    return null;
  }
  return (
    <div ref={ref} className={`bk-box ${boxes.opened === id ? "is-entering" : ""} ${className}`.trim()} data-box={id}>
      <button
        className="bk-min"
        type="button"
        aria-label={`Minimize ${title}`}
        title="Minimize"
        disabled={leaving}
        onClick={() => {
          setLeaving(true);
          const el = ref.current;
          const finish = () => {
            setLeaving(false);
            boxes.minimize(id);
          };
          if (el) {
            flyToDock(el, short, finish);
          } else {
            finish();
          }
        }}
      >
        <svg viewBox="0 0 16 16" width="14" height="14" aria-hidden="true">
          <path d="M3 8h10" stroke="currentColor" strokeWidth="2" strokeLinecap="round" />
        </svg>
      </button>
      {children}
    </div>
  );
}

/** BoxDock lists minimized boxes as buttons; a click brings one back. */
export function BoxDock({ boxes, value }: { boxes: BoxInfo[]; value: Boxes }) {
  const shown = boxes.filter((b) => value.minimized(b.id));
  if (shown.length === 0) {
    return null;
  }
  return (
    <div className="bk-dock" role="group" aria-label="Minimized boxes">
      {shown.map((b) => (
        <button key={b.id} type="button" className="bk-chip" onClick={() => value.restore(b.id)} aria-label={`Show ${b.short}`}>
          <svg viewBox="0 0 16 16" width="12" height="12" aria-hidden="true">
            <path d="M3 8h10M8 3v10" stroke="currentColor" strokeWidth="2" strokeLinecap="round" />
          </svg>
          {b.short}
        </button>
      ))}
    </div>
  );
}

/** CreateMenu is the page bar's "Create +" button with its choices. */
export function CreateMenu({ items }: { items: { label: string; onSelect: () => void }[] }) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open) {
      return;
    }
    const close = (e: MouseEvent | KeyboardEvent) => {
      if (e instanceof KeyboardEvent ? e.key === "Escape" : !ref.current?.contains(e.target as Node)) {
        setOpen(false);
      }
    };
    document.addEventListener("mousedown", close);
    document.addEventListener("keydown", close);
    return () => {
      document.removeEventListener("mousedown", close);
      document.removeEventListener("keydown", close);
    };
  }, [open]);
  return (
    <div className="bk-menu" ref={ref}>
      <button className="btn btn-primary" type="button" aria-haspopup="menu" aria-expanded={open} onClick={() => setOpen((v) => !v)}>
        Create +
      </button>
      {open ? (
        <div className="bk-menu-list" role="menu">
          {items.map((it) => (
            <button
              key={it.label}
              type="button"
              role="menuitem"
              onClick={() => {
                setOpen(false);
                it.onSelect();
              }}
            >
              {it.label}
            </button>
          ))}
        </div>
      ) : null}
    </div>
  );
}
