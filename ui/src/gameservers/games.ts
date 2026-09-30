import {
  CATEGORY_ALL,
  CATEGORY_FAVORITES,
  CATEGORY_ORDER,
  CATEGORY_RECENT,
  categoryLabel,
  categoryOf,
  queryCatalogue,
  type CatalogueQuery,
  type CatalogueSort,
  type CategoryChip,
} from "./catalogue";
import type { CatalogueItem, GameRequirement } from "./types";

/** One game in the catalogue grid. Its templates are the server types. */
export type GameEntry = {
  key: string;
  title: string;
  /** Every template of the game, catalogue order. */
  items: CatalogueItem[];
  /** Templates that match the current search and filters, best first. */
  matched: CatalogueItem[];
  category: string;
  logoUrl?: string;
  logoKind?: string;
};

/** Favourites hold template IDs and whole games as "game:<title>". */
export function gameFavoriteKey(title: string): string {
  return `game:${title}`;
}

export function gameTitleOf(item: CatalogueItem): string {
  return (item.game_title || item.name || item.game || "").trim();
}

function gameKeyOf(title: string): string {
  return title.toLowerCase();
}

/** Groups templates by game, keeping first-appearance order. */
export function groupGames(items: CatalogueItem[]): GameEntry[] {
  const byKey = new Map<string, GameEntry>();
  const out: GameEntry[] = [];
  for (const item of items) {
    const title = gameTitleOf(item);
    const key = gameKeyOf(title);
    let entry = byKey.get(key);
    if (!entry) {
      entry = { key, title, items: [], matched: [], category: categoryOf(item) };
      byKey.set(key, entry);
      out.push(entry);
    }
    entry.items.push(item);
    if (!entry.logoUrl && item.game_logo_url) {
      entry.logoUrl = item.game_logo_url;
      entry.logoKind = item.game_logo_kind;
    }
  }
  for (const entry of out) {
    // A game with no artwork of its own borrows its only template's mark.
    if (!entry.logoUrl && entry.items.length === 1 && entry.items[0].logo_url) {
      entry.logoUrl = entry.items[0].logo_url;
      entry.logoKind = entry.items[0].logo_kind;
    }
  }
  return out;
}

export function isFavoriteGame(entry: GameEntry, favorites: ReadonlySet<string>): boolean {
  return favorites.has(gameFavoriteKey(entry.title)) || entry.items.some((item) => favorites.has(item.id));
}

function recentRank(entry: GameEntry, recents: readonly string[]): number {
  let best = Infinity;
  for (const item of entry.items) {
    const i = recents.indexOf(item.id);
    if (i !== -1 && i < best) {
      best = i;
    }
  }
  return best;
}

export type GameQuery = Omit<CatalogueQuery, "category"> & { category?: string; sort?: CatalogueSort };

/**
 * Games whose templates match the search and filters. The order follows
 * the best matching template when searching, otherwise catalogue order.
 */
export function queryGames(items: CatalogueItem[], opts: GameQuery): GameEntry[] {
  const all = groupGames(items);
  const byKey = new Map(all.map((g) => [g.key, g]));
  const ranked = queryCatalogue(items, { ...opts, category: CATEGORY_ALL, sort: opts.q?.trim() ? opts.sort : "popular" });
  const order: GameEntry[] = [];
  const seen = new Set<string>();
  const matched = new Map<string, CatalogueItem[]>();
  for (const item of ranked) {
    const key = gameKeyOf(gameTitleOf(item));
    const list = matched.get(key);
    if (list) {
      list.push(item);
    } else {
      matched.set(key, [item]);
    }
    if (!seen.has(key)) {
      seen.add(key);
      const g = byKey.get(key);
      if (g) {
        order.push(g);
      }
    }
  }
  const favorites = new Set(opts.favorites ?? []);
  const recents = opts.recents ?? [];
  const category = opts.category ?? CATEGORY_ALL;
  let out = order
    .map((g) => ({ ...g, matched: matched.get(g.key) ?? [] }))
    .filter((g) => {
      if (category === CATEGORY_ALL) {
        return true;
      }
      if (category === CATEGORY_FAVORITES) {
        return isFavoriteGame(g, favorites);
      }
      if (category === CATEGORY_RECENT) {
        return recentRank(g, recents) !== Infinity;
      }
      return g.matched.some((item) => categoryOf(item) === category);
    });
  if (category === CATEGORY_RECENT && opts.sort !== "az") {
    out = [...out].sort((a, b) => recentRank(a, recents) - recentRank(b, recents));
  } else if (opts.sort === "az" && !opts.q?.trim()) {
    out = [...out].sort((a, b) => a.title.localeCompare(b.title));
  }
  return out;
}

/** Category chips counting games (not templates). */
export function gameCategories(items: CatalogueItem[], opts: GameQuery): CategoryChip[] {
  const games = queryGames(items, { ...opts, category: CATEGORY_ALL });
  const favorites = new Set(opts.favorites ?? []);
  const recents = opts.recents ?? [];
  const present = new Set(items.map(categoryOf));
  const counts = new Map<string, number>();
  for (const g of games) {
    const cats = new Set(g.matched.map(categoryOf));
    for (const c of cats) {
      counts.set(c, (counts.get(c) ?? 0) + 1);
    }
  }
  const ordered = [...present].sort((a, b) => {
    const ia = CATEGORY_ORDER.indexOf(a);
    const ib = CATEGORY_ORDER.indexOf(b);
    return (ia === -1 ? 999 : ia) - (ib === -1 ? 999 : ib) || a.localeCompare(b);
  });
  return [
    { id: CATEGORY_ALL, label: "All", count: games.length },
    { id: CATEGORY_FAVORITES, label: "Favourites", count: games.filter((g) => isFavoriteGame(g, favorites)).length },
    { id: CATEGORY_RECENT, label: "Recent", count: games.filter((g) => recentRank(g, recents) !== Infinity).length },
    ...ordered.map((id) => ({ id, label: categoryLabel(id), count: counts.get(id) ?? 0 })),
  ];
}

/** Requirements every server type of a game shares (worth showing on the tile). */
export function sharedRequirements(entry: GameEntry): GameRequirement[] {
  const [first, ...rest] = entry.items;
  if (!first) {
    return [];
  }
  const key = (r: GameRequirement) => `${r.kind}:${r.stage}`;
  const common = (first.requirements ?? []).filter((r) => r.stage !== "optional" && r.kind !== "eula");
  return common.filter((r) => rest.every((item) => (item.requirements ?? []).some((o) => key(o) === key(r))));
}

/** Server types split into labelled sections when a game spans categories (servers and proxies). */
export function variantSections(items: CatalogueItem[]): { label: string; items: CatalogueItem[] }[] {
  const byCat = new Map<string, CatalogueItem[]>();
  for (const item of items) {
    const c = categoryOf(item);
    const list = byCat.get(c);
    if (list) {
      list.push(item);
    } else {
      byCat.set(c, [item]);
    }
  }
  if (byCat.size < 2) {
    return [{ label: "", items }];
  }
  return [...byCat.entries()].map(([c, list]) => ({ label: c === "proxy" ? "Proxies and bridges" : "Servers", items: list }));
}

export function monogram(title: string): string {
  const words = title.replace(/[^A-Za-z0-9 ]+/g, " ").trim().split(/\s+/).filter(Boolean);
  if (words.length === 0) {
    return "?";
  }
  if (words.length === 1) {
    return words[0].slice(0, 2).toUpperCase();
  }
  return (words[0][0] + words[1][0]).toUpperCase();
}
