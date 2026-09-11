import { useEffect, useState } from "react";
import { getWorkload, getWorkloadGuest, listFeatures } from "../api/client";
import type { WorkloadGuest } from "../api/client";
import type { Workload } from "../api/phase5";
import { Icon } from "./Icon";
import { Link } from "./Link";
import { canMutate } from "../rbac";
import { currentPath } from "../router";
import { useSession } from "../session";

function leafOf(path: string): string {
  const parts = path.split("/").filter(Boolean);
  return parts[2] || "summary";
}

export function WorkloadSubnav({
  id,
  kind: kindProp,
  guestOk: guestOkProp,
}: {
  id: string;
  kind?: string;
  guestOk?: boolean;
}) {
  const session = useSession();
  const roles = session.status === "ready" ? session.user?.roles : undefined;
  const mutate = canMutate(roles);
  const path = currentPath();
  const leaf = leafOf(path);
  const [item, setItem] = useState<Workload | null>(null);
  const [guest, setGuest] = useState<WorkloadGuest | null>(null);
  const [dockerOn, setDockerOn] = useState(false);

  useEffect(() => {
    let cancelled = false;
    if (!kindProp) {
      void (async () => {
        try {
          const w = await getWorkload(id);
          if (cancelled) {
            return;
          }
          setItem(w);
          if (w.kind === "vm") {
            try {
              setGuest(await getWorkloadGuest(w.id));
            } catch {
              if (!cancelled) {
                setGuest(null);
              }
            }
          }
        } catch {
          if (!cancelled) {
            setItem(null);
          }
        }
      })();
    }
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
  }, [id, kindProp]);

  const kind = kindProp || item?.kind || "";
  const guestOk = guestOkProp ?? guest?.nodal_ga?.state === "ok";
  const aria = kind === "vm" ? "VM IO" : kind === "oci" ? "OCI IO" : "Workload IO";
  const isSummary = leaf === "summary";

  return (
    <nav className="subnav workload-subnav" aria-label={aria}>
      <span className="subnav-group">
        <Link href={`/workloads/${id}`} aria-current={isSummary ? "page" : undefined}>
          Summary
        </Link>
      </span>
      {kind === "system-container" ? (
        <span className="subnav-group">
          <span className="subnav-label">Access</span>
          <Link href={`/workloads/${id}/terminal`} aria-current={leaf === "terminal" ? "page" : undefined}>
            <Icon name="terminal" size={14} />
            Terminal
          </Link>
          <Link href={`/workloads/${id}/files`} aria-current={leaf === "files" ? "page" : undefined}>
            <Icon name="files" size={14} />
            Files
          </Link>
        </span>
      ) : null}
      {kind === "vm" ? (
        <span className="subnav-group">
          <span className="subnav-label">Access</span>
          <Link href={`/workloads/${id}/console`} aria-current={leaf === "console" ? "page" : undefined}>
            Console
          </Link>
          {guestOk ? (
            <>
              <Link href={`/workloads/${id}/terminal`} aria-current={leaf === "terminal" ? "page" : undefined}>
                Terminal
              </Link>
              <Link href={`/workloads/${id}/files`} aria-current={leaf === "files" ? "page" : undefined}>
                Files
              </Link>
            </>
          ) : (
            <>
              <span>Terminal (unavailable)</span>
              <span>Files (unavailable)</span>
            </>
          )}
        </span>
      ) : null}
      {kind === "vm" ? (
        <span className="subnav-group">
          <span className="subnav-label">Machine</span>
          <Link href={`/workloads/${id}/machine`} aria-current={leaf === "machine" ? "page" : undefined}>
            USB
          </Link>
          {mutate ? (
            <Link href={`/workloads/${id}/gpus`} aria-current={leaf === "gpus" ? "page" : undefined}>
              GPUs
            </Link>
          ) : null}
        </span>
      ) : mutate && kind ? (
        <span className="subnav-group">
          <span className="subnav-label">Machine</span>
          <Link href={`/workloads/${id}/gpus`} aria-current={leaf === "gpus" ? "page" : undefined}>
            GPUs
          </Link>
        </span>
      ) : null}
      {kind === "system-container" || kind === "vm" ? (
        <span className="subnav-group">
          <span className="subnav-label">Protection</span>
          <Link href={`/workloads/${id}/snapshots`} aria-current={leaf === "snapshots" ? "page" : undefined}>
            <Icon name="snapshots" size={14} />
            Snapshots
          </Link>
        </span>
      ) : null}
      {mutate ? (
        <span className="subnav-group">
          <span className="subnav-label">Operations</span>
          <Link href={`/workloads/${id}/operations`} aria-current={leaf === "operations" ? "page" : undefined}>
            Clone / Migrate
          </Link>
        </span>
      ) : null}
      {dockerOn ? (
        <span className="subnav-group">
          <span className="subnav-label">Integrations</span>
          <Link href="/docker">Docker</Link>
        </span>
      ) : null}
    </nav>
  );
}
