import { useEffect, useLayoutEffect, type RefObject } from "react";

/**
 * useFloatingPanel places a panel rendered on document.body (a portal) by
 * its anchor button, so a table or card that scrolls or clips never cuts it
 * off. It opens downwards, or upwards when there is no room below, and
 * closes on an outside click, Escape, or a scroll or resize elsewhere.
 */
export function useFloatingPanel(
  open: boolean,
  anchor: RefObject<HTMLElement | null>,
  panel: RefObject<HTMLElement | null>,
  close: () => void,
): void {
  useEffect(() => {
    if (!open) {
      return;
    }
    function onDoc(event: MouseEvent) {
      const t = event.target as Node;
      if (!anchor.current?.contains(t) && !panel.current?.contains(t)) {
        close();
      }
    }
    function onKey(event: KeyboardEvent) {
      if (event.key === "Escape") {
        close();
      }
    }
    function onMove(event: Event) {
      // Scrolling inside the panel itself is fine; anything else closes it.
      if (!panel.current?.contains(event.target as Node)) {
        close();
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
  }, [open, anchor, panel, close]);

  useLayoutEffect(() => {
    const b = anchor.current;
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
  }, [open, anchor, panel]);
}
