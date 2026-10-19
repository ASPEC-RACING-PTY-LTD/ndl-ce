import { createContext, useContext, useEffect, useState } from "react";
import { getAutomationCatalogue, listAutomationTargets } from "../api/client";

export type CatalogueField = {
  key: string;
  label: string;
  type: string;
  required?: boolean;
  help?: string;
  placeholder?: string;
  default?: unknown;
  options?: { value: string; label: string }[];
  target?: string;
};

export type NodeTypeInfo = {
  type: string;
  display_name: string;
  description: string;
  category: "logic" | "action" | "ndl";
  branches?: string[];
  fields: CatalogueField[];
  idempotent: boolean;
  destructive: boolean;
  action: boolean;
  loop?: boolean;
};

export type EventInfo = { kind: string; label: string; fields: string[] };

export type Catalogue = {
  items: NodeTypeInfo[];
  events: EventInfo[];
  triggers: { type: string; label: string }[];
  schedule_presets: string[];
  limits: Record<string, { default: number; min: number; max: number }>;
  fixed_limits: Record<string, number>;
};

export const EMPTY_CATALOGUE: Catalogue = { items: [], events: [], triggers: [], schedule_presets: [], limits: {}, fixed_limits: {} };

export const CatalogueContext = createContext<Catalogue>(EMPTY_CATALOGUE);

export function useCatalogue(): Catalogue {
  return useContext(CatalogueContext);
}

export function nodeInfo(cat: Catalogue, type: string): NodeTypeInfo | undefined {
  return cat.items.find((n) => n.type === type);
}

export function eventFields(cat: Catalogue): Record<string, string[]> {
  return Object.fromEntries(cat.events.map((e) => [e.kind, e.fields]));
}

export async function loadCatalogue(): Promise<Catalogue> {
  const raw = (await getAutomationCatalogue()) as unknown as Partial<Catalogue>;
  return { ...EMPTY_CATALOGUE, ...raw } as Catalogue;
}

export type TargetOption = { value: string; label: string; meta?: string };

const targetCache = new Map<string, Promise<TargetOption[]>>();

/** useTargets loads what a target field can pick, once per kind per page. */
export function useTargets(kind: string | undefined): TargetOption[] {
  const [items, setItems] = useState<TargetOption[]>([]);
  useEffect(() => {
    if (!kind) {
      return;
    }
    let live = true;
    let p = targetCache.get(kind);
    if (!p) {
      p = listAutomationTargets(kind)
        .then((r) => (r.items ?? []) as unknown as TargetOption[])
        .catch(() => []);
      targetCache.set(kind, p);
    }
    void p.then((list) => {
      if (live) {
        setItems(list);
      }
    });
    return () => {
      live = false;
    };
  }, [kind]);
  return items;
}

export function forgetTargets() {
  targetCache.clear();
}

export const EVENT_OPS = [
  { value: "eq", label: "is" },
  { value: "neq", label: "is not" },
  { value: "contains", label: "contains" },
  { value: "exists", label: "exists" },
];

export const PRESET_LABELS: Record<string, string> = {
  every_5_minutes: "Every 5 minutes",
  hourly: "Every hour",
  daily: "Every day",
  weekly: "Every week",
  cron: "Cron line",
};

export const STATUS_LABELS: Record<string, string> = {
  queued: "Queued",
  pending: "Starting",
  running: "Running",
  waiting: "Waiting",
  waiting_approval: "Needs approval",
  succeeded: "Succeeded",
  failed: "Failed",
  cancelled: "Cancelled",
  dropped: "Dropped",
  skipped: "Skipped",
  retrying: "Retrying",
};

export function statusTone(status: string | undefined): string {
  switch (status) {
    case "succeeded":
      return "succeeded";
    case "failed":
    case "dropped":
      return "failed";
    case "running":
      return "running";
    case "waiting":
    case "waiting_approval":
    case "queued":
    case "pending":
    case "retrying":
      return "pending";
    case "cancelled":
      return "cancelled";
  }
  return "neutral";
}
