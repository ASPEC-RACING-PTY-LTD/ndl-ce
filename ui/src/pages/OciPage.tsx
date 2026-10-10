import { useEffect, useMemo, useState, type DragEvent, type MouseEvent as ReactMouseEvent } from "react";
import {
  addToOCIGroup,
  applyStack,
  createOCIGroup,
  deleteStack,
  getWorkload,
  importStack,
  listNetworks,
  listPools,
  listStacks,
  listWorkloads,
  ociGroupPower,
  removeFromOCIGroup,
  renameStack,
  workloadAction,
  type Stack,
  type StackMember,
} from "../api/client";
import type { Network } from "../api/phase4";
import type { StoragePool } from "../api/phase3";
import type { Workload } from "../api/phase5";
import { ActionMenu } from "../components/ActionMenu";
import { ConfirmDialog } from "../components/ConfirmDialog";
import { ContextMenu, useContextMenu, type ContextItem } from "../components/ContextMenu";
import { EmptyState, LoadingState } from "../components/EmptyState";
import { ErrorNotice } from "../components/ErrorNotice";
import { Link } from "../components/Link";
import { OciContainerForm } from "../components/OciContainerForm";
import { PageHeader } from "../components/PageHeader";
import { StatusBadge } from "../components/StatusBadge";
import { Dialog } from "../ui/Dialog";
import { usePoll } from "../query";
import { navigate } from "../router";
import { canMutate } from "../rbac";
import { useSession } from "../session";

const SAMPLE_COMPOSE = `services:
  web:
    image: nginx:alpine
    ports:
      - "8080:80"
    volumes:
      - webdata:/usr/share/nginx/html
volumes:
  webdata:
`;

type Membership = { groupId: string; memberId: string };

type SpecView = {
  network_mode?: string;
  ipv4_address?: string;
  ports?: { container_port: number; host_port?: number; protocol?: string }[];
};

function netSummary(w: Workload): string {
  const spec = (w.spec ?? {}) as SpecView;
  const ip = w.nics?.[0]?.ipv4 || (spec.ipv4_address ? spec.ipv4_address.split("/")[0] : "");
  const ports = (spec.ports ?? []).map((p) => (p.host_port && p.host_port !== p.container_port ? `${p.host_port}→${p.container_port}` : String(p.container_port)));
  const bits: string[] = [];
  if (spec.network_mode === "host") {
    bits.push("Host network");
  } else if (spec.network_mode === "bridge") {
    bits.push(ip || "DHCP");
  } else {
    bits.push("No network");
  }
  if (ports.length) {
    bits.push(`ports ${ports.slice(0, 4).join(", ")}${ports.length > 4 ? "…" : ""}`);
  }
  return bits.join(" · ");
}

function tone(status?: string): string {
  const s = (status || "").toLowerCase();
  if (s === "running") {
    return "running";
  }
  if (s === "stopped") {
    return "stopped";
  }
  if (s === "failed" || s === "unavailable") {
    return "critical";
  }
  return s || "unknown";
}

/** OciPage manages OCI containers and the groups they run in. */
export function OciPage() {
  const session = useSession();
  const roles = session.status === "ready" ? session.user?.roles : undefined;
  const mutate = canMutate(roles);
  const ctx = useContextMenu();
  const [workloads, setWorkloads] = useState<Workload[] | null>(null);
  const [groups, setGroups] = useState<Stack[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [query, setQuery] = useState("");
  const [creating, setCreating] = useState<{ groupId: string } | null>(null);
  const [editing, setEditing] = useState<Workload | null>(null);
  const [importing, setImporting] = useState(false);
  const [newGroup, setNewGroup] = useState(false);
  const [groupName, setGroupName] = useState("");
  const [renaming, setRenaming] = useState<Stack | null>(null);
  const [deleting, setDeleting] = useState<Stack | null>(null);
  const [deleteContainers, setDeleteContainers] = useState(false);
  const [moving, setMoving] = useState<Workload | null>(null);
  const [moveTo, setMoveTo] = useState("");
  const [adding, setAdding] = useState<Stack | null>(null);
  const [addPick, setAddPick] = useState("");
  const [dropTarget, setDropTarget] = useState<string | null>(null);

  async function reload() {
    try {
      const [w, g] = await Promise.all([listWorkloads(), listStacks(true)]);
      setWorkloads((w.items ?? []).filter((x) => x.kind === "oci"));
      setGroups(g.items ?? []);
      setError(null);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Unavailable");
      setWorkloads((cur) => cur ?? []);
    }
  }

  useEffect(() => {
    void reload();
  }, []);
  usePoll(() => reload(), 8000);

  const membership = useMemo(() => {
    const m = new Map<string, Membership>();
    for (const g of groups) {
      for (const mem of g.members ?? []) {
        if (mem.workload_id) {
          m.set(mem.workload_id, { groupId: g.id, memberId: mem.id });
        }
      }
    }
    return m;
  }, [groups]);

  const byId = useMemo(() => new Map((workloads ?? []).map((w) => [w.id, w])), [workloads]);
  const q = query.trim().toLowerCase();
  const hit = (w: Workload) => !q || `${w.name} ${w.image_pin ?? ""} ${netSummary(w)}`.toLowerCase().includes(q);
  const ungrouped = (workloads ?? []).filter((w) => !membership.has(w.id) && hit(w));

  async function act(label: string, fn: () => Promise<unknown>, done?: string) {
    setBusy(label);
    setError(null);
    try {
      await fn();
      if (done) {
        setNotice(done);
      }
      await reload();
    } catch (err) {
      setError(err instanceof Error ? err.message : `${label} failed`);
    } finally {
      setBusy(null);
    }
  }

  function moveContainer(w: Workload, groupId: string) {
    const cur = membership.get(w.id);
    if ((cur?.groupId ?? "") === groupId) {
      return;
    }
    if (!groupId) {
      if (cur) {
        void act(`Removing ${w.name} from its group`, () => removeFromOCIGroup(cur.groupId, cur.memberId), `${w.name} is no longer in a group. It keeps running.`);
      }
      return;
    }
    const g = groups.find((x) => x.id === groupId);
    void act(`Moving ${w.name}`, () => addToOCIGroup(groupId, w.id), `${w.name} moved to ${g?.name ?? "the group"}.`);
  }

  async function openEditor(w: Workload) {
    try {
      setEditing(await getWorkload(w.id));
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not load the container");
    }
  }

  function containerItems(w: Workload): ContextItem[] {
    const mem = membership.get(w.id);
    const running = (w.status || "").toLowerCase() === "running";
    const items: ContextItem[] = [{ label: "Open", onClick: () => navigate(`/workloads/${encodeURIComponent(w.id)}`) }];
    if (mutate) {
      items.push(
        { label: "Edit settings", onClick: () => void openEditor(w) },
        "sep",
        { label: "Start", onClick: () => void act(`Starting ${w.name}`, () => workloadAction(w.id, "start")), disabled: running },
        { label: "Stop", onClick: () => void act(`Stopping ${w.name}`, () => workloadAction(w.id, "stop")), disabled: !running },
        { label: "Restart", onClick: () => void act(`Restarting ${w.name}`, () => workloadAction(w.id, "restart")) },
        "sep",
        {
          label: "Move to group…",
          onClick: () => {
            setMoveTo(mem?.groupId ?? "");
            setMoving(w);
          },
        },
      );
      if (mem) {
        items.push({ label: "Remove from group", onClick: () => moveContainer(w, "") });
      }
      items.push("sep", {
        label: "Delete container",
        danger: true,
        onClick: () => {
          if (window.confirm(`Delete ${w.name}? The container is removed. Volumes are kept and can be attached again.`)) {
            void act(`Deleting ${w.name}`, () => workloadAction(w.id, "delete"), `${w.name} was deleted.`);
          }
        },
      });
    }
    return items;
  }

  function groupItems(g: Stack): ContextItem[] {
    const pending = (g.members ?? []).some((m) => !m.workload_id);
    const items: ContextItem[] = [];
    if (mutate) {
      items.push(
        { label: "Start all", onClick: () => void power(g, "start") },
        { label: "Stop all", onClick: () => void power(g, "stop") },
        { label: "Restart all", onClick: () => void power(g, "restart") },
      );
      if (pending) {
        items.push({ label: "Deploy containers", onClick: () => void act(`Deploying ${g.name}`, () => applyStack(g.id), `${g.name} deployed.`) });
      }
      items.push(
        "sep",
        { label: "New container in group", onClick: () => setCreating({ groupId: g.id }) },
        {
          label: "Add existing container…",
          onClick: () => {
            setAddPick("");
            setAdding(g);
          },
        },
        "sep",
        {
          label: "Rename group",
          onClick: () => {
            setGroupName(g.name);
            setRenaming(g);
          },
        },
        {
          label: "Delete group",
          danger: true,
          onClick: () => {
            setDeleteContainers(false);
            setDeleting(g);
          },
        },
      );
    }
    return items;
  }

  async function power(g: Stack, action: "start" | "stop" | "restart") {
    setBusy(`${g.name}: ${action}`);
    setError(null);
    try {
      const res = await ociGroupPower(g.id, action);
      if (res.failed) {
        setError(
          `${res.failed} container(s) did not ${action}: ` +
            res.results
              .filter((r) => !r.ok)
              .map((r) => `${r.name} (${r.error || "failed"})`)
              .join(", "),
        );
      } else {
        setNotice(`${g.name}: ${action} sent to ${res.results.length} container(s).`);
      }
      await reload();
    } catch (err) {
      setError(err instanceof Error ? err.message : `${action} failed`);
    } finally {
      setBusy(null);
    }
  }

  function dropProps(groupId: string) {
    if (!mutate) {
      return {};
    }
    return {
      onDragOver: (e: DragEvent) => {
        if (e.dataTransfer.types.includes("application/x-ndl-oci")) {
          e.preventDefault();
          e.dataTransfer.dropEffect = "move";
          setDropTarget(groupId || "ungrouped");
        }
      },
      onDragLeave: () => setDropTarget(null),
      onDrop: (e: DragEvent) => {
        e.preventDefault();
        setDropTarget(null);
        const w = byId.get(e.dataTransfer.getData("application/x-ndl-oci"));
        if (w) {
          moveContainer(w, groupId);
        }
      },
    };
  }

  function row(w: Workload) {
    return (
      <li
        key={w.id}
        className="docker-row oci-row-item"
        draggable={mutate}
        onDragStart={(e) => {
          e.dataTransfer.setData("application/x-ndl-oci", w.id);
          e.dataTransfer.effectAllowed = "move";
        }}
        onContextMenu={(e: ReactMouseEvent) => ctx.openAt(e, w.name, containerItems(w))}
      >
        <Link className="docker-row-name" href={`/workloads/${encodeURIComponent(w.id)}`}>
          <strong>{w.name}</strong>
        </Link>
        <span className="docker-row-status">
          <StatusBadge status={tone(w.status)} label={w.status || "unknown"} />
        </span>
        <span className="meta docker-row-meta" title={w.image_pin}>
          {w.image_pin} · {netSummary(w)}
        </span>
        <ActionMenu label={`${w.name} actions`} items={containerItems(w).filter((i): i is Exclude<ContextItem, "sep"> => i !== "sep" && !i.disabled)} />
      </li>
    );
  }

  function pendingRow(g: Stack, m: StackMember) {
    return (
      <li key={m.id} className="docker-row is-ignored">
        <span className="docker-row-name">
          <strong>{m.service_name}</strong>
        </span>
        <span className="docker-row-status">
          <StatusBadge status={m.status === "failed" ? "critical" : "unknown"} label={m.status === "failed" ? "Failed" : "Not deployed"} />
        </span>
        <span className="meta docker-row-meta">{m.reason || (typeof m.desired?.image_pin === "string" ? m.desired.image_pin : "")}</span>
        {mutate ? (
          <ActionMenu
            label={`${m.service_name} actions`}
            items={[
              { label: "Deploy group", onClick: () => void act(`Deploying ${g.name}`, () => applyStack(g.id), `${g.name} deployed.`) },
              { label: "Drop from group", onClick: () => void act(`Dropping ${m.service_name}`, () => removeFromOCIGroup(g.id, m.id)), danger: true },
            ]}
          />
        ) : null}
      </li>
    );
  }

  if (workloads === null) {
    return (
      <section className="page">
        <LoadingState label="Loading OCI containers" />
      </section>
    );
  }

  const shownGroups = groups.filter(
    (g) => !q || g.name.toLowerCase().includes(q) || (g.members ?? []).some((m) => m.workload_id && byId.get(m.workload_id) && hit(byId.get(m.workload_id)!)),
  );
  const candidates = (workloads ?? []).filter((w) => membership.get(w.id)?.groupId !== adding?.id);

  return (
    <section className="page page-wide" aria-labelledby="oci-heading">
      <PageHeader
        id="oci-heading"
        title="OCI Containers"
        kicker="Run Docker-compatible images directly, no Docker Engine needed. Group containers to manage an app together; drag a container onto a group to move it."
        actions={
          mutate ? (
            <div className="btn-row is-flush">
              <button className="btn btn-primary" type="button" onClick={() => setCreating({ groupId: "" })}>
                New container
              </button>
              <button className="btn btn-secondary" type="button" onClick={() => setImporting(true)}>
                Import Compose
              </button>
              <button
                className="btn btn-secondary"
                type="button"
                onClick={() => {
                  setGroupName("");
                  setNewGroup(true);
                }}
              >
                New group
              </button>
            </div>
          ) : null
        }
      />
      {error ? <ErrorNotice error={error} /> : null}
      {notice ? (
        <p className="banner banner-ok" role="status">
          {notice}{" "}
          <button className="btn btn-ghost btn-sm" type="button" onClick={() => setNotice(null)}>
            Dismiss
          </button>
        </p>
      ) : null}
      {busy ? (
        <p className="banner" role="status">
          Working: {busy}
        </p>
      ) : null}
      <div className="docker-toolbar">
        <input className="field-input" aria-label="Search containers" value={query} onChange={(e) => setQuery(e.target.value)} placeholder="Search name, image, IP" />
      </div>

      {workloads.length === 0 && groups.length === 0 ? (
        <EmptyState title="No OCI containers yet">
          Create a container from any Docker image, or import a Compose file to create a whole app as a group.
        </EmptyState>
      ) : (
        <div className="docker-grid">
          {shownGroups.map((g) => {
            const members = g.members ?? [];
            const live = members.filter((m) => m.workload_id && byId.get(m.workload_id)).map((m) => byId.get(m.workload_id!)!);
            const pending = members.filter((m) => !m.workload_id || !byId.get(m.workload_id));
            const running = live.filter((w) => (w.status || "").toLowerCase() === "running").length;
            const bad = live.some((w) => tone(w.status) === "critical") || pending.some((m) => m.status === "failed");
            return (
              <article
                key={g.id}
                className={"docker-card" + (bad ? " is-issue" : "") + (dropTarget === g.id ? " is-drop" : "")}
                data-group={g.name}
                aria-label={`Group ${g.name}`}
                onContextMenu={(e: ReactMouseEvent) => ctx.openAt(e, g.name, groupItems(g))}
                {...dropProps(g.id)}
              >
                <header className="docker-card-head">
                  <strong className="docker-head-name" title={g.name}>
                    {g.name}
                  </strong>
                  <StatusBadge status={bad ? "critical" : running === live.length && live.length ? "running" : "unknown"} label={`${running}/${live.length} running`} />
                  {mutate ? <ActionMenu label={`${g.name} group actions`} items={groupItems(g).filter((i): i is Exclude<ContextItem, "sep"> => i !== "sep" && !i.disabled)} /> : null}
                </header>
                <ul className="docker-rows">
                  {live.filter(hit).map(row)}
                  {pending.map((m) => pendingRow(g, m))}
                  {members.length === 0 ? <li className="meta oci-empty-drop">Empty group. Drag containers here or use the menu.</li> : null}
                </ul>
              </article>
            );
          })}
          <article
            className={"docker-card is-ignored" + (dropTarget === "ungrouped" ? " is-drop" : "")}
            aria-label="Ungrouped containers"
            {...dropProps("")}
          >
            <header className="docker-card-head">
              <strong className="docker-head-name">Not in a group</strong>
              <span className="meta">{ungrouped.length}</span>
            </header>
            <ul className="docker-rows">
              {ungrouped.length ? ungrouped.map(row) : <li className="meta oci-empty-drop">Every container is in a group.</li>}
            </ul>
          </article>
        </div>
      )}

      <Dialog open={creating != null} title="New OCI container" wide onClose={() => setCreating(null)}>
        {creating ? (
          <OciContainerForm
            defaultGroupId={creating.groupId}
            onCancel={() => setCreating(null)}
            onDone={(_, message) => {
              setCreating(null);
              setNotice(message);
              void reload();
            }}
          />
        ) : null}
      </Dialog>

      <Dialog open={editing != null} title={editing ? `Settings for ${editing.name}` : "Settings"} wide onClose={() => setEditing(null)}>
        {editing ? (
          <OciContainerForm
            key={editing.id}
            workload={editing}
            onCancel={() => setEditing(null)}
            onDone={(_, message) => {
              setEditing(null);
              setNotice(message);
              void reload();
            }}
          />
        ) : null}
      </Dialog>

      <ImportComposeDialog
        open={importing}
        onClose={() => setImporting(false)}
        onDone={(g) => {
          setImporting(false);
          setNotice(`Imported ${g.name} with ${(g.members ?? []).length} container(s).`);
          void reload();
        }}
      />

      <ConfirmDialog
        open={newGroup}
        title="New group"
        confirmLabel="Create"
        confirmDisabled={!groupName.trim()}
        onClose={() => setNewGroup(false)}
        onConfirm={() => {
          setNewGroup(false);
          void act("Creating group", () => createOCIGroup(groupName.trim()), `Group ${groupName.trim()} created.`);
        }}
      >
        <div className="field">
          <label className="field-label" htmlFor="oci-group-name">
            Name
          </label>
          <input id="oci-group-name" className="field-input" value={groupName} onChange={(e) => setGroupName(e.target.value)} autoFocus />
        </div>
      </ConfirmDialog>

      <ConfirmDialog
        open={renaming != null}
        title="Rename group"
        confirmLabel="Rename"
        confirmDisabled={!groupName.trim()}
        onClose={() => setRenaming(null)}
        onConfirm={() => {
          const g = renaming;
          setRenaming(null);
          if (g) {
            void act("Renaming group", () => renameStack(g.id, groupName.trim()));
          }
        }}
      >
        <div className="field">
          <label className="field-label" htmlFor="oci-group-rename">
            Name
          </label>
          <input id="oci-group-rename" className="field-input" value={groupName} onChange={(e) => setGroupName(e.target.value)} autoFocus />
        </div>
      </ConfirmDialog>

      <ConfirmDialog
        open={deleting != null}
        title={deleting ? `Delete group ${deleting.name}` : "Delete group"}
        confirmLabel="Delete"
        danger
        onClose={() => setDeleting(null)}
        onConfirm={() => {
          const g = deleting;
          setDeleting(null);
          if (g) {
            void act(
              `Deleting ${g.name}`,
              () => deleteStack(g.id, deleteContainers),
              deleteContainers ? `${g.name} and its containers were deleted.` : `${g.name} was deleted. Its containers keep running, ungrouped.`,
            );
          }
        }}
      >
        <p>The group is removed. Its containers keep running and move to “Not in a group”.</p>
        <label className="check-row">
          <input type="checkbox" checked={deleteContainers} onChange={(e) => setDeleteContainers(e.target.checked)} />
          <span>Also delete its containers (volumes are kept)</span>
        </label>
      </ConfirmDialog>

      <ConfirmDialog
        open={moving != null}
        title={moving ? `Move ${moving.name}` : "Move"}
        confirmLabel="Move"
        onClose={() => setMoving(null)}
        onConfirm={() => {
          const w = moving;
          setMoving(null);
          if (w) {
            moveContainer(w, moveTo);
          }
        }}
      >
        <div className="field">
          <label className="field-label" htmlFor="oci-move-to">
            Group
          </label>
          <select id="oci-move-to" className="field-input" value={moveTo} onChange={(e) => setMoveTo(e.target.value)}>
            <option value="">Not in a group</option>
            {groups.map((g) => (
              <option key={g.id} value={g.id}>
                {g.name}
              </option>
            ))}
          </select>
        </div>
      </ConfirmDialog>

      <ConfirmDialog
        open={adding != null}
        title={adding ? `Add to ${adding.name}` : "Add"}
        confirmLabel="Add"
        confirmDisabled={!addPick}
        onClose={() => setAdding(null)}
        onConfirm={() => {
          const g = adding;
          const w = byId.get(addPick);
          setAdding(null);
          if (g && w) {
            moveContainer(w, g.id);
          }
        }}
      >
        <div className="field">
          <label className="field-label" htmlFor="oci-add-pick">
            Container
          </label>
          <select id="oci-add-pick" className="field-input" value={addPick} onChange={(e) => setAddPick(e.target.value)}>
            <option value="">Choose a container</option>
            {candidates.map((w) => (
              <option key={w.id} value={w.id}>
                {w.name}
                {membership.get(w.id) ? ` (in ${groups.find((g) => g.id === membership.get(w.id)?.groupId)?.name ?? "a group"})` : ""}
              </option>
            ))}
          </select>
        </div>
      </ConfirmDialog>

      <ContextMenu menu={ctx.menu} onClose={ctx.close} />
    </section>
  );
}

function ImportComposeDialog({ open, onClose, onDone }: { open: boolean; onClose: () => void; onDone: (g: Stack) => void }) {
  const [name, setName] = useState("app");
  const [compose, setCompose] = useState(SAMPLE_COMPOSE);
  const [pools, setPools] = useState<StoragePool[]>([]);
  const [nets, setNets] = useState<Network[]>([]);
  const [poolId, setPoolId] = useState("");
  const [networkId, setNetworkId] = useState("");
  const [deploy, setDeploy] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!open) {
      return;
    }
    setError(null);
    void Promise.all([listPools().catch(() => ({ items: [] as StoragePool[] })), listNetworks().catch(() => ({ items: [] as Network[] }))]).then(([p, n]) => {
      const ready = (p.items ?? []).filter((x) => x.status === "available" || x.status === "warning");
      setPools(ready);
      setPoolId((cur) => cur || ready[0]?.id || "");
      setNets((n.items ?? []).filter((x) => x.status === "available" || x.status === "warning"));
    });
  }, [open]);

  async function submit() {
    setBusy(true);
    setError(null);
    try {
      const g = await importStack({ name: name.trim(), compose, pool_id: poolId || undefined, network_id: networkId || undefined, apply: deploy });
      onDone(g);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Import failed");
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog
      open={open}
      title="Import Docker Compose"
      wide
      onClose={onClose}
      footer={
        <div className="btn-row">
          <button className="btn btn-ghost" type="button" onClick={onClose}>
            Cancel
          </button>
          <button className="btn btn-primary" type="button" disabled={busy || !name.trim() || !compose.trim()} onClick={() => void submit()}>
            {busy ? "Importing" : deploy ? "Import and deploy" : "Import"}
          </button>
        </div>
      }
    >
      {error ? <ErrorNotice error={error} /> : null}
      <p className="field-hint">
        Each service becomes an OCI container in a new group. Named volumes become No-dal volumes. Edit any container afterwards like any other.
      </p>
      <div className="oci-grid">
        <div className="field">
          <label className="field-label" htmlFor="compose-name">
            Group name
          </label>
          <input id="compose-name" className="field-input" value={name} onChange={(e) => setName(e.target.value)} />
        </div>
        <div className="field">
          <label className="field-label" htmlFor="compose-pool">
            Storage pool for volumes
          </label>
          <select id="compose-pool" className="field-input" value={poolId} onChange={(e) => setPoolId(e.target.value)}>
            <option value="">Select pool</option>
            {pools.map((p) => (
              <option key={p.id} value={p.id}>
                {p.name}
              </option>
            ))}
          </select>
        </div>
        <div className="field">
          <label className="field-label" htmlFor="compose-net">
            Network
          </label>
          <select id="compose-net" className="field-input" value={networkId} onChange={(e) => setNetworkId(e.target.value)}>
            <option value="">No network</option>
            {nets.map((n) => (
              <option key={n.id} value={n.id}>
                {n.name}
              </option>
            ))}
          </select>
        </div>
      </div>
      <div className="field">
        <label className="field-label" htmlFor="compose-text">
          compose.yml
        </label>
        <textarea id="compose-text" className="field-input code-input" rows={14} value={compose} onChange={(e) => setCompose(e.target.value)} spellCheck={false} />
      </div>
      <label className="check-row">
        <input type="checkbox" checked={deploy} onChange={(e) => setDeploy(e.target.checked)} />
        <span>Deploy the containers now</span>
      </label>
    </Dialog>
  );
}
