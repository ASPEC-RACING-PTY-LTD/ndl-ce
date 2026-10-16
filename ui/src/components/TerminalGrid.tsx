import { useEffect, useLayoutEffect, useRef } from "react";
import { statusLabel, type TermTab } from "../terminal/types";
import { useTerminalWorkspace } from "../terminal/workspace";

export const TERM_GRIDS = [
  { id: "1", label: "Single", cols: 1, rows: 1 },
  { id: "1x2", label: "1 × 2", cols: 2, rows: 1 },
  { id: "2x2", label: "2 × 2", cols: 2, rows: 2 },
  { id: "2x3", label: "2 × 3", cols: 3, rows: 2 },
  { id: "3x3", label: "3 × 3", cols: 3, rows: 3 },
  { id: "4x4", label: "4 × 4", cols: 4, rows: 4 },
] as const;

export type TermGridId = (typeof TERM_GRIDS)[number]["id"];

function GridCell({ tab, focused }: { tab: TermTab; focused: boolean }) {
  const { attach, detach, fit, setActive, reconnect } = useTerminalWorkspace();
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
    <div className={"term-grid-cell" + (focused ? " is-focused" : "")} onMouseDown={() => setActive(tab.tabId)}>
      <div className="term-grid-head">
        <span className="term-tab-title">{tab.title}</span>
        <span className={`term-conn is-${tab.state}`}>{statusLabel(tab.state)}</span>
        {down ? (
          <button className="btn btn-ghost btn-sm" type="button" onClick={() => reconnect(tab.tabId)}>
            Reconnect
          </button>
        ) : null}
      </div>
      <div ref={ref} className="term-grid-slot" data-term-slot={tab.tabId} />
    </div>
  );
}

/** TerminalGrid shows several sessions at once, in tab order. */
export function TerminalGrid({ grid }: { grid: TermGridId }) {
  const { tabs, activeId } = useTerminalWorkspace();
  const g = TERM_GRIDS.find((x) => x.id === grid) ?? TERM_GRIDS[0];
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
      style={{ gridTemplateColumns: `repeat(${g.cols}, minmax(0, 1fr))`, gridTemplateRows: `repeat(${g.rows}, minmax(0, 1fr))` }}
    >
      {shown.map((tab) => (
        <GridCell key={tab.tabId} tab={tab} focused={tab.tabId === activeId} />
      ))}
    </div>
  );
}
