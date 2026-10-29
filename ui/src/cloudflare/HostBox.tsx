import { memo, type MouseEvent as ReactMouseEvent, type ReactNode } from "react";
import type { CfDNSEntry, CfRoute } from "../api/client";
import type { ContextItem } from "../components/ContextMenu";
import { Icon } from "../components/Icon";
import { Link } from "../components/Link";
import { StatusBadge } from "../components/StatusBadge";

export function dnsBadge(r: CfRoute): { status: string; label: string; title?: string } {
  switch (r.dns?.action) {
    case "ok":
      return { status: "healthy", label: "DNS ok" };
    case "create":
      return { status: "critical", label: "No DNS record" };
    case "proxy":
      return { status: "warning", label: "Not proxied" };
    case "retarget":
      return { status: "warning", label: "Points elsewhere" };
    case "conflict":
      return { status: "critical", label: "DNS conflict" };
  }
  if (r.zone_id) {
    return { status: "unknown", label: "Not readable", title: "No saved token may read DNS in this zone. See Manage accounts > Permissions." };
  }
  return { status: "unknown", label: "No zone", title: "The domain is not in a zone a saved token can see." };
}

export function tunnelTone(status: string): string {
  return status === "healthy" ? "healthy" : status === "degraded" ? "degraded" : status === "inactive" ? "unknown" : "critical";
}

export function dnsEntryBadge(d: CfDNSEntry): { status: string; label: string } {
  return d.status === "routed" ? { status: "healthy", label: "Routed" } : d.status === "no-route" ? { status: "warning", label: "No route" } : { status: "critical", label: "Unknown tunnel" };
}

/** boxTone is the one-dot summary of a hostname: its DNS and its tunnel. */
function boxTone(r: CfRoute): "is-ok" | "is-warn" | "is-bad" | "" {
  if (!r.enabled) {
    return "";
  }
  const dns = dnsBadge(r).status;
  const tunnel = tunnelTone(r.tunnel_status);
  if (dns === "critical" || tunnel === "critical") {
    return "is-bad";
  }
  if (dns === "warning" || tunnel === "degraded" || r.ip_changed) {
    return "is-warn";
  }
  return dns === "healthy" ? "is-ok" : "";
}

type MenuOpener = (event: ReactMouseEvent, title: string, items: ContextItem[]) => void;

/** ItemButtons draws a menu's items as a row of buttons. */
function ItemButtons({ items, label }: { items: ContextItem[]; label: string }) {
  return (
    <div className="btn-row cf-box-actions" role="group" aria-label={label}>
      {items.map((i, n) =>
        i === "sep" ? null : (
          <button
            key={`${n}-${i.label}`}
            type="button"
            className={"btn btn-sm " + (i.danger ? "btn-danger" : "btn-secondary")}
            disabled={i.disabled}
            onClick={i.onClick}
          >
            {i.label}
          </button>
        ),
      )}
    </div>
  );
}

function RecordList({ records }: { records: CfDNSEntry[] }) {
  return (
    <ul className="plain-list cf-box-records" aria-label="DNS records">
      {records.map((d) => {
        const b = dnsEntryBadge(d);
        return (
          <li key={d.record.id}>
            <StatusBadge status={b.status} label={b.label} /> <code>{d.record.type}</code> {d.record.name}
            <span className="meta"> to </span>
            <code>{d.record.content}</code>
            {d.record.proxied ? <span className="tag-inline">proxied</span> : <span className="tag-inline">not proxied</span>}
            {d.managed ? <span className="tag-inline">No-dal</span> : null}
            {d.tunnel_name ? <span className="meta"> · {d.tunnel_name}</span> : null}
          </li>
        );
      })}
    </ul>
  );
}

type HostProps = {
  r: CfRoute;
  records: CfDNSEntry[];
  expanded: boolean;
  selected: boolean;
  canManage: boolean;
  onSelect: (key: string) => void;
  onExpand: (key: string) => void;
  itemsFor: (r: CfRoute) => ContextItem[];
  onMenu: MenuOpener;
};

/** HostBox is one public hostname as a small box; opened, it manages the hostname and its DNS in place. */
export const HostBox = memo(function HostBox({ r, records, expanded, selected, canManage, onSelect, onExpand, itemsFor, onMenu }: HostProps) {
  const dns = dnsBadge(r);
  const tone = boxTone(r);
  const name = r.hostname + (r.path ?? "");
  let detail: ReactNode = null;
  if (expanded) {
    const fields: { label: string; value: ReactNode }[] = [
      {
        label: "Goes to",
        value: (
          <>
            <code>{r.service}</code>
            {r.ip_changed ? (
              <span className="tag-inline" title={`The workload now has ${r.workload?.current_ip}`}>
                IP changed
              </span>
            ) : null}
          </>
        ),
      },
      {
        label: "Tunnel",
        value: (
          <>
            <StatusBadge status={tunnelTone(r.tunnel_status)} label={r.tunnel_name} />
            {!r.editable ? <span className="meta"> config file</span> : null}
          </>
        ),
      },
      { label: "Account", value: r.account_name },
      { label: "Zone", value: r.zone_name ?? <span className="meta">-</span> },
      { label: "Workload", value: r.workload ? <Link href={`/workloads/${r.workload.id}`}>{r.workload.name}</Link> : <span className="meta">-</span> },
      { label: "State", value: r.enabled ? "On" : "Off" },
    ];
    detail = (
      <div className="cf-box-detail">
        <dl className="cf-box-fields">
          {fields.map((f) => (
            <div key={f.label}>
              <dt>{f.label}</dt>
              <dd>{f.value}</dd>
            </div>
          ))}
        </dl>
        <section className="cf-box-dns" aria-label={`DNS for ${r.hostname}`}>
          <h4>
            DNS <StatusBadge status={dns.status} label={dns.label} />
          </h4>
          {r.dns?.reason || dns.title ? <p className="meta">{r.dns?.reason ?? dns.title}</p> : null}
          {records.length ? <RecordList records={records} /> : <p className="meta">{r.dns ? "No tunnel DNS record found for this name." : "DNS records here can't be read."}</p>}
        </section>
        <ItemButtons items={itemsFor(r)} label={`${r.hostname} actions`} />
      </div>
    );
  }
  return (
    <article
      className={"cf-box" + (expanded ? " is-open" : "") + (r.enabled ? "" : " is-muted") + (selected ? " is-selected" : "")}
      aria-label={name}
      onContextMenu={(e) => onMenu(e, name, itemsFor(r))}
    >
      <div className="cf-box-head">
        {canManage ? <input type="checkbox" aria-label={`Select ${name}`} checked={selected} onChange={() => onSelect(r.key)} /> : null}
        <button type="button" className="cf-box-toggle" aria-expanded={expanded} onClick={() => onExpand(r.key)}>
          <span className={"dot " + tone} aria-hidden="true" />
          <span className="cf-box-name" title={name}>
            {r.hostname}
            {r.path ? <span className="meta">{r.path}</span> : null}
          </span>
          {!r.enabled ? <span className="tag-inline">off</span> : null}
          <Icon name="chevron-down" size={14} className={expanded ? undefined : "is-collapsed"} />
        </button>
      </div>
      {expanded ? (
        detail
      ) : (
        <p className="cf-box-sub" title={r.dns?.reason ?? dns.title}>
          <code>{r.service}</code>
          {dns.status !== "healthy" ? <span className="meta"> · {dns.label}</span> : null}
        </p>
      )}
    </article>
  );
});

type DnsProps = {
  d: CfDNSEntry;
  expanded: boolean;
  multiAccount: boolean;
  onExpand: (key: string) => void;
  onMenu: MenuOpener;
};

/** DnsBox is a tunnel DNS record no hostname uses. */
export const DnsBox = memo(function DnsBox({ d, expanded, multiAccount, onExpand, onMenu }: DnsProps) {
  const b = dnsEntryBadge(d);
  const key = "dns:" + d.record.id;
  const items: ContextItem[] = [{ label: "Copy name", onClick: () => void navigator.clipboard?.writeText(d.record.name) }];
  return (
    <article className={"cf-box is-dns" + (expanded ? " is-open" : "")} aria-label={`DNS record ${d.record.name}`} onContextMenu={(e) => onMenu(e, d.record.name, items)}>
      <div className="cf-box-head">
        <button type="button" className="cf-box-toggle" aria-expanded={expanded} onClick={() => onExpand(key)}>
          <span className={"dot " + (d.status === "no-route" ? "is-warn" : "is-bad")} aria-hidden="true" />
          <span className="cf-box-name" title={d.record.name}>
            {d.record.name}
          </span>
          <Icon name="chevron-down" size={14} className={expanded ? undefined : "is-collapsed"} />
        </button>
      </div>
      {expanded ? (
        <div className="cf-box-detail">
          <section className="cf-box-dns" aria-label={`DNS for ${d.record.name}`}>
            <h4>
              DNS <StatusBadge status={b.status} label={b.label} />
            </h4>
            <p className="meta">
              {d.status === "no-route"
                ? "This record sends the name to a tunnel that has no route for it, so visitors get a 404."
                : "This record points at a tunnel no saved account knows, so visitors get error 1016."}{" "}
              Remove it in Cloudflare DNS once it is no longer needed, or add a hostname for it.
            </p>
            <p className="meta">
              Zone {d.zone_name}
              {multiAccount ? ` · ${d.account_name}` : ""} · tunnel {d.tunnel_name || <code>{d.tunnel_id.slice(0, 8)}</code>}
            </p>
            <RecordList records={[d]} />
          </section>
          <ItemButtons items={items} label={`${d.record.name} actions`} />
        </div>
      ) : (
        <p className="cf-box-sub">
          <span className="meta">DNS only · {b.label}</span>
        </p>
      )}
    </article>
  );
});
