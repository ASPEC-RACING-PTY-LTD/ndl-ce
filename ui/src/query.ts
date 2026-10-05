import { useCallback, useEffect, useRef, useState } from "react";

// Server-state helper used until TanStack Query can be added to this tree.
//
// Results are shared by key across components and page visits:
// - a page that mounts with a recent result for its key renders it at once
//   and revalidates in the background, so navigation does not wait for the
//   network to show what was already on screen moments ago;
// - concurrent loads of one key share a single request;
// - interval polls never overlap: a poll is skipped while the previous load
//   of that key is still running, so a slow endpoint cannot pile up requests
//   and starve the browser's connection pool.

/** Cached results older than this are not shown while revalidating. */
export const QUERY_SHOW_STALE_MS = 60_000;

type Entry = {
  data: unknown;
  at: number;
  inflight: Promise<unknown> | null;
};

const cache = new Map<string, Entry>();

function entry(key: string): Entry {
  let e = cache.get(key);
  if (!e) {
    e = { data: undefined, at: 0, inflight: null };
    cache.set(key, e);
  }
  return e;
}

function cachedData<T>(key: string): T | undefined {
  const e = cache.get(key);
  if (!e || e.at === 0 || Date.now() - e.at > QUERY_SHOW_STALE_MS) {
    return undefined;
  }
  return e.data as T;
}

function load<T>(key: string, loader: () => Promise<T>): Promise<T> {
  const e = entry(key);
  if (e.inflight) {
    return e.inflight as Promise<T>;
  }
  const p = loader()
    .then((data) => {
      e.data = data;
      e.at = Date.now();
      return data;
    })
    .finally(() => {
      if (e.inflight === p) {
        e.inflight = null;
      }
    });
  e.inflight = p;
  return p;
}

/** Drops cached results so the next load of these keys hits the network. */
export function invalidateQueries(prefix = ""): void {
  for (const key of cache.keys()) {
    if (key.startsWith(prefix)) {
      const e = cache.get(key);
      if (e) {
        e.at = 0;
      }
    }
  }
}

/** Test helper: forget every cached result. */
export function resetQueryCache(): void {
  cache.clear();
}

type QueryState<T> = {
  data: T | undefined;
  error: string | null;
  loading: boolean;
  reload: () => Promise<void>;
};

export function useQuery<T>(key: string, loader: () => Promise<T>, intervalMs?: number): QueryState<T> {
  const loaderRef = useRef(loader);
  loaderRef.current = loader;
  const [data, setData] = useState<T | undefined>(() => cachedData<T>(key));
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(() => cachedData<T>(key) === undefined);
  const keyRef = useRef(key);
  keyRef.current = key;

  const reload = useCallback(async () => {
    const k = keyRef.current;
    try {
      const next = await load(k, () => loaderRef.current());
      if (keyRef.current === k) {
        setData(next);
        setError(null);
      }
    } catch (err) {
      if (keyRef.current === k) {
        setError(err instanceof Error ? err.message : "Unavailable");
      }
    } finally {
      if (keyRef.current === k) {
        setLoading(false);
      }
    }
  }, []);

  useEffect(() => {
    let cancelled = false;
    const cached = cachedData<T>(key);
    setData(cached);
    setLoading(cached === undefined);
    // Revalidate on every mount; joins a load of this key already in flight.
    void load(key, () => loaderRef.current())
      .then((next) => {
        if (!cancelled) {
          setData(next);
          setError(null);
        }
      })
      .catch((err) => {
        if (!cancelled) {
          setError(err instanceof Error ? err.message : "Unavailable");
        }
      })
      .finally(() => {
        if (!cancelled) {
          setLoading(false);
        }
      });
    return () => {
      cancelled = true;
    };
  }, [key]);

  useEffect(() => {
    if (!intervalMs) {
      return;
    }
    const id = window.setInterval(() => {
      if (cache.get(keyRef.current)?.inflight) {
        return;
      }
      void reload();
    }, intervalMs);
    return () => window.clearInterval(id);
  }, [intervalMs, reload]);

  return { data, error, loading, reload };
}

/**
 * Runs fn every intervalMs, skipping a tick while the previous run is still
 * in flight. For pages that poll with their own loaders.
 */
export function usePoll(fn: () => Promise<unknown> | void, intervalMs: number, enabled = true): void {
  const fnRef = useRef(fn);
  fnRef.current = fn;
  useEffect(() => {
    if (!enabled || intervalMs <= 0) {
      return;
    }
    let busy = false;
    const id = window.setInterval(() => {
      if (busy) {
        return;
      }
      busy = true;
      void Promise.resolve()
        .then(() => fnRef.current())
        .catch(() => undefined)
        .finally(() => {
          busy = false;
        });
    }, intervalMs);
    return () => window.clearInterval(id);
  }, [intervalMs, enabled]);
}
