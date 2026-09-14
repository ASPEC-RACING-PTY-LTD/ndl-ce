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
  it("hides native scrollbar chrome site-wide while leaving overflow scrolling", () => {
    const nav = rule(".sidebar-nav {");
    expect(nav).toContain("overflow-y: auto");
    expect(nav).not.toContain("overflow: hidden");
    expect(nav).not.toContain("overflow-y: hidden");

    expect(css).toContain("scrollbar-width: none");
    expect(css).toContain("-ms-overflow-style: none");
    expect(css).toContain("*::-webkit-scrollbar");
    expect(css).toContain("display: none");
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

  it("keeps contextual rows content-sized instead of stretching to fill the sidebar", () => {
    const nav = rule(".sidebar-nav {");
    expect(nav).toContain("flex-direction: column");
    expect(nav).toContain("justify-content: flex-start");
    expect(nav).toContain("align-content: start");
    expect(nav).not.toContain("space-between");
    expect(nav).not.toContain("display: grid");

    const tree = rule(".ctx-tree {");
    expect(tree).toContain("align-content: start");
    expect(tree).toContain("flex: none");

    const actions = rule(".ctx-actions {");
    expect(actions).toContain("align-content: start");
    expect(actions).toContain("flex: none");

    const item = rule(".ctx-item {");
    expect(item).toContain("min-height: 2.25rem");
    expect(item).toContain("flex: none");

    const search = rule(".ctx-search {");
    expect(search).toContain("min-height: 2.5rem");
    expect(search).toContain("height: 2.5rem");
    expect(search).toContain("flex: none");

    const collapse = rule(".sidebar-collapse {");
    expect(collapse).not.toContain("margin-top: auto");

    const footer = rule(".sidebar-footer {");
    expect(footer).toContain("border-top: 1px solid var(--line)");
    expect(footer).toContain("flex: none");
  });
});
