export function kindLabel(kind?: string): string {
  switch (kind) {
    case "system-container":
      return "System container";
    case "vm":
      return "Virtual machine";
    case "isolated":
      return "Isolated";
    case "isolated-nat":
      return "Isolated with NAT";
    case "lan-bridge":
      return "LAN bridge";
    case "directory":
      return "Directory";
    case "iso":
      return "ISO";
    case "cloud-image":
      return "Cloud image";
    case "vm-disk":
      return "Disk";
    case "container-root":
      return "Container root";
    case "template":
      return "Template";
    case "backup-staging":
      return "Backup staging";
    default:
      return kind && kind.length > 0 ? kind : "Not reported";
  }
}

export function roleLabel(role: string): string {
  switch (role) {
    case "admin":
      return "Administrator";
    case "operator":
      return "Operator";
    case "viewer":
      return "Viewer";
    default:
      return role;
  }
}

export function editionLabel(edition?: string): string {
  if (edition === "ce") {
    return "Community Edition";
  }
  return edition || "Not reported";
}

const OS_PINS: Record<string, string> = {
  "alpine/3.21/amd64/default": "Alpine Linux 3.21",
  "alpine/3.20/amd64/default": "Alpine Linux 3.20",
  "debian/trixie/amd64/default": "Debian 13",
  "debian/bookworm/amd64/default": "Debian 12",
};

export const FALLBACK_IMAGE_PINS = Object.keys(OS_PINS);

export function osLabel(pin?: string): string {
  if (!pin) {
    return "Not reported";
  }
  return OS_PINS[pin] ?? pin;
}

export function metricLabel(name?: string): string {
  switch (name) {
    case "cpu.busy_ratio":
      return "CPU busy";
    case "memory.used_bytes":
      return "Memory used";
    default:
      return name || "Metric";
  }
}

export function hardwareKeyLabel(key: string): string {
  switch (key) {
    case "total_bytes":
      return "Total";
    case "available_bytes":
      return "Available";
    case "size_bytes":
      return "Size";
    case "milli_c":
      return "Temperature";
    case "smart_status":
      return "SMART";
    case "speed_mbps":
      return "Speed";
    case "dimm_status":
      return "DIMM";
    case "sys_vendor":
      return "Vendor";
    case "bios_vendor":
      return "BIOS vendor";
    case "bios_version":
      return "BIOS version";
    case "architecture":
      return "Architecture";
    case "transport":
      return "Transport";
    case "class":
      return "Class";
    case "driver":
      return "Driver";
    case "ifindex":
      return "Index";
    default:
      return key.replaceAll("_", " ");
  }
}

export function capabilityLabel(id: string): string {
  switch (id) {
    case "kvm":
      return "KVM";
    case "lxc":
      return "LXC";
    case "iommu":
      return "IOMMU";
    default:
      return id;
  }
}

export function taskKindLabel(kind?: string): string {
  if (!kind) {
    return "Not reported";
  }
  return kind
    .split(/[._-]/)
    .filter(Boolean)
    .map((part) => part.charAt(0).toUpperCase() + part.slice(1))
    .join(" ");
}

export function eventTypeLabel(type?: string): string {
  return taskKindLabel(type);
}

export function fileTypeLabel(type?: string): string {
  switch (type) {
    case "dir":
      return "Folder";
    case "file":
      return "File";
    default:
      return type || "Not reported";
  }
}
