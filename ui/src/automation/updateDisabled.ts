import type { AutomationFlow } from "../generated/openapi";

/** FLOW_DISABLED_BY_UPDATE matches flowDisabledByUpdate in internal/httpapi/update_notices.go. */
export const FLOW_DISABLED_BY_UPDATE = "Turned off by the update that added flow approvals. Review it, then turn it back on.";

/** flowsOffByUpdate lists flows the update turned off that are still off. */
export function flowsOffByUpdate(flows: AutomationFlow[]): AutomationFlow[] {
  return flows.filter((f) => !f.enabled && f.disabled_reason === FLOW_DISABLED_BY_UPDATE);
}
