import { honestStatus } from "../format";

function tone(status?: string): "ok" | "warn" | "bad" | "info" | "neutral" {
  switch (status) {
    case "available":
    case "running":
    case "ok":
      return "ok";
    case "warning":
    case "stale":
    case "starting":
    case "stopping":
      return "warn";
    case "failed":
    case "unavailable":
      return "bad";
    case "collecting":
    case "stopped":
      return "info";
    default:
      return "neutral";
  }
}

export function StatusBadge({ status, label }: { status?: string; label?: string }) {
  const cls = tone(status);
  const text = label ?? honestStatus(status);
  return (
    <span className={`status-badge is-${cls}`}>
      <span className="visually-hidden">{cls === "ok" ? "OK. " : cls === "warn" ? "Warning. " : cls === "bad" ? "Problem. " : ""}</span>
      {text}
    </span>
  );
}
