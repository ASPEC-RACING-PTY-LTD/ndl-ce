import { describe, expect, it } from "vitest";
import type { Workload } from "./api/phase5";
import type { WorkloadExtra } from "./api/client";
import { FLOW_DISABLED_BY_UPDATE, flowsOffByUpdate } from "./automation/updateDisabled";
import { autoGrid, freeRect } from "./components/TerminalGrid";
import { mergeRows } from "./components/shell/GlobalSearch";
import { modernNav } from "./components/Shell";
import { createHref, num, str, strMap } from "./instanceTemplates";
import { createEntries } from "./nav/AreaFrames";
import { ADMIN_SECTIONS, INSTANCE_SECTIONS, isAdminPath, isInstancePath, visibleSections } from "./nav/sections";
import { popularityRank } from "./pages/FeaturesPage";
import { filterInstances, parseWidgets } from "./pages/InstanceHomePage";
import { nodeTab, parseLayout, seriesStats } from "./pages/NodePage";
import { validAvatarURL } from "./pages/ProfilePage";
import { groupExtras } from "./workloadExtras";

const superadmin = { roles: ["superadmin"], grants: [] };
const plain = { roles: ["user"], grants: [] };

describe("navigation model", () => {
  it("puts pages in the right area", () => {
    expect(isAdminPath("/users")).toBe(true);
    expect(isAdminPath("/settings/updates")).toBe(true);
    expect(isAdminPath("/admin/theme")).toBe(true);
    expect(isAdminPath("/workloads")).toBe(false);
    expect(isInstancePath("/workloads/abc")).toBe(true);
    expect(isInstancePath("/instance/vms")).toBe(true);
    expect(isInstancePath("/node")).toBe(false);
    expect(isInstancePath("/docker")).toBe(false);
    expect(isAdminPath("/docker/uptime")).toBe(true);
    expect(isAdminPath("/codehold/cli")).toBe(true);
  });

  it("shows only areas a person may open, with features on", () => {
    const all = visibleSections(ADMIN_SECTIONS, superadmin, () => false).map((s) => s.id);
    expect(all).toContain("iam");
    expect(all).not.toContain("intelligence");
    expect(visibleSections(ADMIN_SECTIONS, plain, () => true).map((s) => s.id)).not.toContain("updates");
    const inst = visibleSections(INSTANCE_SECTIONS, superadmin, (id) => id === "gameservers").map((s) => s.id);
    expect(inst).toEqual(["workloads", "vms", "game-servers", "templates", "registries"]);
    // Docker and CodeHold live under Infrastructure.
    const infra = visibleSections(ADMIN_SECTIONS, superadmin, () => true).find((s) => s.id === "infrastructure");
    expect(infra?.children?.map((c) => c.id)).toEqual(expect.arrayContaining(["docker", "codehold", "codehold-cli"]));
    expect(visibleSections(ADMIN_SECTIONS, superadmin, () => false).find((s) => s.id === "infrastructure")?.children?.map((c) => c.id)).not.toContain("docker");
  });

  it("keeps the modern sidebar short", () => {
    const labels = modernNav(true).flatMap((g) => g.items.map((i) => i.label));
    expect(labels).toEqual(["Dashboard", "Instance", "Terminal", "Files", "Administration"]);
    expect(modernNav(false).some((g) => g.label === "Administration")).toBe(false);
  });

  it("puts every way to create an instance in + Create only", () => {
    for (const sec of INSTANCE_SECTIONS) {
      for (const c of sec.children ?? []) {
        expect(c.href).not.toMatch(/\/new\/|\/create|\/import/);
      }
    }
    expect(createEntries([], () => true).map((e) => e.href)).toEqual(
      expect.arrayContaining(["/workloads/new/system-container", "/workloads/new/vm", "/workloads/import", "/workloads/new/oci", "/game-servers/create"]),
    );
  });

  it("orders + Create like the Instance bar", () => {
    const on = () => true;
    expect(createEntries(["vms", "workloads", "oci", "game-servers"], on).map((e) => e.section)).toEqual(["vms", "vms", "workloads", "oci", "game-servers"]);
    expect(createEntries([], (id) => id !== "oci").map((e) => e.section)).not.toContain("oci");
  });
});

describe("header search", () => {
  it("puts pages first and groups server results by kind", () => {
    const rows = mergeRows(
      [{ key: "page:x", title: "Storage", href: "/storage", group: "Pages" }],
      [
        { kind: "user", id: "1", title: "alice", link: "/users?u=1" },
        { kind: "workload", id: "2", title: "web", link: "/workloads/2" },
        { kind: "user", id: "3", title: "bob", link: "/users?u=3" },
      ],
    );
    expect(rows.map((r) => r.title)).toEqual(["Storage", "alice", "bob", "web"]);
    expect(rows[1].group).toBe("Users");
  });
});

describe("terminal grid", () => {
  it("has room for any number of sessions", () => {
    expect(autoGrid(1)).toEqual({ cols: 1, rows: 1 });
    expect(autoGrid(5)).toEqual({ cols: 3, rows: 2 });
    expect(autoGrid(30).cols * autoGrid(30).rows).toBeGreaterThanOrEqual(30);
  });

  it("keeps free places inside the area", () => {
    const r = freeRect({ x: 95, y: -10, w: 50, h: 500, z: 3 }, 0);
    expect(r.x + r.w).toBeLessThanOrEqual(100);
    expect(r.y).toBe(0);
    expect(r.h).toBe(100);
    expect(freeRect(null, 2)).toMatchObject({ w: 48, h: 46 });
  });
});

describe("instance overview", () => {
  const items = [
    { id: "a", name: "web", kind: "system-container", status: "running" },
    { id: "b", name: "db", kind: "vm", status: "stopped" },
    { id: "c", name: "broken", kind: "oci", status: "failed" },
  ] as Workload[];

  it("filters by text, type and status", () => {
    expect(filterInstances(items, "we", "all", "all").map((w) => w.id)).toEqual(["a"]);
    expect(filterInstances(items, "", "vm", "all").map((w) => w.id)).toEqual(["b"]);
    expect(filterInstances(items, "", "all", "stopped").map((w) => w.id)).toEqual(["b", "c"]);
    expect(filterInstances(items, "", "all", "attention").map((w) => w.id)).toEqual(["c"]);
  });

  it("keeps known widgets in order", () => {
    expect(parseWidgets(["running", "nope", "types", "running"])).toEqual(["running", "types"]);
    expect(parseWidgets(null)).toHaveLength(4);
  });
});

describe("node page", () => {
  it("sends old addresses to their new sections", () => {
    expect(nodeTab("/node/gpu")).toBe("hardware");
    expect(nodeTab("/node/events")).toBe("activity");
    expect(nodeTab("/node/logs")).toBe("activity");
    expect(nodeTab("/node")).toBe("summary");
  });

  it("summarises a metric series", () => {
    const st = seriesStats({ name: "cpu", status: "ok", points: [{ time: "", value: 1 }, { time: "", value: 3 }, { time: "", value: 2 }] });
    expect(st).toEqual({ last: 2, min: 1, max: 3, avg: 2 });
    expect(seriesStats({ name: "x", status: "ok", points: [] })).toEqual({});
  });

  it("keeps a saved layout to known sections", () => {
    expect(parseLayout(["b", "zz", "a"], ["a", "b", "c"] as const)).toEqual(["b", "a"]);
    expect(parseLayout(undefined, ["a", "b"] as const)).toEqual(["a", "b"]);
  });
});

describe("components and templates", () => {
  it("groups pickable extras and hides baseline tools", () => {
    const catalog = [
      { id: "curl", name: "curl", description: "", category: "utilities", available: true, baseline: true },
      { id: "docker", name: "Docker", description: "", category: "containers", available: true },
      { id: "redis", name: "Redis", description: "", category: "databases", available: true },
    ] as WorkloadExtra[];
    const groups = groupExtras(catalog);
    expect(groups.map((g) => g.label)).toEqual(["Containers", "Databases"]);
    expect(groups.flatMap((g) => g.items.map((i) => i.id))).not.toContain("curl");
  });

  it("reads template specs safely and links each kind to its create page", () => {
    const spec = { cpus: 2, memory_bytes: -1, image: "nginx", env: { A: "1", B: 2 } };
    expect(num(spec, "cpus")).toBe(2);
    expect(num(spec, "memory_bytes")).toBeUndefined();
    expect(str(spec, "image")).toBe("nginx");
    expect(strMap(spec, "env")).toEqual({ A: "1" });
    expect(createHref({ id: "t 1", kind: "oci", name: "x", spec: {} })).toBe("/workloads/new/oci?template=t%201");
    expect(createHref({ id: "t", kind: "game-server", name: "x", spec: { game_template_id: "mc" } })).toBe("/game-servers/create?template=t&game=mc");
    expect(createHref({ id: "t", kind: "system-container", name: "x", spec: {} })).toBe("/workloads/new/system-container?template=t");
  });
});

describe("other pages", () => {
  it("sorts features by how widely they are used", () => {
    expect(popularityRank("docker")).toBeLessThan(popularityRank("kubernetes"));
    expect(popularityRank("unknown")).toBeGreaterThan(popularityRank("ai"));
  });

  it("takes only https picture addresses", () => {
    expect(validAvatarURL("")).toBe(true);
    expect(validAvatarURL("https://example.com/me.png")).toBe(true);
    expect(validAvatarURL("http://example.com/me.png")).toBe(false);
    expect(validAvatarURL("javascript:alert(1)")).toBe(false);
  });

  it("finds flows the update turned off", () => {
    const flows = [
      { id: "a", name: "a", enabled: false, disabled_reason: FLOW_DISABLED_BY_UPDATE },
      { id: "b", name: "b", enabled: true, disabled_reason: FLOW_DISABLED_BY_UPDATE },
      { id: "c", name: "c", enabled: false, disabled_reason: "failed 5 times" },
    ] as Parameters<typeof flowsOffByUpdate>[0];
    expect(flowsOffByUpdate(flows).map((f) => f.id)).toEqual(["a"]);
  });
});
