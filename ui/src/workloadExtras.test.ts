import { describe, expect, it } from "vitest";
import { toggleExtra } from "./workloadExtras";
import type { WorkloadExtra } from "./api/client";

const catalog: WorkloadExtra[] = [
  { id: "docker", name: "Docker Engine", description: "", category: "containers", available: true },
  {
    id: "compose",
    name: "Docker Compose",
    description: "",
    category: "containers",
    requires: ["docker"],
    available: true,
  },
  { id: "git", name: "Git", description: "", category: "development", available: true },
];

describe("toggleExtra", () => {
  it("selects Docker when Compose is chosen", () => {
    expect(toggleExtra([], "compose", catalog).sort()).toEqual(["compose", "docker"]);
  });

  it("clears Compose when Docker is cleared", () => {
    expect(toggleExtra(["docker", "compose"], "docker", catalog)).toEqual([]);
  });
});
