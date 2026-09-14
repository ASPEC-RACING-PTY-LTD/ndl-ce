import type { GameServer } from "./types";

export function hasCap(server: Pick<GameServer, "capabilities"> | null | undefined, cap: string): boolean {
  return Boolean(server?.capabilities?.includes(cap));
}

export function contentKind(server: Pick<GameServer, "capabilities"> | null | undefined): "mods" | "plugins" | "addons" | null {
  if (hasCap(server, "mods")) {
    return "mods";
  }
  if (hasCap(server, "plugins")) {
    return "plugins";
  }
  if (hasCap(server, "workshop")) {
    return "addons";
  }
  return null;
}

export function familyTone(family?: string): string {
  switch (family) {
    case "minecraft":
      return "gs-tone-mc";
    case "steam":
      return "gs-tone-steam";
    case "fivem":
      return "gs-tone-fivem";
    case "source":
      return "gs-tone-source";
    case "mindustry":
      return "gs-tone-mind";
    case "terraria":
      return "gs-tone-terra";
    case "factorio":
      return "gs-tone-fact";
    default:
      return "gs-tone-generic";
  }
}

export function statusLabel(status?: string): string {
  switch (status) {
    case "pending":
      return "Queued";
    case "installing":
      return "Installing";
    case "ready":
      return "Ready";
    case "starting":
      return "Starting";
    case "running":
      return "Running";
    case "stopping":
      return "Stopping";
    case "stopped":
      return "Stopped";
    case "failed":
      return "Failed";
    case "offline":
      return "Offline";
    default:
      return status || "Unknown";
  }
}

export function formatRam(bytes?: number): string {
  if (!bytes) {
    return "RAM not set";
  }
  const mb = bytes / (1024 * 1024);
  if (mb >= 1024) {
    return `${(mb / 1024).toFixed(mb % 1024 === 0 ? 0 : 1)} GiB`;
  }
  return `${Math.round(mb)} MiB`;
}
