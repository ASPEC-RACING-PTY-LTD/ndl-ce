import { describe, expect, it } from "vitest";
import {
  findActiveMigrationJob,
  isActiveMigrationState,
  isRetryableMigrationState,
  jobHeadline,
  listedMigrationJobs,
  migrationJobFields,
} from "./jobView";

describe("jobView", () => {
  it("finds an active job from persisted list JSON", () => {
    const items = listedMigrationJobs({
      items: [
        { id: "done", state: "succeeded" },
        { id: "live", state: "running", status: { workload: "web" } },
        { id: "old", state: "failed" },
      ],
    });
    expect(findActiveMigrationJob(items)?.id).toBe("live");
    expect(isActiveMigrationState("canceling")).toBe(true);
    expect(isRetryableMigrationState("canceled")).toBe(true);
    expect(isRetryableMigrationState("succeeded")).toBe(false);
  });

  it("builds detail fields from job plus diagnostics bundle", () => {
    const job = {
      id: "job-1",
      state: "failed",
      stage: "transfer",
      created_at: "2026-09-08T01:00:00Z",
      updated_at: "2026-09-08T01:02:00Z",
      source_id: "src-1",
      plan: {
        destination_node: "node-a",
        mapping: { storage: { local: "pool-1" } },
        items: [{ name: "web", mode: "offline", source_id: "pve1/100" }],
      },
      status: { workload: "web", percent: 12, message: "disk missing", reports: [] },
    };
    const fields = migrationJobFields(job, {
      logs: [{ source: "job", message: "disk missing" }],
      source: { label: "pve", endpoint: "https://pve.example:8006" },
      destinations: [{ name: "web" }],
    });
    const byLabel = Object.fromEntries(fields.map((field) => [field.label, field.value]));
    expect(byLabel["job id"]).toBe("job-1");
    expect(byLabel.workloads).toContain("web");
    expect(byLabel.progress).toBe("12%");
    expect(byLabel.logs).toContain("disk missing");
    expect(byLabel.mappings).toContain("local -> pool-1");
    expect(jobHeadline(job)).toMatch(/web/);
  });
});
