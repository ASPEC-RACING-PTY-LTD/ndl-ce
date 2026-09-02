import type { MetricSeries } from "../api/phase2";
import { formatMetricValue } from "../format";
import { metricLabel } from "../labels";

type MetricChartProps = {
  series: MetricSeries;
};

export function canDrawSeries(series: MetricSeries): boolean {
  return series.status === "available" && series.points.length >= 2;
}

export function lastPoint(series?: MetricSeries): number | undefined {
  if (!series || !canDrawSeries(series)) {
    return undefined;
  }
  return series.points[series.points.length - 1]?.value;
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
  const h = 72;
  const coords = series.points.map((p, i) => {
    const x = (i / (series.points.length - 1)) * (w - 8) + 4;
    const y = h - 6 - ((p.value - min) / span) * (h - 14);
    return { x, y };
  });
  const d = coords.map((p, i) => `${i === 0 ? "M" : "L"}${p.x.toFixed(1)},${p.y.toFixed(1)}`).join(" ");
  const last = coords[coords.length - 1];
  const area = `${d} L${last.x.toFixed(1)},${h - 2} L4,${h - 2} Z`;
  const latest = values[values.length - 1];
  const latestLabel = formatMetricValue(series.name, latest, series.unit);
  const chartLabel = `${metricLabel(series.name)}, ${latestLabel}`;

  return (
    <svg className="metric-chart" viewBox={`0 0 ${w} ${h}`} role="img" aria-label={chartLabel}>
      <title>{chartLabel}</title>
      <path d="M4 18h312M4 36h312M4 54h312" stroke="currentColor" strokeOpacity="0.12" strokeWidth="1" />
      <path d={area} fill="currentColor" fillOpacity="0.12" stroke="none" />
      <path d={d} fill="none" stroke="currentColor" strokeWidth="1.75" />
      <circle cx={last.x} cy={last.y} r="2.2" fill="currentColor" />
    </svg>
  );
}
