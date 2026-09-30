import { useCallback, useEffect, useRef, useState } from "react";
import { getCataloguePrefs, parseTemplateIdList, putCataloguePrefs } from "./api";

export const FAVORITES_STORAGE_KEY = "ndl.gameservers.catalogue.favorites";
export const RECENTS_STORAGE_KEY = "ndl.gameservers.catalogue.recents";
export const RECENTS_MAX = 12;

function readLocal(key: string): string[] {
  try {
    return parseTemplateIdList(globalThis.localStorage?.getItem(key) ?? "") ?? [];
  } catch {
    return [];
  }
}

function writeLocal(key: string, ids: string[]): void {
  try {
    globalThis.localStorage?.setItem(key, JSON.stringify(ids));
  } catch {
    // storage blocked or full; the in-memory list still works
  }
}

/**
 * Favourite and recently used catalogue templates. Stored through the
 * game server prefs API when the server supports it, and mirrored to
 * localStorage so older servers (and offline reloads) keep working.
 */
export function useCataloguePrefs() {
  const [favorites, setFavorites] = useState<string[]>(() => readLocal(FAVORITES_STORAGE_KEY));
  const [recents, setRecents] = useState<string[]>(() => readLocal(RECENTS_STORAGE_KEY));
  const favRef = useRef(favorites);
  const recRef = useRef(recents);
  const remote = useRef({ favorites: false, recents: false });

  useEffect(() => {
    let alive = true;
    getCataloguePrefs()
      .then((prefs) => {
        if (!alive) {
          return;
        }
        if (prefs.favorites) {
          remote.current.favorites = true;
          if (prefs.favorites.length === 0 && favRef.current.length > 0) {
            void putCataloguePrefs({ favorites: favRef.current }).catch(() => undefined);
          } else {
            favRef.current = prefs.favorites;
            setFavorites(prefs.favorites);
            writeLocal(FAVORITES_STORAGE_KEY, prefs.favorites);
          }
        }
        if (prefs.recents) {
          remote.current.recents = true;
          if (prefs.recents.length === 0 && recRef.current.length > 0) {
            void putCataloguePrefs({ recents: recRef.current }).catch(() => undefined);
          } else {
            recRef.current = prefs.recents;
            setRecents(prefs.recents);
            writeLocal(RECENTS_STORAGE_KEY, prefs.recents);
          }
        }
      })
      .catch(() => {
        // prefs API unavailable; localStorage values stay in use
      });
    return () => {
      alive = false;
    };
  }, []);

  const toggleFavorite = useCallback((id: string) => {
    const cur = favRef.current;
    const next = cur.includes(id) ? cur.filter((x) => x !== id) : [id, ...cur];
    favRef.current = next;
    setFavorites(next);
    writeLocal(FAVORITES_STORAGE_KEY, next);
    if (remote.current.favorites) {
      void putCataloguePrefs({ favorites: next }).catch(() => undefined);
    }
  }, []);

  const markRecent = useCallback((id: string) => {
    const next = [id, ...recRef.current.filter((x) => x !== id)].slice(0, RECENTS_MAX);
    recRef.current = next;
    setRecents(next);
    writeLocal(RECENTS_STORAGE_KEY, next);
    if (remote.current.recents) {
      void putCataloguePrefs({ recents: next }).catch(() => undefined);
    }
  }, []);

  return { favorites, recents, toggleFavorite, markRecent };
}
