import { act, cleanup, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { MetricSeries, MetricsResponse } from "./api/phase2";
import { WORKLOAD_METRICS_POLL_MS, metricReading, seriesBySuffix, useWorkloadMetrics } from "./workloadMetrics";

const { getWorkloadMetrics } = vi.hoisted(() => ({
  getWorkloadMetrics: vi.fn<(id: string, params?: { minutes?: number }) => Promise<MetricsResponse>>(),
}));

vi.mock("./api/client", () => ({ getWorkloadMetrics }));

function cpu(points: number[], status = "available"): MetricSeries {
  return {
    name: "workload.w1.cpu.busy_ratio",
    status,
    unit: "ratio",
    points: points.map((value, i) => ({ time: new Date(Date.UTC(2026, 8, 27, 0, 0, i * 15)).toISOString(), value })),
  };
}

async function flush() {
  await act(async () => {
    await Promise.resolve();
  });
}

describe("useWorkloadMetrics", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    getWorkloadMetrics.mockReset();
  });

  afterEach(() => {
    cleanup();
    vi.useRealTimers();
  });

  it("moves from loading to the latest CPU value and keeps polling while running", async () => {
    getWorkloadMetrics.mockResolvedValueOnce({ status: "available", series: [cpu([], "available")] });
    getWorkloadMetrics.mockResolvedValueOnce({ status: "available", series: [cpu([0.25])] });

    const { result, unmount } = renderHook(() => useWorkloadMetrics("w1", true));
    expect(result.current.state).toBe("loading");
    expect(metricReading(result.current.state, undefined, true)).toBe("Loading");

    await flush();
    const first = seriesBySuffix(result.current.series, ".cpu.busy_ratio");
    expect(result.current.state).toBe("ready");
    expect(metricReading(result.current.state, first, true)).toBe("Collecting");

    await act(async () => {
      vi.advanceTimersByTime(WORKLOAD_METRICS_POLL_MS);
    });
    await flush();
    const second = seriesBySuffix(result.current.series, ".cpu.busy_ratio");
    expect(getWorkloadMetrics).toHaveBeenCalledTimes(2);
    expect(metricReading(result.current.state, second, true)).toMatch(/^25(\.0)?\s?%$/);

    unmount();
    await act(async () => {
      vi.advanceTimersByTime(WORKLOAD_METRICS_POLL_MS * 3);
    });
    expect(getWorkloadMetrics).toHaveBeenCalledTimes(2);
  });

  it("reports unavailable when the request fails", async () => {
    getWorkloadMetrics.mockRejectedValue(new Error("boom"));
    const { result } = renderHook(() => useWorkloadMetrics("w1", true));
    await flush();
    expect(result.current.state).toBe("unavailable");
    expect(metricReading(result.current.state, undefined, true)).toBe("Unavailable");
  });

  it("does not poll or show Collecting for a stopped workload", async () => {
    getWorkloadMetrics.mockResolvedValue({ status: "available", series: [cpu([])] });
    const { result } = renderHook(() => useWorkloadMetrics("w1", false));
    await flush();
    await act(async () => {
      vi.advanceTimersByTime(WORKLOAD_METRICS_POLL_MS * 4);
    });
    expect(getWorkloadMetrics).toHaveBeenCalledTimes(1);
    expect(metricReading(result.current.state, seriesBySuffix(result.current.series, ".cpu.busy_ratio"), false)).toBe(
      "Not running",
    );
  });

  it("refetches on refresh", async () => {
    getWorkloadMetrics.mockResolvedValue({ status: "available", series: [cpu([0.5])] });
    const { result } = renderHook(() => useWorkloadMetrics("w1", false));
    await flush();
    act(() => result.current.refresh());
    await flush();
    expect(getWorkloadMetrics).toHaveBeenCalledTimes(2);
  });
});

describe("metricReading", () => {
  it("shows a value from a single sample and honest series states", () => {
    expect(metricReading("ready", cpu([0.1]), true)).toMatch(/^10(\.0)?\s?%$/);
    expect(metricReading("ready", cpu([0.1], "stale"), true)).toBe("Stale");
    expect(metricReading("ready", cpu([], "unavailable"), true)).toBe("Unavailable");
    expect(metricReading("ready", undefined, true)).toBe("Collecting");
  });
});
