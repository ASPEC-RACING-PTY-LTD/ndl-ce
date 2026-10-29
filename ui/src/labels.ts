export function kindLabel(kind?: string): string {
  switch (kind) {
    case "system-container":
      return "System container";
    case "vm":
      return "Virtual machine";
    case "oci":
      return "OCI";
    case "game-server":
      return "Game server";
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
    case "superadmin":
      return "SuperAdmin";
    case "admin":
      return "Admin";
    case "user":
      return "User";
    case "automation":
      return "Automation";
    case "none":
    case "":
      return "No role";
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
  if (type === "update.available") {
    return "Platform updates available";
  }
  if (type === "update.current") {
    return "Platform is up to date";
  }
  return taskKindLabel(type);
}

const PERMISSION_LABELS: Record<string, string> = {
  "*": "Everything",
  "identity.read": "View identity",
  "identity.token.create": "Create API tokens",
  "identity.token.revoke": "Revoke API tokens",
  "identity.recover": "Recover accounts",
  "identity.mfa": "Manage personal MFA",
  "identity.group.manage": "Manage groups",
  "identity.service": "Manage service accounts",
  "identity.service.force": "Force-delete service accounts in use",
  "secret.reveal": "Reveal secrets",
  "secret.use": "Use secrets",
  "users.read": "View users",
  "users.create": "Create users",
  "users.update": "Edit users",
  "users.delete": "Delete users",
  "users.roles.manage": "Assign roles",
  "users.sessions.revoke": "Revoke sessions",
  "roles.manage": "Manage roles",
  "permissions.assign": "Give permissions",
  "permissions.assign.override": "Give permissions without rank limits",
  "approvals.view": "View approval requests",
  "approvals.decide": "Approve or deny requests",
  "cluster.read": "View the cluster",
  "cluster.destroy": "Destroy the cluster",
  "cluster.join": "Join nodes",
  "cluster.promote": "Promote nodes",
  "node.read": "View nodes",
  "node.update": "Update nodes",
  "node.revoke": "Remove nodes",
  "node.host": "Root terminal and files on node hosts",
  "metrics.read": "View metrics",
  "events.read": "View events and logs",
  "audit.read": "Read the audit log",
  "alert.read": "View alerts",
  "alert.manage": "Manage alerts",
  "compute.read": "View workloads",
  "compute.create": "Create workloads",
  "compute.lifecycle": "Start and stop workloads",
  "compute.start": "Start workloads",
  "compute.stop": "Stop workloads",
  "compute.modify": "Edit workloads",
  "compute.gpu.assign": "Assign GPUs",
  "compute.delete": "Delete workloads",
  "compute.console": "Open consoles",
  "compute.snapshot": "Snapshot workloads",
  "compute.migrate": "Migrate workloads",
  "compute.privileged": "Run privileged containers",
  "storage.read": "View storage",
  "storage.pool.create": "Create storage pools",
  "storage.volume.create": "Create volumes",
  "storage.image.upload": "Upload images",
  "storage.snapshot": "Snapshot volumes",
  "storage.protected.delete": "Delete protected storage",
  "network.read": "View networks",
  "network.create": "Create networks",
  "network.apply": "Apply network changes",
  "backup.read": "View backups",
  "backup.create": "Create backups",
  "backup.restore": "Restore backups",
  "backup.git.manage": "Manage Git backup targets",
  "platform_backup.manage": "Manage platform backups",
  "terminal.open": "Open terminals",
  "terminal.sessions.manage": "Manage other people's terminal sessions",
  "files.read": "Browse files",
  "files.download": "Download files",
  "files.upload": "Upload files",
  "files.create": "Create files and folders",
  "files.modify": "Edit and rename files",
  "files.delete": "Delete files",
  "files.permissions": "Change file permissions",
  "files.ownership": "Change file ownership",
  "settings.tls.read": "View TLS settings",
  "settings.tls.manage": "Manage TLS certificates",
  "settings.license.read": "View the license",
  "settings.license.manage": "Manage the license",
  "settings.security.manage": "Change security policy",
  "settings.appearance.manage": "Manage appearance",
  "settings.appearance.limit": "Set appearance limits for profiles",
  "feature.read": "View features",
  "feature.manage": "Install features",
  "store.read": "View the store",
  "store.install": "Install from the store",
  "store.verify": "Verify store packages",
  "policy.read": "View policies",
  "policy.apply": "Apply policies",
  "policy.run": "Run policies",
  "ai.ask": "Ask the assistant",
  "ai.manage": "Manage the assistant",
  "migration.read": "View migrations",
  "migration.import": "Import workloads",
  "migration.export": "Export workloads",
  "migration.manage": "Manage migrations",
  "migration.privileged": "Privileged migrations",
  "api_access.manage": "Manage API access",
  "updates.manage": "Install platform updates",
  "updates.wipe": "Wipe the previous version after an update",
  "gameserver.read": "View game servers",
  "gameserver.create": "Create game servers",
  "gameserver.power": "Start and stop game servers",
  "gameserver.console": "Game server consoles",
  "gameserver.files": "Game server files",
  "gameserver.config": "Game server settings",
  "gameserver.content": "Game server mods and content",
  "gameserver.backup": "Game server backups",
  "gameserver.schedule": "Game server schedules",
  "gameserver.network": "Game server networking",
  "gameserver.users": "Game server players and admins",
  "gameserver.reinstall": "Reinstall game servers",
  "gameserver.delete": "Delete game servers",
  "gameserver.admin": "Administer game servers",
  "cloudflare.read": "View Cloudflare",
  "cloudflare.manage": "Manage Cloudflare",
  "cloudflare.credentials": "Manage Cloudflare credentials",
  "cloudflare.host_tunnel": "Manage the host's Cloudflare tunnel",
  "ddos.read": "View DDoS protection",
  "ddos.manage": "Manage DDoS protection",
  "search.manage": "Manage search",
  "search.host_files": "Index host files in search",
  "lab.manage": "Manage the lab",
  "docker.scripts.admin": "Run Docker scripts with admin rights",
  "deploy.configure": "Configure deployments",
  "automation.skip_test": "Skip automation test runs",
};

export function permissionLabel(id: string): string {
  return (
    PERMISSION_LABELS[id] ??
    id
      .split(/[._]/)
      .filter(Boolean)
      .map((part) => part.charAt(0).toUpperCase() + part.slice(1))
      .join(" ")
  );
}

export function permissionGroup(id: string): string {
  const prefix = id.split(".")[0];
  switch (prefix) {
    case "identity":
    case "users":
    case "roles":
    case "permissions":
    case "approvals":
      return "Identity";
    case "compute":
      return "Workloads";
    case "gameserver":
      return "Game servers";
    case "docker":
      return "Docker";
    case "deploy":
      return "Deploy";
    case "storage":
      return "Storage";
    case "network":
      return "Network";
    case "cloudflare":
      return "Cloudflare";
    case "ddos":
      return "DDoS protection";
    case "backup":
    case "platform_backup":
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
    case "policy":
    case "automation":
      return "Automation";
    case "ai":
      return "Assistant";
    case "updates":
      return "Updates";
    case "search":
      return "Search";
    case "lab":
      return "Lab";
    case "*":
      return "All";
    default:
      return prefix
        .split("_")
        .filter(Boolean)
        .map((part, i) => (i === 0 ? part.charAt(0).toUpperCase() + part.slice(1) : part))
        .join(" ");
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
