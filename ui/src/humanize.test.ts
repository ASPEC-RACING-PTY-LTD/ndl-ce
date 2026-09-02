import { describe, expect, it } from "vitest";
import { eventHeadline, humanTaskMessage, payloadFacts, taskStageLabel } from "./humanize";

describe("humanize", () => {
  it("hides raw identifiers and JSON-shaped values", () => {
    const facts = payloadFacts({
      workload_id: "wl-1",
      volume_id: "vol-9",
      name: "web-01",
      kind: "system-container",
      nested: { ignored: true },
    });
    expect(facts).toEqual([
      { label: "Name", value: "web-01" },
      { label: "Kind", value: "System container" },
    ]);
    expect(JSON.stringify(facts)).not.toMatch(/workload_id|volume_id/);
    expect(facts.every((fact) => !fact.value.includes("{"))).toBe(true);
  });

  it("uses a resource name in the event headline when present", () => {
    expect(eventHeadline("workload.started", { name: "web-01" })).toBe("Workload Started · web-01");
    expect(eventHeadline("node.stale")).toBe("Node Stale");
  });

  it("title-cases task stages", () => {
    expect(taskStageLabel("pull_image")).toBe("Pull Image");
    expect(taskStageLabel()).toBe("Not reported");
  });

  it("keeps operator task messages and hides raw JSON payloads", () => {
    expect(humanTaskMessage("Pulling image")).toBe("Pulling image");
    expect(humanTaskMessage('{"workload_id":"w-1","volume_id":"v-1"}')).toBe("");
    expect(humanTaskMessage('{"workload_id":"w-1","name":"web-01"}')).toBe("Name web-01");
  });
});
