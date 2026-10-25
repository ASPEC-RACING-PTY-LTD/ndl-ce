import { describe, expect, it } from "vitest";
import { formatMetricValue, formatPercent } from "./format";

describe("formatPercent", () => {
  it("scales every ratio, including ones above 1.5", () => {
    expect(formatPercent(0.02)).toBe("2.0%");
    expect(formatPercent(0.5)).toBe("50%");
    expect(formatPercent(2.02)).toBe("202%");
  });

  it("formats a guest's share of its cores", () => {
    expect(formatMetricValue("workload.x.cpu.busy_ratio", 2.02 / 6, "ratio")).toBe("34%");
  });
});
