import { useEffect, useMemo, useState } from "react";
import { navigate } from "../router";
import { Dialog } from "../ui/Dialog";
import { GS_ROOT } from "../nav/gameServers";
import { listGameCatalogue } from "./api";
import { GameArt } from "./CatalogueBrowser";
import { queryGames } from "./games";
import type { CatalogueItem } from "./types";

const SHOWN = 25;

/**
 * QuickCreateDialog is the first step of Create server: search the catalogue
 * and pick from a 5 by 5 grid of covers, or open the full setup page.
 */
export function QuickCreateDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  const [items, setItems] = useState<CatalogueItem[] | null>(null);
  const [q, setQ] = useState("");
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!open || items) {
      return;
    }
    void listGameCatalogue()
      .then((res) => setItems(res.items ?? []))
      .catch((err) => setError(err instanceof Error ? err.message : "Could not load the catalogue"));
  }, [open, items]);

  const games = useMemo(() => queryGames(items ?? [], { q, favorites: [], recents: [] }).slice(0, SHOWN), [items, q]);

  function go(title?: string) {
    onClose();
    navigate(title ? `${GS_ROOT}/create?game=${encodeURIComponent(title)}` : `${GS_ROOT}/create`);
  }

  return (
    <Dialog
      open={open}
      wide
      title="Create a game server"
      onClose={onClose}
      footer={
        <div className="btn-row">
          <button className="btn btn-ghost" type="button" onClick={onClose}>
            Cancel
          </button>
          <button
            className="btn btn-secondary"
            type="button"
            onClick={() => {
              onClose();
              navigate(`${GS_ROOT}/create?source=modpacks`);
            }}
          >
            Install a modpack
          </button>
          <button className="btn btn-secondary" type="button" onClick={() => go()}>
            Browse all games
          </button>
        </div>
      }
    >
      <input
        className="field-input gs-quick-q"
        type="search"
        autoFocus
        aria-label="Search games"
        placeholder="Search for a game, for example Minecraft or Rust"
        value={q}
        onChange={(e) => setQ(e.target.value)}
      />
      {error ? <p className="banner banner-error">{error}</p> : null}
      {!items && !error ? <p className="muted">Loading games</p> : null}
      <div className="gs-quick-grid">
        {games.map((g) => (
          <button key={g.key} type="button" className="gs-quick-tile" title={g.title} onClick={() => go(g.title)}>
            <GameArt url={g.logoUrl} kind={g.logoKind} title={g.title} size="cover" />
            <span className="gs-quick-title">{g.title}</span>
          </button>
        ))}
      </div>
      {items && games.length === 0 ? <p className="muted">No games match “{q}”. Browse all games to import one.</p> : null}
    </Dialog>
  );
}
