import { getPref, setPref } from "../prefs";
import { storageGet } from "../storage";
import { EMPTY_PREFS, parseDisclosurePrefs, type DisclosurePrefs } from "./disclosure";

/** DISCLOSURE_KEY is the browser-only key used before layouts were saved to the profile. */
export const DISCLOSURE_KEY = "ndl-nav-disclosure";
/** DISCLOSURE_PREF is the profile preference key. */
export const DISCLOSURE_PREF = "nav.disclosure";

export function disclosureFromPref(raw: unknown): DisclosurePrefs {
  if (raw && typeof raw === "object") {
    return parseDisclosurePrefs(JSON.stringify(raw));
  }
  return parseDisclosurePrefs(storageGet(DISCLOSURE_KEY));
}

export function loadDisclosurePrefs(): DisclosurePrefs {
  return disclosureFromPref(getPref<unknown>(DISCLOSURE_PREF, null));
}

export function saveDisclosurePrefs(prefs: DisclosurePrefs): void {
  setPref(DISCLOSURE_PREF, prefs);
}

export function resetDisclosurePrefs(): DisclosurePrefs {
  const next: DisclosurePrefs = { ...EMPTY_PREFS, enabled: [] };
  saveDisclosurePrefs(next);
  return next;
}
