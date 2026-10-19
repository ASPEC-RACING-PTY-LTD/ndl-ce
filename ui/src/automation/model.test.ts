import { describe, expect, it } from "vitest";
import {
  dataRefs,
  expressionToRules,
  insertStep,
  moveStep,
  rulesToExpression,
  stepsBefore,
  toDef,
  toModel,
  whenText,
  type Definition,
} from "./model";

const branched: Definition = {
  schema_version: 1,
  trigger: { id: "trigger", type: "trigger.event", config: { kind: "storage.pool_usage_high" } },
  nodes: [
    { id: "check", type: "logic.condition", config: { rules: [{ left: "{{ trigger.percent }}", op: ">=", right: 85 }] } },
    { id: "tell", type: "action.notify", config: { message: "full" } },
    { id: "log", type: "logic.set_variable", config: { name: "seen", value: "yes" } },
    { id: "each", type: "logic.foreach", config: { items: "{{ trigger.items }}" } },
    { id: "snap", type: "ndl.snapshot.create", config: { workload: "{{ item.id }}" }, on_error: "fail_branch" },
    { id: "oops", type: "action.notify", config: { message: "snapshot failed" } },
    { id: "end", type: "action.notify", config: { message: "done" } },
  ],
  edges: [
    { from: "trigger", to: "check" },
    { from: "check", to: "tell", port: "true" },
    { from: "check", to: "each", port: "false" },
    { from: "tell", to: "log" },
    { from: "log", to: "each" },
    { from: "each", to: "snap", port: "each" },
    { from: "snap", to: "oops", port: "error" },
    { from: "each", to: "end", port: "done" },
  ],
};

function sortEdges(d: Definition) {
  return [...d.edges].map((e) => `${e.from}>${e.to}:${e.port ?? ""}`).sort();
}

describe("builder model", () => {
  it("reads branches, joins, loops and error branches and writes the same graph back", () => {
    const model = toModel(branched);
    expect(model.unsupported).toBeUndefined();
    expect(model.steps.map((s) => s.node.id)).toEqual(["check", "each", "end"]);
    const check = model.steps[0];
    expect(check.branches.map((b) => [b.port, b.steps.map((s) => s.node.id)])).toEqual([
      ["true", ["tell", "log"]],
      ["false", []],
    ]);
    const loop = model.steps[1];
    expect(loop.branches[0].steps[0].node.id).toBe("snap");
    expect(loop.branches[0].steps[0].errorSteps?.map((s) => s.node.id)).toEqual(["oops"]);
    const back = toDef(model);
    expect(sortEdges(back)).toEqual(sortEdges(branched));
    expect(back.nodes.map((n) => n.id).sort()).toEqual(branched.nodes.map((n) => n.id).sort());
  });

  it("inserts and moves steps and keeps the graph linear", () => {
    const model = toModel({ ...branched, nodes: branched.nodes.slice(6), edges: [{ from: "trigger", to: "end" }] });
    const steps = insertStep(model.steps, [], 0, { node: { id: "first", type: "action.notify", config: { message: "hi" } }, branches: [] });
    expect(toDef({ ...model, steps }).edges).toEqual([
      { from: "trigger", to: "first" },
      { from: "first", to: "end" },
    ]);
    const moved = moveStep(steps, [], 0, 1);
    expect(moved.map((s) => s.node.id)).toEqual(["end", "first"]);
  });

  it("marks parallel branches as unsupported", () => {
    const model = toModel({
      ...branched,
      nodes: branched.nodes.slice(1, 3),
      edges: [
        { from: "trigger", to: "tell" },
        { from: "trigger", to: "log" },
      ],
    });
    expect(model.unsupported).toMatch(/parallel/);
  });

  it("offers trigger fields, earlier outputs and loop items as dynamic content", () => {
    const model = toModel(branched);
    const before = stepsBefore(model.steps, [1, "each"], 0);
    const refs = dataRefs(model.trigger, before, { "storage.pool_usage_high": ["pool_name", "percent"] }).map((r) => r.ref);
    expect(refs).toContain("trigger.percent");
    expect(refs).toContain("nodes.check.result");
    expect(refs).toContain("item");
    expect(refs).toContain("vars.seen");
  });
});

describe("condition rules", () => {
  it("turns rules into an expression and back", () => {
    const rules = [
      { left: "{{ trigger.percent }}", op: ">=", right: 85 },
      { left: "{{ trigger.name }}", op: "contains", right: "web" },
    ];
    const src = rulesToExpression(rules, "all");
    expect(src).toBe('trigger.percent >= 85 and contains(trigger.name, "web")');
    expect(expressionToRules(src)).toEqual({ rules, match: "all" });
    expect(expressionToRules("a.b == 1 and c.d == 2 or e == 3")).toBeNull();
    expect(expressionToRules('not contains(trigger.tags, "prod") or trigger.x != 2')?.match).toBe("any");
  });

  it("reads WHEN conditions as a sentence", () => {
    expect(
      whenText([
        { field: "pool_name", op: "eq", value: "fast" },
        { join: "or", group: [{ field: "percent", op: "exists" }] },
      ]),
    ).toBe('pool_name is "fast" or (percent exists)');
  });
});
