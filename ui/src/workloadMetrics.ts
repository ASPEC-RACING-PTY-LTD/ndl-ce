import { useEffect, useState } from "react";
import { getWorkloadMetrics } from "./api/client";
import type { MetricSeries } from "./api/phase2";
import { formatMetricValue } from "./format";

// Matches the agent scrape interval, so each poll can bring a new sample.
export const WORKLOAD_METRICS_POLL_MS = 15_000;

export type WorkloadMetricsState = "loading" | "ready" | "unavailable";

export function seriesBySuffix(series: MetricSeries[] | undefined, suffix: string): MetricSeries | undefined {
  return (series ?? []).find((s) => s.name.endsWith(suffix));
}

// Polls workload metrics while the workload runs. A stopped workload is
// fetched once and not polled.
export function useWorkloadMetrics(id: string, running: boolean) {
  const [series, setSeries] = useState<MetricSeries[]>([]);
  const [state, setState] = useState<WorkloadMetricsState>("loading");
  const [tick, setTick] = useState(0);

  useEffect(() => {
    setSeries([]);
    setState("loading");
  }, [id]);

  useEffect(() => {
    if (!id) {
      return;
    }
    let cancelled = false;
    const load = () =>
      getWorkloadMetrics(id, { minutes: 15 })
        .then((res) => {
          if (!cancelled) {
            setSeries(res.series ?? []);
            setState(res.status === "unavailable" ? "unavailable" : "ready");
          }
        })
        .catch(() => {
          if (!cancelled) {
            setState("unavailable");
          }
        });
    void load();
    const timer = running ? window.setInterval(() => void load(), WORKLOAD_METRICS_POLL_MS) : undefined;
    return () => {
      cancelled = true;
      if (timer !== undefined) {
        window.clearInterval(timer);
      }
    };
  }, [id, running, tick]);

  return { series, state, refresh: () => setTick((n) => n + 1) };
}

// metricReading is a metric tile headline. Collecting only applies to a
// running workload whose first sample (a CPU delta needs two scrapes) has not
// landed yet; a stopped workload never shows Collecting.
export function metricReading(state: WorkloadMetricsState, series: MetricSeries | undefined, running: boolean): string {
  if (!running) {
    return "Not running";
  }
  if (state === "loading") {
    return "Loading";
  }
  if (state === "unavailable") {
    return "Unavailable";
  }
  if (series?.status === "stale") {
    return "Stale";
  }
  if (series?.status === "unavailable") {
    return "Unavailable";
  }
  const last = series?.status === "available" ? series.points[series.points.length - 1] : undefined;
  if (!last) {
    return "Collecting";
  }
  return formatMetricValue(series?.name || "cpu.busy_ratio", last.value, series?.unit);
}
