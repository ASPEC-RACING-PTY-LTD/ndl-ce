import { describe, expect, it } from "vitest";
import { moveId, orderWorkloads, shiftId } from "./workloadOrder";

const items = [
  { id: "1", name: "web10" },
  { id: "2", name: "Alpha" },
  { id: "3", name: "web2" },
  { id: "4", name: "beta" },
];

describe("workload ordering", () => {
  it("sorts alphabetically, case-insensitively and with natural numbers", () => {
    expect(orderWorkloads(items, "name", []).map((w) => w.name)).toEqual(["Alpha", "beta", "web2", "web10"]);
    expect(orderWorkloads(items, "name-desc", []).map((w) => w.name)).toEqual(["web10", "web2", "beta", "Alpha"]);
  });

  it("keeps the saved custom order and appends new workloads alphabetically", () => {
    expect(orderWorkloads(items, "custom", ["3", "1", "gone"]).map((w) => w.id)).toEqual(["3", "1", "2", "4"]);
  });

  it("moves rows by drag target and by keyboard", () => {
    expect(moveId(["a", "b", "c", "d"], "d", "b")).toEqual(["a", "d", "b", "c"]);
    expect(moveId(["a", "b", "c", "d"], "a", "c")).toEqual(["b", "c", "a", "d"]);
    expect(shiftId(["a", "b", "c"], "c", -1)).toEqual(["a", "c", "b"]);
    expect(shiftId(["a", "b", "c"], "a", -1)).toEqual(["a", "b", "c"]);
  });
});
