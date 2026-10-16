import { afterEach, describe, expect, it, vi } from "vitest";
import { applyNavOrder, moveNavItem } from "./nav/disclosure";
import { NAV_MODULES } from "./nav/modules";
import { bindPrefsUser, flushPrefs, getPref, setPref } from "./prefs";
import { applyTheme, hexToOklch, parseThemePrefs, resolveTheme } from "./theme";

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("interface preferences", () => {
  it("cache locally at once and save to the profile in one batch", async () => {
    const calls: string[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (_url: string, init?: RequestInit) => {
        calls.push(String(init?.body));
        return new Response("{}", { status: 200 });
      }),
    );
    bindPrefsUser("u1", { theme: { mode: "dark" } });
    expect(getPref("theme", null)).toEqual({ mode: "dark" });
    setPref("nav.order", { Compute: ["files"] });
    setPref("table.x", { hidden: ["a"] });
    expect(JSON.parse(localStorage.getItem("ndl-ui-prefs:u1") ?? "{}")["table.x"]).toEqual({ hidden: ["a"] });
    await flushPrefs();
    expect(calls).toHaveLength(1);
    expect(JSON.parse(calls[0])).toEqual({ ui_prefs: { "nav.order": { Compute: ["files"] }, "table.x": { hidden: ["a"] } } });
  });

  it("keep each user's preferences apart and upload old local ones once", async () => {
    const calls: string[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (_url: string, init?: RequestInit) => {
        calls.push(String(init?.body));
        return new Response("{}", { status: 200 });
      }),
    );
    localStorage.setItem("ndl-ui-prefs:u2", JSON.stringify({ theme: { mode: "light" } }));
    bindPrefsUser("u2", {});
    expect(getPref("theme", null)).toEqual({ mode: "light" });
    await flushPrefs();
    expect(JSON.parse(calls[0])).toEqual({ ui_prefs: { theme: { mode: "light" } } });
    bindPrefsUser("u3", { theme: { mode: "dark" } });
    expect(getPref("theme", null)).toEqual({ mode: "dark" });
  });
});

describe("themes", () => {
  it("convert picked colours and apply them as tokens", () => {
    const blue = hexToOklch("#3b82f6");
    expect(Math.round(blue.h)).toBeGreaterThan(250);
    expect(Math.round(blue.h)).toBeLessThan(270);
    const t = resolveTheme({ mode: "dark", theme: "nodal", accent: "#ec4899" });
    expect(t.accentHue).toBeGreaterThan(340);
    applyTheme({ mode: "light", theme: "graphite", accent: null });
    const root = document.documentElement;
    expect(root.dataset.mode).toBe("light");
    expect(root.style.getPropertyValue("--ndl-tint")).toBe("0");
    applyTheme({ mode: "system", theme: "nodal", accent: "rainbow" });
    expect(root.dataset.mode).toBeUndefined();
    expect(root.dataset.accent).toBe("rainbow");
  });

  it("ignore saved values that are not themes", () => {
    expect(parseThemePrefs({ mode: "neon", theme: "nope", accent: "red;}" })).toEqual({ mode: "system", theme: "nodal", accent: null });
  });
});

describe("sidebar order", () => {
  it("sorts entries within their category and keeps new ones after", () => {
    const compute = NAV_MODULES.filter((m) => m.group === "Compute");
    const groups = [{ label: "Compute", items: compute }];
    const order = moveNavItem({}, "Compute", compute.map((m) => m.id), "files", "workloads", false);
    const sorted = applyNavOrder(groups, order)[0].items.map((m) => m.id);
    expect(sorted.indexOf("files")).toBeLessThan(sorted.indexOf("workloads"));
    // An entry not in the saved order keeps its place after the saved ones.
    const partial = applyNavOrder(groups, { Compute: ["terminal"] })[0].items.map((m) => m.id);
    expect(partial[0]).toBe("terminal");
    expect(partial).toHaveLength(compute.length);
  });
});
