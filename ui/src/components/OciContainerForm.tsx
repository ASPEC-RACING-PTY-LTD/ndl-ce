import { useEffect, useState } from "react";
import {
  addToOCIGroup,
  createVolume,
  createWorkload,
  listNetworks,
  listPools,
  listRegistries,
  listStacks,
  listVolumes,
  updateOCIConfig,
  type OCIConfig,
  type Registry,
  type Stack,
} from "../api/client";
import type { Network } from "../api/phase4";
import type { StoragePool, StorageVolume } from "../api/phase3";
import type { Workload } from "../api/phase5";
import { formatBytes } from "../format";
import { bytesFromGB, gbFromBytes, parseMemoryGB } from "../memory";
import { isAdmin } from "../rbac";
import { useSession } from "../session";
import { ErrorNotice } from "./ErrorNotice";
import { preferredGuestNetwork } from "./form/NetworkPicker";

type PortRow = { container: string; host: string; protocol: "tcp" | "udp" };
type VolRow = { volumeId: string; path: string; ro: boolean };

type SpecShape = {
  image_pin?: string;
  registry_id?: string;
  env?: { name: string; value?: string }[];
  ports?: { container_port: number; host_port?: number; protocol?: string }[];
  volumes?: { volume_id: string; container_path: string; read_only?: boolean }[];
  command?: string[];
  health?: { http_path?: string; port?: number };
  privileged?: boolean;
  resources?: { cpus?: number; memory_bytes?: number };
  network_id?: string;
  network_mode?: string;
  ipv4_address?: string;
  ipv4_gateway?: string;
  dns?: string[];
};

function envToText(env?: { name: string; value?: string }[]): string {
  return (env ?? []).map((e) => `${e.name}=${e.value ?? ""}`).join("\n");
}

function parseEnv(text: string): { name: string; value: string }[] {
  return text
    .split("\n")
    .map((l) => l.trim())
    .filter((l) => l && !l.startsWith("#"))
    .map((l) => {
      const i = l.indexOf("=");
      return i < 0 ? { name: l, value: "" } : { name: l.slice(0, i).trim(), value: l.slice(i + 1) };
    });
}

/**
 * OciContainerForm creates an OCI container, or changes one when `workload`
 * is set. Everything a Docker-style app needs is here: image, ports,
 * environment, volumes, network and address, limits and command.
 */
export function OciContainerForm({
  workload,
  defaultGroupId = "",
  onDone,
  onCancel,
}: {
  workload?: Workload;
  defaultGroupId?: string;
  onDone: (w: Workload, message: string) => void;
  onCancel?: () => void;
}) {
  const session = useSession();
  const roles = session.status === "ready" ? session.user?.roles : undefined;
  const admin = isAdmin(roles);
  const editing = Boolean(workload);
  const spec = (workload?.spec ?? {}) as SpecShape;
  const nic = workload?.nics?.[0];

  const [nets, setNets] = useState<Network[]>([]);
  const [regs, setRegs] = useState<Registry[]>([]);
  const [groups, setGroups] = useState<Stack[]>([]);
  const [vols, setVols] = useState<StorageVolume[]>([]);
  const [pools, setPools] = useState<StoragePool[]>([]);

  const [name, setName] = useState(workload?.name ?? "");
  const [image, setImage] = useState(spec.image_pin ?? workload?.image_pin ?? "");
  const [registryId, setRegistryId] = useState(spec.registry_id ?? "");
  const [groupId, setGroupId] = useState(defaultGroupId);
  const [cpus, setCpus] = useState(String(spec.resources?.cpus ?? workload?.cpus ?? 1));
  const [memoryGB, setMemoryGB] = useState(gbFromBytes(spec.resources?.memory_bytes ?? workload?.memory_bytes, 0.5));
  const initialMode = (spec.network_mode === "bridge" || spec.network_mode === "host" ? spec.network_mode : editing ? "none" : "bridge") as
    | "none"
    | "bridge"
    | "host";
  const [netMode, setNetMode] = useState<"none" | "bridge" | "host">(initialMode);
  const [networkId, setNetworkId] = useState(spec.network_id ?? nic?.network_id ?? "");
  const [ipMode, setIpMode] = useState<"dhcp" | "static">(spec.ipv4_address ? "static" : "dhcp");
  const [address, setAddress] = useState(spec.ipv4_address ?? "");
  const [gateway, setGateway] = useState(spec.ipv4_gateway ?? "");
  const [dns, setDns] = useState((spec.dns ?? []).join(", "));
  const [ports, setPorts] = useState<PortRow[]>(
    (spec.ports ?? []).map((p) => ({
      container: String(p.container_port),
      host: p.host_port ? String(p.host_port) : "",
      protocol: p.protocol === "udp" ? "udp" : "tcp",
    })),
  );
  const [envText, setEnvText] = useState(envToText(spec.env));
  const [volRows, setVolRows] = useState<VolRow[]>(
    (spec.volumes ?? []).map((v) => ({ volumeId: v.volume_id, path: v.container_path, ro: Boolean(v.read_only) })),
  );
  const [command, setCommand] = useState((spec.command ?? []).join("\n"));
  const [healthPath, setHealthPath] = useState(spec.health?.http_path ?? "");
  const [healthPort, setHealthPort] = useState(spec.health?.port ? String(spec.health.port) : "");
  const [privileged, setPrivileged] = useState(Boolean(spec.privileged ?? workload?.privileged));
  const [newVolPool, setNewVolPool] = useState("");
  const [newVolGB, setNewVolGB] = useState("5");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    void Promise.all([
      listNetworks().catch(() => ({ items: [] as Network[] })),
      listRegistries().catch(() => ({ items: [] as Registry[] })),
      listStacks().catch(() => ({ items: [] as Stack[] })),
      listVolumes().catch(() => [] as StorageVolume[]),
      listPools().catch(() => ({ items: [] as StoragePool[] })),
    ]).then(([n, r, g, v, p]) => {
      if (cancelled) {
        return;
      }
      const ready = (n.items ?? []).filter((item) => item.status === "available" || item.status === "warning");
      setNets(ready);
      setRegs(r.items ?? []);
      setGroups(g.items ?? []);
      setVols(v.filter((x) => x.class === "container-root"));
      const usable = (p.items ?? []).filter((x) => x.status === "available" || x.status === "warning");
      setPools(usable);
      setNewVolPool((cur) => cur || usable[0]?.id || "");
      if (!networkId) {
        const preferred = preferredGuestNetwork(ready);
        if (preferred) {
          setNetworkId(preferred.id);
        }
      }
    });
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  async function addVolume() {
    if (!newVolPool) {
      return;
    }
    setBusy(true);
    setError(null);
    try {
      const v = await createVolume({
        pool_id: newVolPool,
        class: "container-root",
        size_bytes: bytesFromGB(parseMemoryGB(newVolGB, 5)),
        format: "directory",
      });
      setVols((cur) => [...cur, v]);
      setVolRows((cur) => [...cur, { volumeId: v.id, path: `/data${cur.length ? cur.length : ""}`, ro: false }]);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not create the volume");
    } finally {
      setBusy(false);
    }
  }

  function config(): OCIConfig {
    const out: OCIConfig = {
      image_pin: image.trim(),
      registry_id: registryId,
      env: parseEnv(envText),
      ports: ports
        .filter((p) => p.container.trim())
        .map((p) => ({
          container_port: Number(p.container),
          host_port: p.host.trim() ? Number(p.host) : undefined,
          protocol: p.protocol,
        })),
      volumes: volRows.filter((v) => v.volumeId && v.path.trim()).map((v) => ({ volume_id: v.volumeId, container_path: v.path.trim(), read_only: v.ro })),
      command: command
        .split("\n")
        .map((l) => l.trim())
        .filter(Boolean),
      health: healthPath.trim() || Number(healthPort) > 0 ? { http_path: healthPath.trim() || undefined, port: Number(healthPort) || 0 } : { http_path: "", port: 0 },
      cpus: Math.max(1, Number(cpus) || 1),
      memory_bytes: Math.round(bytesFromGB(parseMemoryGB(memoryGB, 0.5))),
      network_mode: netMode,
      network_id: netMode === "bridge" ? networkId : "",
      ipv4_mode: ipMode,
      ipv4_address: netMode === "bridge" && ipMode === "static" ? address.trim() : "",
      ipv4_gateway: netMode === "bridge" && ipMode === "static" ? gateway.trim() : "",
      dns: dns
        .split(/[\s,]+/)
        .map((d) => d.trim())
        .filter(Boolean),
    };
    if (admin) {
      out.privileged = privileged;
    }
    return out;
  }

  async function submit() {
    setBusy(true);
    setError(null);
    try {
      const body = config();
      if (workload) {
        const updated = await updateOCIConfig(workload.id, body);
        onDone(
          updated,
          updated.applied_status === "running"
            ? `${workload.name} restarted on the new settings.`
            : `${workload.name} is saved. The new settings apply when it next starts.`,
        );
        return;
      }
      const { health, command: args, ...rest } = body;
      const created = await createWorkload(
        {
          ...rest,
          name: name.trim(),
          kind: "oci",
          args,
          health: health && (health.http_path || health.port) ? health : undefined,
          dns: body.dns,
        },
        `ui-oci-${name.trim()}`,
      );
      let note = `${created.name} is starting.`;
      if (groupId) {
        try {
          await addToOCIGroup(groupId, created.id);
        } catch (err) {
          note += ` It was not added to the group: ${err instanceof Error ? err.message : "failed"}.`;
        }
      }
      onDone(created, note);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not save the container");
    } finally {
      setBusy(false);
    }
  }

  const selectedNet = nets.find((n) => n.id === networkId);
  const canSubmit = !busy && image.trim() && (editing || name.trim()) && (netMode !== "bridge" || networkId);

  return (
    <form
      className="oci-form"
      onSubmit={(e) => {
        e.preventDefault();
        void submit();
      }}
    >
      {error ? <ErrorNotice error={error} /> : null}
      <fieldset className="oci-fieldset">
        <legend>App</legend>
        <div className="oci-grid">
          {editing ? null : (
            <div className="field">
              <label className="field-label" htmlFor="oci-name">
                Name
              </label>
              <input id="oci-name" className="field-input" value={name} onChange={(e) => setName(e.target.value)} placeholder="web" required />
            </div>
          )}
          <div className="field oci-span2">
            <label className="field-label" htmlFor="oci-image">
              Image
            </label>
            <input
              id="oci-image"
              className="field-input"
              value={image}
              onChange={(e) => setImage(e.target.value)}
              placeholder="docker.io/library/nginx:alpine"
              required
            />
          </div>
          <div className="field">
            <label className="field-label" htmlFor="oci-registry">
              Registry login
            </label>
            <select id="oci-registry" className="field-input" value={registryId} onChange={(e) => setRegistryId(e.target.value)}>
              <option value="">Public image</option>
              {regs.map((r) => (
                <option key={r.id} value={r.id}>
                  {r.name}
                </option>
              ))}
            </select>
          </div>
          {editing ? null : (
            <div className="field">
              <label className="field-label" htmlFor="oci-group">
                Group
              </label>
              <select id="oci-group" className="field-input" value={groupId} onChange={(e) => setGroupId(e.target.value)}>
                <option value="">No group</option>
                {groups.map((g) => (
                  <option key={g.id} value={g.id}>
                    {g.name}
                  </option>
                ))}
              </select>
            </div>
          )}
          <div className="field">
            <label className="field-label" htmlFor="oci-cpus">
              CPUs
            </label>
            <input id="oci-cpus" className="field-input" type="number" min={1} value={cpus} onChange={(e) => setCpus(e.target.value)} />
          </div>
          <div className="field">
            <label className="field-label" htmlFor="oci-mem">
              Memory (GB)
            </label>
            <input id="oci-mem" className="field-input" type="number" min={0.1} step={0.1} value={memoryGB} onChange={(e) => setMemoryGB(e.target.value)} />
          </div>
        </div>
      </fieldset>

      <fieldset className="oci-fieldset">
        <legend>Network</legend>
        <div className="oci-grid">
          <div className="field">
            <label className="field-label" htmlFor="oci-netmode">
              Connection
            </label>
            <select id="oci-netmode" className="field-input" value={netMode} onChange={(e) => setNetMode(e.target.value as "none" | "bridge" | "host")}>
              <option value="bridge">Own IP on a network</option>
              {admin || netMode === "host" ? <option value="host">Share the host's network</option> : null}
              <option value="none">No network</option>
            </select>
          </div>
          {netMode === "bridge" ? (
            <>
              <div className="field">
                <label className="field-label" htmlFor="oci-net">
                  Network
                </label>
                <select id="oci-net" className="field-input" value={networkId} onChange={(e) => setNetworkId(e.target.value)}>
                  <option value="">Choose a network</option>
                  {nets.map((n) => (
                    <option key={n.id} value={n.id}>
                      {n.name}
                      {n.ipv4_cidr ? ` (${n.ipv4_cidr})` : ""}
                    </option>
                  ))}
                </select>
              </div>
              <div className="field">
                <label className="field-label" htmlFor="oci-ipmode">
                  IP address
                </label>
                <select id="oci-ipmode" className="field-input" value={ipMode} onChange={(e) => setIpMode(e.target.value as "dhcp" | "static")}>
                  <option value="dhcp">Automatic (DHCP)</option>
                  <option value="static">Fixed</option>
                </select>
              </div>
              {ipMode === "static" ? (
                <>
                  <div className="field">
                    <label className="field-label" htmlFor="oci-addr">
                      Address
                    </label>
                    <input
                      id="oci-addr"
                      className="field-input"
                      value={address}
                      onChange={(e) => setAddress(e.target.value)}
                      placeholder={selectedNet?.ipv4_cidr ? `e.g. ${selectedNet.ipv4_cidr.replace(/\.\d+\//, ".50/")}` : "192.168.1.50/24"}
                    />
                  </div>
                  <div className="field">
                    <label className="field-label" htmlFor="oci-gw">
                      Gateway
                    </label>
                    <input
                      id="oci-gw"
                      className="field-input"
                      value={gateway}
                      onChange={(e) => setGateway(e.target.value)}
                      placeholder={selectedNet?.gateway || "network default"}
                    />
                  </div>
                </>
              ) : null}
            </>
          ) : null}
          {netMode !== "none" ? (
            <div className="field">
              <label className="field-label" htmlFor="oci-dns">
                DNS servers
              </label>
              <input id="oci-dns" className="field-input" value={dns} onChange={(e) => setDns(e.target.value)} placeholder="Automatic" />
            </div>
          ) : null}
        </div>
        <p className="field-hint">
          {netMode === "bridge"
            ? "The container gets its own address on the network. Published ports are also forwarded from this host's address."
            : netMode === "host"
              ? "The app listens directly on the host's addresses. Container and host ports are the same."
              : "The container has no network access."}
        </p>
      </fieldset>

      {netMode !== "none" ? (
        <fieldset className="oci-fieldset">
          <legend>Ports</legend>
          {ports.map((p, i) => (
            <div key={i} className="oci-row">
              <input
                className="field-input"
                aria-label={`Container port ${i + 1}`}
                type="number"
                min={1}
                max={65535}
                placeholder="Container port"
                value={p.container}
                onChange={(e) => setPorts((cur) => cur.map((x, j) => (j === i ? { ...x, container: e.target.value } : x)))}
              />
              <span className="meta">on host</span>
              <input
                className="field-input"
                aria-label={`Host port ${i + 1}`}
                type="number"
                min={1}
                max={65535}
                placeholder="Same"
                disabled={netMode === "host"}
                value={netMode === "host" ? "" : p.host}
                onChange={(e) => setPorts((cur) => cur.map((x, j) => (j === i ? { ...x, host: e.target.value } : x)))}
              />
              <select
                className="field-input"
                aria-label={`Protocol ${i + 1}`}
                value={p.protocol}
                onChange={(e) => setPorts((cur) => cur.map((x, j) => (j === i ? { ...x, protocol: e.target.value as "tcp" | "udp" } : x)))}
              >
                <option value="tcp">TCP</option>
                <option value="udp">UDP</option>
              </select>
              <button className="btn btn-ghost btn-sm" type="button" aria-label={`Remove port ${i + 1}`} onClick={() => setPorts((cur) => cur.filter((_, j) => j !== i))}>
                Remove
              </button>
            </div>
          ))}
          <button className="btn btn-sm btn-secondary" type="button" onClick={() => setPorts((cur) => [...cur, { container: "", host: "", protocol: "tcp" }])}>
            Add port
          </button>
        </fieldset>
      ) : null}

      <fieldset className="oci-fieldset">
        <legend>Environment</legend>
        <textarea
          className="field-input"
          aria-label="Environment variables"
          rows={4}
          value={envText}
          onChange={(e) => setEnvText(e.target.value)}
          placeholder={"One per line, for example\nTZ=Australia/Sydney\nDB_HOST=10.0.0.5"}
        />
      </fieldset>

      <fieldset className="oci-fieldset">
        <legend>Storage</legend>
        {volRows.map((v, i) => (
          <div key={i} className="oci-row">
            <select
              className="field-input"
              aria-label={`Volume ${i + 1}`}
              value={v.volumeId}
              onChange={(e) => setVolRows((cur) => cur.map((x, j) => (j === i ? { ...x, volumeId: e.target.value } : x)))}
            >
              <option value="">Choose a volume</option>
              {vols.map((vol) => (
                <option key={vol.id} value={vol.id}>
                  {vol.id.slice(0, 8)} · {formatBytes(vol.size_bytes)} · {pools.find((p) => p.id === vol.pool_id)?.name ?? vol.pool_id.slice(0, 8)}
                </option>
              ))}
            </select>
            <span className="meta">at</span>
            <input
              className="field-input"
              aria-label={`Mount path ${i + 1}`}
              placeholder="/data"
              value={v.path}
              onChange={(e) => setVolRows((cur) => cur.map((x, j) => (j === i ? { ...x, path: e.target.value } : x)))}
            />
            <label className="check-row">
              <input type="checkbox" checked={v.ro} onChange={(e) => setVolRows((cur) => cur.map((x, j) => (j === i ? { ...x, ro: e.target.checked } : x)))} />
              <span>Read only</span>
            </label>
            <button className="btn btn-ghost btn-sm" type="button" aria-label={`Remove volume ${i + 1}`} onClick={() => setVolRows((cur) => cur.filter((_, j) => j !== i))}>
              Remove
            </button>
          </div>
        ))}
        <div className="oci-row">
          <button className="btn btn-sm btn-secondary" type="button" onClick={() => setVolRows((cur) => [...cur, { volumeId: "", path: "", ro: false }])}>
            Attach volume
          </button>
          <span className="meta">or create one:</span>
          <select className="field-input" aria-label="Pool for the new volume" value={newVolPool} onChange={(e) => setNewVolPool(e.target.value)}>
            {pools.map((p) => (
              <option key={p.id} value={p.id}>
                {p.name}
              </option>
            ))}
          </select>
          <input
            className="field-input oci-narrow"
            aria-label="New volume size in GB"
            type="number"
            min={1}
            value={newVolGB}
            onChange={(e) => setNewVolGB(e.target.value)}
          />
          <span className="meta">GB</span>
          <button className="btn btn-sm btn-secondary" type="button" disabled={busy || !newVolPool} onClick={() => void addVolume()}>
            New volume
          </button>
        </div>
        <p className="field-hint">Volumes keep data when the container is recreated, moved between groups or changed.</p>
      </fieldset>

      <details className="oci-fieldset">
        <summary>Advanced</summary>
        <div className="oci-grid">
          <div className="field oci-span2">
            <label className="field-label" htmlFor="oci-cmd">
              Command override
            </label>
            <textarea
              id="oci-cmd"
              className="field-input"
              rows={3}
              value={command}
              onChange={(e) => setCommand(e.target.value)}
              placeholder="One argument per line. Empty uses the image's default."
            />
          </div>
          <div className="field">
            <label className="field-label" htmlFor="oci-hpath">
              Health check path
            </label>
            <input id="oci-hpath" className="field-input" value={healthPath} onChange={(e) => setHealthPath(e.target.value)} placeholder="/healthz" />
          </div>
          <div className="field">
            <label className="field-label" htmlFor="oci-hport">
              Health check port
            </label>
            <input id="oci-hport" className="field-input" type="number" min={0} value={healthPort} onChange={(e) => setHealthPort(e.target.value)} />
          </div>
          {admin ? (
            <label className="check-row">
              <input type="checkbox" checked={privileged} onChange={(e) => setPrivileged(e.target.checked)} />
              <span>Privileged (full host device access)</span>
            </label>
          ) : null}
        </div>
      </details>

      <p className="btn-row">
        <button className="btn btn-primary" type="submit" disabled={!canSubmit}>
          {busy ? "Saving" : editing ? "Save and apply" : "Create container"}
        </button>
        {onCancel ? (
          <button className="btn btn-ghost" type="button" onClick={onCancel}>
            Cancel
          </button>
        ) : null}
      </p>
      {editing ? <p className="field-hint">A running container restarts to pick up the new settings. Its volumes and data are kept.</p> : null}
    </form>
  );
}
