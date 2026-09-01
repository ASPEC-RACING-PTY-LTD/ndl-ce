import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { MetricChart, canDrawSeries } from "./MetricChart";
import type { MetricSeries } from "../api/phase2";

function series(points: number[], status = "available"): MetricSeries {
  return {
    name: "cpu.busy_ratio",
    status,
    points: points.map((value, i) => ({
      time: new Date(1_700_000_000_000 + i * 15_000).toISOString(),
      value,
    })),
  };
}

describe("MetricChart", () => {
  it("does not draw a chart from zero samples", () => {
    const empty = series([], "collecting");
    expect(canDrawSeries(empty)).toBe(false);
    const { container } = render(<MetricChart series={empty} />);
    expect(screen.getByRole("status")).toHaveTextContent(/collecting data/i);
    expect(container.querySelector("svg")).toBeNull();
  });

  it("does not duplicate one sample into a line", () => {
    const one = series([0.4]);
    expect(canDrawSeries(one)).toBe(false);
    const { container } = render(<MetricChart series={one} />);
    expect(screen.getByRole("status")).toHaveTextContent(/collecting data/i);
    expect(container.querySelector("svg")).toBeNull();
  });

  it("does not draw stale or unavailable as a healthy chart", () => {
    const stale = series([0.1, 0.2], "stale");
    expect(canDrawSeries(stale)).toBe(false);
    render(<MetricChart series={stale} />);
    expect(screen.getByRole("status")).toHaveTextContent(/^stale$/i);
  });

  it("draws only when two real points exist", () => {
    const two = series([0.1, 0.3]);
    expect(canDrawSeries(two)).toBe(true);
    const { container } = render(<MetricChart series={two} />);
    expect(container.querySelector("svg")).not.toBeNull();
  });
});
