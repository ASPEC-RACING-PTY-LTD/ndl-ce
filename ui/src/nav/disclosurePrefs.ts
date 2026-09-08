import { storageGet, storageSet } from "../storage";
import { EMPTY_PREFS, parseDisclosurePrefs, type DisclosurePrefs } from "./disclosure";

export const DISCLOSURE_KEY = "ndl-nav-disclosure";

export function loadDisclosurePrefs(): DisclosurePrefs {
  return parseDisclosurePrefs(storageGet(DISCLOSURE_KEY));
}

export function saveDisclosurePrefs(prefs: DisclosurePrefs): void {
  storageSet(DISCLOSURE_KEY, JSON.stringify(prefs));
}

export function resetDisclosurePrefs(): DisclosurePrefs {
  const next = {
    ...EMPTY_PREFS,
    enabled: [],
    customVisible: [],
    customOrder: [],
  };
  saveDisclosurePrefs(next);
  return next;
}
