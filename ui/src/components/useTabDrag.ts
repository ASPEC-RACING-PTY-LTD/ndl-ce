import { useCallback, useEffect, useLayoutEffect, useRef, useState, type PointerEvent as ReactPointerEvent } from "react";

type DragState = {
  id: string;
  startX: number;
  grab: number;
  lastX: number;
  tx: number;
  active: boolean;
};

const THRESHOLD = 5;

function tabEls(list: HTMLElement | null): HTMLElement[] {
  if (!list) {
    return [];
  }
  return Array.from(list.querySelectorAll<HTMLElement>("[data-tab-id]"));
}

/**
 * useTabDrag lets a row of tabs be reordered by press, drag and release, the
 * way browser tabs work. Tabs carry data-tab-id; the list element takes
 * listRef. The dragged tab follows the pointer and the others move aside as
 * it crosses their middle.
 */
export function useTabDrag(ids: string[], move: (id: string, toIndex: number) => void) {
  const listRef = useRef<HTMLDivElement>(null);
  const state = useRef<DragState | null>(null);
  const suppress = useRef(false);
  const idsRef = useRef(ids);
  idsRef.current = ids;
  const moveRef = useRef(move);
  moveRef.current = move;
  const [draggingId, setDraggingId] = useState<string | null>(null);

  const follow = useCallback(() => {
    const s = state.current;
    if (!s?.active) {
      return;
    }
    const el = tabEls(listRef.current).find((t) => t.dataset.tabId === s.id);
    if (!el) {
      return;
    }
    const natural = el.getBoundingClientRect().left - s.tx;
    s.tx = s.lastX - s.grab - natural;
    el.style.transform = `translateX(${s.tx}px)`;
  }, []);

  // After a reorder the dragged tab sits in a new slot; keep it under the pointer.
  useLayoutEffect(() => {
    follow();
  }, [ids, follow]);

  const onMove = useCallback(
    (event: PointerEvent) => {
      const s = state.current;
      if (!s) {
        return;
      }
      s.lastX = event.clientX;
      if (!s.active) {
        if (Math.abs(event.clientX - s.startX) < THRESHOLD) {
          return;
        }
        s.active = true;
        setDraggingId(s.id);
        document.body.classList.add("is-tab-dragging");
      }
      event.preventDefault();
      const els = tabEls(listRef.current);
      const el = els.find((t) => t.dataset.tabId === s.id);
      if (!el) {
        return;
      }
      const width = el.getBoundingClientRect().width;
      const center = event.clientX - s.grab + width / 2;
      let index = 0;
      for (const other of els) {
        if (other === el) {
          continue;
        }
        const r = other.getBoundingClientRect();
        if (r.left + r.width / 2 < center) {
          index += 1;
        }
      }
      if (index !== idsRef.current.indexOf(s.id)) {
        moveRef.current(s.id, index);
      }
      follow();
    },
    [follow],
  );

  const onUp = useCallback(() => {
    const s = state.current;
    window.removeEventListener("pointermove", onMove);
    window.removeEventListener("pointerup", onUp);
    window.removeEventListener("pointercancel", onUp);
    state.current = null;
    if (!s?.active) {
      return;
    }
    const el = tabEls(listRef.current).find((t) => t.dataset.tabId === s.id);
    if (el) {
      el.style.transform = "";
    }
    document.body.classList.remove("is-tab-dragging");
    setDraggingId(null);
    // The release fires a click on the tab; it must not count as a click.
    suppress.current = true;
    window.setTimeout(() => {
      suppress.current = false;
    }, 0);
  }, [onMove]);

  useEffect(
    () => () => {
      window.removeEventListener("pointermove", onMove);
      window.removeEventListener("pointerup", onUp);
      window.removeEventListener("pointercancel", onUp);
      document.body.classList.remove("is-tab-dragging");
    },
    [onMove, onUp],
  );

  const onPointerDown = useCallback(
    (event: ReactPointerEvent<HTMLElement>, id: string) => {
      if (event.button !== 0 || idsRef.current.length < 2) {
        return;
      }
      const rect = event.currentTarget.getBoundingClientRect();
      state.current = { id, startX: event.clientX, grab: event.clientX - rect.left, lastX: event.clientX, tx: 0, active: false };
      window.addEventListener("pointermove", onMove);
      window.addEventListener("pointerup", onUp);
      window.addEventListener("pointercancel", onUp);
    },
    [onMove, onUp],
  );

  const consumeClick = useCallback(() => {
    if (suppress.current) {
      suppress.current = false;
      return true;
    }
    return false;
  }, []);

  return { listRef, draggingId, onPointerDown, consumeClick };
}
