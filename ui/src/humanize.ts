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

export function humanTaskMessage(message?: string): string {
  if (!message) {
    return "";
  }
  const trimmed = message.trim();
  if (trimmed.startsWith("{") || trimmed.startsWith("[")) {
    try {
      const parsed = JSON.parse(trimmed) as unknown;
      if (parsed && typeof parsed === "object" && !Array.isArray(parsed)) {
        return payloadFacts(parsed as Record<string, unknown>)
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
