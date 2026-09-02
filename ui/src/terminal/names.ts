export function defaultTabTitle(name: string, existing: string[]): string {
  const base = name.trim() || "Terminal";
  const used = new Set(existing.map((t) => t.trim()));
  if (!used.has(base)) {
    return base;
  }
  for (let n = 2; n < 1000; n += 1) {
    const next = `${base} (${n})`;
    if (!used.has(next)) {
      return next;
    }
  }
  return `${base} (${existing.length + 1})`;
}
