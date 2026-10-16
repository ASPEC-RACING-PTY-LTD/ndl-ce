import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from "react";
import { listFeatures } from "../api/client";
import type { Feature } from "../generated/openapi";
import { useSession } from "../session";
import {
  groupedModules,
  moveCustom,
  seedCustom,
  toggleCustomVisible,
  visibleModules,
  type DisclosurePrefs,
} from "./disclosure";
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
import { CAPABILITIES, type NavTemplate } from "./modules";

type NavDisclosureValue = {
  prefs: DisclosurePrefs;
  features: Feature[];
  featureEnabled: Record<string, boolean>;
  setTemplate: (template: NavTemplate) => void;
  setUiEnabled: (id: string, on: boolean) => void;
  setCustomModule: (id: string, on: boolean) => void;
  reorderCustom: (id: string, delta: number) => void;
  reloadFeatures: () => Promise<void>;
};

const NavDisclosureContext = createContext<NavDisclosureValue | null>(null);

export function NavDisclosureProvider({ children }: { children: ReactNode }) {
  const session = useSession();
  const roles = session.status === "ready" ? session.user?.roles : undefined;
  const grants = session.status === "ready" ? session.user?.grants : undefined;
  const [rawPrefs] = usePref<unknown>(DISCLOSURE_PREF, null);
  const prefs = useMemo(() => disclosureFromPref(rawPrefs), [rawPrefs]);
  const [features, setFeatures] = useState<Feature[]>(cachedFeatures);

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

  const setTemplate = useCallback(
    (template: NavTemplate) => {
      const cur = prefs;
      let next = { ...cur, template };
      if (template === "custom" && next.customVisible.length === 0) {
        const snapshot = visibleModules({ ...cur, template: cur.template === "custom" ? "simple" : cur.template }, featureEnabled, roles, undefined, grants).map(
          (mod) => mod.id,
        );
        next = seedCustom({ ...next, template: "custom" }, snapshot);
      }
      saveDisclosurePrefs(next);
    },
    [featureEnabled, grants, prefs, roles],
  );

  const setUiEnabled = useCallback((id: string, on: boolean) => {
    persist({
      ...prefs,
      enabled: on ? Array.from(new Set([...prefs.enabled, id])) : prefs.enabled.filter((item) => item !== id),
    });
  }, [persist, prefs]);

  const setCustomModule = useCallback(
    (id: string, on: boolean) => {
      persist(toggleCustomVisible(prefs, id, on));
    },
    [persist, prefs],
  );

  const reorderCustom = useCallback(
    (id: string, delta: number) => {
      persist(moveCustom(prefs, id, delta));
    },
    [persist, prefs],
  );

  const value = useMemo<NavDisclosureValue>(
    () => ({ prefs, features, featureEnabled, setTemplate, setUiEnabled, setCustomModule, reorderCustom, reloadFeatures }),
    [prefs, features, featureEnabled, setTemplate, setUiEnabled, setCustomModule, reorderCustom, reloadFeatures],
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

export function useVisibleNav(path: string) {
  const session = useSession();
  const roles = session.status === "ready" ? session.user?.roles : undefined;
  const grants = session.status === "ready" ? session.user?.grants : undefined;
  const { prefs, featureEnabled } = useNavDisclosure();
  const items = visibleModules(prefs, featureEnabled, roles, path, grants);
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
