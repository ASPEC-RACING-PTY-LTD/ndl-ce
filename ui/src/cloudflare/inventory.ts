import { useCallback, useEffect, useRef, useState } from "react";
import { getCfInventoryIfChanged, type CfInventory } from "../api/client";
import { storageGet, storageSet } from "../storage";

// The Cloudflare page shows the last inventory from this browser at once,
// then asks the server whether anything changed (ETag). Unchanged rows keep
// their object identity, so React only redraws what changed.

const CACHE_KEY = "ndl-cf-inventory";

type Cached = { etag: string; inventory: CfInventory };

function readCached(): Cached | null {
  try {
    const raw = storageGet(CACHE_KEY);
    const parsed = raw ? (JSON.parse(raw) as Cached) : null;
    return parsed?.inventory ? parsed : null;
  } catch {
    return null;
  }
}

function writeCached(c: Cached): void {
  storageSet(CACHE_KEY, JSON.stringify(c));
}

/** reuse keeps items from prev whose content did not change, so they stay the same objects. */
export function reuse<T>(prev: T[] | null | undefined, next: T[] | null | undefined, key: (item: T) => string): T[] {
  const out = next ?? [];
  if (!prev?.length) {
    return out;
  }
  const old = new Map(prev.map((item) => [key(item), item]));
  let same = prev.length === out.length;
  const merged = out.map((item, i) => {
    const o = old.get(key(item));
    if (o && JSON.stringify(o) === JSON.stringify(item)) {
      if (prev[i] !== o) {
        same = false;
      }
      return o;
    }
    same = false;
    return item;
  });
  // An unchanged list keeps its identity too.
  return same ? prev : merged;
}

/** mergeInventory applies next over prev, keeping unchanged parts of prev. */
export function mergeInventory(prev: CfInventory | null, next: CfInventory): CfInventory {
  if (!prev) {
    return next;
  }
  return {
    ...next,
    accounts: reuse(prev.accounts, next.accounts, (a) => a.id),
    tunnels: reuse(prev.tunnels, next.tunnels, (t) => t.id),
    routes: reuse(prev.routes, next.routes, (r) => r.key),
    dns: reuse(prev.dns, next.dns, (d) => d.record.id),
    errors: reuse(prev.errors, next.errors, (e) => e),
  };
}

const EMPTY: CfInventory = { fetched_at: "", refreshing: false, accounts: [], tunnels: [], routes: [], dns: [], errors: [] };

/** useCfInventory loads the inventory, cached and diffed. */
export function useCfInventory() {
  const cached = useRef<Cached | null>(readCached());
  const etag = useRef(cached.current?.etag ?? "");
  const [inv, setInv] = useState<CfInventory | null>(cached.current?.inventory ?? null);
  const [fromCache, setFromCache] = useState(cached.current != null);
  const [error, setError] = useState<string | null>(null);

  const reload = useCallback(async (refresh = false) => {
    try {
      const res = await getCfInventoryIfChanged(refresh ? "" : etag.current, refresh);
      setFromCache(false);
      setError(null);
      if (!res) {
        return;
      }
      etag.current = res.etag;
      setInv((prev) => {
        const merged = mergeInventory(prev, res.inventory);
        writeCached({ etag: res.etag, inventory: res.inventory });
        return merged;
      });
    } catch (err) {
      setError(err instanceof Error ? err.message : "Cloudflare is unavailable");
      setInv((cur) => cur ?? EMPTY);
    }
  }, []);

  useEffect(() => {
    void reload();
  }, [reload]);

  return { inv, fromCache, error, reload, setError };
}
