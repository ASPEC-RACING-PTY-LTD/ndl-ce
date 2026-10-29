import { useCallback, useEffect, useMemo, useState } from "react";
import { usePref } from "../prefs";

// Dismissed Cloudflare warnings, saved per user. Each warning has a stable id;
// the saved value is when it was dismissed (ISO time) or "permanent". A
// dismissed warning whose condition still holds shows again as a new
// notification once DISMISS_FOR_MS has passed.

export const DISMISS_FOR_MS = 3 * 24 * 60 * 60 * 1000;
export const PERMANENT = "permanent";
export const DISMISS_PREF = "cf.dismissed";

export type Dismissals = Record<string, string>;

/** isDismissed says whether the warning id is hidden at now. */
export function isDismissed(d: Dismissals, id: string, now: number): boolean {
  const v = d[id];
  if (!v) {
    return false;
  }
  if (v === PERMANENT) {
    return true;
  }
  const at = Date.parse(v);
  return Number.isFinite(at) && now - at < DISMISS_FOR_MS;
}

/** withDismissal records a dismissal of id and drops timed dismissals that have run out. */
export function withDismissal(d: Dismissals, id: string, permanent: boolean, now: number): Dismissals {
  const next: Dismissals = {};
  for (const [k, v] of Object.entries(d)) {
    if (isDismissed(d, k, now)) {
      next[k] = v;
    }
  }
  next[id] = permanent ? PERMANENT : new Date(now).toISOString();
  return next;
}

/** withoutDismissals forgets the dismissals of ids, so those warnings show again. */
export function withoutDismissals(d: Dismissals, ids: string[]): Dismissals {
  const next = { ...d };
  for (const id of ids) {
    delete next[id];
  }
  return next;
}

/** useWarningDismissals holds the signed-in user's dismissed Cloudflare warnings. */
export function useWarningDismissals() {
  const [saved, setSaved] = usePref<Dismissals>(DISMISS_PREF, {});
  const dismissals = useMemo(() => (saved && typeof saved === "object" ? saved : {}), [saved]);
  // Re-checked every minute, so a timed dismissal runs out without a reload.
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const t = window.setInterval(() => setNow(Date.now()), 60_000);
    return () => window.clearInterval(t);
  }, []);
  const hidden = useCallback((id: string) => isDismissed(dismissals, id, now), [dismissals, now]);
  const dismiss = useCallback(
    (id: string, permanent = false) => {
      const at = Date.now();
      setNow(at);
      setSaved((cur) => withDismissal(cur && typeof cur === "object" ? cur : {}, id, permanent, at));
    },
    [setSaved],
  );
  const restore = useCallback((ids: string[]) => setSaved((cur) => withoutDismissals(cur && typeof cur === "object" ? cur : {}, ids)), [setSaved]);
  return { hidden, dismiss, restore };
}
