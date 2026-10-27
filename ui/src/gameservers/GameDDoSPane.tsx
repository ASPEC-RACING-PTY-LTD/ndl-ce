import { useEffect, useState } from "react";
import { getDDoS, type DDoSOverview } from "../api/client";
import { Link } from "../components/Link";
import { StatusBadge } from "../components/StatusBadge";
import { REASON_LABEL } from "../pages/DDoSPage";

const num = new Intl.NumberFormat("en");

/** GameDDoSPane shows a game server's DDoS protection, set on Network > DDoS Protection. */
export function GameDDoSPane({ id }: { id: string }) {
  const [data, setData] = useState<DDoSOverview | null>(null);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    void getDDoS()
      .then(setData)
      .catch((err) => setError(err instanceof Error ? err.message : "DDoS protection could not be loaded"));
  }, [id]);
  if (error) {
    return <p className="muted">{error}</p>;
  }
  if (!data) {
    return <p className="muted">Loading</p>;
  }
  const t = data.targets.find((x) => x.kind === "gameserver" && x.ref_id === id);
  if (!t) {
    return (
      <p className="muted">
        Not protected. Turn on automatic game server protection or add this server on <Link href="/network/ddos">DDoS Protection</Link>.
      </p>
    );
  }
  const prof = data.profiles.find((p) => p.id === t.profile);
  const on = t.enabled && Boolean(t.rule);
  const top = Object.entries(t.traffic.reasons ?? {}).sort((a, b) => b[1].packets - a[1].packets)[0];
  return (
    <div className="gs-form">
      <dl className="definition-list compact">
        <div>
          <dt>Status</dt>
          <dd>
            <StatusBadge
              status={!on ? "warning" : t.traffic.under_attack ? "critical" : "ok"}
              label={!t.enabled ? "Off" : !on ? "Not protected" : t.traffic.under_attack ? "Under attack" : data.settings.mode === "monitor" ? "Monitoring" : "Protected"}
            />
          </dd>
        </div>
        <div>
          <dt>Profile</dt>
          <dd>{prof?.title ?? t.profile}</dd>
        </div>
        <div>
          <dt>Ports</dt>
          <dd>{(t.rule?.ports ?? t.ports ?? []).join(", ") || "None published"}</dd>
        </div>
        <div>
          <dt>Traffic now</dt>
          <dd>
            {num.format(t.traffic.in_pps)}/s, {num.format(t.traffic.drop_pps)}/s dropped
          </dd>
        </div>
        <div>
          <dt>Dropped</dt>
          <dd>
            {num.format(t.traffic.dropped_packets)}
            {top ? ` (mostly ${(REASON_LABEL[top[0]] ?? top[0]).toLowerCase()})` : ""}
          </dd>
        </div>
        <div>
          <dt>Banned sources</dt>
          <dd>{t.traffic.bans}</dd>
        </div>
      </dl>
      {t.warnings?.map((w) => (
        <p key={w} className="muted">
          {w}
        </p>
      ))}
      <p className="meta">
        Limits, bans and trusted networks are set on <Link href="/network/ddos">DDoS Protection</Link>.
      </p>
    </div>
  );
}
