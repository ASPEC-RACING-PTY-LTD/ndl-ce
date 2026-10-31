import { describe, expect, it } from "vitest";
import { buildTermCatalog, canOpenTermTarget, filterTermTargets, pushRecent, targetFromNode, targetFromWorkload } from "./catalog";
import type { TermTarget } from "./types";

const alpine: TermTarget = {
  kind: "workload",
  id: "wl-a",
  name: "Alpine",
  group: "system-container",
  typeLabel: "System container",
  status: "running",
  nodeId: "node-2",
  nodeName: "no-dal-02",
  terminalReady: true,
};

const host: TermTarget = {
  kind: "node",
  id: "node-1",
  name: "no-dal-01",
  group: "host",
  typeLabel: "Host",
  status: "available",
  terminalReady: true,
};

const stopped: TermTarget = {
  kind: "workload",
  id: "wl-stop",
  name: "Test VM",
  group: "vm",
  typeLabel: "Virtual machine",
  status: "stopped",
  terminalReady: false,
};

describe("filterTermTargets", () => {
  it("matches name, type, and node", () => {
    const all = [alpine, host, stopped];
    expect(filterTermTargets(all, "alp").map((t) => t.name)).toEqual(["Alpine"]);
    expect(filterTermTargets(all, "host").map((t) => t.name)).toEqual(["no-dal-01"]);
    expect(filterTermTargets(all, "no-dal-02").map((t) => t.name)).toEqual(["Alpine"]);
    expect(filterTermTargets(all, "system container").map((t) => t.name)).toEqual(["Alpine"]);
  });
});

describe("canOpenTermTarget", () => {
  it("excludes viewers and stopped workloads", () => {
    expect(canOpenTermTarget(alpine, ["user"])).toBe(false);
    expect(canOpenTermTarget(host, ["admin"])).toBe(false);
    expect(canOpenTermTarget(host, ["superadmin"])).toBe(true);
    expect(canOpenTermTarget(alpine, ["admin"])).toBe(true);
    expect(canOpenTermTarget(stopped, ["superadmin"])).toBe(false);
  });
});

describe("buildTermCatalog", () => {
  it("omits hosts for operators and includes node metadata on workloads", () => {
    const catalog = buildTermCatalog({
      roles: ["admin"],
      nodes: [{ id: "node-2", name: "no-dal-02", status: "available" }],
      workloads: [
        { id: "wl-a", name: "Alpine", kind: "system-container", status: "running", node_id: "node-2" },
        { id: "wl-oci", name: "web", kind: "oci", status: "running" },
        { id: "wl-stop", name: "Test VM", kind: "vm", status: "stopped" },
      ],
    });
    expect(catalog.some((t) => t.group === "host")).toBe(false);
    const hit = catalog.find((t) => t.name === "Alpine");
    expect(hit?.nodeName).toBe("no-dal-02");
    expect(hit?.terminalReady).toBe(true);
    expect(catalog.find((t) => t.name === "Test VM")?.terminalReady).toBe(false);
    expect(catalog.some((t) => t.name === "web")).toBe(false);
  });

  it("includes hosts only for admin", () => {
    const catalog = buildTermCatalog({
      roles: ["superadmin"],
      nodes: [{ id: "node-1", name: "no-dal-01", status: "available" }],
      workloads: [],
    });
    expect(catalog).toHaveLength(1);
    expect(catalog[0].group).toBe("host");
  });

  it("builds host and workload identity helpers", () => {
    const node = targetFromNode({ id: "node-1", name: "no-dal-01", status: "available" });
    expect(node.group).toBe("host");
    expect(node.kind).toBe("node");
    const wl = targetFromWorkload(
      { id: "wl-a", name: "Alpine", kind: "system-container", status: "running", node_id: "node-2" },
      "no-dal-02",
    );
    expect(wl.nodeName).toBe("no-dal-02");
    expect(wl.terminalReady).toBe(true);
  });
});

describe("pushRecent", () => {
  it("keeps a target once in recents even with many sessions", () => {
    const once = pushRecent([], alpine);
    const twice = pushRecent(once, alpine);
    expect(twice).toHaveLength(1);
  });
});

describe("game server targets", () => {
  const gs = (id: string, status: string) =>
    ({ id, name: `JvS ${id}`, status, template_id: "t", template_name: "Garry's Mod", game: "gmod" }) as unknown as import("../gameservers/types").GameServer;

  it("lists game servers for terminal users, ready only while running", () => {
    const out = buildTermCatalog({ roles: ["admin"], nodes: [], workloads: [], gameServers: [gs("a", "running"), gs("b", "stopped")] });
    expect(out.map((t) => [t.kind, t.group, t.terminalReady])).toEqual([
      ["gameserver", "game-server", true],
      ["gameserver", "game-server", false],
    ]);
    expect(out[0].typeLabel).toBe("Game server · Garry's Mod");
  });

  it("leaves them out without terminal access", () => {
    expect(buildTermCatalog({ user: { roles: ["user"], grants: ["gameserver.files"] }, nodes: [], workloads: [], gameServers: [gs("a", "running")] })).toEqual([]);
  });
});

describe("recents", () => {
  it("never records hosts, which always sit at the top", () => {
    expect(pushRecent([alpine], host)).toEqual([alpine]);
  });
});
