import { useEffect, useLayoutEffect, useRef, useState, type CSSProperties, type PointerEvent as ReactPointerEvent } from "react";
import { usePref } from "../prefs";
import { statusLabel, type TermTab } from "../terminal/types";
import { useTerminalWorkspace } from "../terminal/workspace";

// cols 0 means "as many as the open sessions need": the grid has no cap.
export const TERM_GRIDS = [
  { id: "1", label: "Single", cols: 1, rows: 1 },
  { id: "1x2", label: "1 × 2", cols: 2, rows: 1 },
  { id: "2x2", label: "2 × 2", cols: 2, rows: 2 },
  { id: "2x3", label: "2 × 3", cols: 3, rows: 2 },
  { id: "3x3", label: "3 × 3", cols: 3, rows: 3 },
  { id: "4x4", label: "4 × 4", cols: 4, rows: 4 },
  { id: "all", label: "All sessions", cols: 0, rows: 0 },
  { id: "free", label: "Free place terminals", cols: 0, rows: 0 },
] as const;

export type TermGridId = (typeof TERM_GRIDS)[number]["id"];

/** autoGrid is the column and row count that shows n sessions at once. */
export function autoGrid(n: number): { cols: number; rows: number } {
  const count = Math.max(1, n);
  const cols = Math.ceil(Math.sqrt(count));
  return { cols, rows: Math.ceil(count / cols) };
}

/** FreeRect is one terminal's place in free mode, in percent of the area. */
export type FreeRect = {
  x: number;
  y: number;
  w: number;
  h: number;
  z: number;
};

export const FREE_LAYOUT_KEY = "terminal.free";

/** freeRect is a saved place, or a cascade for a new one; values stay inside the area. */
export function freeRect(saved: unknown, index: number): FreeRect {
  const r = (saved ?? {}) as Partial<FreeRect>;
  const clamp = (v: unknown, lo: number, hi: number, d: number) => (typeof v === "number" && Number.isFinite(v) ? Math.min(hi, Math.max(lo, v)) : d);
  const w = clamp(r.w, 15, 100, 48);
  const h = clamp(r.h, 15, 100, 46);
  return {
    x: clamp(r.x, 0, 100 - w, (index * 4) % 40),
    y: clamp(r.y, 0, 100 - h, (index * 5) % 40),
    w,
    h,
    z: clamp(r.z, 0, 1e6, index),
  };
}

function GridCell({
  tab,
  focused,
  style,
  onHeadPointerDown,
  onResizePointerDown,
}: {
  tab: TermTab;
  focused: boolean;
  style?: CSSProperties;
  onHeadPointerDown?: (e: ReactPointerEvent<HTMLDivElement>) => void;
  onResizePointerDown?: (e: ReactPointerEvent<HTMLSpanElement>) => void;
}) {
  const { attach, detach, fit, setActive, focusTab, reconnect } = useTerminalWorkspace();
  const ref = useRef<HTMLDivElement>(null);

  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) {
      return;
    }
    attach(tab.tabId, el);
    return () => detach(tab.tabId);
  }, [tab.tabId, tab.ioSessionId, tab.state, attach, detach]);

  // Each cell fits its terminal to its own size and tells the shell.
  useEffect(() => {
    const el = ref.current;
    if (!el || typeof ResizeObserver === "undefined") {
      return;
    }
    let frame = 0;
    const ro = new ResizeObserver(() => {
      cancelAnimationFrame(frame);
      frame = requestAnimationFrame(() => fit(tab.tabId));
    });
    ro.observe(el);
    return () => {
      cancelAnimationFrame(frame);
      ro.disconnect();
    };
  }, [tab.tabId, fit]);

  const down = tab.state === "disconnected" || tab.state === "closed";
  return (
    <div className={"term-grid-cell" + (focused ? " is-focused" : "")} style={style} onMouseDown={() => setActive(tab.tabId)}>
      <div className="term-grid-head" onPointerDown={onHeadPointerDown} onClick={() => focusTab(tab.tabId)}>
        <span className="term-tab-title">{tab.title}</span>
        <span className={`term-conn is-${tab.state}`}>{statusLabel(tab.state)}</span>
        {down ? (
          <button className="btn btn-ghost btn-sm" type="button" onClick={() => reconnect(tab.tabId)}>
            Reconnect
          </button>
        ) : null}
      </div>
      <div ref={ref} className="term-grid-slot" data-term-slot={tab.tabId} />
      {onResizePointerDown ? <span className="term-free-resize" aria-hidden="true" onPointerDown={onResizePointerDown} /> : null}
    </div>
  );
}

/** FreeTerminals places every session where the person dragged it; places are saved per person. */
function FreeTerminals() {
  const { tabs, activeId } = useTerminalWorkspace();
  const [saved, setSaved] = usePref<Record<string, FreeRect>>(FREE_LAYOUT_KEY, {});
  const [live, setLive] = useState<Record<string, FreeRect>>({});
  const area = useRef<HTMLDivElement>(null);
  const rects = tabs.map((t, i) => live[t.tabId] ?? freeRect(saved?.[t.tabId], i));
  const top = Math.max(0, ...rects.map((r) => r.z));

  function start(e: ReactPointerEvent, tabId: string, index: number, mode: "move" | "size") {
    const el = area.current;
    if (!el || e.button !== 0) {
      return;
    }
    if (mode === "move" && (e.target as HTMLElement).closest("button")) {
      return;
    }
    e.preventDefault();
    const box = el.getBoundingClientRect();
    const from = { ...rects[index], z: top + 1 };
    const sx = e.clientX;
    const sy = e.clientY;
    let cur = from;
    const onMove = (ev: PointerEvent) => {
      const dx = ((ev.clientX - sx) / box.width) * 100;
      const dy = ((ev.clientY - sy) / box.height) * 100;
      cur =
        mode === "move"
          ? freeRect({ ...from, x: from.x + dx, y: from.y + dy }, index)
          : freeRect(
              {
                ...from,
                w: Math.min(100 - from.x, from.w + dx),
                h: Math.min(100 - from.y, from.h + dy),
              },
              index,
            );
      setLive((m) => ({ ...m, [tabId]: cur }));
    };
    const onUp = () => {
      window.removeEventListener("pointermove", onMove);
      window.removeEventListener("pointerup", onUp);
      setSaved((m) => {
        const next: Record<string, FreeRect> = {};
        // Keep places only for sessions that still exist.
        for (const t of tabs) {
          const r = t.tabId === tabId ? cur : m?.[t.tabId];
          if (r) {
            next[t.tabId] = r;
          }
        }
        return next;
      });
      setLive((m) => {
        const next = { ...m };
        delete next[tabId];
        return next;
      });
    };
    window.addEventListener("pointermove", onMove);
    window.addEventListener("pointerup", onUp);
    setLive((m) => ({ ...m, [tabId]: from }));
  }

  if (tabs.length === 0) {
    return <p className="muted">Open a terminal to place it.</p>;
  }
  return (
    <div ref={area} className="term-free">
      {tabs.map((tab, i) => {
        const r = rects[i];
        return (
          <GridCell
            key={tab.tabId}
            tab={tab}
            focused={tab.tabId === activeId}
            style={{
              left: `${r.x}%`,
              top: `${r.y}%`,
              width: `${r.w}%`,
              height: `${r.h}%`,
              zIndex: tab.tabId === activeId ? top + 1 : r.z,
            }}
            onHeadPointerDown={(e) => start(e, tab.tabId, i, "move")}
            onResizePointerDown={(e) => start(e, tab.tabId, i, "size")}
          />
        );
      })}
    </div>
  );
}

/** TerminalGrid shows several sessions at once, in tab order. */
export function TerminalGrid({ grid }: { grid: TermGridId }) {
  const { tabs, activeId } = useTerminalWorkspace();
  if (grid === "free") {
    return <FreeTerminals />;
  }
  return <FixedGrid grid={grid} tabs={tabs} activeId={activeId} />;
}

function FixedGrid({ grid, tabs, activeId }: { grid: TermGridId; tabs: TermTab[]; activeId: string | null }) {
  const preset = TERM_GRIDS.find((x) => x.id === grid) ?? TERM_GRIDS[0];
  const g = preset.cols === 0 ? autoGrid(tabs.length) : preset;
  const slots = g.cols * g.rows;
  // Tabs fill the grid in order, a page at a time; the page is the one
  // holding the focused session, so focusing a cell never reorders it.
  const activeIndex = Math.max(0, tabs.findIndex((t) => t.tabId === activeId));
  const page = Math.floor(activeIndex / slots);
  const shown: TermTab[] = tabs.slice(page * slots, page * slots + slots);
  if (shown.length === 0) {
    return <p className="muted">Open a terminal to fill the grid.</p>;
  }
  return (
    <div
      className="term-grid"
      style={{
        gridTemplateColumns: `repeat(${g.cols}, minmax(0, 1fr))`,
        gridTemplateRows: `repeat(${g.rows}, minmax(0, 1fr))`,
      }}
    >
      {shown.map((tab) => (
        <GridCell key={tab.tabId} tab={tab} focused={tab.tabId === activeId} />
      ))}
    </div>
  );
}
