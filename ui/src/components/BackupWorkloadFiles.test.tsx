import { describe, expect, it } from "vitest";
import { effectiveRule, sortEntries } from "./WorkloadFiles";
import type { WorkloadFileEntry } from "../generated/openapi";

describe("Workload files", () => {
  const e = (name: string, dir: boolean, size: number, mtime: number): WorkloadFileEntry => ({ name, path: "/" + name, dir, size, mtime, label: "" });
  const entries = [e("b.txt", false, 5, 3), e("Zeta", true, 1, 1), e("alpha", true, 9, 2)];

  it("sorts folders first A to Z by default, or by size or date", () => {
    expect(sortEntries(entries, "name").map((x) => x.name)).toEqual(["alpha", "Zeta", "b.txt"]);
    expect(sortEntries(entries, "size").map((x) => x.name)).toEqual(["alpha", "b.txt", "Zeta"]);
    expect(sortEntries(entries, "date").map((x) => x.name)).toEqual(["b.txt", "alpha", "Zeta"]);
  });

  it("applies the most specific rule", () => {
    expect(effectiveRule("/var/cache/x", ["/var/cache"], [])).toBe("skip");
    expect(effectiveRule("/var/cache/app/k", ["/var/cache"], ["/var/cache/app"])).toBe("keep");
    expect(effectiveRule("/etc", ["/var/cache"], [])).toBe("");
  });
});
