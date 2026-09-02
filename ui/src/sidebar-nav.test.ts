import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const css = readFileSync(join(dirname(fileURLToPath(import.meta.url)), "styles.css"), "utf8");

function rule(selector: string): string {
  const idx = css.indexOf(selector);
  if (idx < 0) {
    throw new Error(`missing ${selector}`);
  }
  const start = css.indexOf("{", idx);
  const end = css.indexOf("}", start);
  return css.slice(start, end + 1);
}

describe("sidebar-nav scrollbar", () => {
  it("keeps the nav scrollable while hiding only that scrollbar", () => {
    const nav = rule(".sidebar-nav {");
    expect(nav).toContain("overflow-y: auto");
    expect(nav).toContain("scrollbar-width: none");
    expect(nav).toContain("-ms-overflow-style: none");
    expect(nav).not.toContain("overflow: hidden");
    expect(nav).not.toContain("overflow-y: hidden");

    const webkit = rule(".sidebar-nav::-webkit-scrollbar {");
    expect(webkit).toContain("display: none");
    expect(webkit).toContain("width: 0");

    expect(css.split("scrollbar-width: none").length - 1).toBe(1);
    expect(css.split("::-webkit-scrollbar").length - 1).toBe(1);
    expect(css).toContain(".sidebar-nav::-webkit-scrollbar");
  });

  it("lets the terminal fill remaining layout instead of a fixed viewport offset", () => {
    const page = rule(".page-term {");
    expect(page).toContain("height: 100%");
    expect(page).not.toContain("100vh - 5.5rem");
    const wrap = rule(".term-wrap {");
    expect(wrap).toContain("resize: none");
    expect(wrap).not.toContain("min-height: 24rem");
    expect(css).toContain('.page-term .term-wrap[data-term-size="manual"]');
  });

  it("truncates long contextual names without a second native scrollbar", () => {
    const label = rule(".ctx-item-label {");
    expect(label).toContain("text-overflow: ellipsis");
    expect(label).toContain("overflow: hidden");
  });
});
