export type VmCreateFields = {
  name: string;
  cpus: string;
  memoryGB: string;
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
  bootDisk?: "virtual" | "physical";
  physicalDeviceID?: string;
};

export function buildVmCreateBody(fields: VmCreateFields) {
  const physical = fields.bootDisk === "physical" && Boolean(fields.physicalDeviceID);
  return {
    name: fields.name,
    kind: "vm",
    network_id: fields.networkID,
    pool_id: physical ? undefined : fields.poolID || undefined,
    cpus: Number(fields.cpus) || 2,
    memory_bytes: (Number(fields.memoryGB) || 2) * 1024 * 1024 * 1024,
    firmware: fields.firmware,
    autostart: fields.autostart,
    cloud_image_id: physical ? undefined : fields.cloudImageID || undefined,
    iso_library_id: fields.isoID || undefined,
    placement: fields.placement || "automatic",
    node_id: fields.nodeID || undefined,
    require_gpu: Boolean(fields.requireGPU),
    spec: physical
      ? {
          disks: [
            {
              role: "boot",
              source: "physical",
              device_id: fields.physicalDeviceID,
              format: "raw",
              bus: "ahci",
            },
          ],
        }
      : undefined,
    nocloud: {
      enable: !physical,
      hostname: fields.hostname || fields.name,
      username: fields.username,
      ssh_authorized_keys: fields.sshKeys
        .split("\n")
        .map((line) => line.trim())
        .filter(Boolean),
    },
  };
}
