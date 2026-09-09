import { useEffect, useState } from "react";
import {
  attachWorkloadUSB,
  createTemplate,
  exportWorkload,
  getWorkload,
  getWorkloadGuest,
  getWorkloadLogs,
  listNodeUSB,
  migrateWorkload,
  patchWorkload,
  workloadAction,
} from "../api/client";
import type { USBDeviceRow, WorkloadGuest } from "../api/client";
import type { Workload } from "../api/phase5";
import { ConfirmDialog } from "../components/ConfirmDialog";
import { ErrorState, LoadingState } from "../components/EmptyState";
import {
  ContainerIPFields,
  containerIPBody,
  defaultContainerIPForm,
  summarizeContainerIP,
  type ContainerIPForm,
} from "../components/form/ContainerIPFields";
import { Field } from "../components/Field";
import { Icon } from "../components/Icon";
import { Link } from "../components/Link";
import { PageHeader } from "../components/PageHeader";
import { StatusBadge } from "../components/StatusBadge";
import { formatBytes, honestStatus } from "../format";
import { bytesFromGB, gbFromBytes, parseMemoryGB } from "../memory";
import { kindLabel } from "../labels";
import { canMutate } from "../rbac";
import { currentPath, navigate } from "../router";
import { useSession } from "../session";

function workloadIDFromPath(): string {
  const parts = currentPath().split("/").filter(Boolean);
  return parts[0] === "workloads" ? (parts[1] ?? "") : "";
}

export function WorkloadDetailPage() {
  const session = useSession();
  const roles = session.status === "ready" ? session.user?.roles : undefined;
  const mutate = canMutate(roles);
  const id = workloadIDFromPath();
  const [item, setItem] = useState<Workload | null>(null);
  const [cpus, setCpus] = useState("1");
  const [memoryGB, setMemoryGB] = useState("1");
  const [diskGB, setDiskGB] = useState("8");
  const [ip, setIP] = useState<ContainerIPForm>(defaultContainerIPForm);
  const [mac, setMAC] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [usbs, setUsbs] = useState<USBDeviceRow[]>([]);
  const [usbAddr, setUsbAddr] = useState("");
  const [guest, setGuest] = useState<WorkloadGuest | null>(null);
  const [destNode, setDestNode] = useState("");
  const [migrateMode, setMigrateMode] = useState<"live" | "offline">("live");
  const [logLines, setLogLines] = useState<string[]>([]);
  const [logStatus, setLogStatus] = useState<string>("");
  const [logMessage, setLogMessage] = useState<string>("");
  const [showLogs, setShowLogs] = useState(false);
  const [confirm, setConfirm] = useState<"delete" | null>(null);

  async function reload() {
    const w = await getWorkload(id);
    setItem(w);
    setCpus(String(w.cpus ?? 1));
    setMemoryGB(gbFromBytes(w.memory_bytes, 1));
    setDiskGB(gbFromBytes(w.disk_bytes, 8));
    const nic = w.nics?.[0];
    setMAC(w.mac || nic?.mac || "");
    setIP({
      ipv4Mode: nic?.ipv4_mode || "dhcp",
      ipv4Address: nic?.ipv4_address || "",
      ipv4Gateway: nic?.ipv4_gateway || "",
      ipv6Mode: nic?.ipv6_mode || "disabled",
      ipv6Address: nic?.ipv6_address || "",
      ipv6Gateway: nic?.ipv6_gateway || "",
      dns: (nic?.dns ?? []).join(", "),
    });
    if (w.kind === "vm") {
      try {
        setGuest(await getWorkloadGuest(w.id));
      } catch {
        setGuest(null);
      }
    } else {
      setGuest(null);
    }
    if (w.kind === "vm" && w.node_id) {
      const listed = await listNodeUSB(w.node_id);
      setUsbs(listed.items ?? []);
      if (!usbAddr && listed.items?.[0]?.address) {
        setUsbAddr(listed.items[0].address);
      }
    }
  }

  useEffect(() => {
    let cancelled = false;
    void reload().catch((err) => {
      if (!cancelled) {
        setError(err instanceof Error ? err.message : "Unavailable");
      }
    });
    const timer = window.setInterval(() => {
      void reload().catch(() => undefined);
    }, 4000);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id]);

  async function onAction(action: "start" | "stop" | "restart" | "delete" | "clone" | "force-stop") {
    setBusy(true);
    setError(null);
    try {
      if (action === "delete" && item?.kind === "vm") {
        if (!window.confirm("Delete this VM configuration? Attached volumes are preserved.")) {
          setBusy(false);
          setConfirm(null);
          return;
        }
      }
      const next = await workloadAction(
        id,
        action,
        undefined,
        action === "delete" && item?.kind === "vm" ? "delete" : undefined,
      );
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

  async function onMigrate() {
    if (!destNode.trim()) {
      setError("Dest node id is required");
      return;
    }
    setBusy(true);
    setError(null);
    try {
      const mode = item?.kind === "vm" ? migrateMode : "offline";
      await migrateWorkload(id, { dest_node_id: destNode.trim(), mode });
      await reload();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Migrate failed");
    } finally {
      setBusy(false);
    }
  }

  async function onSave() {
    setBusy(true);
    setError(null);
    try {
      await patchWorkload(id, {
        cpus: Number(cpus) || 1,
        memory_bytes: bytesFromGB(parseMemoryGB(memoryGB, 1)),
        ...(item?.kind === "system-container"
          ? { disk_bytes: bytesFromGB(parseMemoryGB(diskGB, 8)) }
          : {}),
        ...(item?.kind === "system-container" ? containerIPBody(ip) : {}),
        ...(item?.kind === "system-container" && mac.trim() ? { mac: mac.trim() } : {}),
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
  const currentMAC = item.mac || item.nics?.[0]?.mac;
  const guestOk = guest?.nodal_ga?.state === "ok";

  return (
    <section className="page page-wide" aria-labelledby="workload-heading">
      <PageHeader
        id="workload-heading"
        title={item.name}
        kicker={
          <>
            {kindLabel(item.kind)} · <StatusBadge status={item.status} />
          </>
        }
        actions={
          mutate ? (
            <div className="btn-row is-flush">
              <button className="btn btn-sm btn-secondary" type="button" disabled={busy} onClick={() => void onAction("start")}>
                <Icon name="start" size={14} />
                Start
              </button>
              <button className="btn btn-sm btn-secondary" type="button" disabled={busy} onClick={() => void onAction("stop")}>
                <Icon name="stop" size={14} />
                Stop
              </button>
              <button className="btn btn-sm btn-ghost" type="button" disabled={busy} onClick={() => void onAction("restart")}>
                <Icon name="restart" size={14} />
                Restart
              </button>
              <button className="btn btn-sm btn-danger" type="button" disabled={busy} onClick={() => setConfirm("delete")}>
                <Icon name="delete" size={14} />
                Delete
              </button>
            </div>
          ) : null
        }
      />
      {item.kind === "system-container" ? (
        <nav className="subnav" aria-label="Workload IO">
          <Link href={`/workloads/${item.id}`} aria-current="page">
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
          <Link href={`/workloads/${item.id}/snapshots`}>
            <Icon name="snapshots" size={14} />
            Snapshots
          </Link>
          {mutate ? <Link href={`/workloads/${item.id}/gpus`}>GPUs</Link> : null}
        </nav>
      ) : item.kind === "oci" ? (
        <nav className="subnav" aria-label="OCI IO">
          <Link href={`/workloads/${item.id}`} aria-current="page">
            Summary
          </Link>
          <button type="button" className="btn btn-ghost" onClick={() => setShowLogs((v) => !v)}>
            {showLogs ? "Hide logs" : "Logs"}
          </button>
          {mutate ? <Link href={`/workloads/${item.id}/gpus`}>GPUs</Link> : null}
        </nav>
      ) : (
        <>
          <nav className="subnav" aria-label="VM IO">
            <Link href={`/workloads/${item.id}`} aria-current="page">
              Summary
            </Link>
            <Link href={`/workloads/${item.id}/console`}>Console</Link>
            {guestOk ? (
              <>
                <Link href={`/workloads/${item.id}/terminal`}>Terminal</Link>
                <Link href={`/workloads/${item.id}/files`}>Files</Link>
              </>
            ) : (
              <>
                <span>Terminal (unavailable)</span>
                <span>Files (unavailable)</span>
              </>
            )}
            <Link href={`/workloads/${item.id}/snapshots`}>Snapshots</Link>
            {mutate ? <Link href={`/workloads/${item.id}/gpus`}>GPUs</Link> : null}
          </nav>
          {guestOk ? null : (
            <p className="banner banner-warn" role="status">
              {guest?.nodal_ga?.reason ||
                "VM Terminal and Files stay disabled until the Guest Agent is installed and connected."}
            </p>
          )}
          <article className="panel">
            <h2>Guest Agent</h2>
            <dl className="definition-list">
              <div>
                <dt>qemu-ga</dt>
                <dd>
                  {guest?.qemu_ga.state ?? "Collecting"}
                  {guest?.qemu_ga.reason ? ` (${guest.qemu_ga.reason})` : ""}
                </dd>
              </div>
              <div>
                <dt>No-dal Guest Agent</dt>
                <dd>
                  {guest?.nodal_ga.state ?? "Collecting"}
                  {guest?.nodal_ga.version ? ` ${guest.nodal_ga.version}` : ""}
                  {guest?.nodal_ga.reason ? ` (${guest.nodal_ga.reason})` : ""}
                </dd>
              </div>
              <div>
                <dt>Guest OS</dt>
                <dd>{guest?.guest_os || "Not reported"}</dd>
              </div>
              <div>
                <dt>Guest IPv4</dt>
                <dd>{guest?.ipv4?.length ? guest.ipv4.join(", ") : "Not reported"}</dd>
              </div>
            </dl>
            {guest?.nodal_ga.state === "not_installed" || guest?.nodal_ga.state === "unavailable" || !guest ? (
              <div>
                <h3>Install inside the guest</h3>
                <p>
                  {guest?.install?.linux ||
                    "Install the ndl-guest package inside the Linux guest and enable ndl-guest.service. The virtio-serial channel org.nodal.guest.0 is attached to every No-dal VM."}
                </p>
                <p>
                  {guest?.install?.windows ||
                    "Install ndl-guest.exe inside Windows guests for shutdown, IP, and Files. PTY stays on Console."}
                </p>
                <pre className="code-block">sudo apt install ndl-guest && sudo systemctl enable --now ndl-guest</pre>
              </div>
            ) : null}
          </article>
        </>
      )}
      {item.kind === "oci" && showLogs ? (
        <article className="panel">
          <h2>Logs</h2>
          <p className="page-kicker">
            Unit {item.unit || `nodal-oci@${item.id}.service`}. Status: {logStatus || "Collecting"}
            {logMessage ? ` (${logMessage})` : ""}
          </p>
          <button
            type="button"
            className="btn btn-ghost"
            onClick={() => {
              void getWorkloadLogs(item.id)
                .then((logs) => {
                  setLogStatus(logs.status);
                  setLogMessage(logs.message || "");
                  setLogLines(logs.lines ?? []);
                })
                .catch((err) => setError(err instanceof Error ? err.message : "Logs unavailable"));
            }}
          >
            Refresh logs
          </button>
          {logLines.length === 0 ? (
            <p>No lines. Status stays unavailable when journald is missing.</p>
          ) : (
            <pre className="code-block">{logLines.join("\n")}</pre>
          )}
        </article>
      ) : null}
      {error ? <ErrorState>{error}</ErrorState> : null}
      <section className="section">
        <h2>Summary</h2>
        <dl className="definition-list compact">
          <div>
            <dt>Status</dt>
            <dd>
              <StatusBadge status={item.status} />
            </dd>
          </div>
          {item.kind === "oci" ? (
            <div>
              <dt>Health</dt>
              <dd>
                {item.health?.status ? honestStatus(item.health.status) : "Collecting"}
                {item.health?.message ? ` (${item.health.message})` : ""}
              </dd>
            </div>
          ) : null}
          <div>
            <dt>Reason</dt>
            <dd>{item.reason || "None"}</dd>
          </div>
          <div>
            <dt>Image</dt>
            <dd>
              {item.image_pin || "Not reported"} {item.image_verified ? "(verified)" : "(not verified)"}
            </dd>
          </div>
          <div>
            <dt>PID</dt>
            <dd>{item.pid ?? "Not reported"}</dd>
          </div>
          <div>
            <dt>Unit</dt>
            <dd>{item.unit_active ? "active" : "inactive"}</dd>
          </div>
          <div>
            <dt>Memory</dt>
            <dd>{formatBytes(item.memory_bytes)}</dd>
          </div>
          <div>
            <dt>Disk</dt>
            <dd>{item.disk_bytes ? formatBytes(item.disk_bytes) : "Not reported"}</dd>
          </div>
          <div>
            <dt>Node</dt>
            <dd>{item.node_id || "Not reported"}</dd>
          </div>
          <div>
            <dt>Firmware</dt>
            <dd>{item.firmware || (item.kind === "vm" ? "bios" : "n/a")}</dd>
          </div>
          <div>
            <dt>Autostart</dt>
            <dd>{item.autostart ? "yes" : "no"}</dd>
          </div>
          <div>
            <dt>Pending restart</dt>
            <dd>{item.pending_restart ? "desired spec differs from running config" : "no"}</dd>
          </div>
          <div>
            <dt>IPv4</dt>
            <dd>{ipv4 || item.nics?.[0]?.ipv4_address || "Not reported"}</dd>
          </div>
          <div>
            <dt>Addressing</dt>
            <dd>{item.kind === "system-container" ? summarizeContainerIP(ip) : currentMAC ? "MAC assigned" : "Not reported"}</dd>
          </div>
          <div>
            <dt>MAC</dt>
            <dd>{currentMAC || "Not reported"}</dd>
          </div>
          {item.kind === "vm" ? (
            <>
              <div>
                <dt>Disks</dt>
                <dd>
                  {(item.disks ?? []).length
                    ? (item.disks ?? []).map((d) => `${d.role || "disk"} ${d.volume_id}`).join(", ")
                    : "Not reported"}
                </dd>
              </div>
              <div>
                <dt>NICs</dt>
                <dd>
                  {(item.nics ?? []).length
                    ? (item.nics ?? []).map((n) => `${n.mac || "mac pending"} ${n.pci_addr || ""}`.trim()).join(", ")
                    : "Not reported"}
                </dd>
              </div>
            </>
          ) : null}
          <div>
            <dt>Migrate ready</dt>
            <dd>{item.migrate_ready ? "yes" : "no"}</dd>
          </div>
        </dl>
      </section>
      {mutate ? (
        <article className="panel">
          <h2>Lifecycle</h2>
          <div className="btn-row">
            {item.kind === "vm" ? (
              <>
                <button className="btn" type="button" disabled={busy} onClick={() => void onAction("force-stop")}>
                  Force Stop
                </button>
                <button className="btn" type="button" disabled={busy} onClick={() => void onAction("clone")}>
                  Clone
                </button>
                <button
                  className="btn"
                  type="button"
                  disabled={busy}
                  onClick={() => {
                    setBusy(true);
                    setError(null);
                    void createTemplate({ workload_id: item.id, name: `${item.name}-template` })
                      .then(() => navigate("/templates"))
                      .catch((err) => setError(err instanceof Error ? err.message : "Template failed"))
                      .finally(() => setBusy(false));
                  }}
                >
                  Save template
                </button>
                <button
                  className="btn"
                  type="button"
                  disabled={busy}
                  onClick={() => {
                    setBusy(true);
                    setError(null);
                    void exportWorkload(item.id, `${item.name}.qcow2`)
                      .catch((err) => setError(err instanceof Error ? err.message : "Export failed"))
                      .finally(() => setBusy(false));
                  }}
                >
                  Export disk
                </button>
              </>
            ) : (
              <button className="btn" type="button" disabled={busy} onClick={() => void onAction("clone")}>
                Clone
              </button>
            )}
          </div>
        </article>
      ) : null}
      {mutate ? (
        <article className="panel">
          <h2>Migrate</h2>
          <p className="page-kicker">
            Live is VM-only over QMP. A failed live migrate leaves the source running. CT and OCI use offline. Dest
            agent is required; this page does not start a second copy on the current node.
          </p>
          <Field id="wl-dest" label="Dest node id" value={destNode} onChange={(e) => setDestNode(e.target.value)} />
          {item.kind === "vm" ? (
            <fieldset className="field">
              <legend>Mode</legend>
              <label>
                <input type="radio" name="migrate-mode" checked={migrateMode === "live"} onChange={() => setMigrateMode("live")} />{" "}
                Live
              </label>
              <label>
                <input
                  type="radio"
                  name="migrate-mode"
                  checked={migrateMode === "offline"}
                  onChange={() => setMigrateMode("offline")}
                />{" "}
                Offline
              </label>
            </fieldset>
          ) : (
            <p>Offline only</p>
          )}
          <button className="btn" type="button" disabled={busy || !destNode.trim()} onClick={() => void onMigrate()}>
            Migrate
          </button>
        </article>
      ) : null}
      {mutate && item.kind === "vm" ? (
        <article className="panel">
          <h2>USB passthrough</h2>
          {usbs.length === 0 ? <p>None detected</p> : null}
          {usbs.length > 0 ? (
            <>
              <label htmlFor="usb-addr">
                Inventory device
                <select id="usb-addr" className="field-input" value={usbAddr} onChange={(e) => setUsbAddr(e.target.value)}>
                  {usbs.map((u) => (
                    <option key={u.address} value={u.address}>
                      {u.address} {u.vendor}:{u.product} {u.name || ""} {u.claimed_by ? "(claimed)" : ""}
                    </option>
                  ))}
                </select>
              </label>
              <button
                className="btn"
                type="button"
                disabled={busy || !usbAddr}
                onClick={() => {
                  setBusy(true);
                  setError(null);
                  void attachWorkloadUSB(item.id, usbAddr)
                    .then(() => reload())
                    .catch((err) => setError(err instanceof Error ? err.message : "USB attach failed"))
                    .finally(() => setBusy(false));
                }}
              >
                Attach USB
              </button>
            </>
          ) : null}
        </article>
      ) : null}
      {mutate ? (
        <article className="panel">
          <h2>Spec</h2>
          <div className="field-row">
            <Field id="wl-cpus" label="CPUs" type="number" min={1} value={cpus} onChange={(e) => setCpus(e.target.value)} />
            <Field
              id="wl-mem"
              label="Memory (GB)"
              type="number"
              min={1}
              value={memoryGB}
              onChange={(e) => setMemoryGB(e.target.value)}
            />
          </div>
          {item.kind === "system-container" ? (
            <Field
              id="wl-disk"
              label="Disk size (GB)"
              type="number"
              min={1}
              value={diskGB}
              onChange={(e) => setDiskGB(e.target.value)}
              hint="Growing only. Stop the container first. Shrinking is not supported."
            />
          ) : null}
          {item.kind === "system-container" ? <ContainerIPFields id="wl-ip" form={ip} onChange={setIP} /> : null}
          {item.kind === "system-container" ? (
            <Field
              id="wl-mac"
              label="MAC address"
              value={mac}
              onChange={(e) => setMAC(e.target.value)}
              hint="Stop the container before changing MAC. The new address is written to the LXC interface. Leave unchanged to keep the current reservation."
            />
          ) : null}
          <div className="btn-row">
            <button className="btn btn-primary" type="button" disabled={busy} onClick={() => void onSave()}>
              Save spec
            </button>
          </div>
        </article>
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
    </section>
  );
}
