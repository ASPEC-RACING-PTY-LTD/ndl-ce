export type WorkloadDisk = {
  id: string;
  volume_id: string;
  role?: string;
};

export type WorkloadNIC = {
  id: string;
  network_id: string;
  mac: string;
  ipv4?: string;
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
  health?: { status?: string; message?: string };
  unit?: string;
};

export type WorkloadListResponse = {
  items: Workload[];
  image_pins?: string[];
};
