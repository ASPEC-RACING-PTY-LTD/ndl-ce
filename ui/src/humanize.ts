import { honestStatus } from "./format";
import { eventTypeLabel, kindLabel, osLabel, taskKindLabel } from "./labels";

const SKIP = /(_id|^id$|cluster_id|user_id|volume_id|pool_id|network_id|workload_id)$/i;

function prettyValue(key: string, value: unknown): string | null {
  if (value == null || value === "") {
    return null;
  }
  if (typeof value === "boolean") {
    return value ? "Yes" : "No";
  }
  if (typeof value === "number") {
    return Number.isFinite(value) ? String(value) : null;
  }
  if (typeof value !== "string") {
    return null;
  }
  if (key === "kind") {
    return kindLabel(value);
  }
  if (key === "image_pin") {
    return osLabel(value);
  }
  if (key === "status" || key === "state") {
    return honestStatus(value);
  }
  return value;
}

export function payloadFacts(payload?: Record<string, unknown> | null): { label: string; value: string }[] {
  if (!payload) {
    return [];
  }
  const facts: { label: string; value: string }[] = [];
  for (const [key, raw] of Object.entries(payload)) {
    if (SKIP.test(key)) {
      continue;
    }
    const value = prettyValue(key, raw);
    if (!value) {
      continue;
    }
    facts.push({
      label: taskKindLabel(key),
      value,
    });
  }
  return facts;
}

export function eventHeadline(type?: string, payload?: Record<string, unknown> | null): string {
  const title = eventTypeLabel(type);
  const name = typeof payload?.name === "string" ? payload.name : null;
  return name ? `${title} · ${name}` : title;
}

export function taskStageLabel(stage?: string): string {
  if (!stage) {
    return "Not reported";
  }
  return taskKindLabel(stage);
}

export function taskResourceName(task: {
  resource_name?: string;
  message?: string;
}): string {
  if (task.resource_name?.trim()) {
    return task.resource_name.trim();
  }
  const message = task.message?.trim() ?? "";
  if (!message.startsWith("{")) {
    return "";
  }
  try {
    const parsed = JSON.parse(message) as { name?: unknown };
    return typeof parsed.name === "string" ? parsed.name.trim() : "";
  } catch {
    return "";
  }
}

export function taskIntentTitle(task: { kind?: string; state?: string; resource_name?: string; message?: string }): string {
  const name = taskResourceName(task);
  const kind = (task.kind || "").toLowerCase();
  const done = task.state === "succeeded" || task.state === "completed";
  const map: Record<string, [string, string]> = {
    "workload.create": ["Creating", "Created"],
    "workload.delete": ["Deleting", "Deleted"],
    "workload.start": ["Starting", "Started"],
    "workload.stop": ["Stopping", "Stopped"],
    "workload.restart": ["Restarting", "Restarted"],
    "workload.clone": ["Cloning", "Cloned"],
    "workload.setup-extras": ["Installing extras on", "Installed extras on"],
    "inventory.refresh": ["Refreshing host inventory", "Refreshed host inventory"],
    "backup.run": ["Backing up", "Backed up"],
    "backup.policy": ["Running backup policy", "Ran backup policy"],
    "backup.restore": ["Restoring", "Restored"],
  };
  const pair = map[kind] ?? [taskKindLabel(task.kind), taskKindLabel(task.kind)];
  const verb = done ? pair[1] : pair[0];
  if (kind === "inventory.refresh") {
    return verb;
  }
  if (name) {
    return `${verb} ${name}`;
  }
  if (kind.startsWith("backup")) {
    return verb;
  }
  return verb;
}

export function taskStageFriendly(stage?: string): string {
  switch ((stage || "").toLowerCase()) {
    case "creating":
      return "Creating";
    case "planning":
      return "Planning";
    case "collected":
      return "Finished collecting";
    case "installing":
      return "Installing extras";
    case "start":
      return "Starting";
    case "stop":
      return "Stopping";
    default:
      return taskStageLabel(stage);
  }
}

export function auditActionLabel(action?: string): string {
  switch (action) {
    case "workload.create":
      return "Created workload";
    case "workload.setup-extras":
      return "Installed guest extras";
    case "inventory.refresh":
      return "Refreshed host inventory";
    case "backup.policy":
    case "backup.policy.update":
      return "Changed backup policy";
    case "security.mfa_policy":
      return "Changed MFA policy";
    case "identity.token.create":
    case "token.create":
      return "Created API token";
    case "identity.token.revoke":
    case "token.revoke":
      return "Revoked API token";
    case "auth.login":
      return "Signed in";
    case "auth.logout":
      return "Signed out";
    default:
      return taskKindLabel(action);
  }
}

export function humanTaskMessage(message?: string): string {
  if (!message) {
    return "";
  }
  const trimmed = message.trim();
  if (trimmed.startsWith("{") || trimmed.startsWith("[")) {
    try {
      const parsed = JSON.parse(trimmed) as unknown;
      if (parsed && typeof parsed === "object" && !Array.isArray(parsed)) {
        const rec = parsed as Record<string, unknown>;
        if (typeof rec.error === "string" && rec.error.trim()) {
          return rec.error;
        }
        if (typeof rec.message === "string" && rec.message.trim() && rec.message !== "created") {
          return rec.message;
        }
        return payloadFacts(rec)
          .map((fact) => `${fact.label} ${fact.value}`)
          .join(" · ");
      }
      return "";
    } catch {
      return message;
    }
  }
  return message;
}
