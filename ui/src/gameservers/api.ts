import { ApiError } from "../api/client";
import type {
  CatalogueItem,
  GameContentItem,
  GameServer,
  GameTemplate,
  GameView,
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

export async function getGamePrefs(): Promise<{ view: GameView; recents: string }> {
  return readJson("/game-servers/prefs");
}

export async function putGamePrefs(body: { view?: GameView; recents?: string }): Promise<{ view: GameView; recents: string }> {
  return readJson("/game-servers/prefs", { method: "PUT", body: JSON.stringify(body) });
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

export async function gameConsoleHistory(id: string, q = ""): Promise<{ items: string[] }> {
  return readJson(`/game-servers/${id}/console/history?q=${encodeURIComponent(q)}`);
}

export async function createConsoleFav(id: string, name: string, command: string): Promise<unknown> {
  return readJson(`/game-servers/${id}/console/favorites`, { method: "POST", body: JSON.stringify({ name, command }) });
}

export async function deleteConsoleFav(id: string, fid: string): Promise<unknown> {
  return readJson(`/game-servers/${id}/console/favorites/${fid}`, { method: "DELETE" });
}

export async function listGameFiles(id: string, path = ""): Promise<{ path: string; items: { name: string; dir: boolean; size: number }[] }> {
  return readJson(`/game-servers/${id}/files?path=${encodeURIComponent(path)}`);
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

export async function deleteGameFile(id: string, path: string): Promise<unknown> {
  return readJson(`/game-servers/${id}/files/delete`, { method: "POST", body: JSON.stringify({ path }) });
}

export async function getGameStartup(id: string): Promise<{ startup: string; variables: { env: string; name: string; value: string; description?: string; secret?: boolean; field_type?: string; editable?: boolean }[] }> {
  return readJson(`/game-servers/${id}/startup`);
}

export async function putGameStartup(id: string, body: Record<string, unknown>): Promise<GameServer> {
  return readJson(`/game-servers/${id}/startup`, { method: "PUT", body: JSON.stringify(body) });
}

export async function getGameConfig(id: string): Promise<{ settings: { id: string; label: string; help?: string; kind: string; options?: string[]; restart?: boolean; advanced?: boolean }[]; values: Record<string, string> }> {
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

export async function listGameBackups(id: string): Promise<{ items: { id: string; name: string; reason?: string; bytes: number; created_at: string }[] }> {
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

export async function searchGameContent(id: string, q: string): Promise<{ items: GameContentItem[]; supported?: boolean }> {
  return readJson(`/game-servers/${id}/content/search?q=${encodeURIComponent(q)}`);
}

export async function installGameContent(id: string, externalId: string, installDependencies = true): Promise<{ item: GameContentItem; missing_dependencies?: string[] }> {
  return readJson(`/game-servers/${id}/content/install`, {
    method: "POST",
    body: JSON.stringify({ external_id: externalId, version: "latest", install_dependencies: installDependencies }),
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
