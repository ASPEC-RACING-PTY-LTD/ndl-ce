import type { ActivityField } from "../components/ActivityDetail";

export function asJobRecord(value: unknown): Record<string, unknown> | null {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    return null;
  }
  return value as Record<string, unknown>;
}

export function listedMigrationJobs(value: unknown): Record<string, unknown>[] {
  const rec = asJobRecord(value);
  const items = rec?.items;
  if (!Array.isArray(items)) {
    return [];
  }
  return items.map((item) => asJobRecord(item)).filter((item): item is Record<string, unknown> => item != null);
}

export function jobStateOf(job: Record<string, unknown> | null | undefined): string {
  return String(job?.state ?? "").toLowerCase();
}

export function isActiveMigrationState(state: string): boolean {
  const normalized = state.toLowerCase();
  return normalized === "running" || normalized === "canceling";
}

export function isRetryableMigrationState(state: string): boolean {
  const normalized = state.toLowerCase();
  return normalized === "failed" || normalized === "canceled";
}

export function findActiveMigrationJob(items: Record<string, unknown>[]): Record<string, unknown> | null {
  return items.find((row) => isActiveMigrationState(jobStateOf(row))) ?? null;
}

function collectNames(job: Record<string, unknown>, diagnostics?: Record<string, unknown> | null): string[] {
  const names: string[] = [];
  const add = (value: unknown) => {
    const text = String(value ?? "").trim();
    if (text && !names.includes(text)) {
      names.push(text);
    }
  };
  const status = asJobRecord(job.status) ?? asJobRecord(diagnostics?.status);
  add(status?.workload);
  const reports = status?.reports;
  if (Array.isArray(reports)) {
    for (const row of reports) {
      add(asJobRecord(row)?.name);
    }
  }
  const plan = asJobRecord(job.plan) ?? asJobRecord(diagnostics?.plan);
  const items = plan?.items;
  if (Array.isArray(items)) {
    for (const row of items) {
      add(asJobRecord(row)?.name);
    }
  }
  const dests = diagnostics?.destinations;
  if (Array.isArray(dests)) {
    for (const row of dests) {
      add(asJobRecord(row)?.name);
    }
  }
  return names;
}

export function jobWorkloadNames(job: Record<string, unknown>, diagnostics?: Record<string, unknown> | null): string {
  return collectNames(job, diagnostics).join(", ");
}

export function jobHeadline(job: Record<string, unknown>): string {
  const names = jobWorkloadNames(job);
  const adapter = String(job.adapter ?? "migration");
  const direction = String(job.direction ?? "");
  const state = String(job.state ?? "unknown");
  const lead = names || `${adapter} ${direction}`.trim();
  return `${lead} · ${state}`.trim();
}

function mappingLines(mapping: unknown): string {
  const rec = asJobRecord(mapping);
  if (!rec) {
    return "";
  }
  const parts: string[] = [];
  for (const key of ["storage", "network", "vlan"]) {
    const entries = asJobRecord(rec[key]);
    if (!entries) {
      continue;
    }
    for (const [src, dest] of Object.entries(entries)) {
      parts.push(`${key} ${src} -> ${String(dest)}`);
    }
  }
  return parts.join(", ");
}

function formatLogs(logs: unknown): string {
  if (!Array.isArray(logs) || logs.length === 0) {
    return "";
  }
  const lines: string[] = [];
  for (const row of logs) {
    const rec = asJobRecord(row);
    if (!rec) {
      continue;
    }
    if (rec.message) {
      lines.push(String(rec.message));
    }
    if (Array.isArray(rec.lines)) {
      for (const line of rec.lines.slice(0, 40)) {
        lines.push(String(line));
      }
    }
  }
  return lines.join("\n");
}

function formatVerification(job: Record<string, unknown>, diagnostics?: Record<string, unknown> | null): string {
  const status = asJobRecord(job.status) ?? asJobRecord(diagnostics?.status);
  const reports = status?.reports;
  if (!Array.isArray(reports) || reports.length === 0) {
    return "";
  }
  return reports
    .map((row) => {
      const rec = asJobRecord(row);
      if (!rec) {
        return "";
      }
      const observed = Array.isArray(rec.observed) ? rec.observed.join(", ") : "";
      const unobserved = Array.isArray(rec.unobserved) ? rec.unobserved.join(", ") : "";
      return `${String(rec.name ?? "workload")}: observed ${observed || "none"}; unobserved ${unobserved || "none"}`;
    })
    .filter(Boolean)
    .join("\n");
}

function formatModes(job: Record<string, unknown>, diagnostics?: Record<string, unknown> | null): string {
  const plan = asJobRecord(job.plan) ?? asJobRecord(diagnostics?.plan);
  const items = plan?.items;
  if (!Array.isArray(items) || items.length === 0) {
    return String(asJobRecord(job.status)?.workload ? "" : "");
  }
  return items
    .map((row) => {
      const rec = asJobRecord(row);
      if (!rec) {
        return "";
      }
      return `${String(rec.name ?? rec.source_id ?? "item")}: ${String(rec.mode ?? "unset")}`;
    })
    .filter(Boolean)
    .join(", ");
}

export function migrationJobFields(job: Record<string, unknown>, diagnostics?: Record<string, unknown> | null): ActivityField[] {
  const status = asJobRecord(job.status) ?? asJobRecord(diagnostics?.status);
  const plan = asJobRecord(job.plan) ?? asJobRecord(diagnostics?.plan);
  const source = asJobRecord(diagnostics?.source);
  const dests = Array.isArray(diagnostics?.destinations)
    ? diagnostics.destinations.map((row) => String(asJobRecord(row)?.name ?? "")).filter(Boolean).join(", ")
    : "";
  const logs = formatLogs(diagnostics?.logs);
  const verification = formatVerification(job, diagnostics);
  const mappings = mappingLines(plan?.mapping ?? diagnostics?.mappings);
  const modes = formatModes(job, diagnostics);
  const fields: ActivityField[] = [
    { label: "job id", value: String(job.id ?? "Not reported") },
    { label: "workloads", value: jobWorkloadNames(job, diagnostics) || "Not reported" },
    { label: "status", value: String(job.state ?? status?.state ?? "Not reported") },
    { label: "stage", value: String(job.stage ?? status?.stage ?? "Not reported") },
    { label: "progress", value: status?.percent != null ? `${String(status.percent)}%` : "Not reported" },
    { label: "message", value: String(status?.message ?? "") },
    { label: "created", value: String(job.created_at ?? "Not reported") },
    { label: "updated", value: String(job.updated_at ?? "Not reported") },
    { label: "source", value: String(source?.label || source?.endpoint || job.source_id || plan?.source_id || "Not reported") },
    { label: "destination", value: dests || String(plan?.destination_node ?? "") || "Not reported" },
    { label: "mode", value: modes || "Not reported" },
    { label: "mappings", value: mappings || "Not reported" },
    { label: "verification", value: verification || "Not reported" },
    { label: "logs", value: logs || "Not reported" },
  ];
  return fields.filter((field) => field.value !== "");
}
