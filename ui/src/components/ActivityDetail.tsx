import { useState, type ReactNode } from "react";

export type ActivityField = { label: string; value: string };

function asRecord(value: unknown): Record<string, unknown> | null {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    return null;
  }
  return value as Record<string, unknown>;
}

function stringifyValue(value: unknown): string {
  if (value == null) {
    return "";
  }
  if (typeof value === "string" || typeof value === "number" || typeof value === "boolean") {
    return String(value);
  }
  try {
    return JSON.stringify(value);
  } catch {
    return String(value);
  }
}

export function fieldsFromRecord(raw: Record<string, unknown> | null | undefined, extra: ActivityField[] = []): ActivityField[] {
  const fields = [...extra];
  const seen = new Set(fields.map((item) => item.label.toLowerCase()));
  if (!raw) {
    return fields;
  }
  for (const [key, value] of Object.entries(raw)) {
    const text = stringifyValue(value);
    if (!text) {
      continue;
    }
    if (seen.has(key.toLowerCase())) {
      continue;
    }
    seen.add(key.toLowerCase());
    fields.push({ label: key.replaceAll("_", " "), value: text });
  }
  return fields;
}

export function formatActivityDetails(title: string, fields: ActivityField[], raw?: unknown): string {
  const lines = [title];
  for (const field of fields) {
    lines.push(`${field.label}: ${field.value}`);
  }
  if (raw !== undefined) {
    lines.push("---");
    try {
      lines.push(JSON.stringify(raw, null, 2));
    } catch {
      lines.push(String(raw));
    }
  }
  return lines.join("\n");
}

async function copyText(text: string): Promise<boolean> {
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(text);
      return true;
    }
  } catch {
    // Fall through to a textarea copy.
  }
  try {
    const area = document.createElement("textarea");
    area.value = text;
    area.setAttribute("readonly", "");
    area.style.position = "fixed";
    area.style.left = "-9999px";
    document.body.appendChild(area);
    area.select();
    const ok = document.execCommand("copy");
    document.body.removeChild(area);
    return ok;
  } catch {
    return false;
  }
}

export function ActivityDetail({
  title,
  fields,
  raw,
  diagnostics,
  extraActions,
  onClose,
}: {
  title: string;
  fields: ActivityField[];
  raw?: unknown;
  diagnostics?: unknown;
  extraActions?: ReactNode;
  onClose?: () => void;
}) {
  const [copied, setCopied] = useState<"details" | "diagnostics" | null>(null);
  const record = asRecord(raw);
  return (
    <div className="activity-detail" role="region" aria-label={`${title} details`}>
      <div className="activity-detail-head">
        <strong>{title}</strong>
        <div className="btn-row">
          <button
            type="button"
            className="btn btn-ghost"
            onClick={() => {
              void copyText(formatActivityDetails(title, fields, raw)).then((ok) => {
                setCopied(ok ? "details" : null);
                window.setTimeout(() => setCopied(null), 2000);
              });
            }}
          >
            {copied === "details" ? "Copied" : "Copy Details"}
          </button>
          {diagnostics !== undefined ? (
            <button
              type="button"
              className="btn btn-ghost"
              disabled={diagnostics == null}
              onClick={() => {
                void copyText(formatActivityDetails(`${title} diagnostics`, fields, diagnostics)).then((ok) => {
                  setCopied(ok ? "diagnostics" : null);
                  window.setTimeout(() => setCopied(null), 2000);
                });
              }}
            >
              {copied === "diagnostics" ? "Copied" : "Copy Diagnostics"}
            </button>
          ) : null}
          {extraActions}
          {onClose ? (
            <button type="button" className="btn btn-ghost" onClick={onClose}>
              Close
            </button>
          ) : null}
        </div>
      </div>
      <dl className="definition-list compact">
        {fields.map((field) => (
          <div key={field.label}>
            <dt>{field.label}</dt>
            <dd className="activity-detail-value">{field.value}</dd>
          </div>
        ))}
      </dl>
      {record ? (
        <pre className="activity-detail-raw">{JSON.stringify(record, null, 2)}</pre>
      ) : null}
    </div>
  );
}
