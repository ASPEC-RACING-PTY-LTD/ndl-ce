import type { MetricSeries } from "../api/phase2";

type MetricChartProps = {
  series: MetricSeries;
};

export function canDrawSeries(series: MetricSeries): boolean {
  return series.status === "available" && series.points.length >= 2;
}

export function MetricChart({ series }: MetricChartProps) {
  if (!canDrawSeries(series)) {
    const label =
      series.status === "stale"
        ? "Stale"
        : series.status === "unavailable"
          ? "Unavailable"
          : "Collecting data";
    return (
      <p className="chart-empty" role="status">
        {label}
      </p>
    );
  }

  const values = series.points.map((p) => p.value);
  const min = Math.min(...values);
  const max = Math.max(...values);
  const span = max - min || 1;
  const w = 320;
  const h = 96;
  const d = series.points
    .map((p, i) => {
      const x = (i / (series.points.length - 1)) * (w - 8) + 4;
      const y = h - 8 - ((p.value - min) / span) * (h - 16);
      return `${i === 0 ? "M" : "L"}${x.toFixed(1)},${y.toFixed(1)}`;
    })
    .join(" ");

  return (
    <svg className="metric-chart" viewBox={`0 0 ${w} ${h}`} role="img" aria-label={series.name}>
      <path d={d} fill="none" stroke="currentColor" strokeWidth="2" />
    </svg>
  );
}
