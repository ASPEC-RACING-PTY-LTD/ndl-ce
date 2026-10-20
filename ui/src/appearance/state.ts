import { useSyncExternalStore } from "react";
import { getMyAppearance, type MyAppearance } from "../api/client";
import { allPrefs, getPref, setPref, usePref } from "../prefs";
import { setInheritedTheme } from "../theme";

// Appearance choices that are not colours: which navigation and Instance
// layout a person uses, how notifications show, terminal text size, and
// design profiles (saved sets of all of these, switched in one click).
//
// Each choice is a per-person preference; until a person picks one, the
// default an admin set (GET /me/appearance) applies.

export type NavLayout = "modern" | "classic";
export type InstanceLayout = "instance" | "classic";

export const NAV_LAYOUT_KEY = "layout.nav";
export const INSTANCE_LAYOUT_KEY = "layout.instance";
export const NOTIFY_KEY = "notify.display";
export const TERMINAL_FONT_KEY = "terminal.font";
export const PROFILES_KEY = "design.profiles";
export const ACTIVE_PROFILE_KEY = "design.active";

export const MAX_PROFILES = 3;

type Defaults = { nav: NavLayout; instance: InstanceLayout; profileLimit: number; canManage: boolean; themeSource: string };

let defaults: Defaults = { nav: "modern", instance: "instance", profileLimit: MAX_PROFILES, canManage: false, themeSource: "" };
const listeners = new Set<() => void>();

function emit() {
  for (const l of listeners) {
    l();
  }
}

/** applyAppearanceDefaults takes what GET /me/appearance returned. */
export function applyAppearanceDefaults(a: MyAppearance | null): void {
  defaults = {
    nav: a?.nav_layout === "classic" ? "classic" : "modern",
    instance: a?.instance_layout === "classic" ? "classic" : "instance",
    profileLimit: Math.max(1, Math.min(MAX_PROFILES, a?.profile_limit ?? MAX_PROFILES)),
    canManage: Boolean(a?.can_manage),
    themeSource: a?.theme_source ?? "",
  };
  setInheritedTheme(a?.theme ?? null);
  emit();
}

/** loadAppearanceDefaults fetches the defaults for the signed-in person. */
export async function loadAppearanceDefaults(): Promise<void> {
  try {
    applyAppearanceDefaults(await getMyAppearance());
  } catch {
    // Keep the built-in defaults: a failed read changes nothing on screen.
  }
}

function subscribe(l: () => void) {
  listeners.add(l);
  return () => listeners.delete(l);
}

export function useAppearanceDefaults(): Defaults {
  return useSyncExternalStore(
    subscribe,
    () => defaults,
    () => defaults,
  );
}

/** Test helper. */
export function resetAppearanceDefaults(): void {
  applyAppearanceDefaults(null);
}

/** useNavLayout is the navigation layout in use, and a setter (undefined: back to the default). */
export function useNavLayout(): [NavLayout, (v: NavLayout | undefined) => void, boolean] {
  const d = useAppearanceDefaults();
  const [own, setOwn] = usePref<NavLayout | null>(NAV_LAYOUT_KEY, null);
  const value = own === "classic" || own === "modern" ? own : d.nav;
  return [value, (v) => setOwn(v ?? null), own !== null];
}

export function useInstanceLayout(): [InstanceLayout, (v: InstanceLayout | undefined) => void, boolean] {
  const d = useAppearanceDefaults();
  const [own, setOwn] = usePref<InstanceLayout | null>(INSTANCE_LAYOUT_KEY, null);
  const value = own === "classic" || own === "instance" ? own : d.instance;
  return [value, (v) => setOwn(v ?? null), own !== null];
}

// ---- notifications ----

export type NotifyDisplay = {
  /** panel: the bell opens a panel of notifications. */
  panel: boolean;
  /** toast: new notifications slide down from the top for a few seconds. */
  toast: boolean;
  /** seconds a toast stays, 2 to 5. */
  seconds: number;
};

export const DEFAULT_NOTIFY: NotifyDisplay = { panel: true, toast: true, seconds: 3 };

export function parseNotify(raw: unknown): NotifyDisplay {
  if (!raw || typeof raw !== "object") {
    return DEFAULT_NOTIFY;
  }
  const r = raw as Partial<NotifyDisplay>;
  const seconds = typeof r.seconds === "number" ? Math.round(Math.max(2, Math.min(5, r.seconds))) : 3;
  return { panel: r.panel !== false, toast: r.toast !== false, seconds };
}

export function useNotifyDisplay(): [NotifyDisplay, (v: NotifyDisplay) => void] {
  const [raw, set] = usePref<unknown>(NOTIFY_KEY, null);
  return [parseNotify(raw), (v) => set(v)];
}

// ---- terminal text size ----

/** Terminal font sizes ctrl+scroll steps through. */
export const TERMINAL_FONT_STEPS = [9, 10, 11, 12, 13, 14, 16, 18, 20, 22, 24, 28, 32, 36];
export const DEFAULT_TERMINAL_FONT = 13;

export function clampTerminalFont(n: unknown): number {
  const v = typeof n === "number" && Number.isFinite(n) ? n : DEFAULT_TERMINAL_FONT;
  return Math.max(TERMINAL_FONT_STEPS[0], Math.min(TERMINAL_FONT_STEPS[TERMINAL_FONT_STEPS.length - 1], v));
}

/** stepTerminalFont moves one step up (dir 1) or down (dir -1). */
export function stepTerminalFont(cur: number, dir: 1 | -1): number {
  const c = clampTerminalFont(cur);
  if (dir > 0) {
    return TERMINAL_FONT_STEPS.find((s) => s > c) ?? c;
  }
  return [...TERMINAL_FONT_STEPS].reverse().find((s) => s < c) ?? c;
}

// ---- design profiles ----

export type DesignProfile = { id: string; name: string; data: Record<string, unknown> };

/** Preferences a design profile holds: everything about how the app looks and is laid out. */
export function isDesignKey(key: string): boolean {
  return (
    key === "theme" ||
    key === "nav.order" ||
    key === NAV_LAYOUT_KEY ||
    key === INSTANCE_LAYOUT_KEY ||
    key === NOTIFY_KEY ||
    key === TERMINAL_FONT_KEY ||
    key.startsWith("pagenav.") ||
    key.startsWith("layout.") ||
    key.startsWith("widgets.")
  );
}

export function parseProfiles(raw: unknown): DesignProfile[] {
  if (!Array.isArray(raw)) {
    return [];
  }
  return raw.filter(
    (p): p is DesignProfile =>
      Boolean(p) && typeof p === "object" && typeof (p as DesignProfile).id === "string" && typeof (p as DesignProfile).name === "string",
  ).map((p) => ({ ...p, data: p.data && typeof p.data === "object" ? p.data : {} }));
}

/** captureDesign is the current look as profile data. */
export function captureDesign(): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  for (const [k, v] of Object.entries(allPrefs())) {
    if (isDesignKey(k)) {
      out[k] = v;
    }
  }
  return out;
}

/** applyDesign switches to a profile: its keys are set, other design keys cleared. */
export function applyDesign(data: Record<string, unknown>): void {
  const keys = new Set([...Object.keys(allPrefs()).filter(isDesignKey), ...Object.keys(data).filter(isDesignKey)]);
  for (const k of keys) {
    setPref(k, data[k]);
  }
}

export function newProfileId(): string {
  return typeof crypto !== "undefined" && "randomUUID" in crypto ? crypto.randomUUID() : `p-${Date.now()}-${Math.random().toString(36).slice(2)}`;
}

export function activeProfileId(): string {
  return getPref<string>(ACTIVE_PROFILE_KEY, "");
}
