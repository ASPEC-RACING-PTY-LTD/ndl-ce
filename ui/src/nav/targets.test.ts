import { describe, expect, it } from "vitest";
import { buildNavTargets, filterNavTargets, groupedTargets } from "./targets";
import { targetIsLive } from "./types";

describe("buildNavTargets", () => {
  it("groups hosts, containers, VMs, and applications without inventing extra resources", () => {
    const targets = buildNavTargets({
      nodes: [
        { id: "node-1", name: "no-dal-01", status: "available" },
        { id: "node-2", name: "no-dal-02", status: "unavailable" },
      ],
      workloads: [
        { id: "wl-a", name: "Alpine", kind: "system-container", status: "running", node_id: "node-1" },
        { id: "wl-u", name: "Ubuntu", kind: "vm", status: "running", node_id: "node-2" },
        { id: "wl-stop", name: "Test VM", kind: "vm", status: "stopped", node_id: "node-1" },
        { id: "wl-pg", name: "PostgreSQL", kind: "oci", status: "running", node_id: "node-1" },
        { id: "wl-secret", name: "hidden", kind: "other", status: "running" },
      ],
      stacks: [
        {
          id: "st-1",
          name: "web",
          status: "running",
          members: [
            {
              id: "m-1",
              service_name: "caddy",
              status: "running",
              workload: { id: "wl-caddy", name: "Caddy", kind: "oci", status: "running" },
            },
            {
              id: "m-2",
              service_name: "dup",
              status: "running",
              workload: { id: "wl-pg", name: "PostgreSQL", kind: "oci", status: "running" },
            },
          ],
        },
      ],
    });

    expect(targets.map((t) => t.name)).toEqual([
      "no-dal-01",
      "no-dal-02",
      "Alpine",
      "Ubuntu",
      "Test VM",
      "PostgreSQL",
      "Caddy",
    ]);
    expect(targets.some((t) => t.name === "hidden")).toBe(false);
    expect(targetIsLive(targets[0])).toBe(true);
    expect(targetIsLive(targets[1])).toBe(false);
    expect(targets.find((t) => t.name === "Alpine")?.nodeName).toBe("no-dal-01");
    expect(targets.find((t) => t.name === "Test VM")?.terminalReady).toBe(false);
    expect(targets.find((t) => t.name === "PostgreSQL")?.group).toBe("application");
    expect(targets.filter((t) => t.id === "wl-pg")).toHaveLength(1);

    const groups = groupedTargets(targets);
    expect(groups.map((g) => g.heading)).toEqual([
      "Hosts",
      "System containers",
      "Virtual machines",
      "Applications",
    ]);
    expect(groups.find((g) => g.group === "vm")?.items.map((i) => i.name)).toEqual(["Ubuntu", "Test VM"]);
  });
});

describe("filterNavTargets", () => {
  it("matches name, type, and node", () => {
    const targets = buildNavTargets({
      nodes: [{ id: "node-1", name: "no-dal-01", status: "available" }],
      workloads: [
        { id: "wl-a", name: "Alpine", kind: "system-container", status: "running", node_id: "node-1" },
        { id: "wl-u", name: "Ubuntu", kind: "vm", status: "stopped", node_id: "node-1" },
      ],
    });
    expect(filterNavTargets(targets, "alp").map((t) => t.name)).toEqual(["Alpine"]);
    expect(filterNavTargets(targets, "virtual").map((t) => t.name)).toEqual(["Ubuntu"]);
    expect(filterNavTargets(targets, "no-dal-01").map((t) => t.name)).toEqual(["no-dal-01", "Alpine", "Ubuntu"]);
    expect(filterNavTargets(targets, "host").map((t) => t.name)).toEqual(["no-dal-01"]);
    expect(filterNavTargets(targets, "this-is-missing")).toEqual([]);
  });
});
