import { useCallback, useEffect, useState } from "react";
import { ErrorNotice } from "../components/ErrorNotice";
import { Segmented } from "../ui/Segmented";
import { CurseForgeKey } from "./CurseForgeKey";
import { searchModpacks, type Modpack } from "./api";

type Provider = "modrinth" | "curseforge";

const LOADER_LABELS: Record<string, string> = { forge: "Forge", neoforge: "NeoForge", fabric: "Fabric", quilt: "Quilt" };

function compact(n: number): string {
  if (n >= 1_000_000) {
    return `${(n / 1_000_000).toFixed(n >= 10_000_000 ? 0 : 1)}M`;
  }
  if (n >= 1_000) {
    return `${Math.round(n / 1_000)}K`;
  }
  return String(n);
}

/**
 * ModpackBrowser searches Minecraft modpacks on Modrinth and CurseForge.
 * An empty search lists the most downloaded packs.
 */
export function ModpackBrowser({ onPick, busy }: { onPick: (pack: Modpack) => void; busy: boolean }) {
  const [provider, setProvider] = useState<Provider>("modrinth");
  const [q, setQ] = useState("");
  const [items, setItems] = useState<Modpack[] | null>(null);
  const [needsKey, setNeedsKey] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [searching, setSearching] = useState(false);

  const search = useCallback(
    async (query: string) => {
      setSearching(true);
      setError(null);
      try {
        const r = await searchModpacks(provider, query);
        setNeedsKey(Boolean(r.needs_key));
        setItems(r.items ?? []);
      } catch (err) {
        setItems([]);
        setError(err instanceof Error ? err.message : "Modpack search failed");
      } finally {
        setSearching(false);
      }
    },
    [provider],
  );

  useEffect(() => {
    setItems(null);
    void search("");
    // A new catalogue starts from its most downloaded packs.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [provider]);

  return (
    <div className="gs-modpacks">
      <div className="gs-modpacks-head">
        <Segmented<Provider>
          value={provider}
          ariaLabel="Modpack catalogue"
          options={[
            { id: "modrinth", label: "Modrinth" },
            { id: "curseforge", label: "CurseForge" },
          ]}
          onChange={setProvider}
        />
        <form
          className="gs-toolbar"
          onSubmit={(e) => {
            e.preventDefault();
            void search(q);
          }}
        >
          <input
            className="field-input"
            type="search"
            aria-label="Search modpacks"
            placeholder="Search modpacks, for example All the Mods or Cobblemon"
            value={q}
            onChange={(e) => setQ(e.target.value)}
          />
          <button className="btn btn-sm btn-primary" type="submit" disabled={searching}>
            {searching ? "Searching" : "Search"}
          </button>
        </form>
      </div>
      {error ? <ErrorNotice error={error} /> : null}
      {needsKey ? <CurseForgeKey onSaved={() => void search(q)} /> : null}
      {items === null ? <p className="muted">Loading modpacks</p> : null}
      {items !== null && items.length === 0 && !needsKey && !searching ? <p className="muted">No modpacks match. Try another search.</p> : null}
      <div className="gs-modpack-grid">
        {(items ?? []).map((pack) => (
          <button
            key={`${pack.provider}:${pack.id}`}
            type="button"
            className="gs-modpack"
            disabled={busy}
            onClick={() => onPick(pack)}
            aria-label={`Install ${pack.name}`}
          >
            {pack.icon_url ? <img src={pack.icon_url} alt="" loading="lazy" referrerPolicy="no-referrer" /> : <span className="gs-modpack-icon" aria-hidden="true" />}
            <span className="gs-modpack-body">
              <strong>{pack.name}</strong>
              {pack.summary ? <span className="gs-meta">{pack.summary}</span> : null}
              <span className="gs-modpack-tags">
                <span className="gs-badge">{compact(pack.downloads)} downloads</span>
                {(pack.loaders ?? []).map((l) => (
                  <span key={l} className="gs-badge">
                    {LOADER_LABELS[l] ?? l}
                  </span>
                ))}
                {pack.game_versions?.[0] ? <span className="gs-badge">MC {pack.game_versions[0]}</span> : null}
              </span>
            </span>
          </button>
        ))}
      </div>
    </div>
  );
}
