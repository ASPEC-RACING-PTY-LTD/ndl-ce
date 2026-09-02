import { honestStatus } from "../format";
import { Icon, type IconName } from "./Icon";

function tone(status?: string): "ok" | "warn" | "bad" | "info" | "neutral" {
  switch (status) {
    case "available":
    case "running":
    case "ok":
    case "healthy":
    case "succeeded":
    case "completed":
      return "ok";
    case "warning":
    case "stale":
    case "starting":
    case "stopping":
    case "degraded":
    case "pending":
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

function mark(cls: ReturnType<typeof tone>): IconName {
  switch (cls) {
    case "ok":
      return "mark-ok";
    case "warn":
      return "mark-warn";
    case "bad":
      return "mark-bad";
    case "info":
      return "mark-info";
    default:
      return "mark-neutral";
  }
}

export function StatusBadge({ status, label }: { status?: string; label?: string }) {
  const cls = tone(status);
  const text = label ?? honestStatus(status);
  return (
    <span className={`status-badge is-${cls}`}>
      <Icon name={mark(cls)} size={10} />
      <span className="visually-hidden">
        {cls === "ok" ? "OK. " : cls === "warn" ? "Warning. " : cls === "bad" ? "Problem. " : ""}
      </span>
      {text}
    </span>
  );
}
