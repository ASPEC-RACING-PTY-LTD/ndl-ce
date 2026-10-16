import { describe, expect, it } from "vitest";
import type { BackupRun } from "../generated/openapi";
import { filterRuns } from "./BackupsPage";

const now = Date.parse("2026-10-17T03:00:00Z");
const run = (id: string, minsAgo: number, status: BackupRun["status"] = "succeeded"): BackupRun =>
  ({ id, workload_id: id, target_id: "t", status, started_at: new Date(now - minsAgo * 60000).toISOString() }) as BackupRun;

describe("filterRuns", () => {
  const runs = [
    run("a", 5),
    run("b", 15, "failed"),
    run("c", 30),
    // Previous round, hours earlier.
    run("d", 300, "failed"),
    run("e", 310),
    // Two days ago.
    run("f", 2 * 24 * 60),
  ];

  it("shows only the latest round by default", () => {
    expect(filterRuns(runs, "latest", false, now).map((r) => r.id)).toEqual(["a", "b", "c"]);
  });

  it("keeps a run that is still going even when it started before the round", () => {
    const withLong = [...runs, run("g", 400, "running")];
    expect(filterRuns(withLong, "latest", false, now).map((r) => r.id)).toEqual(["a", "b", "c", "g"]);
  });

  it("widens to a day or everything, and narrows to failures", () => {
    expect(filterRuns(runs, "day", false, now)).toHaveLength(5);
    expect(filterRuns(runs, "all", false, now)).toHaveLength(6);
    expect(filterRuns(runs, "all", true, now).map((r) => r.id)).toEqual(["b", "d"]);
  });
});
