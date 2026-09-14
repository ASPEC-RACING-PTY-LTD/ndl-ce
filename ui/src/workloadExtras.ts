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
