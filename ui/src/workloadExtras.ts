import type { WorkloadExtra } from "./api/client";

export function toggleExtra(selected: string[], id: string, catalog: WorkloadExtra[]): string[] {
  const extra = catalog.find((item) => item.id === id);
  const on = selected.includes(id);
  if (on) {
    const next = selected.filter((item) => item !== id);
    return next.filter((item) => {
      const row = catalog.find((c) => c.id === item);
      return !(row?.requires ?? []).includes(id);
    });
  }
  const next = [...selected, id];
  for (const req of extra?.requires ?? []) {
    if (!next.includes(req)) {
      next.push(req);
    }
  }
  return next;
}

const CATEGORY_LABELS: Record<string, string> = {
  containers: "Containers",
  networking: "Networking",
  web: "Web servers",
  databases: "Databases",
  development: "Development",
  tools: "Tools",
  utilities: "Utilities",
};

const CATEGORY_ORDER = ["containers", "networking", "web", "databases", "development", "tools", "utilities"];

/** groupExtras groups the pickable extras by category; baseline tools are left out since every container gets them. */
export function groupExtras(catalog: WorkloadExtra[]): { category: string; label: string; items: WorkloadExtra[] }[] {
  const pickable = catalog.filter((item) => !item.baseline);
  const cats = [...new Set(pickable.map((item) => item.category || "utilities"))].sort(
    (a, b) => (CATEGORY_ORDER.indexOf(a) + 1 || 99) - (CATEGORY_ORDER.indexOf(b) + 1 || 99) || a.localeCompare(b),
  );
  return cats.map((category) => ({
    category,
    label: CATEGORY_LABELS[category] ?? category.charAt(0).toUpperCase() + category.slice(1),
    items: pickable.filter((item) => (item.category || "utilities") === category),
  }));
}
