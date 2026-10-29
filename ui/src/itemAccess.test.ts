import { describe, expect, it } from "vitest";
import { modernNav, modernShow } from "./components/Shell";
import type { MeResponse } from "./generated/openapi";
import { parseDisclosurePrefs, visibleModules } from "./nav/disclosure";
import { buildWorkloadNav, ITEM_WORKLOAD_PAGES } from "./nav/workloadNav";
import { gameServerTabList, gameTabAllow } from "./nav/gameServersNav";
import { ADMIN_SECTIONS, INSTANCE_SECTIONS, itemOnlyPathOpen, visibleSections } from "./nav/sections";
import { visiblePaletteActions } from "./palette";
import {
  canOnItem,
  dockerItemId,
  firstItemArea,
  grantedItemIds,
  hasItemAccess,
  hasRoleAccess,
  itemAllows,
  itemPerms,
} from "./rbac";
import { buildTermCatalog, canOpenTermTarget, mayOpenTerminal } from "./terminal/catalog";
import type { NodeSummary } from "./api/phase2";
import type { Workload } from "./api/phase5";

const base: MeResponse = {
  user_id: "u-1",
  username: "guest",
  roles: [],
  edition: "ce",
  ux_level: "guided",
  expert_ack: false,
  grants: ["identity.read", "identity.mfa"],
};

/** A person without a role who holds one workload: view, power and a terminal. */
const workloadOnly: MeResponse = {
  ...base,
  item_access: { workload: ["compute.read", "compute.lifecycle", "terminal.open"] },
  item_grants: [{ kind: "workload", id: "wl-a", permissions: ["compute.read", "compute.lifecycle", "terminal.open"] }],
};

const nothing: MeResponse = { ...base };

const superadmin = { roles: ["superadmin"], grants: ["*"] };
const viewer = { roles: ["user"], grants: ["identity.read", "compute.read", "files.read", "node.read"] };

const allOff = () => false;

describe("item grant helpers", () => {
  it("reads one item's permissions and checks them", () => {
    expect(itemPerms(workloadOnly, "workload", "wl-a")).toEqual(["compute.read", "compute.lifecycle", "terminal.open"]);
    expect(itemPerms(workloadOnly, "workload", "wl-b")).toEqual([]);
    expect(canOnItem(workloadOnly, "workload", "wl-a", "compute.lifecycle")).toBe(true);
    expect(canOnItem(workloadOnly, "workload", "wl-a", "compute.delete")).toBe(false);
    expect(canOnItem(workloadOnly, "workload", "wl-b", "compute.lifecycle")).toBe(false);
    expect(canOnItem(workloadOnly, "gameserver", "wl-a", "compute.lifecycle")).toBe(false);
    expect(itemAllows(workloadOnly, "workload", "wl-a", "terminal.open")).toBe(true);
  });

  it("lets a role permission through on any item", () => {
    expect(canOnItem(superadmin, "workload", "anything", "compute.delete")).toBe(true);
    expect(canOnItem(viewer, "workload", "anything", "compute.read")).toBe(true);
    expect(canOnItem(viewer, "workload", "anything", "compute.lifecycle")).toBe(false);
    // itemAllows ignores roles on purpose: it is the grant alone.
    expect(itemAllows(superadmin, "workload", "anything", "compute.read")).toBe(false);
  });

  it("knows which kinds a person holds items of", () => {
    expect(hasItemAccess(workloadOnly, "workload")).toBe(true);
    expect(hasItemAccess(workloadOnly, "workload", "terminal.open")).toBe(true);
    expect(hasItemAccess(workloadOnly, "workload", "files.read")).toBe(false);
    expect(hasItemAccess(workloadOnly, "node")).toBe(false);
    expect(hasItemAccess(null, "workload")).toBe(false);
    expect(grantedItemIds(workloadOnly, "workload", "terminal.open")).toEqual(["wl-a"]);
    // item_access alone (no item_grants) still counts for the kind.
    expect(hasItemAccess({ ...base, item_access: { docker: ["compute.read"] } }, "docker", "compute.read")).toBe(true);
  });

  it("builds Docker item ids without a leading slash", () => {
    expect(dockerItemId("wl-a", "/web")).toBe("wl-a/web");
    expect(dockerItemId("host", "db")).toBe("host/db");
  });

  it("tells role holders from item-only people", () => {
    expect(hasRoleAccess(workloadOnly)).toBe(false);
    expect(hasRoleAccess(nothing)).toBe(false);
    expect(hasRoleAccess(viewer)).toBe(true);
    expect(hasRoleAccess(superadmin)).toBe(true);
    expect(hasRoleAccess(null)).toBe(false);
  });

  it("starts an item-only person in their first area", () => {
    expect(firstItemArea(workloadOnly)).toBe("/instance");
    expect(firstItemArea({ ...base, item_grants: [{ kind: "gameserver", id: "gs-1", permissions: ["gameserver.read"] }] })).toBe("/game-servers");
    expect(firstItemArea({ ...base, item_grants: [{ kind: "docker", id: "m/web", permissions: ["compute.read"] }] })).toBe("/docker");
    expect(firstItemArea({ ...base, item_grants: [{ kind: "node", id: "n-1", permissions: ["node.read"] }] })).toBe("/node");
    expect(firstItemArea(nothing)).toBeNull();
  });
});

describe("navigation for a person without a role", () => {
  it("shows Workloads and hides System, IAM, Storage and the rest", () => {
    const inst = visibleSections(INSTANCE_SECTIONS, workloadOnly, allOff).map((s) => s.id);
    expect(inst).toEqual(["workloads", "vms"]);
    expect(visibleSections(INSTANCE_SECTIONS, workloadOnly, allOff).find((s) => s.id === "workloads")?.children).toEqual([]);
    expect(visibleSections(ADMIN_SECTIONS, workloadOnly, () => true)).toEqual([]);

    const classic = visibleModules(parseDisclosurePrefs(null), {}, workloadOnly.roles, workloadOnly.grants, workloadOnly).map((m) => m.id);
    expect(classic).toEqual(["workloads", "terminal"]);
    for (const hidden of ["dashboard", "storage", "network", "iam", "add-features", "backups", "audit", "docs", "node"]) {
      expect(classic).not.toContain(hidden);
    }
  });

  it("keeps the modern sidebar to the areas they can use", () => {
    const labels = modernNav(false, modernShow(workloadOnly, allOff)).flatMap((g) => g.items.map((i) => i.label));
    expect(labels).toEqual(["Instance", "Terminal"]);
    expect(modernNav(false, modernShow(nothing, allOff)).flatMap((g) => g.items)).toEqual([]);
  });

  it("opens Infrastructure only at the node or Docker they hold", () => {
    const nodeOnly: MeResponse = { ...base, item_grants: [{ kind: "node", id: "n-1", permissions: ["node.read", "terminal.open"] }] };
    const infra = visibleSections(ADMIN_SECTIONS, nodeOnly, allOff);
    expect(infra.map((s) => s.id)).toEqual(["infrastructure"]);
    expect(infra[0].children?.map((c) => c.id)).toEqual(["node"]);

    const dockerOnly: MeResponse = { ...base, item_grants: [{ kind: "docker", id: "m/web", permissions: ["compute.read"] }] };
    // The feature list is closed to them, so the grant stands in for the Docker feature.
    const dk = visibleSections(ADMIN_SECTIONS, dockerOnly, allOff);
    expect(dk.map((s) => s.id)).toEqual(["infrastructure"]);
    expect(dk[0].href).toBe("/docker");
    expect(dk[0].children?.map((c) => c.id)).toEqual(["docker"]);

    const gsOnly: MeResponse = { ...base, item_grants: [{ kind: "gameserver", id: "gs-1", permissions: ["gameserver.read"] }] };
    expect(visibleSections(INSTANCE_SECTIONS, gsOnly, allOff).map((s) => s.id)).toEqual(["game-servers"]);
  });

  it("limits typed addresses to their own items", () => {
    expect(itemOnlyPathOpen("/me", workloadOnly)).toBe(true);
    expect(itemOnlyPathOpen("/instance", workloadOnly)).toBe(true);
    expect(itemOnlyPathOpen("/workloads/wl-a", workloadOnly)).toBe(true);
    expect(itemOnlyPathOpen("/workloads/wl-a/terminal", workloadOnly)).toBe(true);
    expect(itemOnlyPathOpen("/workloads/wl-a/files", workloadOnly)).toBe(false);
    expect(itemOnlyPathOpen("/workloads/wl-a/storage", workloadOnly)).toBe(false);
    expect(itemOnlyPathOpen("/workloads/wl-b", workloadOnly)).toBe(false);
    expect(itemOnlyPathOpen("/workloads/new/vm", workloadOnly)).toBe(false);
    expect(itemOnlyPathOpen("/terminal", workloadOnly)).toBe(true);
    expect(itemOnlyPathOpen("/files", workloadOnly)).toBe(false);
    for (const closed of ["/storage", "/network", "/settings/features", "/users", "/backups", "/docker", "/node", "/game-servers"]) {
      expect(itemOnlyPathOpen(closed, workloadOnly)).toBe(false);
    }
  });

  it("filters the search palette to their pages", () => {
    const ids = visiblePaletteActions(workloadOnly.roles, workloadOnly).map((a) => a.id);
    expect(ids).toEqual(expect.arrayContaining(["workloads", "terminal", "account"]));
    for (const hidden of ["storage", "network", "features", "users", "audit", "backups", "create-vm", "dashboard"]) {
      expect(ids).not.toContain(hidden);
    }
  });

  it("narrows a workload's own pages to its grant", () => {
    const allow = (page: string) => Boolean(ITEM_WORKLOAD_PAGES[page]) && canOnItem(workloadOnly, "workload", "wl-a", ITEM_WORKLOAD_PAGES[page]);
    const labels = buildWorkloadNav({ id: "wl-a", kind: "system-container", leaf: "summary", guestOk: false, mutate: false, dockerOn: false, allow })
      .flatMap((g) => g.items.map((i) => i.label));
    // Snapshots list with view access; Files, Storage, Components and Operations need more.
    expect(labels).toEqual(["Summary", "Terminal", "Snapshots"]);
  });

  it("narrows a game server's tabs to its grant", () => {
    const server = { capabilities: ["console", "files", "backups", "mods"] };
    const perms = ["gameserver.read", "gameserver.console"];
    const tabs = gameServerTabList(server, gameTabAllow(false, (p) => perms.includes(p))).map((t) => t.id);
    expect(tabs).toEqual(["overview", "console"]);
    expect(gameTabAllow(true, () => false)).toBeUndefined();
  });

  it("lists terminal targets they hold a terminal grant on", () => {
    const workloads = [
      { id: "wl-a", name: "a", kind: "system-container", status: "running" },
      { id: "wl-b", name: "b", kind: "system-container", status: "running" },
    ] as Workload[];
    const nodes = [{ id: "n-1", name: "host", status: "ready" }] as NodeSummary[];
    const targets = buildTermCatalog({ roles: [], user: workloadOnly, nodes, workloads });
    expect(targets.map((t) => `${t.kind}:${t.id}`)).toEqual(["workload:wl-a"]);
    expect(canOpenTermTarget(targets[0], [], workloadOnly)).toBe(true);
    expect(mayOpenTerminal("node", "n-1", [], workloadOnly)).toBe(false);
    const hostUser: MeResponse = { ...base, item_grants: [{ kind: "node", id: "n-1", permissions: ["node.read", "terminal.open"] }] };
    expect(buildTermCatalog({ roles: [], user: hostUser, nodes, workloads }).map((t) => t.id)).toEqual(["n-1"]);
    const dockerUser: MeResponse = { ...base, item_grants: [{ kind: "docker", id: "m/web", permissions: ["compute.read", "terminal.open"] }] };
    expect(mayOpenTerminal("docker", "m/abc123", [], dockerUser, "web")).toBe(true);
    expect(mayOpenTerminal("docker", "m/abc123", [], dockerUser, "db")).toBe(false);
  });
});

describe("navigation for role holders is unchanged", () => {
  it("keeps every area and page for an admin", () => {
    expect(visibleSections(INSTANCE_SECTIONS, superadmin, (id) => id === "gameservers").map((s) => s.id)).toEqual([
      "workloads",
      "vms",
      "game-servers",
      "templates",
      "registries",
    ]);
    expect(modernNav(true, modernShow(superadmin, allOff)).flatMap((g) => g.items.map((i) => i.label))).toEqual([
      "Dashboard",
      "Instance",
      "Terminal",
      "Files",
      "Administration",
    ]);
    const viewerModules = visibleModules(parseDisclosurePrefs(null), {}, viewer.roles, viewer.grants, viewer).map((m) => m.id);
    expect(viewerModules).toEqual(expect.arrayContaining(["dashboard", "workloads", "storage", "network", "docs", "audit"]));
    expect(visiblePaletteActions(["user"], viewer).some((a) => a.id === "storage")).toBe(true);
  });

  it("adds a role holder's item grants without taking anything away", () => {
    const viewerWithTerm = { ...viewer, item_grants: [{ kind: "workload", id: "wl-a", permissions: ["compute.read", "terminal.open"] }] };
    const workloads = [{ id: "wl-a", name: "a", kind: "system-container", status: "running" }] as Workload[];
    // The User role cannot open terminals; the grant opens this one workload.
    expect(buildTermCatalog({ roles: ["user"], nodes: [], workloads })).toEqual([]);
    expect(buildTermCatalog({ roles: ["user"], user: viewerWithTerm, nodes: [], workloads }).map((t) => t.id)).toEqual(["wl-a"]);
    // An admin keeps host terminals out of reach, as before.
    expect(mayOpenTerminal("node", "n-1", ["admin"], { roles: ["admin"], grants: ["terminal.open"] })).toBe(false);
    expect(mayOpenTerminal("node", "n-1", ["superadmin"])).toBe(true);
  });
});
