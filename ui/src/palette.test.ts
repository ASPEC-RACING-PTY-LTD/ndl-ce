import { describe, expect, it } from "vitest";
import { filterPaletteActions, visiblePaletteActions } from "./palette";

describe("command palette authorization", () => {
  it("hides Create VM and other mutations from a viewer, including Expert viewers", () => {
    const actions = visiblePaletteActions(["viewer"]);
    expect(actions.some((a) => a.id === "create-vm")).toBe(false);
    expect(actions.some((a) => a.id === "import-vm")).toBe(false);
    expect(actions.some((a) => a.id === "templates")).toBe(false);
    expect(actions.some((a) => a.id === "create-ct")).toBe(false);
    expect(actions.some((a) => a.id === "audit")).toBe(false);
    expect(actions.some((a) => a.id === "updates")).toBe(false);
    expect(actions.some((a) => a.id === "tasks")).toBe(true);
    expect(actions.some((a) => a.id === "dashboard")).toBe(true);
    expect(actions.some((a) => a.id === "terminal")).toBe(true);
  });

  it("lists Create VM for operator and admin", () => {
    expect(visiblePaletteActions(["operator"]).some((a) => a.id === "create-vm")).toBe(true);
    expect(visiblePaletteActions(["admin"]).some((a) => a.id === "create-vm")).toBe(true);
    expect(visiblePaletteActions(["admin"]).some((a) => a.id === "audit")).toBe(true);
  });

  it("filters by label and keywords", () => {
    const actions = visiblePaletteActions(["admin"]);
    expect(filterPaletteActions(actions, "create vm").some((a) => a.id === "create-vm")).toBe(true);
    expect(filterPaletteActions(actions, "qemu").some((a) => a.id === "create-vm")).toBe(true);
    expect(filterPaletteActions(actions, "no-such-action")).toHaveLength(0);
  });
});
