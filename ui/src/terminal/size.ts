import { storageGet, storageSet } from "../storage";

export const TERM_SIZE_KEY = "ndl-term-size";
export const MIN_TERM_W = 320;
export const MIN_TERM_H = 200;

export type TermSizePref =
  | { mode: "auto" }
  | { mode: "manual"; width: number; height: number };

export type TermBox = {
  mode: "auto" | "manual";
  width: string;
  height: string;
  pxWidth: number;
  pxHeight: number;
};

export function loadTermSize(): TermSizePref {
  try {
    const raw = storageGet(TERM_SIZE_KEY);
    if (!raw) {
      return { mode: "auto" };
    }
    const parsed = JSON.parse(raw) as TermSizePref;
    if (parsed?.mode === "manual" && Number.isFinite(parsed.width) && Number.isFinite(parsed.height)) {
      return {
        mode: "manual",
        width: parsed.width,
        height: parsed.height,
      };
    }
    return { mode: "auto" };
  } catch {
    return { mode: "auto" };
  }
}

export function saveTermSize(pref: TermSizePref): void {
  storageSet(TERM_SIZE_KEY, JSON.stringify(pref));
}

export function clampTermSize(
  width: number,
  height: number,
  maxW: number,
  maxH: number,
): { width: number; height: number } {
  const w = Number.isFinite(width) ? Math.round(width) : MIN_TERM_W;
  const h = Number.isFinite(height) ? Math.round(height) : MIN_TERM_H;
  const loW = maxW > 0 ? Math.min(MIN_TERM_W, maxW) : MIN_TERM_W;
  const loH = maxH > 0 ? Math.min(MIN_TERM_H, maxH) : MIN_TERM_H;
  const hiW = maxW > 0 ? maxW : Number.POSITIVE_INFINITY;
  const hiH = maxH > 0 ? maxH : Number.POSITIVE_INFINITY;
  return {
    width: Math.min(hiW, Math.max(loW, w)),
    height: Math.min(hiH, Math.max(loH, h)),
  };
}

export function layoutLimit(el: HTMLElement): { width: number; height: number } {
  const main = (el.closest(".shell-main") as HTMLElement | null) ?? document.documentElement;
  const mainRect = main.getBoundingClientRect();
  const rect = el.getBoundingClientRect();
  const cs = typeof getComputedStyle === "function" ? getComputedStyle(main) : undefined;
  const padRight = cs ? Number.parseFloat(cs.paddingRight) || 0 : 0;
  const padBottom = cs ? Number.parseFloat(cs.paddingBottom) || 0 : 0;
  return {
    width: Math.max(0, mainRect.right - padRight - rect.left),
    height: Math.max(0, mainRect.bottom - padBottom - rect.top),
  };
}

export function termStyle(pref: TermSizePref, limit: { width: number; height: number }): { width: string; height: string } {
  if (pref.mode === "manual") {
    const next = clampTermSize(pref.width, pref.height, limit.width, limit.height);
    return { width: `${next.width}px`, height: `${next.height}px` };
  }
  return { width: "100%", height: `${Math.max(MIN_TERM_H, limit.height)}px` };
}

export function computeTermBox(wrap: HTMLElement, pref: TermSizePref): TermBox {
  const limit = layoutLimit(wrap);
  const style = termStyle(pref, limit);
  if (pref.mode === "manual") {
    const next = clampTermSize(pref.width, pref.height, limit.width, limit.height);
    return { mode: "manual", width: style.width, height: style.height, pxWidth: next.width, pxHeight: next.height };
  }
  return {
    mode: "auto",
    width: style.width,
    height: style.height,
    pxWidth: Math.max(MIN_TERM_W, limit.width),
    pxHeight: Math.max(MIN_TERM_H, limit.height),
  };
}

export function applyTermBox(wrap: HTMLElement, pref: TermSizePref): { width: number; height: number } {
  const box = computeTermBox(wrap, pref);
  wrap.style.width = box.width;
  wrap.style.height = box.height;
  wrap.dataset.termSize = box.mode;
  return { width: box.pxWidth, height: box.pxHeight };
}

export function observeTermLayout(wrap: HTMLElement, onChange: () => void): () => void {
  window.addEventListener("resize", onChange);
  const viewport = window.visualViewport;
  viewport?.addEventListener("resize", onChange);
  viewport?.addEventListener("scroll", onChange);
  const ro = typeof ResizeObserver === "function" ? new ResizeObserver(onChange) : null;
  const seen = new Set<Element>();
  function watch(node: Element | null) {
    if (!node || !ro || seen.has(node)) {
      return;
    }
    seen.add(node);
    ro.observe(node);
  }
  watch(wrap);
  watch(wrap.parentElement);
  const page = wrap.closest(".page-term");
  if (page) {
    watch(page);
    for (const child of Array.from(page.children)) {
      watch(child);
    }
  }
  watch(wrap.closest(".shell-main"));
  watch(wrap.closest(".shell-body"));
  watch(wrap.closest(".shell"));
  return () => {
    window.removeEventListener("resize", onChange);
    viewport?.removeEventListener("resize", onChange);
    viewport?.removeEventListener("scroll", onChange);
    ro?.disconnect();
  };
}
