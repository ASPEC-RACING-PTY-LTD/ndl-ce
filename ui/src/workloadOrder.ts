/** Workloads page ordering. Hosts are not part of it: they always stay on top. */

export type WorkloadSort = "name" | "name-desc" | "custom";

type Named = { id: string; name: string };

function byName(a: Named, b: Named): number {
  return a.name.localeCompare(b.name, undefined, { sensitivity: "base", numeric: true }) || a.id.localeCompare(b.id);
}

/**
 * Orders workloads. In custom order, saved ids come first in their saved
 * order; workloads created since are appended alphabetically.
 */
export function orderWorkloads<T extends Named>(items: T[], sort: WorkloadSort, saved: string[]): T[] {
  const list = [...items];
  if (sort === "name") {
    return list.sort(byName);
  }
  if (sort === "name-desc") {
    return list.sort((a, b) => byName(b, a));
  }
  const rank = new Map(saved.map((id, i) => [id, i]));
  return list.sort((a, b) => {
    const ra = rank.get(a.id);
    const rb = rank.get(b.id);
    if (ra != null && rb != null) {
      return ra - rb;
    }
    if (ra != null) {
      return -1;
    }
    if (rb != null) {
      return 1;
    }
    return byName(a, b);
  });
}

/** Moves id to the position of target (before it when moving up, after when moving down). */
export function moveId(order: string[], id: string, target: string): string[] {
  const from = order.indexOf(id);
  const to = order.indexOf(target);
  if (from < 0 || to < 0 || from === to) {
    return order;
  }
  const next = [...order];
  next.splice(from, 1);
  next.splice(to, 0, id);
  return next;
}

/** Moves id by delta positions, clamped to the list. */
export function shiftId(order: string[], id: string, delta: number): string[] {
  const from = order.indexOf(id);
  if (from < 0) {
    return order;
  }
  const to = Math.max(0, Math.min(order.length - 1, from + delta));
  if (to === from) {
    return order;
  }
  return moveId(order, id, order[to]);
}

/** Reads the saved sort; anything unknown falls back to alphabetical. */
export function savedSort(value: string | undefined): WorkloadSort {
  return value === "custom" ? "custom" : "name";
}
