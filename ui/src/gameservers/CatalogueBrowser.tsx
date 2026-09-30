import { memo, useCallback, useDeferredValue, useEffect, useId, useMemo, useRef, useState } from "react";
import { familyTone, formatRam } from "./caps";
import {
  CATEGORY_ALL,
  CATEGORY_FAVORITES,
  CATEGORY_RECENT,
  VERIFICATION_INFO,
  architecturesOf,
  catalogueCategories,
  catalogueInstallMethods,
  catalogueMark,
  groupCatalogue,
  installMethodOf,
  queryCatalogue,
  requirementBadge,
  type CatalogueEntry,
  type CatalogueSort,
} from "./catalogue";
import type { CatalogueItem } from "./types";

/** Cards rendered per page before "Show more". */
export const CATALOGUE_PAGE_SIZE = 60;
/** Groups with more templates than this start collapsed unless searching. */
export const GROUP_OPEN_MAX = 6;

type Props = {
  items: CatalogueItem[];
  favorites: string[];
  recents: string[];
  onPick: (item: CatalogueItem) => void;
  onToggleFavorite: (id: string) => void;
};

export function CatalogueBrowser({ items, favorites, recents, onPick, onToggleFavorite }: Props) {
  const [q, setQ] = useState("");
  const [category, setCategory] = useState(CATEGORY_ALL);
  const [method, setMethod] = useState("");
  const [arch, setArch] = useState("");
  const [noCredentials, setNoCredentials] = useState(false);
  const [sort, setSort] = useState<CatalogueSort>("popular");
  const [expanded, setExpanded] = useState<Record<string, boolean>>({});
  const deferredQ = useDeferredValue(q);
  const searching = deferredQ.trim() !== "";

  const methods = useMemo(() => catalogueInstallMethods(items), [items]);
  const facetQuery = useMemo(
    () => ({ q: deferredQ, method, arch, noCredentials, favorites, recents }),
    [deferredQ, method, arch, noCredentials, favorites, recents],
  );
  const chips = useMemo(() => catalogueCategories(items, facetQuery), [items, facetQuery]);
  const results = useMemo(() => queryCatalogue(items, { ...facetQuery, category, sort }), [items, facetQuery, category, sort]);
  const entries = useMemo(() => groupCatalogue(results), [results]);
  const favoriteSet = useMemo(() => new Set(favorites), [favorites]);

  // Reset the page size whenever the result set changes shape.
  const filterKey = `${deferredQ}\u0000${category}\u0000${method}\u0000${arch}\u0000${noCredentials}\u0000${sort}`;
  const [paging, setPaging] = useState({ key: filterKey, limit: CATALOGUE_PAGE_SIZE });
  const limit = paging.key === filterKey ? paging.limit : CATALOGUE_PAGE_SIZE;
  const showMore = useCallback(() => {
    setPaging((cur) => ({ key: filterKey, limit: (cur.key === filterKey ? cur.limit : CATALOGUE_PAGE_SIZE) + CATALOGUE_PAGE_SIZE }));
  }, [filterKey]);

  const isOpen = useCallback(
    (entry: Extract<CatalogueEntry, { kind: "group" }>) => expanded[entry.key] ?? (searching || entry.items.length <= GROUP_OPEN_MAX),
    [expanded, searching],
  );
  const toggleGroup = useCallback(
    (key: string, open: boolean) => {
      setExpanded((cur) => ({ ...cur, [key]: !open }));
    },
    [],
  );

  const { visible, shown, total } = useMemo(() => {
    let remaining = limit;
    let shownCards = 0;
    let totalCards = 0;
    const out: { entry: CatalogueEntry; open: boolean; items: CatalogueItem[] }[] = [];
    for (const entry of entries) {
      if (entry.kind === "single") {
        totalCards++;
        if (remaining > 0) {
          out.push({ entry, open: true, items: [entry.item] });
          remaining--;
          shownCards++;
        }
        continue;
      }
      const open = isOpen(entry);
      const units = open ? entry.items.length : 1;
      totalCards += units;
      if (remaining <= 0) {
        continue;
      }
      const slice = open ? entry.items.slice(0, remaining) : [];
      out.push({ entry, open, items: slice });
      const used = open ? slice.length : 1;
      remaining -= used;
      shownCards += used;
    }
    return { visible: out, shown: shownCards, total: totalCards };
  }, [entries, isOpen, limit]);

  const hasMore = shown < total;
  const sentinel = useRef<HTMLDivElement | null>(null);
  useEffect(() => {
    const el = sentinel.current;
    if (!el || !hasMore || typeof IntersectionObserver === "undefined") {
      return;
    }
    const observer = new IntersectionObserver(
      (records) => {
        if (records.some((r) => r.isIntersecting)) {
          showMore();
        }
      },
      { rootMargin: "400px 0px" },
    );
    observer.observe(el);
    return () => observer.disconnect();
  }, [hasMore, showMore]);

  const emptyMessage =
    category === CATEGORY_FAVORITES && favorites.length === 0
      ? "No favourites yet. Use the star on a template to keep it here."
      : category === CATEGORY_RECENT && recents.length === 0
        ? "Templates you pick show up here."
        : "No templates match that search.";

  return (
    <>
      <label className="gs-catalogue-search">
        <span>Available game templates</span>
        <input
          className="field-input"
          type="search"
          value={q}
          placeholder="Search name, alias, engine, or need. Try mc, pz, cs2, arm64, gslt"
          aria-label="Search available game templates"
          onChange={(e) => setQ(e.target.value)}
        />
      </label>
      <div className="gs-catalogue-groups" role="tablist" aria-label="Catalogue categories">
        {chips.map((chip) => (
          <button
            key={chip.id}
            type="button"
            role="tab"
            className={"gs-chip" + (category === chip.id ? " is-on" : "")}
            aria-selected={category === chip.id}
            onClick={() => setCategory(chip.id)}
          >
            {chip.label}
            <span className="gs-chip-count">{chip.count}</span>
          </button>
        ))}
      </div>
      <div className="gs-catalogue-filters">
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
        <label className="gs-catalogue-check">
          <input type="checkbox" checked={noCredentials} onChange={(e) => setNoCredentials(e.target.checked)} />
          <span>No credentials needed</span>
        </label>
        <label>
          <span>Sort</span>
          <select className="field-input" value={sort} onChange={(e) => setSort(e.target.value as CatalogueSort)}>
            <option value="popular">Popular</option>
            <option value="az">A-Z</option>
          </select>
        </label>
      </div>
      <p className="gs-catalogue-count" aria-live="polite">
        {results.length} {results.length === 1 ? "template" : "templates"}
        {hasMore ? `, showing ${shown}` : ""}
      </p>
      <div className="gs-catalogue-grid">
        {visible.map(({ entry, open, items: slice }) =>
          entry.kind === "single" ? (
            <CatalogueCard
              key={entry.key}
              item={entry.item}
              favorite={favoriteSet.has(entry.item.id)}
              showGameTitle
              onPick={onPick}
              onToggleFavorite={onToggleFavorite}
            />
          ) : (
            <CatalogueGameGroup
              key={entry.key}
              groupKey={entry.key}
              title={entry.title}
              all={entry.items}
              items={slice}
              open={open}
              favorites={favoriteSet}
              onToggle={toggleGroup}
              onPick={onPick}
              onToggleFavorite={onToggleFavorite}
            />
          ),
        )}
      </div>
      {results.length === 0 ? <p className="gs-meta">{emptyMessage}</p> : null}
      {hasMore ? (
        <div className="gs-catalogue-more" ref={sentinel}>
          <button type="button" className="btn btn-ghost" onClick={showMore}>
            Show more ({total - shown} more)
          </button>
        </div>
      ) : null}
    </>
  );
}

type GroupProps = {
  groupKey: string;
  title: string;
  all: CatalogueItem[];
  items: CatalogueItem[];
  open: boolean;
  favorites: ReadonlySet<string>;
  onToggle: (key: string, open: boolean) => void;
  onPick: (item: CatalogueItem) => void;
  onToggleFavorite: (id: string) => void;
};

const CatalogueGameGroup = memo(function CatalogueGameGroup({ groupKey, title, all, items, open, favorites, onToggle, onPick, onToggleFavorite }: GroupProps) {
  const panelId = useId();
  const preview = all
    .slice(0, 4)
    .map((item) => item.name)
    .join(", ");
  return (
    <section className={"gs-catalogue-game " + familyTone(all[0]?.family)} aria-label={title}>
      <h3 className="gs-catalogue-game-head">
        <button type="button" aria-expanded={open} aria-controls={panelId} onClick={() => onToggle(groupKey, open)}>
          <span className="gs-catalogue-mark" aria-hidden="true">
            {title.slice(0, 1).toUpperCase()}
          </span>
          <span className="gs-catalogue-copy">
            <span className="gs-catalogue-game-title">{title}</span>
            <span className="gs-catalogue-type">
              {all.length} server types
              {open ? "" : `: ${preview}${all.length > 4 ? ", ..." : ""}`}
            </span>
          </span>
          <span className="gs-catalogue-caret" aria-hidden="true">
            {open ? "▾" : "▸"}
          </span>
        </button>
      </h3>
      {open ? (
        <div className="gs-catalogue-game-items" id={panelId}>
          {items.map((item) => (
            <CatalogueCard key={item.id} item={item} favorite={favorites.has(item.id)} showGameTitle={false} onPick={onPick} onToggleFavorite={onToggleFavorite} />
          ))}
        </div>
      ) : null}
    </section>
  );
});

type CardProps = {
  item: CatalogueItem;
  favorite: boolean;
  showGameTitle: boolean;
  onPick: (item: CatalogueItem) => void;
  onToggleFavorite: (id: string) => void;
};

export const CatalogueCard = memo(function CatalogueCard({ item, favorite, showGameTitle, onPick, onToggleFavorite }: CardProps) {
  const titleId = useId();
  const verifyId = useId();
  const verify = item.verification ? VERIFICATION_INFO[item.verification] : undefined;
  const archs = architecturesOf(item);
  const ram = item.default_memory_mb;
  const title = item.game_title?.trim();
  return (
    <div className={"gs-catalogue-card " + familyTone(item.family)}>
      <button type="button" className="gs-catalogue-item" aria-describedby={verify ? verifyId : undefined} onClick={() => onPick(item)}>
        <span className="gs-catalogue-mark" aria-hidden="true">
          {catalogueMark(item)}
        </span>
        <span className="gs-catalogue-copy">
          <h3 id={titleId}>{item.name}</h3>
          {showGameTitle && title && title.toLowerCase() !== item.name.toLowerCase() ? <span className="gs-catalogue-type">{title}</span> : null}
          <span className="gs-catalogue-badges">
            <span className="gs-badge">{installMethodOf(item)}</span>
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
          {item.hint ? <span className="gs-catalogue-hint">{item.hint}</span> : null}
        </span>
      </button>
      {verify ? (
        <span id={verifyId} className="visually-hidden">
          {verify.title}
        </span>
      ) : null}
      <button
        type="button"
        className={"gs-catalogue-star" + (favorite ? " is-on" : "")}
        aria-pressed={favorite}
        aria-label="Favourite"
        aria-describedby={titleId}
        title={favorite ? "Remove from favourites" : "Add to favourites"}
        onClick={() => onToggleFavorite(item.id)}
      >
        <span aria-hidden="true">{favorite ? "★" : "☆"}</span>
      </button>
    </div>
  );
});
