export function kindLabel(kind?: string): string {
  switch (kind) {
    case "system-container":
      return "System container";
    case "vm":
      return "Virtual machine";
    case "oci":
      return "OCI";
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
      return "Owner";
    case "operator":
      return "Admin";
    case "viewer":
      return "User";
    case "automation":
      return "Automation";
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
      if (name?.endsWith(".cpu.busy_ratio")) {
        return "CPU busy";
      }
      if (name?.endsWith(".memory.current_bytes")) {
        return "Memory used";
      }
      if (name?.endsWith(".memory.max_bytes")) {
        return "Memory limit";
      }
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

export function permissionLabel(id: string): string {
  switch (id) {
    case "identity.read":
      return "View identity";
    case "identity.token.create":
      return "Create API tokens";
    case "identity.token.revoke":
      return "Revoke API tokens";
    case "identity.mfa":
      return "Manage personal MFA";
    case "identity.group.manage":
      return "Manage groups";
    case "identity.service":
      return "Manage service accounts";
    case "users.read":
      return "View users";
    case "users.create":
      return "Create users";
    case "users.update":
      return "Edit users";
    case "users.delete":
      return "Delete users";
    case "users.roles.manage":
      return "Assign roles";
    case "users.sessions.revoke":
      return "Revoke sessions";
    case "roles.manage":
      return "Manage roles";
    case "compute.read":
      return "View workloads";
    case "compute.create":
      return "Create workloads";
    case "compute.lifecycle":
    case "compute.start":
    case "compute.stop":
      return "Start and stop workloads";
    case "compute.modify":
      return "Edit workloads";
    case "compute.delete":
      return "Delete workloads";
    case "compute.console":
      return "Open consoles";
    case "compute.snapshot":
      return "Snapshot workloads";
    case "compute.migrate":
      return "Migrate workloads";
    case "storage.read":
      return "View storage";
    case "storage.pool.create":
      return "Create storage pools";
    case "storage.volume.create":
      return "Create volumes";
    case "storage.image.upload":
      return "Upload images";
    case "network.read":
      return "View networks";
    case "network.create":
      return "Create networks";
    case "network.apply":
      return "Apply network changes";
    case "backup.read":
      return "View backups";
    case "backup.create":
      return "Create backups";
    case "backup.restore":
      return "Restore backups";
    case "feature.read":
      return "View features";
    case "feature.manage":
      return "Install features";
    case "audit.read":
      return "Read the audit log";
    case "api_access.manage":
      return "Manage API access";
    case "settings.security.manage":
      return "Change security policy";
    case "*":
      return "Full access";
    default:
      return id
        .split(".")
        .filter(Boolean)
        .map((part) => part.charAt(0).toUpperCase() + part.slice(1))
        .join(" ");
  }
}

export function permissionGroup(id: string): string {
  const prefix = id.split(".")[0];
  switch (prefix) {
    case "identity":
    case "users":
    case "roles":
      return "Identity";
    case "compute":
      return "Workloads";
    case "storage":
      return "Storage";
    case "network":
      return "Network";
    case "backup":
      return "Backups";
    case "feature":
    case "store":
      return "Features";
    case "settings":
    case "secret":
      return "Security";
    case "audit":
    case "events":
    case "alert":
      return "Audit";
    case "api_access":
    case "identity.token":
      return "API";
    case "cluster":
    case "node":
    case "metrics":
      return "Host";
    case "migration":
      return "Migration";
    case "files":
    case "terminal":
      return "Access";
    default:
      return prefix === "*" ? "All" : prefix.charAt(0).toUpperCase() + prefix.slice(1);
  }
}

export function featureRuntimeLabel(status?: string): string {
  switch (status) {
    case "not_configured":
      return "Not configured";
    case "not_started":
    case "stopped":
      return "Stopped";
    case "running":
      return "Running";
    default:
      return status ? honestStatusSafe(status) : "Not reported";
  }
}

function honestStatusSafe(status: string): string {
  return status.replaceAll("_", " ");
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
