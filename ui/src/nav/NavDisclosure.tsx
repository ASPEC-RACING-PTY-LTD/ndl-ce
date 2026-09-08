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
import { loadDisclosurePrefs, saveDisclosurePrefs } from "./disclosurePrefs";
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
  const [prefs, setPrefs] = useState<DisclosurePrefs>(() => loadDisclosurePrefs());
  const [features, setFeatures] = useState<Feature[]>([]);

  const persist = useCallback((next: DisclosurePrefs) => {
    setPrefs(next);
    saveDisclosurePrefs(next);
  }, []);

  const reloadFeatures = useCallback(async () => {
    try {
      const list = await listFeatures();
      setFeatures(list.items ?? []);
    } catch {
      setFeatures([]);
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
      setPrefs((cur) => {
        let next = { ...cur, template };
        if (template === "custom" && next.customVisible.length === 0) {
          const snapshot = visibleModules({ ...cur, template: cur.template === "custom" ? "simple" : cur.template }, featureEnabled, roles).map(
            (mod) => mod.id,
          );
          next = seedCustom({ ...next, template: "custom" }, snapshot);
        }
        saveDisclosurePrefs(next);
        return next;
      });
    },
    [featureEnabled, roles],
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
  const { prefs, featureEnabled } = useNavDisclosure();
  const items = visibleModules(prefs, featureEnabled, roles, path);
  return groupedModules(items);
}

export function capabilityIds(): string[] {
  return CAPABILITIES.map((item) => item.id);
}
