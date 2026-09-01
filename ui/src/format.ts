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
    default:
      return value && value.length > 0 ? value : "Not reported";
  }
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
