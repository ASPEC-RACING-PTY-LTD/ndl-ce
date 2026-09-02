import { storageGet, storageSet } from "../storage";
import type { OpView } from "./types";

const VIEW_KEY = "ndl-ops-view";
const GROUPS_KEY = "ndl-ctx-groups";

const VIEWS: OpView[] = ["summary", "terminal", "files", "snapshots", "console", "gpus"];

export function loadLastView(): OpView {
  const raw = storageGet(VIEW_KEY);
  if (raw && (VIEWS as string[]).includes(raw)) {
    return raw as OpView;
  }
  return "summary";
}

export function saveLastView(view: OpView): void {
  storageSet(VIEW_KEY, view);
}

export function loadGroupState(): Record<string, boolean> {
  try {
    const raw = storageGet(GROUPS_KEY);
    if (!raw) {
      return {};
    }
    const parsed = JSON.parse(raw) as Record<string, boolean>;
    return parsed && typeof parsed === "object" ? parsed : {};
  } catch {
    return {};
  }
}

export function saveGroupState(state: Record<string, boolean>): void {
  storageSet(GROUPS_KEY, JSON.stringify(state));
}

export function isMainNavPreferred(state: unknown): boolean {
  return Boolean(state && typeof state === "object" && (state as { ndlNav?: string }).ndlNav === "main");
}

export function stripMainNavOnLoad(): void {
  const state = window.history.state;
  if (!state || typeof state !== "object" || (state as { ndlNav?: string }).ndlNav !== "main") {
    return;
  }
  const next = { ...(state as Record<string, unknown>) };
  delete next.ndlNav;
  window.history.replaceState(next, "", window.location.pathname + window.location.search);
}

export function preferMainNav(): void {
  const prev = window.history.state && typeof window.history.state === "object" ? window.history.state : {};
  window.history.replaceState({ ...prev, ndlNav: "main" }, "", window.location.pathname + window.location.search);
  window.dispatchEvent(new PopStateEvent("popstate"));
}

export function clearMainNavPreference(): void {
  const prev = window.history.state && typeof window.history.state === "object" ? window.history.state : {};
  const next = { ...(prev as Record<string, unknown>) };
  delete next.ndlNav;
  window.history.replaceState(next, "", window.location.pathname + window.location.search);
  window.dispatchEvent(new PopStateEvent("popstate"));
}
