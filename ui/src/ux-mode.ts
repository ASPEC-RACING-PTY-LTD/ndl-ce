import { storageGet, storageSet } from "./storage";

export type UxLevel = "guided" | "advanced" | "expert";

const KEY = "ndl-ux-level";

export function getUxLevelDefault(): UxLevel {
  const raw = storageGet(KEY);
  if (raw === "advanced" || raw === "expert" || raw === "guided") {
    return raw;
  }
  return "guided";
}

export function setUxLevelDefault(level: UxLevel): void {
  storageSet(KEY, level);
}

export function isAdvanced(level: UxLevel): boolean {
  return level === "advanced" || level === "expert";
}

export function isExpert(level: UxLevel): boolean {
  return level === "expert";
}
