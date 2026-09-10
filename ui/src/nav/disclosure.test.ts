import { describe, expect, it } from "vitest";
import { groupedModules, isCapabilityEnabled, paletteMatchesVisible, parseDisclosurePrefs, seedCustom, visibleModules } from "./disclosure";

const admin = ["admin"];

describe("nav disclosure", () => {
  it("defaults Simple to core virtualization without optional platform links", () => {
    const prefs = parseDisclosurePrefs(null);
    const items = visibleModules(prefs, {}, admin);
    const labels = items.map((item) => item.label);
    expect(labels).toContain("Dashboard");
    expect(labels).toContain("Workloads");
    expect(labels).toContain("Storage");
    expect(labels).toContain("Add Features");
    expect(labels).toContain("Users");
    expect(labels).toContain("Audit Log");
    expect(labels).not.toContain("Cluster");
    expect(labels).not.toContain("Automation");
    expect(labels).not.toContain("Ask");
    expect(labels).not.toContain("Docker");
  });

  it("shows Docker on Simple when the docker feature is enabled", () => {
    const prefs = parseDisclosurePrefs(null);
    const items = visibleModules(prefs, { docker: true }, admin);
    expect(items.map((item) => item.label)).toContain("Docker");
  });

  it("adds enabled optional modules on Simple without showing the rest of Advanced", () => {
    const prefs = parseDisclosurePrefs(JSON.stringify({ version: 1, template: "simple", enabled: ["clustering"] }));
    const items = visibleModules(prefs, { k8s: true }, admin);
    const labels = items.map((item) => item.label);
    expect(labels).toContain("Cluster");
    expect(labels).toContain("Kubernetes");
    expect(labels).not.toContain("Ask");
  });

  it("keeps Custom visibility independent of enablement", () => {
    const prefs = parseDisclosurePrefs(
      JSON.stringify({ version: 1, template: "custom", enabled: ["clustering"], customVisible: ["dashboard"], customOrder: ["dashboard"] }),
    );
    expect(isCapabilityEnabled("clustering", prefs, {})).toBe(true);
    const items = visibleModules(prefs, {}, admin);
    expect(items.map((item) => item.id)).toEqual(["dashboard"]);
  });

  it("does not drop Custom config when switching templates", () => {
    const custom = parseDisclosurePrefs(
      JSON.stringify({
        version: 1,
        template: "advanced",
        enabled: ["automation"],
        customVisible: ["workloads", "storage"],
        customOrder: ["storage", "workloads"],
      }),
    );
    expect(custom.enabled).toEqual(["automation"]);
    expect(custom.customVisible).toEqual(["workloads", "storage"]);
    const seeded = seedCustom({ ...custom, template: "custom", customVisible: [], customOrder: [] }, ["dashboard", "node"]);
    expect(seeded.customVisible).toEqual(["dashboard", "node"]);
  });

  it("hides audit from non-admins", () => {
    const prefs = parseDisclosurePrefs(JSON.stringify({ version: 1, template: "advanced" }));
    const viewer = visibleModules(prefs, {}, ["viewer"]).map((item) => item.id);
    const adminItems = visibleModules(prefs, {}, admin).map((item) => item.id);
    expect(viewer).not.toContain("audit");
    expect(viewer).not.toContain("users");
    expect(viewer).not.toContain("add-features");
    expect(viewer).not.toContain("api-access");
    expect(adminItems).toContain("audit");
    expect(adminItems).toContain("users");
    expect(groupedModules(visibleModules(prefs, {}, admin)).some((g) => g.label === "Management")).toBe(true);
    expect(groupedModules(visibleModules(prefs, {}, ["viewer"])).some((g) => g.label === "Management")).toBe(false);
    expect(groupedModules(visibleModules(prefs, {}, admin)).some((g) => g.label === "Settings")).toBe(false);
  });

  it("lets an operator see Management without Users", () => {
    const prefs = parseDisclosurePrefs(JSON.stringify({ version: 1, template: "advanced" }));
    const items = visibleModules(prefs, {}, ["operator"]).map((item) => item.id);
    expect(items).toContain("api-access");
    expect(items).toContain("add-features");
    expect(items).toContain("updates");
    expect(items).not.toContain("users");
    expect(items).not.toContain("roles");
    expect(items).not.toContain("audit");
    expect(items).not.toContain("license");
  });

  it("pins the current page so deep links stay reachable", () => {
    const prefs = parseDisclosurePrefs(null);
    const items = visibleModules(prefs, {}, admin, "/settings/cluster");
    expect(items.some((item) => item.id === "cluster")).toBe(true);
  });

  it("keeps create actions in Search while hiding optional destinations on Simple", () => {
    const prefs = parseDisclosurePrefs(null);
    const ids = new Set(visibleModules(prefs, {}, admin).map((item) => item.id));
    expect(paletteMatchesVisible({ id: "create-vm", href: "/workloads/new/vm" }, ids)).toBe(true);
    expect(paletteMatchesVisible({ id: "features", href: "/settings/features" }, ids)).toBe(true);
    expect(paletteMatchesVisible({ id: "cluster", href: "/settings/cluster" }, ids)).toBe(false);
    expect(paletteMatchesVisible({ id: "ask", href: "/ask" }, ids)).toBe(false);
  });

  it("merges pinned modules into an existing group", () => {
    const prefs = parseDisclosurePrefs(null);
    const groups = groupedModules(visibleModules(prefs, {}, admin, "/settings/cluster"));
    const labels = groups.map((group) => group.label);
    expect(labels.filter((label) => label === "Infrastructure")).toHaveLength(1);
    expect(groups.find((group) => group.label === "Infrastructure")?.items.map((item) => item.id)).toContain("cluster");
  });
});
