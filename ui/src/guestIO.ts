import { getWorkload, getWorkloadGuest } from "./api/client";

export async function workloadGuestIOReason(id: string): Promise<string | null> {
  const w = await getWorkload(id);
  if (w.kind === "system-container") {
    return null;
  }
  if (w.kind !== "vm") {
    return "Files and Terminal are not supported for this workload kind.";
  }
  const g = await getWorkloadGuest(id);
  if (g.nodal_ga?.state === "ok") {
    return null;
  }
  const state = g.nodal_ga?.state || "unavailable";
  return g.nodal_ga?.reason || `No-dal Guest Agent is ${state}`;
}
