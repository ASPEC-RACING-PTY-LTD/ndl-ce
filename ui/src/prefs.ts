import { useCallback, useSyncExternalStore } from "react";
import { storageGet, storageSet } from "./storage";

// Interface preferences (theme, sidebar order, table columns, ...).
//
// They are saved to the signed-in user's account so they follow them to other
// browsers, and cached in this browser so pages render with them at once,
// before any request returns. Changes are written to the cache immediately
// and sent to the server shortly after, batched.

const USER_KEY = "ndl-ui-user";
const SAVE_DELAY_MS = 600;

type Listener = () => void;

let userId = storageGet(USER_KEY) ?? "";
let values: Record<string, unknown> = readCache(userId);
let pending: Record<string, unknown> = {};
let timer: ReturnType<typeof setTimeout> | null = null;
let bound = false;
const listeners = new Set<Listener>();

function cacheKey(uid: string): string {
  return `ndl-ui-prefs:${uid || "anonymous"}`;
}

function readCache(uid: string): Record<string, unknown> {
  try {
    const raw = storageGet(cacheKey(uid));
    const parsed = raw ? (JSON.parse(raw) as unknown) : null;
    return parsed && typeof parsed === "object" && !Array.isArray(parsed) ? (parsed as Record<string, unknown>) : {};
  } catch {
    return {};
  }
}

function writeCache(): void {
  storageSet(cacheKey(userId), JSON.stringify(values));
}

function notify(): void {
  for (const l of listeners) {
    l();
  }
}

/**
 * bindPrefsUser switches to the signed-in user's preferences. Saved server
 * values win over the cache, except keys changed here that are still being
 * saved. A server without saved preferences keeps the cached ones and is
 * sent them.
 */
export function bindPrefsUser(uid: string, server: Record<string, unknown> | undefined): void {
  const switching = uid !== userId;
  if (switching) {
    pending = {};
  }
  userId = uid;
  storageSet(USER_KEY, uid);
  const local = readCache(uid);
  if (server && Object.keys(server).length > 0) {
    values = { ...server, ...pending };
  } else {
    values = { ...local, ...pending };
    if (server && Object.keys(local).length > 0) {
      // First sign-in since preferences moved to the account: upload them.
      pending = { ...local, ...pending };
      schedule();
    }
  }
  bound = true;
  writeCache();
  notify();
}

/** unbindPrefs forgets which user is signed in, keeping their cache. */
export function unbindPrefs(): void {
  void flushPrefs();
  bound = false;
}

/** allPrefs is every saved preference (a snapshot; do not mutate). */
export function allPrefs(): Record<string, unknown> {
  return values;
}

export function getPref<T>(key: string, fallback: T): T {
  const v = values[key];
  return v === undefined ? fallback : (v as T);
}

/** setPref saves a preference; undefined removes it. */
export function setPref(key: string, value: unknown): void {
  const next = { ...values };
  if (value === undefined) {
    delete next[key];
  } else {
    next[key] = value;
  }
  values = next;
  writeCache();
  pending = { ...pending, [key]: value === undefined ? null : value };
  notify();
  schedule();
}

function schedule(): void {
  if (!bound) {
    return;
  }
  if (timer) {
    clearTimeout(timer);
  }
  timer = setTimeout(() => void flushPrefs(), SAVE_DELAY_MS);
}

/** flushPrefs sends unsaved changes now. Failed saves stay cached and are retried with the next change. */
export async function flushPrefs(keepalive = false): Promise<void> {
  if (timer) {
    clearTimeout(timer);
    timer = null;
  }
  if (!bound || Object.keys(pending).length === 0) {
    return;
  }
  const body = pending;
  pending = {};
  try {
    const res = await fetch("/api/v1/me", {
      method: "PATCH",
      credentials: "include",
      keepalive,
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ ui_prefs: body }),
    });
    if (!res.ok && res.status !== 400 && res.status !== 401) {
      pending = { ...body, ...pending };
    }
  } catch {
    pending = { ...body, ...pending };
  }
}

if (typeof window !== "undefined") {
  window.addEventListener("pagehide", () => void flushPrefs(true));
  // Another tab changed a preference.
  window.addEventListener("storage", (event) => {
    if (event.key === cacheKey(userId)) {
      values = readCache(userId);
      notify();
    }
  });
}

function subscribe(l: Listener): () => void {
  listeners.add(l);
  return () => listeners.delete(l);
}

/** subscribePrefs calls l whenever any preference changes. */
export const subscribePrefs = subscribe;

let snapshot = values;
function getSnapshot(): Record<string, unknown> {
  // values is replaced, never mutated, so identity tells React what changed.
  snapshot = values;
  return snapshot;
}

/** usePref reads and writes one preference and re-renders when it changes anywhere. */
export function usePref<T>(key: string, fallback: T): [T, (next: T | ((cur: T) => T)) => void] {
  const all = useSyncExternalStore(subscribe, getSnapshot, getSnapshot);
  const raw = all[key];
  const value = raw === undefined ? fallback : (raw as T);
  const set = useCallback(
    (next: T | ((cur: T) => T)) => {
      const cur = getPref<T>(key, fallback);
      const v = typeof next === "function" ? (next as (c: T) => T)(cur) : next;
      setPref(key, v);
    },
    // fallback is usually a literal; key decides identity.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [key],
  );
  return [value, set];
}

/** Test helper. */
export function resetPrefsForTest(): void {
  values = {};
  pending = {};
  bound = false;
  userId = "";
  if (timer) {
    clearTimeout(timer);
    timer = null;
  }
  notify();
}
