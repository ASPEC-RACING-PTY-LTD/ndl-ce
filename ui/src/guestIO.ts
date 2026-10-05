import { getWorkload, getWorkloadGuest } from "./api/client";
import type { Workload } from "./api/phase5";

/** Loads the workload once and says why Files and Terminal are unavailable, or null. */
export async function workloadGuestIO(id: string): Promise<{ workload: Workload; reason: string | null }> {
  const w = await getWorkload(id);
  if (w.kind === "system-container") {
    return { workload: w, reason: null };
  }
  if (w.kind !== "vm") {
    return { workload: w, reason: "Files and Terminal are not supported for this workload kind." };
  }
  const g = await getWorkloadGuest(id);
  if (g.nodal_ga?.state === "ok") {
    return { workload: w, reason: null };
  }
  const state = g.nodal_ga?.state || "unavailable";
  return { workload: w, reason: g.nodal_ga?.reason || `No-dal Guest Agent is ${state}` };
}

export async function workloadGuestIOReason(id: string): Promise<string | null> {
  return (await workloadGuestIO(id)).reason;
}
