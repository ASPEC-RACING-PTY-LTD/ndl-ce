import { useEffect, useState, type ReactNode } from "react";
import {
  getCerts,
  getLicense,
  getUpdates,
  listAlerts,
  listAPITokens,
  listApprovals,
  listAudit,
  listBackupRuns,
  listFeatures,
  listManagedUsers,
  listTasks,
} from "../api/client";
import { Icon, type IconName } from "../components/Icon";
import { hasGrant } from "../rbac";
import { useSession } from "../session";

const DAY = 24 * 60 * 60 * 1000;

type Load<T> = { state: "loading" } | { state: "ready"; data: T } | { state: "hidden" };

/** useStat loads one figure; a failure (or no permission) hides its tile. */
function useStat<T>(load: () => Promise<T>, allowed: boolean): Load<T> {
  const [v, setV] = useState<Load<T>>(allowed ? { state: "loading" } : { state: "hidden" });
  useEffect(() => {
    if (!allowed) {
      setV({ state: "hidden" });
      return;
    }
    let live = true;
    load().then(
      (data) => live && setV({ state: "ready", data }),
      () => live && setV({ state: "hidden" }),
    );
    return () => {
      live = false;
    };
    // load is a fresh closure each render; the tile loads once per permission change.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [allowed]);
  return v;
}

export type StatLine = { label: string; value: ReactNode; tone?: "ok" | "warn" | "bad" };

/** recentCount is how many timestamps fall within the last day. */
export function recentCount(times: (string | undefined)[], now = Date.now()): number {
  return times.filter((t) => {
    const at = t ? Date.parse(t) : NaN;
    return Number.isFinite(at) && now - at <= DAY;
  }).length;
}

/** daysUntil is whole days from now to iso (negative when past). */
export function daysUntil(iso: string | undefined, now = Date.now()): number | null {
  const at = iso ? Date.parse(iso) : NaN;
  return Number.isFinite(at) ? Math.floor((at - now) / DAY) : null;
}

function Tile<T>({ title, icon, stat, main, lines }: { title: string; icon: IconName; stat: Load<T>; main: (d: T) => ReactNode; lines?: (d: T) => StatLine[] }) {
  if (stat.state === "hidden") {
    return null;
  }
  return (
    <section className="panel admin-stat" aria-label={title}>
      <header className="admin-stat-head">
        <Icon name={icon} size={16} />
        <span>{title}</span>
      </header>
      {stat.state === "loading" ? (
        <p className="admin-stat-main muted">…</p>
      ) : (
        <>
          <p className="admin-stat-main">{main(stat.data)}</p>
          {lines ? (
            <dl className="admin-stat-lines">
              {lines(stat.data).map((l) => (
                <div key={l.label} className={l.tone ? "is-" + l.tone : undefined}>
                  <dt>{l.label}</dt>
                  <dd>{l.value}</dd>
                </div>
              ))}
            </dl>
          ) : null}
        </>
      )}
    </section>
  );
}

/** AdminHome is /admin: live figures for the areas this person runs. */
export function AdminHome() {
  const session = useSession();
  const user = session.status === "ready" ? session.user : null;
  const can = (p: string) => hasGrant(user, p);

  const users = useStat(() => listManagedUsers(), can("users.read"));
  const approvals = useStat(() => listApprovals("pending"), can("approvals.view"));
  const tokens = useStat(() => listAPITokens(), can("api_access.manage") || can("identity.token.create"));
  const tasks = useStat(() => listTasks(200), true);
  const updates = useStat(() => getUpdates(), can("updates.manage"));
  const features = useStat(() => listFeatures(), can("feature.manage"));
  const backups = useStat(() => listBackupRuns(), true);
  const alerts = useStat(() => listAlerts(), true);
  const audit = useStat(() => listAudit(500), true);
  const certs = useStat(() => getCerts(), can("settings.tls.manage"));
  const license = useStat(() => getLicense(), can("settings.license.manage"));

  return (
    <section className="page page-wide admin-dash" aria-label="Administration">
      <div className="admin-stats">
        <Tile
          title="Users"
          icon="users"
          stat={users}
          main={(d) => d.total ?? d.items.length}
          lines={(d) => {
            const disabled = d.items.filter((u) => u.status === "disabled" || u.disabled_at).length;
            const noMfa = d.items.filter((u) => !u.mfa_enabled).length;
            return [
              { label: "Active", value: d.items.length - disabled },
              { label: "Disabled", value: disabled },
              { label: "Without MFA", value: noMfa, tone: noMfa > 0 ? "warn" : "ok" },
              { label: "Signed in today", value: recentCount(d.items.map((u) => u.last_login_at)) },
            ];
          }}
        />
        <Tile
          title="Approvals"
          icon="shield"
          stat={approvals}
          main={(d) => d.pending ?? d.items.length}
          lines={(d) => [{ label: "Waiting for a decision", value: d.pending ?? d.items.length, tone: (d.pending ?? 0) > 0 ? "warn" : "ok" }]}
        />
        <Tile
          title="API tokens"
          icon="key"
          stat={tokens}
          main={(d) => d.items.filter((t) => !t.revoked_at && !t.expired && !t.disabled).length}
          lines={(d) => [
            { label: "Used today", value: recentCount(d.items.map((t) => t.last_used_at)) },
            { label: "Expiring in 7 days", value: d.items.filter((t) => !t.revoked_at && (daysUntil(t.expires_at) ?? 99) < 7 && (daysUntil(t.expires_at) ?? -1) >= 0).length },
          ]}
        />
        <Tile
          title="Tasks"
          icon="tasks"
          stat={tasks}
          main={(d) => d.filter((t) => t.state === "running" || t.state === "queued").length}
          lines={(d) => {
            const failed = recentCount(d.filter((t) => t.state === "failed").map((t) => t.updated_at ?? t.created_at));
            return [
              { label: "Running now", value: d.filter((t) => t.state === "running" || t.state === "queued").length },
              { label: "Finished today", value: recentCount(d.filter((t) => t.state === "succeeded").map((t) => t.updated_at ?? t.created_at)) },
              { label: "Failed today", value: failed, tone: failed > 0 ? "bad" : "ok" },
            ];
          }}
        />
        <Tile
          title="Updates"
          icon="restart"
          stat={updates}
          main={(d) => d.packages.find((p) => p.name === "nodal")?.version || d.packages[0]?.version || "Unknown"}
          lines={(d) => {
            const available = d.packages.some((p) => p.status === "update_available");
            const ends = d.rollback?.available ? d.rollback.window_ends_at : undefined;
            return [
              { label: "Status", value: available ? "Update available" : d.last_check ? "Up to date" : "Not checked yet", tone: available ? "warn" : "ok" },
              { label: "Last check", value: d.last_check?.finished_at ? new Date(d.last_check.finished_at).toLocaleString() : "Never" },
              { label: "Roll back until", value: ends ? new Date(ends).toLocaleString() : "Not available" },
            ];
          }}
        />
        <Tile
          title="Features"
          icon="settings"
          stat={features}
          main={(d) => `${d.items.filter((f) => f.enabled).length} on`}
          lines={(d) => [
            { label: "Installed", value: d.items.filter((f) => f.installed !== false && f.package_status !== "removed").length },
            { label: "Not installed", value: d.items.filter((f) => f.installed === false || f.package_status === "removed").length },
          ]}
        />
        <Tile
          title="Backups"
          icon="backups"
          stat={backups}
          main={(d) => recentCount(d.items.map((r) => r.started_at))}
          lines={(d) => {
            const failed = recentCount(d.items.filter((r) => r.status === "failed" || r.status === "interrupted").map((r) => r.started_at));
            const last = d.items
              .filter((r) => r.status === "succeeded" || r.status === "succeeded_with_warnings")
              .map((r) => r.finished_at ?? r.started_at)
              .sort()
              .pop();
            return [
              { label: "Runs today", value: recentCount(d.items.map((r) => r.started_at)) },
              { label: "Failed today", value: failed, tone: failed > 0 ? "bad" : "ok" },
              { label: "Last good backup", value: last ? new Date(last).toLocaleString() : "None yet", tone: last ? undefined : "warn" },
            ];
          }}
        />
        <Tile
          title="Alerts"
          icon="bell"
          stat={alerts}
          main={(d) => `${d.items.filter((a) => a.enabled).length} on`}
          lines={(d) => [
            { label: "Rules", value: d.items.length },
            { label: "Fired today", value: recentCount(d.items.map((a) => a.last_fired_at)), tone: recentCount(d.items.map((a) => a.last_fired_at)) > 0 ? "warn" : "ok" },
          ]}
        />
        <Tile
          title="Audit log"
          icon="activity"
          stat={audit}
          main={(d) => recentCount(d.items.map((e) => e.created_at))}
          lines={(d) => {
            const today = d.items.filter((e) => recentCount([e.created_at]) === 1);
            const denied = today.filter((e) => e.result !== "success" && e.result !== "ok").length;
            return [
              { label: "Actions today", value: today.length },
              { label: "Failed or denied today", value: denied, tone: denied > 0 ? "warn" : "ok" },
            ];
          }}
        />
        <Tile
          title="Certificate"
          icon="lock"
          stat={certs}
          main={(d) => {
            const days = daysUntil(d.not_after);
            return days === null ? (d.enabled ? "On" : "Off") : days < 0 ? "Expired" : `${days} days left`;
          }}
          lines={(d) => [
            { label: "Kind", value: d.mode === "acme" ? "ACME" : d.mode === "imported" ? "Imported" : d.mode === "self_signed" ? "Self-signed" : "None" },
            { label: "Name", value: d.common_name || "None" },
          ]}
        />
        <Tile
          title="License"
          icon="key"
          stat={license}
          main={(d) => d.status[0].toUpperCase() + d.status.slice(1)}
          lines={(d) => [{ label: "Last checked", value: d.last_checked ? new Date(d.last_checked).toLocaleString() : "Never" }]}
        />
      </div>
    </section>
  );
}
