import { useEffect, useState } from "react";
import { getWorkload, getWorkloadGuest, listFeatures } from "../api/client";
import type { Workload } from "../api/phase5";
import { Icon } from "../components/Icon";
import { Link } from "../components/Link";
import { honestStatus } from "../format";
import { kindLabel } from "../labels";
import { canMutate } from "../rbac";
import { navigate, usePath } from "../router";
import { useSession } from "../session";
import { buildWorkloadNav, leafFromWorkloadPath } from "./workloadNav";
import { targetIsLive, type NavTarget } from "./types";

export function WorkloadContextNav({
  id,
  target,
}: {
  id: string;
  target?: NavTarget;
}) {
  const path = usePath();
  const session = useSession();
  const roles = session.status === "ready" ? session.user?.roles : undefined;
  const mutate = canMutate(roles);
  const leaf = leafFromWorkloadPath(path);
  const [item, setItem] = useState<Workload | null>(null);
  const [guestOk, setGuestOk] = useState(false);
  const [dockerOn, setDockerOn] = useState(false);

  useEffect(() => {
    let cancelled = false;
    setGuestOk(false);
    void (async () => {
      try {
        const w = await getWorkload(id);
        if (cancelled) {
          return;
        }
        setItem(w);
        if (w.kind === "vm") {
          try {
            const guest = await getWorkloadGuest(w.id);
            if (!cancelled) {
              setGuestOk(guest.nodal_ga?.state === "ok");
            }
          } catch {
            if (!cancelled) {
              setGuestOk(false);
            }
          }
        }
      } catch {
        if (!cancelled) {
          setItem(null);
        }
      }
    })();
    void listFeatures()
      .then((feats) => {
        if (!cancelled) {
          setDockerOn(Boolean(feats.items?.some((f) => f.id === "docker" && f.enabled)));
        }
      })
      .catch(() => {
        if (!cancelled) {
          setDockerOn(false);
        }
      });
    return () => {
      cancelled = true;
    };
  }, [id]);

  const kind = item?.kind || kindFromTarget(target);
  const name = item?.name || target?.name || id;
  const status = item?.status || target?.status || "";
  const typeLabel = item?.kind ? kindLabel(item.kind) : target?.typeLabel || kindLabel(kind);
  const live = target ? targetIsLive(target) : (item?.status || "").toLowerCase() === "running";
  const groups = buildWorkloadNav({ id, kind, leaf, guestOk, mutate, dockerOn });
  const aria = kind === "vm" ? "VM IO" : kind === "oci" ? "OCI IO" : "Workload IO";

  return (
    <>
      <button className="ctx-back" type="button" aria-label="Back to Workloads" onClick={() => navigate("/workloads")}>
        <span aria-hidden="true">←</span>
        <span className="ctx-back-label">Back to Workloads</span>
      </button>
      <p className="nav-group-label ctx-area-title">Workloads</p>
      <div
        className="ctx-item is-current"
        data-nav-id={`workload:${id}`}
        title={`${name} · ${typeLabel} · ${status || "unknown"}`}
      >
        <span className={"ctx-dot" + (live ? " is-live" : "")} aria-hidden="true">
          {live ? "●" : "○"}
        </span>
        <span className="ctx-item-label">{name}</span>
      </div>
      <p className="ctx-selected-meta">
        {typeLabel} · {honestStatus(status)}
      </p>
      <nav className="wl-ctx-nav" aria-label={aria}>
        {groups.map((group) => (
          <div className="nav-group" key={group.label}>
            <p className="nav-group-label">{group.label}</p>
            {group.items.map((entry) => (
              <Link
                key={entry.href + entry.label}
                href={entry.href}
                className="nav-link"
                aria-label={entry.label}
                title={entry.label}
                aria-current={entry.current ? "page" : undefined}
              >
                <Icon name={entry.icon} />
                <span className="nav-link-label">{entry.label}</span>
              </Link>
            ))}
          </div>
        ))}
      </nav>
    </>
  );
}

function kindFromTarget(target?: NavTarget): string {
  if (!target) {
    return "";
  }
  if (target.group === "vm") {
    return "vm";
  }
  if (target.group === "system-container") {
    return "system-container";
  }
  if (target.group === "application") {
    return "oci";
  }
  return "";
}
