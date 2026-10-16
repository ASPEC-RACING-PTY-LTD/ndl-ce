import { getCodehold, getCfInventory, getDocker, getHostDisk, getProxies, listUptimeMonitors } from "../api/client";
import { listGameServers } from "../gameservers/api";
import { useFeatureFlags } from "../nav/NavDisclosure";
import { useQuery } from "../query";
import { hasGrant } from "../rbac";
import { navigate } from "../router";
import { useSession } from "../session";
import { SummaryCard } from "../ui/SummaryCard";
import { Link } from "./Link";

// Dashboard cards for the optional features that are turned on. Each card
// reads its feature's own summary (from the server's caches, so the dashboard
// stays cheap) and opens the feature when clicked.

const nothing = () => Promise.resolve(null);

function Card({ label, href, value, meta, tone }: { label: string; href: string; value: string; meta?: string; tone?: "warn" | "danger" }) {
  return <SummaryCard label={label} value={value} meta={meta} tone={tone} onClick={() => navigate(href)} />;
}

/** FeatureCards shows one card per optional feature that is on. Nothing renders when none are. */
export function FeatureCards() {
  const session = useSession();
  const user = session.status === "ready" ? session.user : null;
  const featureEnabled = useFeatureFlags();
  const on = (id: string, grant: string) => Boolean(featureEnabled[id]) && hasGrant(user, grant);
  const docker = on("docker", "compute.read");
  const cloudflare = on("cloudflare", "cloudflare.read");
  const caddy = on("caddy", "network.read");
  const nginx = on("nginx", "network.read");
  const games = on("gameservers", "gameserver.read");
  const disk = hasGrant(user, "storage.read");
  const runners = on("codehold", "compute.read");

  const dockerQ = useQuery(`dash-docker:${docker}`, docker ? () => getDocker() : nothing, 30000);
  const uptimeQ = useQuery(`dash-uptime:${docker}`, docker ? () => listUptimeMonitors() : nothing, 30000);
  const cfQ = useQuery(`dash-cf:${cloudflare}`, cloudflare ? () => getCfInventory() : nothing, 60000);
  const caddyQ = useQuery(`dash-caddy:${caddy}`, caddy ? () => getProxies("caddy") : nothing, 60000);
  const nginxQ = useQuery(`dash-nginx:${nginx}`, nginx ? () => getProxies("nginx") : nothing, 60000);
  const gamesQ = useQuery(`dash-games:${games}`, games ? () => listGameServers() : nothing, 30000);
  const chQ = useQuery(`dash-codehold:${runners}`, runners ? () => getCodehold() : nothing, 30000);
  const diskQ = useQuery(`dash-disk:${disk}`, disk ? () => getHostDisk() : nothing, 60000);

  const cards = [];
  if (docker) {
    const s = dockerQ.data?.summary;
    const bad = s ? s.critical + s.degraded + s.daemons_down : 0;
    cards.push(
      <Card
        key="docker"
        label="Docker"
        href="/docker"
        value={s ? `${s.running}/${s.containers} running` : "Collecting"}
        meta={s ? (bad ? `${bad} need attention` : `${s.projects} projects on ${s.machines} machines`) : "Loading"}
        tone={s && s.critical + s.daemons_down ? "danger" : bad ? "warn" : undefined}
      />,
    );
    const mons = (uptimeQ.data?.items ?? []).filter((m) => m.enabled);
    if (uptimeQ.data && mons.length) {
      const down = mons.filter((m) => m.state === "down").length;
      cards.push(
        <Card
          key="uptime"
          label="Uptime"
          href="/docker/uptime"
          value={`${mons.length - down}/${mons.length} up`}
          meta={down ? `${down} down` : "All monitors up"}
          tone={down ? "danger" : undefined}
        />,
      );
    }
  }
  if (cloudflare) {
    const inv = cfQ.data;
    const tunnels = inv?.tunnels ?? [];
    const routes = inv?.routes ?? [];
    const healthy = tunnels.filter((t) => t.status === "healthy").length;
    const dns = routes.filter((r) => r.enabled && r.dns && r.dns.action !== "ok").length;
    const dnsBlocked = (inv?.accounts ?? []).some((a) => a.dns_access === "denied" || a.dns_access === "partial");
    cards.push(
      <Card
        key="cloudflare"
        label="Cloudflare"
        href="/network/cloudflare"
        value={inv ? `${healthy}/${tunnels.length} tunnels healthy` : "Collecting"}
        meta={inv ? `${routes.length} hostnames${dnsBlocked ? " · DNS not readable" : dns ? ` · ${dns} DNS issues` : ""}` : "Loading"}
        tone={inv && (healthy < tunnels.length || dns || dnsBlocked) ? "warn" : undefined}
      />,
    );
  }
  for (const [id, label, q, enabled] of [
    ["caddy", "Caddy", caddyQ, caddy],
    ["nginx", "Nginx", nginxQ, nginx],
  ] as const) {
    if (!enabled) {
      continue;
    }
    const inst = q.data?.instances ?? [];
    const running = inst.filter((i) => i.active).length;
    const sites = inst.reduce((n, i) => n + i.sites.length, 0);
    cards.push(
      <Card
        key={id}
        label={label}
        href={`/network/${id}`}
        value={q.data ? (inst.length ? `${running}/${inst.length} running` : "Not installed") : "Collecting"}
        meta={q.data ? `${sites} site${sites === 1 ? "" : "s"}` : "Loading"}
        tone={q.data && running < inst.length ? "warn" : undefined}
      />,
    );
  }
  if (runners) {
    const d = chQ.data;
    const list = (d?.runners ?? []).filter((r) => !r.detached);
    const ready = list.filter((r) => r.state === "running").length;
    const busy = list.reduce((n, r) => n + (r.busy || 0), 0);
    const bad = list.filter((r) => r.state === "error").length;
    const conn = d?.controller?.status;
    cards.push(
      <Card
        key="codehold"
        label="CodeHold runners"
        href="/codehold"
        value={d ? (d.connected ? `${ready}/${list.length} ready` : "Not connected") : "Collecting"}
        meta={d ? (conn && conn !== "connected" ? `CodeHold ${conn}` : bad ? `${bad} with errors` : `${busy} job${busy === 1 ? "" : "s"} running`) : "Loading"}
        tone={d && ((conn && conn !== "connected") || bad) ? "warn" : undefined}
      />,
    );
  }
  if (games) {
    const items = gamesQ.data?.items ?? [];
    const running = items.filter((g) => g.status === "running").length;
    cards.push(
      <Card key="games" label="Game Servers" href="/game-servers" value={gamesQ.data ? `${running}/${items.length} running` : "Collecting"} meta={gamesQ.data ? "Game servers" : "Loading"} />,
    );
  }
  if (disk && diskQ.data) {
    const fs = diskQ.data.status.filesystems ?? [];
    const worst = fs.reduce((m, f) => Math.max(m, f.used_percent), 0);
    const level = diskQ.data.status.level;
    cards.push(
      <Card
        key="disk"
        label="Host disk"
        href="/storage/manage"
        value={`${Math.round(worst)}% used`}
        meta={level === "ok" ? "Find what uses space in Manage" : `Space is ${level}; see Manage`}
        tone={level === "ok" ? undefined : level === "warning" ? "warn" : "danger"}
      />,
    );
  }
  if (cards.length === 0) {
    return null;
  }
  return (
    <section className="section">
      <div className="section-head">
        <h2>Features</h2>
        {hasGrant(user, "feature.manage") ? <Link href="/settings/features">Add Features</Link> : null}
      </div>
      <div className="summary-grid">{cards}</div>
    </section>
  );
}
