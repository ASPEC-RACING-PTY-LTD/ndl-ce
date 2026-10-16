import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { Box, BoxDock, BoxesProvider, CreateMenu, useBoxes } from "./BackupBoxes";
import { effectiveRule, sortEntries } from "./WorkloadFiles";
import type { WorkloadFileEntry } from "../generated/openapi";

afterEach(() => {
  cleanup();
  window.localStorage.clear();
});

function Page() {
  const boxes = useBoxes(["b"]);
  const list = [
    { id: "a", short: "Alpha" },
    { id: "b", short: "Beta" },
  ];
  return (
    <BoxesProvider value={boxes.value}>
      <div className="bk-bar">
        <BoxDock boxes={list} value={boxes.value} />
      </div>
      <Box id="a" title="Alpha box" short="Alpha">
        <p>alpha body</p>
      </Box>
      <Box id="b" title="Beta box" short="Beta">
        <p>beta body</p>
      </Box>
    </BoxesProvider>
  );
}

describe("Backups boxes", () => {
  it("minimizes a box into a bar button and brings it back", () => {
    render(<Page />);
    expect(screen.getByText("alpha body")).toBeVisible();
    expect(screen.queryByText("beta body")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Minimize Alpha box" }));
    expect(screen.queryByText("alpha body")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Show Alpha" }));
    expect(screen.getByText("alpha body")).toBeVisible();
    expect(JSON.parse(window.localStorage.getItem("ndl.backups.minimized") ?? "[]")).toEqual(["b"]);
  });

  it("offers Policy and Target under Create +", () => {
    const picked: string[] = [];
    render(<CreateMenu items={[{ label: "Policy", onSelect: () => picked.push("p") }, { label: "Target", onSelect: () => picked.push("t") }]} />);
    fireEvent.click(screen.getByRole("button", { name: "Create +" }));
    fireEvent.click(screen.getByRole("menuitem", { name: "Target" }));
    expect(picked).toEqual(["t"]);
    expect(screen.queryByRole("menu")).toBeNull();
  });
});

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
