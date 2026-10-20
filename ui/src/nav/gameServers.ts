export const GS_ROOT = "/game-servers";

export const GS_AREA_SEGMENTS = new Set([
  "create",
  "catalogue",
  "favourites",
  "favorites",
  "recent",
  "fleet",
]);

export const GS_SECTION_LABELS: Record<string, string> = {
  overview: "Overview",
  console: "Console",
  files: "Files",
  plugins: "Plugins",
  mods: "Mods",
  workshop: "Workshop",
  players: "Players",
  backups: "Backups & Schedules",
  settings: "Server Settings",
};

/**
 * Sections that became part of a merged tab. Old links keep working: they
 * open the tab and scroll to the section (gameServerAnchorFromPath).
 */
export const GS_MERGED_SECTIONS: Record<string, string> = {
  schedules: "backups",
  config: "settings",
  startup: "settings",
  network: "settings",
  resources: "settings",
  databases: "settings",
  activity: "settings",
  diagnostics: "settings",
  notes: "settings",
  users: "settings",
};

export function isGameServersContext(path: string): boolean {
  return (
    path === GS_ROOT ||
    path.startsWith(`${GS_ROOT}/`) ||
    path === "/workloads/game-servers" ||
    path.startsWith("/workloads/game-servers/")
  );
}

export function isGameServersHomePath(path: string): boolean {
  return (
    path === GS_ROOT ||
    path === `${GS_ROOT}/favourites` ||
    path === `${GS_ROOT}/favorites` ||
    path === `${GS_ROOT}/recent`
  );
}

export function isGameServersCreatePath(path: string): boolean {
  return path === `${GS_ROOT}/create`;
}

export function gameServerIdFromPath(path: string): string | null {
  const parts = path.split("/").filter(Boolean);
  if (parts[0] !== "game-servers" || !parts[1] || GS_AREA_SEGMENTS.has(parts[1])) {
    return null;
  }
  return parts[1];
}

export function gameServerSectionFromPath(path: string): string {
  const id = gameServerIdFromPath(path);
  if (!id) {
    return "";
  }
  const parts = path.split("/").filter(Boolean);
  const raw = parts[2] || "overview";
  return GS_MERGED_SECTIONS[raw] ?? raw;
}

/** gameServerAnchorFromPath is the merged section an old link points at, e.g. "config", or "". */
export function gameServerAnchorFromPath(path: string): string {
  if (!gameServerIdFromPath(path)) {
    return "";
  }
  const raw = path.split("/").filter(Boolean)[2] ?? "";
  return GS_MERGED_SECTIONS[raw] ? raw : "";
}

export function gameServerHref(id?: string, section?: string): string {
  if (!id) {
    return GS_ROOT;
  }
  if (!section || section === "overview") {
    return `${GS_ROOT}/${id}`;
  }
  return `${GS_ROOT}/${id}/${section}`;
}

export function gameServersHomeHref(filter?: string): string {
  switch (filter) {
    case "favorites":
    case "favourites":
      return `${GS_ROOT}/favourites`;
    case "recent":
      return `${GS_ROOT}/recent`;
    default:
      return GS_ROOT;
  }
}

export function gameServersHomeFilter(path: string): "all" | "favorites" | "recent" {
  if (path === `${GS_ROOT}/favourites` || path === `${GS_ROOT}/favorites`) {
    return "favorites";
  }
  if (path === `${GS_ROOT}/recent`) {
    return "recent";
  }
  return "all";
}

export function legacyGameServersRedirect(path: string, search = ""): string | null {
  if (path !== "/workloads/game-servers" && !path.startsWith("/workloads/game-servers/")) {
    return null;
  }
  const rest = path === "/workloads/game-servers" ? "" : path.slice("/workloads/game-servers".length);
  const query = search.startsWith("?") ? search.slice(1) : search;
  const tab = new URLSearchParams(query).get("tab");
  if (rest === "" || rest === "/") {
    return GS_ROOT;
  }
  if (rest === "/create") {
    return `${GS_ROOT}/create`;
  }
  const parts = rest.split("/").filter(Boolean);
  const id = parts[0];
  if (!id) {
    return GS_ROOT;
  }
  const section = parts[1] || (tab && tab !== "overview" ? tab : "");
  return gameServerHref(id, section || undefined);
}

export function gameServerCrumbs(path: string): { href: string; label: string }[] {
  const trail = [{ href: GS_ROOT, label: "Game Servers" }];
  if (path === `${GS_ROOT}/create`) {
    trail.push({ href: path, label: "Create" });
    return trail;
  }
  if (path === `${GS_ROOT}/favourites` || path === `${GS_ROOT}/favorites`) {
    trail.push({ href: path, label: "Favourites" });
    return trail;
  }
  if (path === `${GS_ROOT}/recent`) {
    trail.push({ href: path, label: "Recent" });
    return trail;
  }
  const id = gameServerIdFromPath(path);
  if (!id) {
    return trail;
  }
  trail.push({ href: gameServerHref(id), label: "Server" });
  const section = gameServerSectionFromPath(path);
  if (section && section !== "overview") {
    trail.push({ href: path, label: GS_SECTION_LABELS[section] ?? section });
  }
  return trail;
}
