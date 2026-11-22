import { ApiError } from "../api/client";
import type {
  CatalogueItem,
  GameContentItem,
  GameCreateBody,
  GameServer,
  GameTemplate,
  GameView,
  PreflightResult,
} from "./types";

async function readJson<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers);
  if (init.body !== undefined && !headers.has("Content-Type")) {
    headers.set("Content-Type", "application/json");
  }
  const res = await fetch(`/api/v1${path}`, { ...init, credentials: "include", headers });
  if (!res.ok) {
    let message = `Request failed (${res.status})`;
    try {
      const body = (await res.json()) as { error?: string };
      if (body?.error) {
        message = body.error;
      }
    } catch {
      // keep status text
    }
    throw new ApiError(res.status, message);
  }
  return (await res.json()) as T;
}

/** Whether this host can run game servers (Docker installed and running). */
export async function getGameRuntime(): Promise<{ ready: boolean; reason?: string; detail?: string }> {
  return readJson("/game-servers/runtime");
}

export async function listGameServers(): Promise<{ items: GameServer[] }> {
  return readJson("/game-servers");
}

export async function searchGameServers(q: string): Promise<{ items: GameServer[] }> {
  return readJson(`/game-servers/search?q=${encodeURIComponent(q)}`);
}

export async function getGameServer(id: string): Promise<GameServer> {
  return readJson(`/game-servers/${id}`);
}

export async function createGameServer(body: Record<string, unknown>): Promise<GameServer> {
  return readJson("/game-servers", { method: "POST", body: JSON.stringify(body) });
}

export async function patchGameServer(id: string, body: Record<string, unknown>): Promise<GameServer> {
  return readJson(`/game-servers/${id}`, { method: "PATCH", body: JSON.stringify(body) });
}

export async function gamePower(id: string, action: "start" | "stop" | "restart" | "kill"): Promise<GameServer> {
  return readJson(`/game-servers/${id}/${action}`, { method: "POST", body: "{}" });
}

export async function reinstallGameServer(id: string): Promise<GameServer> {
  return readJson(`/game-servers/${id}/reinstall`, { method: "POST", body: "{}" });
}

export async function deleteGameServer(id: string): Promise<{ deleted: boolean }> {
  return readJson(`/game-servers/${id}/delete`, {
    method: "POST",
    body: "{}",
    headers: { "X-Nodal-Confirm": "delete" },
  });
}

export async function favoriteGameServer(id: string): Promise<GameServer> {
  return readJson(`/game-servers/${id}/favorite`, { method: "POST", body: "{}" });
}

export async function listGameCatalogue(q = ""): Promise<{ items: CatalogueItem[] }> {
  return readJson(`/game-servers/catalogue?q=${encodeURIComponent(q)}`);
}

export async function refreshGameCatalogue(): Promise<{ items: CatalogueItem[]; errors?: string[] }> {
  return readJson("/game-servers/catalogue/refresh", { method: "POST", body: "{}" });
}

export async function importGameTemplate(body: Record<string, unknown>): Promise<GameTemplate> {
  return readJson("/game-servers/catalogue/import", { method: "POST", body: JSON.stringify(body) });
}

export async function getGameTemplate(id: string): Promise<GameTemplate> {
  return readJson(`/game-servers/templates?id=${encodeURIComponent(id)}`);
}

export type GamePrefs = { view: GameView; recents: string; favorites?: string };

export async function getGamePrefs(): Promise<GamePrefs> {
  return readJson("/game-servers/prefs");
}

export async function putGamePrefs(body: { view?: GameView; recents?: string; favorites?: string }): Promise<GamePrefs> {
  return readJson("/game-servers/prefs", { method: "PUT", body: JSON.stringify(body) });
}

/**
 * Parses a prefs value stored as a JSON array string of template ids.
 * Returns null when the value is not a JSON string array (older servers
 * stored free text in `recents`), and [] for an empty value.
 */
export function parseTemplateIdList(raw: unknown): string[] | null {
  if (raw === "") {
    return [];
  }
  if (typeof raw !== "string") {
    return null;
  }
  try {
    const parsed: unknown = JSON.parse(raw);
    if (Array.isArray(parsed) && parsed.every((v) => typeof v === "string")) {
      return parsed as string[];
    }
  } catch {
    // not a JSON array
  }
  return null;
}

/** Catalogue favourites and recents from the prefs API. A null list means the server does not store it. */
export async function getCataloguePrefs(): Promise<{ favorites: string[] | null; recents: string[] | null }> {
  const prefs = await getGamePrefs();
  return {
    favorites: "favorites" in (prefs ?? {}) ? parseTemplateIdList(prefs.favorites) : null,
    recents: parseTemplateIdList(prefs?.recents),
  };
}

export async function putCataloguePrefs(body: { favorites?: string[]; recents?: string[] }): Promise<void> {
  const out: { favorites?: string; recents?: string } = {};
  if (body.favorites) {
    out.favorites = JSON.stringify(body.favorites);
  }
  if (body.recents) {
    out.recents = JSON.stringify(body.recents);
  }
  await putGamePrefs(out);
}

export async function preflightGameServer(body: GameCreateBody): Promise<PreflightResult> {
  return readJson("/game-servers/preflight", { method: "POST", body: JSON.stringify(body) });
}

export async function fleetGameServers(action: string, ids: string[]): Promise<{ items: { id: string; ok: boolean; error?: string }[] }> {
  return readJson("/game-servers/fleet", { method: "POST", body: JSON.stringify({ action, ids }) });
}

export async function gameConsole(id: string): Promise<{ log: string; running: boolean; favorites: { id: string; name: string; command: string }[] }> {
  return readJson(`/game-servers/${id}/console`);
}

export async function sendGameConsole(id: string, command: string): Promise<{ sent: boolean }> {
  return readJson(`/game-servers/${id}/console`, { method: "POST", body: JSON.stringify({ command }) });
}

export type GamePlayer = { name: string; score?: number; seconds?: number; id?: string };
export type GamePlayers = {
  supported: boolean;
  running: boolean;
  actions: string[];
  error?: string;
  list?: { protocol: string; name?: string; map?: string; online: number; max: number; bots?: number; players: GamePlayer[]; partial?: boolean };
};

/** gamePlayers reads who is online from the game's own query protocol. */
export async function gamePlayers(id: string): Promise<GamePlayers> {
  return readJson(`/game-servers/${id}/players`);
}

export async function gamePlayerAction(id: string, action: string, name: string): Promise<{ sent: boolean; command: string }> {
  return readJson(`/game-servers/${id}/players/action`, { method: "POST", body: JSON.stringify({ action, name }) });
}

/** createGameConsoleSession opens a streamed console: the live log, and typed lines to the server. */
export async function createGameConsoleSession(id: string): Promise<import("../api/phase6").IOSession> {
  return readJson(`/game-servers/${id}/console/sessions`, { method: "POST", body: JSON.stringify({}) });
}

export async function gameConsoleHistory(id: string, q = ""): Promise<{ items: string[] }> {
  return readJson(`/game-servers/${id}/console/history?q=${encodeURIComponent(q)}`);
}

export async function createConsoleFav(id: string, name: string, command: string): Promise<unknown> {
  return readJson(`/game-servers/${id}/console/favorites`, { method: "POST", body: JSON.stringify({ name, command }) });
}

export async function deleteConsoleFav(id: string, fid: string): Promise<unknown> {
  return readJson(`/game-servers/${id}/console/favorites/${fid}`, { method: "DELETE" });
}

export async function listGameFiles(
  id: string,
  path = "",
): Promise<{ path: string; items: { name: string; dir: boolean; size: number; modified_at?: string }[] | null }> {
  return readJson(`/game-servers/${id}/files?path=${encodeURIComponent(path)}`);
}

export async function moveGameFile(id: string, from: string, to: string): Promise<unknown> {
  return readJson(`/game-servers/${id}/files/move`, { method: "POST", body: JSON.stringify({ from, to }) });
}

export async function uploadGameFile(id: string, path: string, file: File, signal?: AbortSignal): Promise<void> {
  const data = new FormData();
  data.append("path", path);
  data.append("file", file);
  // Not readJson: the browser sets the multipart boundary itself.
  const res = await fetch(`/api/v1/game-servers/${id}/files/upload`, { method: "POST", body: data, credentials: "include", signal });
  if (!res.ok) {
    const body = (await res.json().catch(() => null)) as { error?: string } | null;
    throw new ApiError(res.status, body?.error || `Upload failed (${res.status})`);
  }
}

export async function downloadGameFile(id: string, path: string, filename: string): Promise<void> {
  const res = await fetch(`/api/v1/game-servers/${id}/files/download?path=${encodeURIComponent(path)}`, { credentials: "include" });
  if (!res.ok) {
    const body = (await res.json().catch(() => null)) as { error?: string } | null;
    throw new ApiError(res.status, body?.error || `Download failed (${res.status})`);
  }
  const href = URL.createObjectURL(await res.blob());
  const a = document.createElement("a");
  a.href = href;
  a.download = filename;
  a.click();
  URL.revokeObjectURL(href);
}

export async function readGameFile(id: string, path: string): Promise<{ path: string; content: string }> {
  return readJson(`/game-servers/${id}/files/content?path=${encodeURIComponent(path)}`);
}

export async function writeGameFile(id: string, path: string, content: string): Promise<unknown> {
  return readJson(`/game-servers/${id}/files/content`, { method: "POST", body: JSON.stringify({ path, content }) });
}

export async function mkdirGameFile(id: string, path: string): Promise<unknown> {
  return readJson(`/game-servers/${id}/files/mkdir`, { method: "POST", body: JSON.stringify({ path }) });
}

export async function extractGameFile(id: string, path: string, dest: string): Promise<{ dest: string }> {
  return readJson(`/game-servers/${id}/files/extract`, { method: "POST", body: JSON.stringify({ path, dest }) });
}

export async function compressGameFiles(id: string, paths: string[], dest: string): Promise<{ path: string; bytes: number; files: number }> {
  return readJson(`/game-servers/${id}/files/compress`, { method: "POST", body: JSON.stringify({ paths, dest }) });
}

export async function deleteGameFile(id: string, path: string): Promise<unknown> {
  return readJson(`/game-servers/${id}/files/delete`, { method: "POST", body: JSON.stringify({ path }) });
}

export async function getGameStartup(id: string): Promise<{ startup: string; variables: { env: string; name: string; value: string; description?: string; secret?: boolean; field_type?: string; editable?: boolean }[] }> {
  return readJson(`/game-servers/${id}/startup`);
}

export async function putGameStartup(id: string, body: Record<string, unknown>): Promise<GameServer> {
  return readJson(`/game-servers/${id}/startup`, { method: "PUT", body: JSON.stringify(body) });
}

export async function getGameConfig(id: string): Promise<{ settings: { id: string; label: string; help?: string; kind: string; options?: string[]; restart?: boolean; advanced?: boolean; env?: string; file?: string }[]; values: Record<string, string> }> {
  return readJson(`/game-servers/${id}/config`);
}

export async function putGameConfig(id: string, values: Record<string, string>): Promise<unknown> {
  return readJson(`/game-servers/${id}/config`, { method: "PUT", body: JSON.stringify({ values }) });
}

export async function diffGameConfig(id: string, values: Record<string, string>): Promise<{ changes: { id: string; label: string; from: string; to: string; restart?: boolean }[] }> {
  return readJson(`/game-servers/${id}/config/diff`, { method: "POST", body: JSON.stringify({ values }) });
}

export async function revertGameConfig(id: string): Promise<unknown> {
  return readJson(`/game-servers/${id}/config/revert`, { method: "POST", body: "{}" });
}

export async function getGameNetwork(id: string): Promise<{ ports: { name?: string; container_port: number; host_port?: number; protocol?: string }[] }> {
  return readJson(`/game-servers/${id}/network`);
}

export async function putGameNetwork(id: string, ports: unknown[]): Promise<unknown> {
  return readJson(`/game-servers/${id}/network`, { method: "PUT", body: JSON.stringify({ ports }) });
}

export async function getGameResources(id: string): Promise<{ cpus: number; memory_bytes: number; disk_bytes: number }> {
  return readJson(`/game-servers/${id}/resources`);
}

export async function putGameResources(id: string, body: Record<string, number>): Promise<unknown> {
  return readJson(`/game-servers/${id}/resources`, { method: "PUT", body: JSON.stringify(body) });
}

/** Which build (32-bit or 64-bit) a server runs. */
export type GameBuildState = {
  supported: boolean;
  current: string;
  running: boolean;
  custom?: boolean;
  options: { id: string; label: string; installed: boolean }[];
};

export async function getGameBuild(id: string): Promise<GameBuildState> {
  return readJson(`/game-servers/${id}/build`);
}

export async function putGameBuild(id: string, build: string): Promise<GameBuildState> {
  return readJson(`/game-servers/${id}/build`, { method: "PUT", body: JSON.stringify({ build }) });
}

/** A server's own MariaDB. The password comes only from getGameDatabaseCredentials. */
export type GameDatabase = {
  supported: boolean;
  state: "none" | "configured" | "installing" | "running" | "stopped" | "missing";
  database?: string;
  user?: string;
  host?: string;
  port?: number;
  image?: string;
  installed_at?: string;
  password?: string;
  socket?: string;
  job?: GameBackupJob | null;
};

export async function getGameDatabase(id: string): Promise<GameDatabase> {
  return readJson(`/game-servers/${id}/database`);
}

export async function getGameDatabaseCredentials(id: string): Promise<GameDatabase> {
  return readJson(`/game-servers/${id}/database/credentials`);
}

export async function putGameDatabase(id: string, database: string): Promise<GameDatabase> {
  return readJson(`/game-servers/${id}/database`, { method: "PUT", body: JSON.stringify({ database }) });
}

export async function gameDatabaseAction(id: string, action: "install" | "start" | "stop"): Promise<GameDatabase> {
  return readJson(`/game-servers/${id}/database/${action}`, { method: "POST", body: "{}" });
}

export async function removeGameDatabase(id: string, deleteData: boolean, confirm = ""): Promise<GameDatabase> {
  return readJson(`/game-servers/${id}/database/remove`, { method: "POST", body: JSON.stringify({ delete_data: deleteData, confirm }) });
}

/** The latest background backup or restore for a server. */
export type GameBackupJob = {
  op: "backup" | "restore" | "database";
  name: string;
  running: boolean;
  started_at: string;
  finished_at?: string;
  error?: string;
};

/** What a server's backups take. No include entries: the whole server folder. */
export type GameBackupScope = { include: string[]; exclude: string[]; newest: string[] };

export async function getGameBackupScope(id: string): Promise<GameBackupScope> {
  return readJson(`/game-servers/${id}/backup-scope`);
}

export async function putGameBackupScope(id: string, scope: GameBackupScope): Promise<GameBackupScope> {
  return readJson(`/game-servers/${id}/backup-scope`, { method: "PUT", body: JSON.stringify(scope) });
}

export async function listGameBackups(
  id: string,
): Promise<{ items: { id: string; name: string; reason?: string; bytes: number; created_at: string }[]; job?: GameBackupJob | null }> {
  return readJson(`/game-servers/${id}/backups`);
}

export async function createGameBackup(id: string, name?: string): Promise<unknown> {
  return readJson(`/game-servers/${id}/backups`, { method: "POST", body: JSON.stringify({ name, reason: "manual" }) });
}

export async function restoreGameBackup(id: string, bid: string): Promise<unknown> {
  return readJson(`/game-servers/${id}/backups/${bid}/restore`, { method: "POST", body: "{}" });
}

export async function deleteGameBackup(id: string, bid: string): Promise<unknown> {
  return readJson(`/game-servers/${id}/backups/${bid}`, { method: "DELETE" });
}

export async function listGameSchedules(id: string): Promise<{ items: { id: string; name: string; action: string; cron: string; enabled: boolean }[] }> {
  return readJson(`/game-servers/${id}/schedules`);
}

export async function createGameSchedule(id: string, body: Record<string, string>): Promise<unknown> {
  return readJson(`/game-servers/${id}/schedules`, { method: "POST", body: JSON.stringify(body) });
}

export async function deleteGameSchedule(id: string, sid: string): Promise<unknown> {
  return readJson(`/game-servers/${id}/schedules/${sid}`, { method: "DELETE" });
}

export async function listGameContent(id: string): Promise<{ items: GameContentItem[]; supported?: boolean }> {
  return readJson(`/game-servers/${id}/content`);
}

export async function searchGameContent(
  id: string,
  q: string,
  source = "",
): Promise<{
  items: GameContentItem[] | null;
  supported?: boolean;
  provider?: string;
  kind?: string;
  loader?: string;
  /** Catalogues this server can install from, such as modrinth and curseforge. */
  sources?: string[];
  /** CurseForge was asked for and no API key is saved. */
  needs_key?: boolean;
}> {
  const from = source ? `&source=${encodeURIComponent(source)}` : "";
  return readJson(`/game-servers/${id}/content/search?q=${encodeURIComponent(q)}${from}`);
}

export async function installGameContent(
  id: string,
  externalId: string,
  installDependencies = true,
  source = "",
): Promise<{ item: GameContentItem; missing_dependencies?: string[] }> {
  return readJson(`/game-servers/${id}/content/install`, {
    method: "POST",
    body: JSON.stringify({ external_id: externalId, version: "latest", install_dependencies: installDependencies, source }),
  });
}

export async function toggleGameContent(id: string, cid: string, enabled: boolean): Promise<unknown> {
  return readJson(`/game-servers/${id}/content/${cid}/${enabled ? "enable" : "disable"}`, { method: "POST", body: "{}" });
}

export async function uninstallGameContent(id: string, cid: string): Promise<unknown> {
  return readJson(`/game-servers/${id}/content/${cid}/uninstall`, { method: "POST", body: "{}" });
}

export async function updateGameContent(id: string, cid: string): Promise<{ warning?: string }> {
  return readJson(`/game-servers/${id}/content/${cid}/update`, { method: "POST", body: "{}" });
}

export async function bulkGameContent(id: string, action: string, ids: string[]): Promise<unknown> {
  return readJson(`/game-servers/${id}/content/bulk`, { method: "POST", body: JSON.stringify({ action, ids }) });
}

export async function listGameUsers(id: string): Promise<{ items: { user_id: string; username: string; grants: string[] }[]; owner_user_id?: string }> {
  return readJson(`/game-servers/${id}/users`);
}

export async function createGameUser(id: string, username: string, grants: string[]): Promise<unknown> {
  return readJson(`/game-servers/${id}/users`, { method: "POST", body: JSON.stringify({ username, grants }) });
}

export async function deleteGameUser(id: string, uid: string): Promise<unknown> {
  return readJson(`/game-servers/${id}/users/${uid}`, { method: "DELETE" });
}

export async function gameActivity(id: string): Promise<{ items: { id: string; kind: string; summary: string; detail?: string; created_at: string }[] }> {
  return readJson(`/game-servers/${id}/activity`);
}

export async function getGameNotes(id: string): Promise<{ notes: string }> {
  return readJson(`/game-servers/${id}/notes`);
}

export async function putGameNotes(id: string, notes: string): Promise<unknown> {
  return readJson(`/game-servers/${id}/notes`, { method: "PUT", body: JSON.stringify({ notes }) });
}

export async function gameDiagnostics(id: string): Promise<Record<string, unknown>> {
  return readJson(`/game-servers/${id}/diagnostics`);
}

export async function gameTuning(id: string): Promise<{ items: { id: string; title: string; detail: string; severity: string }[] }> {
  return readJson(`/game-servers/${id}/tuning`);
}

/** A Minecraft modpack on Modrinth or CurseForge. */
export type Modpack = {
  provider: "modrinth" | "curseforge";
  id: string;
  slug?: string;
  name: string;
  summary?: string;
  icon_url?: string;
  downloads: number;
  game_versions?: string[];
  loaders?: string[];
  url?: string;
};

/** One release of a modpack, with the Java runtime its Minecraft version needs. */
export type ModpackVersion = {
  id: string;
  name: string;
  game_version?: string;
  loader?: string;
  date?: string;
  release: boolean;
  server_pack?: boolean;
  image_label?: string;
};

export async function searchModpacks(
  provider: "modrinth" | "curseforge",
  q: string,
): Promise<{ items: Modpack[]; template_id?: string; needs_key?: boolean }> {
  return readJson(`/game-servers/modpacks?provider=${provider}&q=${encodeURIComponent(q)}`);
}

export async function modpackVersions(
  provider: "modrinth" | "curseforge",
  id: string,
): Promise<{ items: ModpackVersion[]; template_id: string }> {
  return readJson(`/game-servers/modpacks/${provider}/${encodeURIComponent(id)}/versions`);
}

export async function getGameSettings(): Promise<{ curseforge_key_set: boolean }> {
  return readJson("/game-servers/settings");
}

export async function putGameSettings(body: { curseforge_key?: string; clear_curseforge?: boolean }): Promise<{ curseforge_key_set: boolean }> {
  return readJson("/game-servers/settings", { method: "PUT", body: JSON.stringify(body) });
}

export type WorkshopSync = {
  supported: boolean;
  enabled: boolean;
  collection: string;
  running: boolean;
  checking: boolean;
  seq: number;
  pending_request?: string;
  last_check?: string;
  next_check?: string;
  last_result?: string;
  last_error?: string;
  errors: { wsid: string; error: string }[];
  items: { wsid: string; title: string; state: string; kind: string; time_updated: number; size: number }[];
};

export async function getWorkshopSync(id: string): Promise<WorkshopSync> {
  return readJson(`/game-servers/${id}/workshop-sync`);
}

export async function putWorkshopSync(id: string, enabled: boolean): Promise<WorkshopSync> {
  return readJson(`/game-servers/${id}/workshop-sync`, { method: "PUT", body: JSON.stringify({ enabled }) });
}

export async function checkWorkshopSync(id: string): Promise<WorkshopSync> {
  return readJson(`/game-servers/${id}/workshop-sync/check`, { method: "POST", body: "{}" });
}

/* ---------------- Database manager ---------------- */

export type DBResultSet = { columns: string[]; rows: (string | null)[][]; row_count: number; truncated?: boolean };
export type DBStatementResult = { sql: string; sets: DBResultSet[]; affected: number; checked?: "rolled_back" | "syntax" };
export type DBQueryResult = { statements: DBStatementResult[]; error?: string; error_index: number; elapsed_ms: number; dry_run?: boolean };
export type DBTable = { name: string; engine: string; rows: number; data_bytes: number; index_bytes: number; comment?: string; view?: boolean };
export type DBSchema = { database: string; server_database: string; databases: string[]; tables: DBTable[] };
export type DBColumn = { name: string; type: string; nullable: boolean; key?: string; default: string | null; extra?: string };
export type DBTablePage = { database: string; table: string; columns: DBColumn[]; rows: (string | null)[][]; total: number; offset: number; limit: number };
export type DBUser = { user: string; host: string; grants: string[]; protected?: boolean; server?: boolean; privileges: Record<string, string[]> };
export type DBUsers = { users: DBUser[]; privileges: string[]; databases: string[]; server_database: string };

export async function runGameDatabaseQuery(id: string, sql: string, database = "", maxRows = 500, dryRun = false): Promise<DBQueryResult> {
  return readJson(`/game-servers/${id}/database/query`, { method: "POST", body: JSON.stringify({ sql, database, max_rows: maxRows, dry_run: dryRun }) });
}

export type DBIndex = { name: string; columns: string[]; unique: boolean; type: string; primary?: boolean; comment?: string; cardinality: number };
export type DBIndexes = { database: string; table: string; columns: string[]; indexes: DBIndex[] };

export async function getGameDatabaseIndexes(id: string, database: string, table: string): Promise<DBIndexes> {
  return readJson(`/game-servers/${id}/database/indexes?${new URLSearchParams({ database, table }).toString()}`);
}

export async function createGameDatabaseIndex(
  id: string,
  body: { database: string; table: string; name: string; columns: string[]; kind: "index" | "unique" | "fulltext" },
): Promise<{ sql: string }> {
  return readJson(`/game-servers/${id}/database/indexes`, { method: "POST", body: JSON.stringify(body) });
}

export async function dropGameDatabaseIndex(id: string, database: string, table: string, name: string): Promise<unknown> {
  return readJson(`/game-servers/${id}/database/indexes/drop`, { method: "POST", body: JSON.stringify({ database, table, name }) });
}

export async function getGameDatabaseSchema(id: string, database = ""): Promise<DBSchema> {
  return readJson(`/game-servers/${id}/database/schema?database=${encodeURIComponent(database)}`);
}

export async function getGameDatabaseTable(
  id: string,
  database: string,
  table: string,
  offset: number,
  limit: number,
  order = "",
  dir: "asc" | "desc" = "asc",
): Promise<DBTablePage> {
  const q = new URLSearchParams({ database, table, offset: String(offset), limit: String(limit), order, dir });
  return readJson(`/game-servers/${id}/database/table?${q.toString()}`);
}

export async function getGameDatabaseUsers(id: string): Promise<DBUsers> {
  return readJson(`/game-servers/${id}/database/users`);
}

export async function createGameDatabaseUser(
  id: string,
  body: { user: string; host: string; password: string; database: string; privileges: string[] },
): Promise<{ user: string; host: string; password: string }> {
  return readJson(`/game-servers/${id}/database/users`, { method: "POST", body: JSON.stringify(body) });
}

export async function dropGameDatabaseUser(id: string, user: string, host: string): Promise<unknown> {
  return readJson(`/game-servers/${id}/database/users/drop`, { method: "POST", body: JSON.stringify({ user, host }) });
}

export async function setGameDatabaseUserPassword(id: string, user: string, host: string, password = ""): Promise<{ password: string }> {
  return readJson(`/game-servers/${id}/database/users/password`, { method: "POST", body: JSON.stringify({ user, host, password }) });
}

export async function putGameDatabaseGrants(id: string, user: string, host: string, database: string, privileges: string[]): Promise<unknown> {
  return readJson(`/game-servers/${id}/database/grants`, { method: "PUT", body: JSON.stringify({ user, host, database, privileges }) });
}

export type GlobalDatabase = {
  server_id: string;
  server_name: string;
  game: string;
  database: string;
  user: string;
  state: GameDatabase["state"];
  image?: string;
  installed_at?: string;
  size_bytes: number;
  tables: number;
  memory_mb: number;
  cpus: number;
};

export async function listAllDatabases(): Promise<{ items: GlobalDatabase[] }> {
  return readJson(`/databases`);
}

export type BulkDBResult = { server_id: string; server_name: string; ok: boolean; error?: string; state?: string; query?: DBQueryResult };

export async function bulkDatabases(
  serverIds: string[],
  action: "start" | "stop" | "restart" | "query",
  sql = "",
  dryRun = false,
): Promise<{ results: BulkDBResult[] }> {
  return readJson(`/databases/bulk`, { method: "POST", body: JSON.stringify({ server_ids: serverIds, action, sql, dry_run: dryRun }) });
}

export type DBUsage = {
  memory_bytes: number;
  memory_limit_bytes: number;
  cpu_percent: number;
  buffer_pool_bytes: number;
  effective_buffer_pool_mb: number;
  flush_log_at_commit: number;
  restart_needed: boolean;
  running: boolean;
};
export type DBResourcesState = { state: string; memory_mb: number; cpus: number; buffer_pool_mb: number; fast_commit?: boolean; usage?: DBUsage | null };

/** With usage false it answers at once, without measuring what the database uses. */
export async function getGameDatabaseResources(id: string, usage = true): Promise<DBResourcesState> {
  return readJson(`/game-servers/${id}/database/resources${usage ? "" : "?usage=0"}`);
}

export async function putGameDatabaseResources(
  id: string,
  body: { memory_mb: number; cpus: number; buffer_pool_mb: number; fast_commit: boolean },
): Promise<DBResourcesState> {
  return readJson(`/game-servers/${id}/database/resources`, { method: "PUT", body: JSON.stringify(body) });
}

export async function restartGameDatabase(id: string): Promise<GameDatabase> {
  return readJson(`/game-servers/${id}/database/restart`, { method: "POST", body: "{}" });
}
