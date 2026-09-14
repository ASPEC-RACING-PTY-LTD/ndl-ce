import { afterEach, describe, expect, it } from "vitest";
import {
  applyTermBox,
  clampTermSize,
  layoutLimit,
  loadTermSize,
  MIN_TERM_H,
  MIN_TERM_W,
  saveTermSize,
  TERM_SIZE_KEY,
  termStyle,
} from "./size";

afterEach(() => {
  try {
    localStorage.clear();
  } catch {
    // ignore
  }
});

function mockRect(el: Element, box: { top: number; left: number; width: number; height: number; paddingRight?: number; paddingBottom?: number }) {
  Object.defineProperty(el, "getBoundingClientRect", {
    configurable: true,
    value: () => ({
      x: box.left,
      y: box.top,
      top: box.top,
      left: box.left,
      width: box.width,
      height: box.height,
      bottom: box.top + box.height,
      right: box.left + box.width,
      toJSON() {
        return {};
      },
    }),
  });
  if (el instanceof HTMLElement && (box.paddingRight != null || box.paddingBottom != null)) {
    Object.defineProperty(el, "style", {
      configurable: true,
      value: el.style,
    });
  }
}

describe("term size prefs", () => {
  it("defaults to auto and round-trips a manual size", () => {
    expect(loadTermSize()).toEqual({ mode: "auto" });
    saveTermSize({ mode: "manual", width: 720, height: 480 });
    expect(loadTermSize()).toEqual({ mode: "manual", width: 720, height: 480 });
    saveTermSize({ mode: "auto" });
    expect(loadTermSize()).toEqual({ mode: "auto" });
    expect(localStorage.getItem(TERM_SIZE_KEY)).toContain("auto");
  });

  it("treats invalid stored values as auto", () => {
    localStorage.setItem(TERM_SIZE_KEY, "nope");
    expect(loadTermSize()).toEqual({ mode: "auto" });
    localStorage.setItem(TERM_SIZE_KEY, JSON.stringify({ mode: "manual", width: "tall" }));
    expect(loadTermSize()).toEqual({ mode: "auto" });
  });
});

describe("clampTermSize", () => {
  it("enforces minimums and does not exceed the usable max", () => {
    expect(clampTermSize(10, 10, 2000, 1000)).toEqual({ width: MIN_TERM_W, height: MIN_TERM_H });
    expect(clampTermSize(900, 700, 800, 600)).toEqual({ width: 800, height: 600 });
    expect(clampTermSize(400, 300, 0, 0)).toEqual({ width: 400, height: 300 });
    expect(clampTermSize(10, 10, 0, 0)).toEqual({ width: MIN_TERM_W, height: MIN_TERM_H });
    expect(clampTermSize(900, 700, 100, 80)).toEqual({ width: 100, height: 80 });
    expect(clampTermSize(Number.NaN, Number.NaN, 800, 600)).toEqual({ width: MIN_TERM_W, height: MIN_TERM_H });
  });
});

describe("layoutLimit and applyTermBox", () => {
  it("measures remaining space from the wrap to the shell main padding box", () => {
    const main = document.createElement("div");
    main.className = "shell-main";
    const wrap = document.createElement("div");
    main.appendChild(wrap);
    document.body.appendChild(main);
    mockRect(main, { top: 0, left: 0, width: 1000, height: 800 });
    mockRect(wrap, { top: 200, left: 100, width: 400, height: 240 });
    const original = window.getComputedStyle;
    window.getComputedStyle = ((el: Element) => {
      if (el === main) {
        return { paddingRight: "16px", paddingBottom: "16px" } as CSSStyleDeclaration;
      }
      return original.call(window, el);
    }) as typeof window.getComputedStyle;

    expect(layoutLimit(wrap)).toEqual({ width: 884, height: 584 });
    const auto = applyTermBox(wrap, { mode: "auto" });
    expect(wrap.dataset.termSize).toBe("auto");
    expect(wrap.style.width).toBe("100%");
    expect(wrap.style.height).toBe("584px");
    expect(auto.height).toBe(584);

    const manual = applyTermBox(wrap, { mode: "manual", width: 500, height: 360 });
    expect(wrap.dataset.termSize).toBe("manual");
    expect(wrap.style.width).toBe("500px");
    expect(wrap.style.height).toBe("360px");
    expect(manual).toEqual({ width: 500, height: 360 });

    const clamped = applyTermBox(wrap, { mode: "manual", width: 5000, height: 5000 });
    expect(clamped).toEqual({ width: 884, height: 584 });

    expect(termStyle({ mode: "manual", width: 500, height: 360 }, { width: 884, height: 584 })).toEqual({
      width: "500px",
      height: "360px",
    });
    expect(termStyle({ mode: "auto" }, { width: 884, height: 584 }).height).toBe("584px");

    window.getComputedStyle = original;
    main.remove();
  });
});
