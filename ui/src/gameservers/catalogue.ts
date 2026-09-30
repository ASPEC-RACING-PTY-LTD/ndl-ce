import type { CatalogueItem, GameRequirement } from "./types";

export type CatalogueGroup = "all" | "builtin" | "steamcmd" | "standalone" | "minecraft";

export const CATALOGUE_GROUPS: { id: CatalogueGroup; label: string }[] = [
  { id: "all", label: "All" },
  { id: "builtin", label: "Built in" },
  { id: "steamcmd", label: "SteamCMD" },
  { id: "standalone", label: "Standalone" },
  { id: "minecraft", label: "Minecraft" },
];

/** Chip ids that are not data categories. */
export const CATEGORY_ALL = "all";
export const CATEGORY_FAVORITES = "favorites";
export const CATEGORY_RECENT = "recent";

const CATEGORY_ORDER = [
  "minecraft",
  "proxy",
  "survival",
  "sandbox",
  "shooter",
  "tactical",
  "simulation",
  "strategy",
  "racing",
  "rpg",
  "roleplay",
  "party",
  "sports",
  "other",
  "imported",
];

const CATEGORY_LABELS: Record<string, string> = {
  minecraft: "Minecraft",
  proxy: "Proxies",
  survival: "Survival",
  sandbox: "Sandbox",
  shooter: "Shooters",
  tactical: "Tactical",
  simulation: "Simulation",
  strategy: "Strategy",
  racing: "Racing",
  rpg: "RPG",
  roleplay: "Roleplay",
  party: "Party",
  sports: "Sports",
  other: "Other",
  imported: "Imported",
};

const REQUIREMENT_LABELS: Record<string, string> = {
  steam_account: "Steam account",
  gslt: "GSLT",
  license_key: "Licence key",
  token: "Token",
  api_key: "API key",
  eula: "EULA",
  purchase: "Purchase",
};

/** Extra words so people can search the way they talk about requirements. */
const REQUIREMENT_SEARCH: Record<string, string> = {
  steam_account: "steam account steam login steam_account",
  gslt: "gslt game server login token",
  license_key: "license key licence key license_key",
  token: "token",
  api_key: "api key api_key",
  eula: "eula",
  purchase: "purchase paid",
};

export const VERIFICATION_INFO: Record<string, { label: string; title: string }> = {
  schema: {
    label: "Schema checked",
    title: "Schema checked: passes automated catalogue validation only. It has not been installed by the test suite.",
  },
  source: {
    label: "Source checked",
    title: "Source checked: upstream IDs and download URLs were cross-checked against a named reference.",
  },
  installed: {
    label: "Install tested",
    title: "Install tested: a real install completed in an isolated test environment.",
  },
  started: {
    label: "Start tested",
    title: "Start tested: installed, and the server process started in an isolated test environment.",
  },
};

export function categoryOf(item: CatalogueItem): string {
  const cat = (item.category ?? "").trim().toLowerCase();
  if (cat) {
    return cat;
  }
  if (!item.builtin && !item.id.startsWith("ndl-")) {
    return "imported";
  }
  return item.family === "minecraft" ? "minecraft" : "other";
}

export function categoryLabel(id: string): string {
  if (CATEGORY_LABELS[id]) {
    return CATEGORY_LABELS[id];
  }
  return id ? id.slice(0, 1).toUpperCase() + id.slice(1) : "Other";
}

export function installMethodOf(item: CatalogueItem): string {
  return item.install_method || item.runtime_kind || (item.builtin ? "Built in" : "Imported");
}

/** Templates that do not declare architectures are treated as amd64 only. */
export function architecturesOf(item: Pick<CatalogueItem, "architectures">): string[] {
  return item.architectures?.length ? item.architectures : ["amd64"];
}

/** True when the template needs credentials (not just an EULA) to install or start. */
export function needsCredentials(item: CatalogueItem): boolean {
  if (item.requirements) {
    return item.requirements.some((r) => (r.stage === "install" || r.stage === "start") && r.kind !== "eula");
  }
  return (item.capabilities ?? []).includes("license_key");
}

export function requirementBadge(req: Pick<GameRequirement, "kind" | "stage" | "label">): string {
  const base = REQUIREMENT_LABELS[req.kind] ?? req.label ?? req.kind;
  return req.stage === "optional" ? `${base} optional` : base;
}

export function requirementStageLabel(stage: string): string {
  switch (stage) {
    case "install":
      return "Needed to install";
    case "start":
      return "Needed to start";
    case "optional":
      return "Optional";
    default:
      return stage;
  }
}

const searchTextCache = new WeakMap<CatalogueItem, string>();

export function catalogueSearchText(item: CatalogueItem): string {
  const cached = searchTextCache.get(item);
  if (cached !== undefined) {
    return cached;
  }
  const category = categoryOf(item);
  const text = [
    item.id,
    item.name,
    item.game,
    item.game_title,
    item.implementation,
    item.family,
    item.summary,
    item.runtime_kind,
    item.install_method,
    item.engine,
    category,
    categoryLabel(category),
    item.hint,
    item.builtin ? "built in builtin" : "",
    ...(item.architectures ?? []),
    ...(item.tags ?? []),
    ...(item.aliases ?? []),
    ...(item.requirements ?? []).flatMap((r) => [r.kind, r.label, REQUIREMENT_SEARCH[r.kind] ?? ""]),
  ]
    .filter(Boolean)
    .join(" ")
    .toLowerCase();
  searchTextCache.set(item, text);
  return text;
}

function words(value: string): string[] {
  return value.split(/[^a-z0-9]+/).filter(Boolean);
}

/**
 * Scores how well an item matches a query. 0 means no match.
 * Exact alias, name, or id matches rank first, then prefix matches,
 * then word prefix matches, then substring matches anywhere in the
 * searchable metadata (engine, method, platform, requirements, ...).
 */
export function catalogueScore(item: CatalogueItem, query: string): number {
  const needle = query.trim().toLowerCase();
  if (!needle) {
    return 1;
  }
  const aliases = (item.aliases ?? []).map((a) => a.trim().toLowerCase());
  const name = item.name.toLowerCase();
  const primary = [name, item.id.toLowerCase(), (item.game ?? "").toLowerCase(), ...aliases];
  if (primary.includes(needle)) {
    return 100;
  }
  const title = (item.game_title ?? "").toLowerCase();
  if (title === needle) {
    return 90;
  }
  const named = title ? [...primary, title] : primary;
  if (named.some((n) => n.startsWith(needle))) {
    return 70;
  }
  if (named.some((n) => words(n).some((w) => w.startsWith(needle)))) {
    return 50;
  }
  const text = catalogueSearchText(item);
  if (text.includes(needle)) {
    return 20;
  }
  const tokens = needle.split(/\s+/).filter(Boolean);
  if (tokens.length > 1 && tokens.every((t) => text.includes(t))) {
    return 10;
  }
  return 0;
}

export function catalogueMatches(item: CatalogueItem, query: string): boolean {
  return catalogueScore(item, query) > 0;
}

export function catalogueInGroup(item: CatalogueItem, group: CatalogueGroup): boolean {
  switch (group) {
    case "all":
      return true;
    case "builtin":
      return Boolean(item.builtin);
    case "steamcmd":
      return item.runtime_kind === "SteamCMD" || item.install_method === "SteamCMD" || item.family === "steam" || item.family === "source";
    case "standalone":
      return item.runtime_kind === "Standalone";
    case "minecraft":
      return item.family === "minecraft";
    default:
      return true;
  }
}

function rankByScore(items: CatalogueItem[], query: string): CatalogueItem[] {
  const scored: { item: CatalogueItem; score: number; index: number }[] = [];
  items.forEach((item, index) => {
    const score = catalogueScore(item, query);
    if (score > 0) {
      scored.push({ item, score, index });
    }
  });
  scored.sort((a, b) => b.score - a.score || a.index - b.index);
  return scored.map((s) => s.item);
}

/** Filters by legacy group and query; results are ranked when a query is given. */
export function filterCatalogue(items: CatalogueItem[], query: string, group: CatalogueGroup = "all"): CatalogueItem[] {
  const inGroup = items.filter((item) => catalogueInGroup(item, group));
  return query.trim() ? rankByScore(inGroup, query) : inGroup;
}

export type CatalogueSort = "popular" | "az";

export type CatalogueQuery = {
  q?: string;
  /** "all", "favorites", "recent", or a data category id. */
  category?: string;
  /** Install method label, or "" for any. */
  method?: string;
  /** "amd64", "arm64", or "" for any. */
  arch?: string;
  noCredentials?: boolean;
  sort?: CatalogueSort;
  group?: CatalogueGroup;
  favorites?: readonly string[];
  recents?: readonly string[];
};

function passesFacets(item: CatalogueItem, opts: CatalogueQuery): boolean {
  if (opts.group && !catalogueInGroup(item, opts.group)) {
    return false;
  }
  if (opts.method && installMethodOf(item) !== opts.method) {
    return false;
  }
  if (opts.arch && !architecturesOf(item).includes(opts.arch)) {
    return false;
  }
  if (opts.noCredentials && needsCredentials(item)) {
    return false;
  }
  return true;
}

function inCategory(item: CatalogueItem, category: string, favorites: ReadonlySet<string>, recents: ReadonlySet<string>): boolean {
  switch (category) {
    case "":
    case CATEGORY_ALL:
      return true;
    case CATEGORY_FAVORITES:
      return favorites.has(item.id);
    case CATEGORY_RECENT:
      return recents.has(item.id);
    default:
      return categoryOf(item) === category;
  }
}

function byName(a: CatalogueItem, b: CatalogueItem): number {
  return a.name.localeCompare(b.name, undefined, { sensitivity: "base" });
}

/** Applies search, chips, facets, and sort. */
export function queryCatalogue(items: CatalogueItem[], opts: CatalogueQuery): CatalogueItem[] {
  const favorites = new Set(opts.favorites ?? []);
  const recentList = opts.recents ?? [];
  const recents = new Set(recentList);
  const category = opts.category ?? CATEGORY_ALL;
  const q = (opts.q ?? "").trim();
  const base = items.filter((item) => passesFacets(item, opts) && inCategory(item, category, favorites, recents));
  if (q) {
    const ranked = rankByScore(base, q);
    if (opts.sort === "az") {
      const score = new Map(ranked.map((item) => [item, catalogueScore(item, q)]));
      return [...ranked].sort((a, b) => (score.get(b) ?? 0) - (score.get(a) ?? 0) || byName(a, b));
    }
    return ranked;
  }
  if (category === CATEGORY_RECENT && opts.sort !== "az") {
    const order = new Map(recentList.map((id, i) => [id, i]));
    return [...base].sort((a, b) => (order.get(a.id) ?? 0) - (order.get(b.id) ?? 0));
  }
  if (opts.sort === "az") {
    return [...base].sort(byName);
  }
  return base;
}

export type CategoryChip = { id: string; label: string; count: number };

/**
 * Chips derived from the data. The set of chips comes from all items so
 * the row stays stable; counts reflect the current search and facets.
 */
export function catalogueCategories(items: CatalogueItem[], opts: CatalogueQuery = {}): CategoryChip[] {
  const favorites = new Set(opts.favorites ?? []);
  const recents = new Set(opts.recents ?? []);
  const present = new Set(items.map(categoryOf));
  const counts = new Map<string, number>();
  let all = 0;
  let fav = 0;
  let rec = 0;
  const q = opts.q ?? "";
  for (const item of items) {
    if (!passesFacets(item, opts) || !catalogueMatches(item, q)) {
      continue;
    }
    all++;
    if (favorites.has(item.id)) {
      fav++;
    }
    if (recents.has(item.id)) {
      rec++;
    }
    const cat = categoryOf(item);
    counts.set(cat, (counts.get(cat) ?? 0) + 1);
  }
  const ordered = [...present].sort((a, b) => {
    const ia = CATEGORY_ORDER.indexOf(a);
    const ib = CATEGORY_ORDER.indexOf(b);
    if (ia === -1 && ib === -1) {
      return a.localeCompare(b);
    }
    if (ia === -1) {
      return 1;
    }
    if (ib === -1) {
      return -1;
    }
    return ia - ib;
  });
  return [
    { id: CATEGORY_ALL, label: "All", count: all },
    { id: CATEGORY_FAVORITES, label: "Favourites", count: fav },
    { id: CATEGORY_RECENT, label: "Recent", count: rec },
    ...ordered.map((id) => ({ id, label: categoryLabel(id), count: counts.get(id) ?? 0 })),
  ];
}

export function catalogueInstallMethods(items: CatalogueItem[]): string[] {
  return [...new Set(items.map(installMethodOf))].sort((a, b) => a.localeCompare(b));
}

export type CatalogueEntry =
  | { kind: "single"; key: string; item: CatalogueItem }
  | { kind: "group"; key: string; title: string; items: CatalogueItem[] };

/**
 * Groups templates that share a game_title. A game with one template
 * stays a single entry. Entry order follows the first item of each game.
 */
export function groupCatalogue(items: CatalogueItem[]): CatalogueEntry[] {
  const byTitle = new Map<string, CatalogueItem[]>();
  for (const item of items) {
    const title = item.game_title?.trim().toLowerCase();
    if (!title) {
      continue;
    }
    const list = byTitle.get(title);
    if (list) {
      list.push(item);
    } else {
      byTitle.set(title, [item]);
    }
  }
  const out: CatalogueEntry[] = [];
  const emitted = new Set<string>();
  for (const item of items) {
    const title = item.game_title?.trim().toLowerCase();
    const list = title ? byTitle.get(title) : undefined;
    if (!title || !list || list.length < 2) {
      out.push({ kind: "single", key: item.id, item });
      continue;
    }
    if (emitted.has(title)) {
      continue;
    }
    emitted.add(title);
    out.push({ kind: "group", key: `game:${title}`, title: item.game_title!.trim(), items: list });
  }
  return out;
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

/** Label of the image the template runs by default. */
export function defaultImageLabel(images?: Record<string, string>, defaultImage?: string): string {
  const entries = Object.entries(images ?? {});
  if (entries.length === 0) {
    return "";
  }
  const hit = entries.find(([, image]) => image === defaultImage);
  return (hit ?? entries[0])[0];
}
