import { describe, expect, it } from "vitest";
import { isRecentFailure } from "./TaskIndicator";

describe("isRecentFailure", () => {
  const now = Date.parse("2026-09-14T12:00:00Z");

  it("ignores failed tasks older than an hour", () => {
    expect(
      isRecentFailure(
        { state: "failed", updated_at: "2026-09-13T10:00:00Z" },
        now,
      ),
    ).toBe(false);
  });

  it("flags a failed task updated in the last hour", () => {
    expect(
      isRecentFailure(
        { state: "failed", updated_at: "2026-09-14T11:30:00Z" },
        now,
      ),
    ).toBe(true);
  });

  it("does not flag running tasks", () => {
    expect(
      isRecentFailure(
        { state: "running", updated_at: "2026-09-14T11:59:00Z" },
        now,
      ),
    ).toBe(false);
  });
});
