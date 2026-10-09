import { memo, useCallback, useDeferredValue, useEffect, useMemo, useRef, useState } from "react";
import { formatRam } from "./caps";
import {
  CATEGORY_ALL,
  CATEGORY_FAVORITES,
  CATEGORY_RECENT,
  VERIFICATION_INFO,
  architecturesOf,
  catalogueInstallMethods,
  categoryLabel,
  categoryOf,
  installMethodOf,
  requirementBadge,
  type CatalogueSort,
} from "./catalogue";
import {
  gameCategories,
  gameFavoriteKey,
  isFavoriteGame,
  monogram,
  queryGames,
  sharedRequirements,
  variantSections,
  type GameEntry,
} from "./games";
import type { CatalogueItem } from "./types";

/** Game tiles rendered per page before "Show more". */
export const CATALOGUE_PAGE_SIZE = 200;

type Props = {
  items: CatalogueItem[];
  favorites: string[];
  recents: string[];
  /** Open game (server type chooser), kept by the page across wizard steps. */
  game: string | null;
  onGameChange: (title: string | null) => void;
  onPick: (item: CatalogueItem) => void;
  onToggleFavorite: (id: string) => void;
};

export function CatalogueBrowser({ items, favorites, recents, game, onGameChange, onPick, onToggleFavorite }: Props) {
  const [q, setQ] = useState("");
  const [category, setCategory] = useState(CATEGORY_ALL);
  const [method, setMethod] = useState("");
  const [arch, setArch] = useState("");
  const [noCredentials, setNoCredentials] = useState(false);
  const [sort, setSort] = useState<CatalogueSort>("popular");
  const [filtersOpen, setFiltersOpen] = useState(false);
  const deferredQ = useDeferredValue(q);

  const methods = useMemo(() => catalogueInstallMethods(items), [items]);
  const facetQuery = useMemo(
    () => ({ q: deferredQ, method, arch, noCredentials, favorites, recents, sort }),
    [deferredQ, method, arch, noCredentials, favorites, recents, sort],
  );
  const chips = useMemo(() => gameCategories(items, facetQuery), [items, facetQuery]);
  const games = useMemo(() => queryGames(items, { ...facetQuery, category }), [items, facetQuery, category]);
  const favoriteSet = useMemo(() => new Set(favorites), [favorites]);
  const templateCount = useMemo(() => games.reduce((n, g) => n + g.matched.length, 0), [games]);
  const activeFilters = (method ? 1 : 0) + (arch ? 1 : 0) + (noCredentials ? 1 : 0) + (sort !== "popular" ? 1 : 0);

  const filterKey = `${deferredQ}\u0000${category}\u0000${method}\u0000${arch}\u0000${noCredentials}\u0000${sort}`;
  const [paging, setPaging] = useState({ key: filterKey, limit: CATALOGUE_PAGE_SIZE });
  const limit = paging.key === filterKey ? paging.limit : CATALOGUE_PAGE_SIZE;
  const showMore = useCallback(() => {
    setPaging((cur) => ({ key: filterKey, limit: (cur.key === filterKey ? cur.limit : CATALOGUE_PAGE_SIZE) + CATALOGUE_PAGE_SIZE }));
  }, [filterKey]);
  const visible = games.slice(0, limit);
  const hasMore = games.length > visible.length;


  const openGame = useCallback(
    (entry: GameEntry) => {
      if (entry.items.length === 1) {
        onPick(entry.items[0]);
      } else {
        onGameChange(entry.title);
      }
    },
    [onGameChange, onPick],
  );

  const selected = useMemo(() => {
    if (!game) {
      return null;
    }
    const all = queryGames(items, { favorites, recents });
    const entry = all.find((g) => g.title === game);
    if (!entry) {
      return null;
    }
    const filtered = games.find((g) => g.key === entry.key);
    return { entry, matched: filtered?.matched ?? [] };
  }, [game, items, favorites, recents, games]);

  if (selected) {
    return (
      <ServerTypeChooser
        entry={selected.entry}
        matched={selected.matched}
        favorites={favoriteSet}
        onBack={() => onGameChange(null)}
        onPick={onPick}
        onToggleFavorite={onToggleFavorite}
      />
    );
  }

  const emptyMessage =
    category === CATEGORY_FAVORITES && favorites.length === 0
      ? "No favourites yet. Use the star on a game to keep it here."
      : category === CATEGORY_RECENT && recents.length === 0
        ? "Games you pick show up here."
        : "No games match that search.";

  return (
    <div className="gs-browser">
      <aside className="gs-browser-side" aria-label="Browse the catalogue">
        <label className="gs-browser-search">
          <span className="visually-hidden">Search games</span>
          <input
            className="field-input"
            type="search"
            value={q}
            placeholder="Search games or engines"
            title="Try mc, rust, cs2, paper, gslt"
            aria-label="Search available game templates"
            onChange={(e) => setQ(e.target.value)}
          />
        </label>
        <div className="gs-browser-cats" role="tablist" aria-orientation="vertical" aria-label="Catalogue categories">
          {chips
            .filter((chip) => chip.count > 0 || chip.id === category || chip.id === CATEGORY_ALL)
            .map((chip) => (
              <button
                key={chip.id}
                type="button"
                role="tab"
                className={"gs-cat" + (category === chip.id ? " is-on" : "")}
                aria-selected={category === chip.id}
                onClick={() => setCategory(chip.id)}
              >
                <span className="gs-cat-label">{chip.label}</span>
                <span className="gs-chip-count">{chip.count}</span>
              </button>
            ))}
        </div>
        <button
          type="button"
          className={"btn btn-ghost btn-sm gs-browser-filter-btn" + (filtersOpen ? " is-on" : "")}
          aria-expanded={filtersOpen}
          aria-controls="gs-browser-filters"
          onClick={() => setFiltersOpen((v) => !v)}
        >
          Filters{activeFilters ? ` (${activeFilters})` : ""}
        </button>
        {filtersOpen ? (
          <div className="gs-browser-filters" id="gs-browser-filters">
            <label>
              <span>Install method</span>
              <select className="field-input" value={method} onChange={(e) => setMethod(e.target.value)}>
                <option value="">Any method</option>
                {methods.map((m) => (
                  <option key={m} value={m}>
                    {m}
                  </option>
                ))}
              </select>
            </label>
            <label>
              <span>Platform</span>
              <select className="field-input" value={arch} onChange={(e) => setArch(e.target.value)}>
                <option value="">Any platform</option>
                <option value="amd64">amd64 (x86_64)</option>
                <option value="arm64">arm64</option>
              </select>
            </label>
            <label>
              <span>Sort</span>
              <select className="field-input" value={sort} onChange={(e) => setSort(e.target.value as CatalogueSort)}>
                <option value="popular">Popular</option>
                <option value="az">A-Z</option>
              </select>
            </label>
            <label className="gs-browser-check">
              <input type="checkbox" checked={noCredentials} onChange={(e) => setNoCredentials(e.target.checked)} />
              <span>No credentials needed</span>
            </label>
          </div>
        ) : null}
      </aside>
      <div className="gs-browser-main">
        <p className="gs-catalogue-count" aria-live="polite">
          {games.length} {games.length === 1 ? "game" : "games"}, {templateCount} server {templateCount === 1 ? "type" : "types"}
        </p>
        <div className="gs-game-grid">
          {visible.map((entry) => (
            <GameTile
              key={entry.key}
              entry={entry}
              favorite={isFavoriteGame(entry, favoriteSet)}
              onOpen={openGame}
              onToggleFavorite={onToggleFavorite}
            />
          ))}
        </div>
        {games.length === 0 ? <p className="gs-meta">{emptyMessage}</p> : null}
        {hasMore ? (
          <div className="gs-catalogue-more">
            <button type="button" className="btn btn-ghost" onClick={showMore}>
              Show more ({games.length - visible.length} more)
            </button>
          </div>
        ) : null}
      </div>
    </div>
  );
}

/** Game or template artwork with a monogram fallback when it cannot load. */
export function GameArt({
  url,
  kind,
  title,
  size,
}: {
  url?: string;
  kind?: string;
  title: string;
  size: "thumb" | "hero" | "icon" | "cover";
}) {
  const candidates = useMemo(() => artCandidates(url, size === "cover"), [url, size]);
  const [attempt, setAttempt] = useState(0);
  const current = candidates[attempt];
  const show = Boolean(current);
  const mode = !show ? "is-mono" : kind === "banner" ? "is-banner" : "is-icon";
  return (
    <span className={`gs-art gs-art-${size} ${mode}`} aria-hidden="true" data-hue={hueOf(title)}>
      {show ? (
        <img key={current} src={current} alt="" loading="lazy" decoding="async" referrerPolicy="no-referrer" onError={() => setAttempt((n) => n + 1)} />
      ) : (
        <span className="gs-art-mono">{monogram(title)}</span>
      )}
    </span>
  );
}

const STEAM_ASSET = /^https:\/\/shared\.akamai\.steamstatic\.com\/store_item_assets\/steam\/apps\/(\d+)\/header\.jpg$/;

/**
 * Image URLs to try in order; Steam art also has an older CDN path. Square
 * covers try the portrait library art first, which crops well to a square,
 * then the wide store header.
 */
export function artCandidates(url?: string, cover = false): string[] {
  if (!url) {
    return [];
  }
  const steam = STEAM_ASSET.exec(url);
  if (steam) {
    const header = [url, `https://cdn.cloudflare.steamstatic.com/steam/apps/${steam[1]}/header.jpg`];
    if (cover) {
      return [`https://shared.akamai.steamstatic.com/store_item_assets/steam/apps/${steam[1]}/library_600x900.jpg`, ...header];
    }
    return header;
  }
  return [url];
}

function hueOf(title: string): number {
  let h = 0;
  for (const ch of title) {
    h = (h * 31 + ch.charCodeAt(0)) % 360;
  }
  return Math.round(h / 30) * 30;
}

type TileProps = {
  entry: GameEntry;
  favorite: boolean;
  onOpen: (entry: GameEntry) => void;
  onToggleFavorite: (id: string) => void;
};

// A game is a square cover. Its name shows on hover and focus, and is
// always its accessible name; what it needs (a token, an account) is a
// small corner mark.
const GameTile = memo(function GameTile({ entry, favorite, onOpen, onToggleFavorite }: TileProps) {
  const count = entry.items.length;
  const single = count === 1 ? entry.items[0] : null;
  const needs = sharedRequirements(entry);
  const meta = single ? installMethodOf(single) : `${count} server types`;
  const favKey = single ? single.id : gameFavoriteKey(entry.title);
  const hint = `${entry.title}\n${categoryLabel(entry.category)} · ${meta}${needs.length ? `\nNeeds: ${needs.map((r) => r.label).join(", ")}` : ""}`;
  return (
    <div className="gs-game">
      <button
        type="button"
        className="gs-game-tile"
        title={hint}
        onClick={() => onOpen(entry)}
        aria-label={count > 1 ? `${entry.title}, ${count} server types` : entry.title}
      >
        <GameArt url={entry.logoUrl} kind={entry.logoKind} title={entry.title} size="cover" />
        <span className="gs-game-label" aria-hidden="true">
          <span className="gs-game-title">{entry.title}</span>
          <span className="gs-game-meta">{meta}</span>
        </span>
        {needs.length ? (
          <span className="gs-game-need" aria-hidden="true">
            {requirementBadge(needs[0])}
          </span>
        ) : null}
      </button>
      <button
        type="button"
        className={"gs-catalogue-star" + (favorite ? " is-on" : "")}
        aria-pressed={favorite}
        aria-label={`Favourite ${entry.title}`}
        title={favorite ? "Remove from favourites" : "Add to favourites"}
        onClick={() => onToggleFavorite(favKey)}
      >
        <span aria-hidden="true">{favorite ? "★" : "☆"}</span>
      </button>
    </div>
  );
});

type ChooserProps = {
  entry: GameEntry;
  matched: CatalogueItem[];
  favorites: ReadonlySet<string>;
  onBack: () => void;
  onPick: (item: CatalogueItem) => void;
  onToggleFavorite: (id: string) => void;
};

function ServerTypeChooser({ entry, matched, favorites, onBack, onPick, onToggleFavorite }: ChooserProps) {
  const [showAll, setShowAll] = useState(false);
  const filtered = matched.length > 0 && matched.length < entry.items.length && !showAll;
  const list = filtered ? entry.items.filter((i) => matched.includes(i)) : entry.items;
  const sections = variantSections(list);
  const heading = useRef<HTMLHeadingElement | null>(null);
  useEffect(() => {
    heading.current?.focus();
  }, [entry.key]);
  return (
    <div className="gs-chooser">
      <button type="button" className="btn btn-ghost gs-chooser-back" onClick={onBack}>
        ← All games
      </button>
      <header className="gs-chooser-head">
        <GameArt url={entry.logoUrl} kind={entry.logoKind} title={entry.title} size="hero" />
        <div>
          <h2 ref={heading} tabIndex={-1}>
            {entry.title}
          </h2>
          <p className="gs-meta">
            Choose a server type. {entry.items.length} available
            {filtered ? `, ${list.length} match your search.` : "."}
          </p>
          {filtered ? (
            <button type="button" className="btn btn-link" onClick={() => setShowAll(true)}>
              Show all {entry.items.length}
            </button>
          ) : null}
        </div>
      </header>
      {sections.map((section) => (
        <section key={section.label || "all"} className="gs-chooser-section" aria-label={section.label || `${entry.title} server types`}>
          {section.label ? <h3 className="gs-chooser-label">{section.label}</h3> : null}
          <div className="gs-variant-list">
            {section.items.map((item) => (
              <VariantCard key={item.id} item={item} favorite={favorites.has(item.id)} onPick={onPick} onToggleFavorite={onToggleFavorite} />
            ))}
          </div>
        </section>
      ))}
    </div>
  );
}

type VariantProps = {
  item: CatalogueItem;
  favorite: boolean;
  onPick: (item: CatalogueItem) => void;
  onToggleFavorite: (id: string) => void;
};

export const VariantCard = memo(function VariantCard({ item, favorite, onPick, onToggleFavorite }: VariantProps) {
  const verify = item.verification ? VERIFICATION_INFO[item.verification] : undefined;
  const archs = architecturesOf(item);
  const ram = item.default_memory_mb;
  return (
    <div className="gs-variant">
      <button type="button" className="gs-variant-main" onClick={() => onPick(item)}>
        <GameArt url={item.logo_url} kind={item.logo_kind} title={item.name} size="icon" />
        <span className="gs-variant-body">
          <span className="gs-variant-name">{item.name}</span>
          {item.summary ? <span className="gs-variant-summary">{item.summary}</span> : null}
          <span className="gs-catalogue-badges">
            <span className="gs-badge">{installMethodOf(item)}</span>
            {categoryOf(item) === "proxy" ? <span className="gs-badge">Proxy</span> : null}
            {archs.includes("arm64") ? <span className="gs-badge is-arch">arm64</span> : null}
            {(item.requirements ?? []).map((req) => (
              <span key={`${req.kind}:${req.stage}:${req.env ?? ""}`} className={"gs-badge" + (req.stage === "optional" ? "" : " is-need")} title={req.label}>
                {requirementBadge(req)}
              </span>
            ))}
            {ram ? <span className="gs-badge">{formatRam(ram * 1024 * 1024)} RAM</span> : null}
            {verify ? (
              <span className={"gs-badge is-verify is-" + item.verification} title={verify.title}>
                {verify.label}
              </span>
            ) : null}
          </span>
        </span>
      </button>
      <button
        type="button"
        className={"gs-catalogue-star" + (favorite ? " is-on" : "")}
        aria-pressed={favorite}
        aria-label={`Favourite ${item.name}`}
        title={favorite ? "Remove from favourites" : "Add to favourites"}
        onClick={() => onToggleFavorite(item.id)}
      >
        <span aria-hidden="true">{favorite ? "★" : "☆"}</span>
      </button>
    </div>
  );
});
