import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from "react";
import { listFeatures } from "../api/client";
import type { Feature } from "../generated/openapi";
import { useSession } from "../session";
import { groupedModules, isCapabilityEnabled, visibleModules, type DisclosurePrefs } from "./disclosure";
import { DISCLOSURE_PREF, disclosureFromPref, saveDisclosurePrefs } from "./disclosurePrefs";
import { usePref } from "../prefs";
import { storageGet, storageSet } from "../storage";

// The feature list is cached in this browser so the sidebar draws at once.
const FEATURES_CACHE = "ndl-features-cache";

function cachedFeatures(): Feature[] {
  try {
    const raw = storageGet(FEATURES_CACHE);
    const parsed = raw ? (JSON.parse(raw) as Feature[]) : [];
    return Array.isArray(parsed) ? parsed : [];
  } catch {
    return [];
  }
}
import { CAPABILITIES } from "./modules";

type NavDisclosureValue = {
  prefs: DisclosurePrefs;
  features: Feature[];
  featureEnabled: Record<string, boolean>;
  /** False until a feature list is known (cached or fetched). */
  featuresLoaded: boolean;
  /** capabilityOn reports whether a capability (backend or UI-only) is on. */
  capabilityOn: (id: string) => boolean;
  setUiEnabled: (id: string, on: boolean) => void;
  reloadFeatures: () => Promise<void>;
};

const NavDisclosureContext = createContext<NavDisclosureValue | null>(null);

export function NavDisclosureProvider({ children }: { children: ReactNode }) {
  const [rawPrefs] = usePref<unknown>(DISCLOSURE_PREF, null);
  const prefs = useMemo(() => disclosureFromPref(rawPrefs), [rawPrefs]);
  const [features, setFeatures] = useState<Feature[]>(cachedFeatures);
  const [featuresLoaded, setFeaturesLoaded] = useState(() => cachedFeatures().length > 0);

  const persist = useCallback((next: DisclosurePrefs) => {
    saveDisclosurePrefs(next);
  }, []);

  const reloadFeatures = useCallback(async () => {
    try {
      const list = await listFeatures();
      setFeatures(list.items ?? []);
      storageSet(FEATURES_CACHE, JSON.stringify(list.items ?? []));
    } catch {
      // Keep the cached list; a failed read does not turn features off.
    } finally {
      setFeaturesLoaded(true);
    }
  }, []);

  useEffect(() => {
    void reloadFeatures();
  }, [reloadFeatures]);

  const featureEnabled = useMemo(() => {
    const out: Record<string, boolean> = {};
    for (const item of features) {
      out[item.id] = Boolean(item.enabled || item.core);
    }
    return out;
  }, [features]);

  const setUiEnabled = useCallback((id: string, on: boolean) => {
    persist({
      ...prefs,
      enabled: on ? Array.from(new Set([...prefs.enabled, id])) : prefs.enabled.filter((item) => item !== id),
    });
  }, [persist, prefs]);

  const capabilityOn = useCallback((id: string) => isCapabilityEnabled(id, prefs, featureEnabled), [prefs, featureEnabled]);

  const value = useMemo<NavDisclosureValue>(
    () => ({
      prefs,
      features,
      featureEnabled,
      featuresLoaded,
      capabilityOn,
      setUiEnabled,
      reloadFeatures,
    }),
    [prefs, features, featureEnabled, featuresLoaded, capabilityOn, setUiEnabled, reloadFeatures],
  );

  return <NavDisclosureContext.Provider value={value}>{children}</NavDisclosureContext.Provider>;
}

export function useNavDisclosure(): NavDisclosureValue {
  const ctx = useContext(NavDisclosureContext);
  if (!ctx) {
    throw new Error("NavDisclosureProvider is required");
  }
  return ctx;
}

export function useVisibleNav() {
  const session = useSession();
  const user = session.status === "ready" ? session.user : null;
  const { prefs, featureEnabled } = useNavDisclosure();
  const items = visibleModules(prefs, featureEnabled, user?.roles, user?.grants, user);
  return groupedModules(items);
}

export function capabilityIds(): string[] {
  return CAPABILITIES.map((item) => item.id);
}

/** useFeatureOn reports whether an optional feature is on; false outside the provider. */
export function useFeatureOn(id: string): boolean {
  const ctx = useContext(NavDisclosureContext);
  return Boolean(ctx?.featureEnabled[id]);
}

const NO_FEATURES: Record<string, boolean> = {};

/** useFeatureFlags lists which optional features are on; empty outside the provider. */
export function useFeatureFlags(): Record<string, boolean> {
  return useContext(NavDisclosureContext)?.featureEnabled ?? NO_FEATURES;
}

/** useCapabilityOn reports whether a capability is on; true outside the provider so nothing is hidden in isolation. */
export function useCapabilityOn(id: string | undefined): boolean {
  const ctx = useContext(NavDisclosureContext);
  if (!id || !ctx) {
    return true;
  }
  return ctx.capabilityOn(id);
}
