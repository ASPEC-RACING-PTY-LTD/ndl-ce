import type { UptimeMonitor, UptimeMonitorIn } from "../api/client";
import type { DockerTunnel } from "../generated/openapi";

/** A monitor proposed from a Cloudflare tunnel route, before it is saved. */
export type UptimeCandidate = {
  hostname: string;
  monitor: UptimeMonitorIn;
  /** Where the route points, such as http://localhost:3000. */
  services: string[];
  machines: string[];
  /** Set when a monitor already watches this hostname. */
  existing?: string;
};

type Rule = { hostname?: string; service?: string };
type Link = { origin?: string; name?: string };

function webService(service: string): boolean {
  return /^https?:\/\//i.test(service);
}

function hostOf(target: string): string {
  const t = target.trim().toLowerCase();
  if (!t) {
    return "";
  }
  try {
    return new URL(/^[a-z]+:\/\//.test(t) ? t : `https://${t}`).hostname;
  } catch {
    return t.split("/")[0].split(":")[0];
  }
}

/**
 * discoverMonitors turns the tunnel routes the Docker page already reads into
 * one proposed monitor per public web hostname, linked to the containers that
 * serve it. Wildcard, SSH/TCP and status-only routes are skipped, and hostnames
 * an existing monitor already watches are marked so they are not duplicated.
 */
export function discoverMonitors(tunnels: DockerTunnel[], monitors: UptimeMonitor[]): UptimeCandidate[] {
  const watched = new Map<string, string>();
  for (const m of monitors) {
    const h = hostOf(m.target);
    if (h) {
      watched.set(h, m.name);
    }
  }
  const byHost = new Map<string, UptimeCandidate>();
  for (const t of tunnels) {
    const machine = t.machine_name || t.machine_id || "";
    const links = (t.links ?? []) as Link[];
    for (const rule of (t.ingress ?? []) as Rule[]) {
      const hostname = (rule.hostname ?? "").trim().toLowerCase();
      const service = (rule.service ?? "").trim();
      if (!hostname || hostname.includes("*") || !webService(service)) {
        continue;
      }
      let c = byHost.get(hostname);
      if (!c) {
        c = {
          hostname,
          monitor: { name: hostname, target: `https://${hostname}`, expect_status: "", keyword: "", containers: [], auto_restart: true, enabled: true },
          services: [],
          machines: [],
          existing: watched.get(hostname),
        };
        byHost.set(hostname, c);
      }
      if (!c.services.includes(service)) {
        c.services.push(service);
      }
      if (machine && !c.machines.includes(machine)) {
        c.machines.push(machine);
      }
      for (const l of links) {
        if (l.origin === service && l.name && !c.monitor.containers.some((x) => x.machine === machine && x.name === l.name)) {
          c.monitor.containers.push({ machine, name: l.name });
        }
      }
    }
  }
  return [...byHost.values()].sort((a, b) => Number(!!a.existing) - Number(!!b.existing) || a.hostname.localeCompare(b.hostname));
}
