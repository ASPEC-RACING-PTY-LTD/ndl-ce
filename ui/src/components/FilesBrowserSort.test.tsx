import { describe, expect, it } from "vitest";
import type { FileEntry } from "../api/phase6";
import { sortEntries } from "./FilesBrowser";

const entry = (name: string, type = "file"): FileEntry => ({ name, type, size: 0, path: name });

describe("file listing order", () => {
  it("lists folders first, then A to Z by name", () => {
    const got = sortEntries([
      entry("srcds_run"),
      entry("garrysmod", "dir"),
      entry("Bin", "dir"),
      entry("map10.bsp"),
      entry("map2.bsp"),
      entry("README.txt"),
      entry("addons", "dir"),
    ]);
    expect(got.map((e) => e.name)).toEqual(["addons", "Bin", "garrysmod", "map2.bsp", "map10.bsp", "README.txt", "srcds_run"]);
  });
});
