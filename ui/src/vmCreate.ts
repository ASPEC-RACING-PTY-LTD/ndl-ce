export type VmCreateFields = {
  name: string;
  cpus: string;
  memoryMiB: string;
  networkID: string;
  poolID: string;
  firmware: string;
  autostart: boolean;
  cloudImageID: string;
  isoID: string;
  hostname: string;
  username: string;
  sshKeys: string;
  placement?: string;
  nodeID?: string;
  requireGPU?: boolean;
};

export function buildVmCreateBody(fields: VmCreateFields) {
  return {
    name: fields.name,
    kind: "vm",
    network_id: fields.networkID,
    pool_id: fields.poolID || undefined,
    cpus: Number(fields.cpus) || 2,
    memory_bytes: (Number(fields.memoryMiB) || 2048) * 1024 * 1024,
    firmware: fields.firmware,
    autostart: fields.autostart,
    cloud_image_id: fields.cloudImageID || undefined,
    iso_library_id: fields.isoID || undefined,
    placement: fields.placement || "automatic",
    node_id: fields.nodeID || undefined,
    require_gpu: Boolean(fields.requireGPU),
    nocloud: {
      enable: true,
      hostname: fields.hostname || fields.name,
      username: fields.username,
      ssh_authorized_keys: fields.sshKeys
        .split("\n")
        .map((line) => line.trim())
        .filter(Boolean),
    },
  };
}
