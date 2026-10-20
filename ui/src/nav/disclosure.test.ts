import { describe, expect, it } from "vitest";
import { groupedModules, isCapabilityEnabled, parseDisclosurePrefs, visibleModules } from "./disclosure";

const admin = ["superadmin"];

describe("nav disclosure", () => {
  it("shows every page a SuperAdmin may open, without optional features that are off", () => {
    const labels = visibleModules(parseDisclosurePrefs(null), {}, admin).map((item) => item.label);
    expect(labels).toContain("Dashboard");
    expect(labels).toContain("Workloads");
    expect(labels).toContain("Templates");
    expect(labels).toContain("IAM");
    expect(labels).toContain("Audit Log");
    expect(labels).not.toContain("Cluster");
    expect(labels).not.toContain("Docker");
    expect(labels).not.toContain("Game Servers");
  });

  it("shows a feature's page once the feature is on", () => {
    const items = visibleModules(parseDisclosurePrefs(null), { gameservers: true, docker: true }, admin);
    expect(items.map((item) => item.label)).toEqual(expect.arrayContaining(["Game Servers", "Docker"]));
    const compute = items.filter((item) => item.group === "Compute").map((item) => item.id);
    expect(compute.indexOf("workloads")).toBeLessThan(compute.indexOf("game-servers"));
  });

  it("keeps UI-only capabilities and drops the old layout fields", () => {
    const prefs = parseDisclosurePrefs(JSON.stringify({ version: 1, template: "custom", enabled: ["clustering"], customVisible: ["dashboard"] }));
    expect(prefs).toEqual({ version: 1, enabled: ["clustering"] });
    expect(isCapabilityEnabled("clustering", prefs, {})).toBe(true);
    expect(visibleModules(prefs, {}, admin).map((item) => item.label)).toContain("Cluster");
  });

  it("shows the Audit Log to everyone and IAM to admins", () => {
    const prefs = parseDisclosurePrefs(null);
    const viewer = visibleModules(prefs, {}, ["user"]).map((item) => item.id);
    const adminItems = visibleModules(prefs, {}, admin).map((item) => item.id);
    expect(viewer).toContain("audit");
    expect(viewer).not.toContain("iam");
    expect(viewer).not.toContain("add-features");
    expect(adminItems).toContain("iam");
    expect(groupedModules(visibleModules(prefs, {}, admin)).some((g) => g.label === "Management")).toBe(true);
  });

  it("lets an admin reach IAM, features and updates but not the license", () => {
    const items = visibleModules(parseDisclosurePrefs(null), {}, ["admin"]).map((item) => item.id);
    expect(items).toEqual(expect.arrayContaining(["iam", "add-features", "updates", "audit"]));
    expect(items).not.toContain("license");
  });

  it("puts each group once", () => {
    const labels = groupedModules(visibleModules(parseDisclosurePrefs(null), {}, admin)).map((g) => g.label);
    expect(new Set(labels).size).toBe(labels.length);
  });
});
