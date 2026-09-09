export type WorkloadDisk = {
  id: string;
  volume_id: string;
  role?: string;
  size_bytes?: number;
};

export type WorkloadNIC = {
  id: string;
  network_id: string;
  mac: string;
  ipv4?: string;
  ipv4_mode?: string;
  ipv4_address?: string;
  ipv4_gateway?: string;
  ipv6_mode?: string;
  ipv6_address?: string;
  ipv6_gateway?: string;
  dns?: string[];
  pci_addr?: string;
  model?: string;
};

export type Workload = {
  id: string;
  name: string;
  kind: string;
  status: string;
  reason?: string;
  desired_power?: string;
  image_pin?: string;
  image_verified?: boolean;
  cpus?: number;
  memory_bytes?: number;
  disk_bytes?: number;
  mac?: string;
  privileged?: boolean;
  pid?: number | null;
  unit_active?: boolean;
  migrate_ready?: boolean;
  migrate_blockers?: unknown;
  devices?: unknown;
  warnings?: string[];
  disks?: WorkloadDisk[];
  nics?: WorkloadNIC[];
  autostart?: boolean;
  pending_restart?: boolean;
  firmware?: string;
  spec?: unknown;
  node_id?: string;
  ownership_epoch?: number;
  health?: { status?: string; message?: string };
  unit?: string;
};

export type WorkloadListResponse = {
  items: Workload[];
  image_pins?: string[];
};
