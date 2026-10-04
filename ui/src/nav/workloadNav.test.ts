import { describe, expect, it } from "vitest";
import { buildWorkloadNav } from "./workloadNav";

describe("buildWorkloadNav", () => {
  it("shows system-container access, snapshots, and operations without VM console", () => {
    const groups = buildWorkloadNav({
      id: "wl-a",
      kind: "system-container",
      leaf: "summary",
      guestOk: false,
      mutate: true,
      dockerOn: false,
    });
    const labels = groups.flatMap((g) => [g.label, ...g.items.map((i) => i.label)]);
    expect(labels).toEqual([
      "Overview",
      "Summary",
      "Diagnostics",
      "Access",
      "Terminal",
      "Files",
      "Machine",
      "GPUs",
      "Storage",
      "Storage",
      "Protection",
      "Snapshots",
      "Operations",
      "Clone",
      "Migrate",
    ]);
    expect(groups.find((g) => g.label === "Storage")?.items[0]).toMatchObject({
      href: "/workloads/wl-a/storage",
      current: false,
    });
    expect(groups[0].items[0].current).toBe(true);
    expect(groups[0].items[1]).toMatchObject({ href: "/workloads/wl-a/diagnostics", current: false });
    const onDiagnostics = buildWorkloadNav({
      id: "wl-a",
      kind: "system-container",
      leaf: "diagnostics",
      guestOk: false,
      mutate: false,
      dockerOn: false,
    });
    expect(onDiagnostics[0].items.map((i) => [i.label, i.current])).toEqual([
      ["Summary", false],
      ["Diagnostics", true],
    ]);
  });

  it("omits unavailable VM guest IO and viewer-only operations", () => {
    const groups = buildWorkloadNav({
      id: "wl-u",
      kind: "vm",
      leaf: "console",
      guestOk: false,
      mutate: false,
      dockerOn: false,
    });
    const items = groups.flatMap((g) => g.items.map((i) => i.label));
    expect(items).toEqual(["Summary", "Console", "Snapshots"]);
  });

  it("adds VM guest IO, USB, and Docker when those surfaces exist", () => {
    const groups = buildWorkloadNav({
      id: "wl-u",
      kind: "vm",
      leaf: "clone",
      guestOk: true,
      mutate: true,
      dockerOn: true,
    });
    const items = groups.flatMap((g) => g.items.map((i) => i.label));
    expect(items).toEqual([
      "Summary",
      "Console",
      "Terminal",
      "Files",
      "USB",
      "GPUs",
      "Snapshots",
      "Clone",
      "Migrate",
      "Docker",
    ]);
    expect(groups.find((g) => g.label === "Operations")?.items[0].current).toBe(true);
  });

  it("omits access and snapshots for OCI workloads", () => {
    const groups = buildWorkloadNav({
      id: "wl-pg",
      kind: "oci",
      leaf: "summary",
      guestOk: false,
      mutate: true,
      dockerOn: false,
    });
    const items = groups.flatMap((g) => g.items.map((i) => i.label));
    expect(items).toEqual(["Summary", "GPUs", "Clone", "Migrate"]);
  });
});
