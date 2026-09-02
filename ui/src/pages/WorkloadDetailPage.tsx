import { useEffect, useState } from "react";
import { getPool, getWorkload, listPools, listVolumes, patchWorkload, workloadAction } from "../api/client";
import type { StoragePool } from "../api/phase3";
import type { Workload } from "../api/phase5";
import { ConfirmDialog } from "../components/ConfirmDialog";
import { ErrorState, LoadingState } from "../components/EmptyState";
import { Field } from "../components/Field";
import { Icon } from "../components/Icon";
import { Link } from "../components/Link";
import { PageHeader } from "../components/PageHeader";
import { StatusBadge } from "../components/StatusBadge";
import { UxModeToggle } from "../components/UxModeToggle";
import { formatBytes } from "../format";
import { kindLabel, osLabel } from "../labels";
import { canMutate, mutateHint } from "../rbac";
import { currentPath, navigate } from "../router";
import { useSession } from "../session";
import { getUxLevelDefault, isAdvanced, isExpert, type UxLevel } from "../ux-mode";

function workloadIDFromPath(): string {
  const parts = currentPath().split("/").filter(Boolean);
  return parts[0] === "workloads" ? (parts[1] ?? "") : "";
}

function workloadTab(): "summary" | "settings" | "snapshots" {
  if (currentPath().endsWith("/settings")) {
    return "settings";
  }
  if (currentPath().endsWith("/snapshots")) {
    return "snapshots";
  }
  return "summary";
}

export function WorkloadDetailPage() {
  const session = useSession();
  const roles = session.status === "ready" ? session.user?.roles : undefined;
  const mutate = canMutate(roles);
  const id = workloadIDFromPath();
  const tab = workloadTab();
  const [item, setItem] = useState<Workload | null>(null);
  const [pool, setPool] = useState<StoragePool | null>(null);
  const [cpus, setCpus] = useState("1");
  const [memoryMiB, setMemoryMiB] = useState("256");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [confirm, setConfirm] = useState<"delete" | "clone" | null>(null);
  const [cloneName, setCloneName] = useState("");
  const [mode, setMode] = useState<UxLevel>(getUxLevelDefault);

  async function reload() {
    const w = await getWorkload(id);
    setItem(w);
    setCpus(String(w.cpus ?? 1));
    setMemoryMiB(String(Math.round((w.memory_bytes ?? 256 * 1024 * 1024) / (1024 * 1024))));
    const [pools, volumes] = await Promise.all([listPools(), listVolumes().catch(() => [])]);
    const diskIds = new Set((w.disks ?? []).map((disk) => disk.volume_id));
    const matched = volumes.find((volume) => diskIds.has(volume.id));
    const listed = pools.items ?? [];
    const chosen = listed.find((item) => item.id === matched?.pool_id) ?? listed[0];
    if (chosen) {
      const detailed = await getPool(chosen.id).catch(() => chosen);
      setPool(detailed);
    }
  }

  useEffect(() => {
    let cancelled = false;
    void reload().catch((err) => {
      if (!cancelled) {
        setError(err instanceof Error ? err.message : "Unavailable");
      }
    });
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id]);

  async function onAction(action: "start" | "stop" | "restart" | "delete" | "clone") {
    setBusy(true);
    setError(null);
    try {
      const next = await workloadAction(id, action, action === "clone" ? { name: cloneName || undefined } : undefined);
      if (action === "clone") {
        navigate(`/workloads/${next.id}`);
        return;
      }
      if (action === "delete") {
        navigate("/workloads");
        return;
      }
      await reload();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Action failed");
    } finally {
      setBusy(false);
      setConfirm(null);
    }
  }

  async function onSave() {
    setBusy(true);
    setError(null);
    try {
      await patchWorkload(id, {
        cpus: Number(cpus) || 1,
        memory_bytes: (Number(memoryMiB) || 256) * 1024 * 1024,
      });
      await reload();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Update failed");
    } finally {
      setBusy(false);
    }
  }

  if (!item) {
    return (
      <section className="page">
        <h1>Workload</h1>
        {error ? <ErrorState>{error}</ErrorState> : <LoadingState />}
      </section>
    );
  }

  const ipv4 = item.nics?.[0]?.ipv4;
  const mac = item.nics?.[0]?.mac;
  const isCT = item.kind === "system-container";
  const snapshotsSupported = Boolean(pool?.capabilities?.snapshots);

  return (
    <section className="page" aria-labelledby="workload-heading">
      <PageHeader
        id="workload-heading"
        title={item.name}
        kicker={
          <>
            {kindLabel(item.kind)} · <StatusBadge status={item.status} />
          </>
        }
        actions={
          <div className="btn-row is-flush">
            <button
              className="btn btn-sm btn-secondary"
              type="button"
              disabled={!mutate || busy || item.status === "running"}
              onClick={() => void onAction("start")}
            >
              <Icon name="start" size={14} />
              Start
            </button>
            <button
              className="btn btn-sm btn-secondary"
              type="button"
              disabled={!mutate || busy || item.status === "stopped"}
              onClick={() => void onAction("stop")}
            >
              <Icon name="stop" size={14} />
              Stop
            </button>
            <button className="btn btn-sm btn-ghost" type="button" disabled={!mutate || busy} onClick={() => void onAction("restart")}>
              <Icon name="restart" size={14} />
              Restart
            </button>
            <button className="btn btn-sm btn-ghost" type="button" disabled={!mutate || busy} onClick={() => setConfirm("clone")}>
              Clone
            </button>
            <button
              className="btn btn-sm btn-danger"
              type="button"
              disabled={!mutate || busy}
              onClick={() => setConfirm("delete")}
            >
              <Icon name="delete" size={14} />
              Delete
            </button>
            {!mutate ? <p className="field-hint">{mutateHint(roles)}</p> : null}
          </div>
        }
      />
      {isCT ? (
        <nav className="subnav" aria-label="Workload sections">
          <Link href={`/workloads/${item.id}`} aria-current={tab === "summary" ? "page" : undefined}>
            Summary
          </Link>
          <Link href={`/workloads/${item.id}/terminal`}>
            <Icon name="terminal" size={14} />
            Terminal
          </Link>
          <Link href={`/workloads/${item.id}/files`}>
            <Icon name="files" size={14} />
            Files
          </Link>
          <Link href={`/workloads/${item.id}/snapshots`} aria-current={tab === "snapshots" ? "page" : undefined}>
            <Icon name="snapshots" size={14} />
            Snapshots
          </Link>
          {mutate ? (
            <Link href={`/workloads/${item.id}/settings`} aria-current={tab === "settings" ? "page" : undefined}>
              <Icon name="settings" size={14} />
              Settings
            </Link>
          ) : null}
        </nav>
      ) : (
        <p className="banner banner-warn" role="status">
          Console and files are not available for this workload type.
        </p>
      )}
      {error ? <ErrorState>{error}</ErrorState> : null}
      {tab === "summary" || !isCT ? (
        <section className="section">
          <h2>Summary</h2>
          <dl className="definition-list compact">
            <div>
              <dt>Status</dt>
              <dd>
                <StatusBadge status={item.status} />
              </dd>
            </div>
            <div>
              <dt>Reason</dt>
              <dd>{item.reason || "None"}</dd>
            </div>
            <div>
              <dt>Image</dt>
              <dd>
                {osLabel(item.image_pin)}
                {item.image_verified ? " (verified)" : " (not verified)"}
              </dd>
            </div>
            <div>
              <dt>Memory</dt>
              <dd>{formatBytes(item.memory_bytes)}</dd>
            </div>
            <div>
              <dt>IPv4</dt>
              <dd>{ipv4 || "Not reported"}</dd>
            </div>
            <div>
              <dt>MAC</dt>
              <dd>{mac || "Not reported"}</dd>
            </div>
            <div>
              <dt>Privileged</dt>
              <dd>{item.privileged ? "Yes" : "No"}</dd>
            </div>
          </dl>
        </section>
      ) : null}
      {tab === "snapshots" && isCT ? (
        <section className="section stack">
          <h2>Snapshots</h2>
          {snapshotsSupported ? (
            <p>No snapshots yet.</p>
          ) : (
            <p>
              Snapshots are supported for system containers, but this container uses{" "}
              {pool?.name ? `${pool.name} (` : ""}
              Directory storage
              {pool?.name ? ")" : ""}. Move or recreate it on a snapshot-capable pool such as ZFS to
              enable snapshots. Snapshot actions are not available on this pool.
            </p>
          )}
        </section>
      ) : null}
      {tab === "settings" && mutate ? (
        <section className="section form form-narrow">
          <PageHeader
            id="wl-settings-heading"
            title="Settings"
            actions={<UxModeToggle value={mode} onChange={setMode} />}
          />
          <div className="field-row">
            <Field id="wl-cpus" label="CPUs" type="number" min={1} value={cpus} onChange={(e) => setCpus(e.target.value)} />
            <Field
              id="wl-mem"
              label="Memory (MiB)"
              type="number"
              min={64}
              value={memoryMiB}
              onChange={(e) => setMemoryMiB(e.target.value)}
            />
          </div>
          {isAdvanced(mode) ? (
            <dl className="definition-list">
              <div>
                <dt>PID</dt>
                <dd>{item.pid ?? "Not reported"}</dd>
              </div>
              <div>
                <dt>Service</dt>
                <dd>{item.unit_active ? "Active" : "Inactive"}</dd>
              </div>
              <div>
                <dt>Migrate ready</dt>
                <dd>{item.migrate_ready ? "Yes" : "No"}</dd>
              </div>
            </dl>
          ) : null}
          {isExpert(mode) ? (
            <p className="picker-meta">
              ID <code>{item.id}</code>
              {item.image_pin ? ` · ${item.image_pin}` : ""}
            </p>
          ) : null}
          <div className="btn-row">
            <button className="btn btn-primary" type="button" disabled={busy} onClick={() => void onSave()}>
              Save
            </button>
          </div>
        </section>
      ) : null}
      <ConfirmDialog
        open={confirm === "delete"}
        title="Delete workload"
        confirmLabel="Delete"
        danger
        onClose={() => setConfirm(null)}
        onConfirm={() => void onAction("delete")}
      >
        <p>Delete {item.name}? This cannot be undone.</p>
      </ConfirmDialog>
      <ConfirmDialog
        open={confirm === "clone"}
        title="Clone workload"
        confirmLabel="Clone"
        onClose={() => setConfirm(null)}
        onConfirm={() => void onAction("clone")}
      >
        <Field
          id="clone-name"
          label="Name (optional)"
          value={cloneName}
          onChange={(e) => setCloneName(e.target.value)}
        />
      </ConfirmDialog>
    </section>
  );
}
