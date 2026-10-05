import type { ReactNode } from "react";
import { getWorkload, getWorkloadGuest } from "../api/client";
import { Icon } from "../components/Icon";
import { Link } from "../components/Link";
import { StatusBadge } from "../components/StatusBadge";
import { kindLabel } from "../labels";
import { useQuery } from "../query";
import { canMutate } from "../rbac";
import { usePath } from "../router";
import { useSession } from "../session";
import { useNavDisclosure } from "./NavDisclosure";
import { buildWorkloadNav, leafFromWorkloadPath } from "./workloadNav";

/**
 * The workload's own navigation (Summary, Terminal, Files, Snapshots, ...)
 * shown above every workload page. It replaced the dedicated Workloads
 * sidebar, so the main sidebar stays visible.
 */
export function WorkloadFrame({ id, children }: { id: string; children: ReactNode }) {
  const path = usePath();
  const session = useSession();
  const mutate = canMutate(session.status === "ready" ? session.user?.roles : undefined);
  const { featureEnabled } = useNavDisclosure();
  const wl = useQuery(`workload:${id}`, () => getWorkload(id), 10000);
  const item = wl.data?.id === id ? wl.data : undefined;
  const isVM = item?.kind === "vm";
  const guest = useQuery(
    `workload-guest:${id}:${isVM ? "vm" : "none"}`,
    () => (isVM ? getWorkloadGuest(id) : Promise.resolve(null)),
    isVM ? 15000 : undefined,
  );
  const guestOk = Boolean(isVM && guest.data?.nodal_ga?.state === "ok");
  const leaf = leafFromWorkloadPath(path);
  const groups = buildWorkloadNav({
    id,
    kind: item?.kind ?? "",
    leaf,
    guestOk,
    mutate,
    dockerOn: Boolean(featureEnabled.docker),
  });
  const links = groups.flatMap((g) => g.items).filter((entry) => entry.href !== "/docker");
  return (
    <div className="wl-frame">
      <div className="wl-frame-bar">
        <Link href="/workloads" className="wl-frame-back" aria-label="Back to Workloads" title="Back to Workloads">
          <Icon name="collapse" size={14} />
        </Link>
        <div className="wl-frame-id">
          <span className="wl-frame-name">{item?.name ?? (wl.error ? "Workload" : "Loading")}</span>
          {item ? (
            <span className="wl-frame-meta">
              {kindLabel(item.kind)}
              <StatusBadge status={item.status} />
            </span>
          ) : null}
        </div>
        <nav
          className="wl-frame-tabs"
          aria-label={item?.kind === "vm" ? "VM IO" : item?.kind === "oci" ? "OCI IO" : "Workload IO"}
        >
          {links.map((entry) => (
            <Link
              key={entry.href + entry.label}
              href={entry.href}
              className="wl-frame-tab"
              aria-current={entry.current ? "page" : undefined}
            >
              <Icon name={entry.icon} size={14} />
              {entry.label}
            </Link>
          ))}
        </nav>
      </div>
      {children}
    </div>
  );
}
