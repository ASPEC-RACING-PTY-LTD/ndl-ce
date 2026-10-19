import { describe, expect, it } from "vitest";
import { roleIDFor } from "../pages/AccessControlPage";
import { stepDiff } from "./ApprovalsPage";

describe("stepDiff", () => {
  const base = {
    trigger: { type: "trigger.manual" },
    nodes: [
      { id: "a", type: "ndl.workload.restart", name: "Restart", config: { workload: "web" } },
      { id: "b", type: "action.notify", config: { message: "hi" } },
    ],
    edges: [{ from: "trigger", to: "a" }],
  };

  it("marks added, removed, changed and unchanged steps", () => {
    const next = {
      trigger: { type: "trigger.manual" },
      nodes: [
        { id: "a", type: "ndl.workload.restart", name: "Restart", config: { workload: "db" } },
        { id: "c", type: "ndl.docker.prune", config: {} },
      ],
      edges: [{ from: "trigger", to: "a" }],
    };
    const rows = Object.fromEntries(stepDiff(base, next).map((r) => [r.id, r.change]));
    expect(rows).toEqual({ trigger: "same", a: "changed", b: "removed", c: "added", edges: "same" });
  });

  it("treats a first version as all added", () => {
    const rows = stepDiff(null, base);
    expect(rows.every((r) => r.change === "added")).toBe(true);
  });
});

describe("roleIDFor", () => {
  it("matches the server rule", () => {
    expect(roleIDFor("  Help   Desk! ")).toBe("help-desk");
    expect(roleIDFor("SuperAdmin")).toBe("superadmin");
    expect(roleIDFor("Ops 2")).toBe("ops-2");
  });
});
