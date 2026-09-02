export function formatBytes(value?: number): string {
  if (value == null || !Number.isFinite(value)) {
    return "Not reported";
  }
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  let n = value;
  let i = 0;
  while (n >= 1024 && i < units.length - 1) {
    n /= 1024;
    i += 1;
  }
  return `${n.toFixed(i === 0 ? 0 : 1)} ${units[i]}`;
}

export function honestStatus(value?: string): string {
  switch (value) {
    case "available":
      return "Available";
    case "running":
      return "Running";
    case "stopped":
      return "Stopped";
    case "failed":
      return "Failed";
    case "succeeded":
      return "Succeeded";
    case "completed":
      return "Succeeded";
    case "warning":
      return "Warning";
    case "unavailable":
      return "Unavailable";
    case "not_configured":
      return "Not configured";
    case "not_reported":
      return "Not reported";
    case "collecting":
      return "Collecting";
    case "stale":
      return "Stale";
    case "unknown":
      return "Unknown";
    case "draft":
      return "Draft";
    case "applying":
      return "Applying";
    case "applied":
      return "Applied";
    case "partial":
      return "Partial";
    case "pending":
      return "Pending";
    case "creating":
      return "Creating";
    case "starting":
      return "Starting";
    case "stopping":
      return "Stopping";
    case "ok":
    case "healthy":
      return "Healthy";
    case "degraded":
      return "Degraded";
    case "ready":
    case "Ready":
      return "Ready";
    case "NotReady":
    case "not_ready":
      return "Not ready";
    default:
      return value && value.length > 0 ? value : "Not reported";
  }
}

export function formatPercent(ratio?: number): string {
  if (ratio == null || !Number.isFinite(ratio)) {
    return "Not reported";
  }
  const pct = ratio <= 1.5 ? ratio * 100 : ratio;
  return `${pct.toFixed(pct >= 10 ? 0 : 1)}%`;
}

export function formatTempMilliC(value?: number): string {
  if (value == null || !Number.isFinite(value)) {
    return "Not reported";
  }
  return `${(value / 1000).toFixed(1)} C`;
}

export function formatMbps(value?: number): string {
  if (value == null || !Number.isFinite(value)) {
    return "Not reported";
  }
  return `${value} Mbps`;
}

export function formatNicState(value?: string): string {
  switch (value) {
    case "up":
      return "Up";
    case "down":
      return "Down";
    default:
      return value && value.length > 0 ? value : "Not reported";
  }
}

export function formatMetricValue(name: string, value: number, unit?: string): string {
  if (unit === "bytes" || name.includes("bytes")) {
    return formatBytes(value);
  }
  if (unit === "ratio" || name.includes("ratio")) {
    return formatPercent(value);
  }
  return String(value);
}

export function formatWhen(value?: string): string {
  if (!value) {
    return "Not reported";
  }
  const d = new Date(value);
  if (Number.isNaN(d.getTime())) {
    return value;
  }
  return d.toISOString().replace("T", " ").replace("Z", " UTC");
}
