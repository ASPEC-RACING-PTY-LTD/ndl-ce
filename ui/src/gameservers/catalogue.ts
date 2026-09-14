import type { CatalogueItem } from "./types";

export type CatalogueGroup = "all" | "builtin" | "steamcmd" | "standalone" | "minecraft";

export const CATALOGUE_GROUPS: { id: CatalogueGroup; label: string }[] = [
  { id: "all", label: "All" },
  { id: "builtin", label: "Built in" },
  { id: "steamcmd", label: "SteamCMD" },
  { id: "standalone", label: "Standalone" },
  { id: "minecraft", label: "Minecraft" },
];

export function catalogueSearchText(item: CatalogueItem): string {
  return [
    item.id,
    item.name,
    item.game,
    item.implementation,
    item.family,
    item.summary,
    item.runtime_kind,
    item.hint,
    item.builtin ? "built in builtin" : "",
    ...(item.tags ?? []),
    ...(item.aliases ?? []),
  ]
    .filter(Boolean)
    .join(" ")
    .toLowerCase();
}

export function catalogueMatches(item: CatalogueItem, query: string): boolean {
  const needle = query.trim().toLowerCase();
  if (!needle) {
    return true;
  }
  if (catalogueSearchText(item).includes(needle)) {
    return true;
  }
  return (item.aliases ?? []).some((alias) => alias.trim().toLowerCase() === needle);
}

export function catalogueInGroup(item: CatalogueItem, group: CatalogueGroup): boolean {
  switch (group) {
    case "all":
      return true;
    case "builtin":
      return Boolean(item.builtin);
    case "steamcmd":
      return item.runtime_kind === "SteamCMD" || item.family === "steam" || item.family === "source";
    case "standalone":
      return item.runtime_kind === "Standalone";
    case "minecraft":
      return item.family === "minecraft";
    default:
      return true;
  }
}

export function filterCatalogue(items: CatalogueItem[], query: string, group: CatalogueGroup = "all"): CatalogueItem[] {
  return items.filter((item) => catalogueInGroup(item, group) && catalogueMatches(item, query));
}

export function catalogueMark(item: CatalogueItem): string {
  const source = (item.name || item.game || "?").trim();
  return source.slice(0, 1).toUpperCase();
}

export function showCreateVariable(env: string, viewable?: boolean, required?: boolean, startRequires: string[] = []): boolean {
  if (viewable !== false || required) {
    return true;
  }
  return startRequires.includes(env);
}
