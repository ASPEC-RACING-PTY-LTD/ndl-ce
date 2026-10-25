import { describe, expect, it } from "vitest";
import { formatMetricValue, formatPercent } from "./format";

describe("formatPercent", () => {
  it("scales every ratio, including more than one busy core", () => {
    expect(formatPercent(0.02)).toBe("2.0%");
    expect(formatPercent(0.5)).toBe("50%");
    expect(formatPercent(2.02)).toBe("202%");
  });

  it("formats a guest's busy cores as a percent of one core", () => {
    expect(formatMetricValue("workload.x.cpu.busy_ratio", 2.023, "ratio")).toBe("202%");
  });
});
