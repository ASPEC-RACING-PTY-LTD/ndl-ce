import type { IconName } from "../components/Icon";

export type WorkloadNavLink = {
  href: string;
  label: string;
  icon: IconName;
  current: boolean;
};

export type WorkloadNavGroup = {
  label: string;
  items: WorkloadNavLink[];
};

export function leafFromWorkloadPath(path: string): string {
  const parts = path.split("/").filter(Boolean);
  return parts[2] || "summary";
}

export function isOperationsLeaf(leaf: string): boolean {
  return leaf === "operations" || leaf === "clone" || leaf === "migrate";
}

/**
 * itemWorkloadPage maps a workload page to the permission a single-item
 * grant needs to open it. Pages missing here need a role.
 */
export const ITEM_WORKLOAD_PAGES: Record<string, string> = {
  summary: "compute.read",
  terminal: "terminal.open",
  files: "files.read",
  console: "compute.console",
  snapshots: "compute.read",
};

export function buildWorkloadNav(opts: {
  id: string;
  kind: string;
  leaf: string;
  guestOk: boolean;
  mutate: boolean;
  dockerOn: boolean;
  /**
   * allow narrows the pages to those the person may open (by the page's
   * last address part, such as "terminal"). Left out, every page shows.
   */
  allow?: (leaf: string) => boolean;
}): WorkloadNavGroup[] {
  const { id, kind, leaf, guestOk, mutate, dockerOn, allow } = opts;
  const groups = buildAll(id, kind, leaf, guestOk, mutate, dockerOn);
  if (!allow) {
    return groups;
  }
  return groups
    .map((g) => ({ ...g, items: g.items.filter((item) => item.href === "/docker" || allow(leafFromWorkloadPath(item.href))) }))
    .filter((g) => g.items.length > 0);
}

function buildAll(id: string, kind: string, leaf: string, guestOk: boolean, mutate: boolean, dockerOn: boolean): WorkloadNavGroup[] {
  const groups: WorkloadNavGroup[] = [];
  const summaryCurrent = leaf === "summary";

  const overview: WorkloadNavLink[] = [
    {
      href: `/workloads/${id}`,
      label: "Summary",
      icon: "dashboard",
      current: summaryCurrent,
    },
  ];
  if (kind === "system-container") {
    overview.push({
      href: `/workloads/${id}/diagnostics`,
      label: "Diagnostics",
      icon: "diagnostics",
      current: leaf === "diagnostics",
    });
  }
  groups.push({ label: "Overview", items: overview });

  const access: WorkloadNavLink[] = [];
  if (kind === "system-container") {
    access.push(
      {
        href: `/workloads/${id}/terminal`,
        label: "Terminal",
        icon: "terminal",
        current: leaf === "terminal",
      },
      {
        href: `/workloads/${id}/files`,
        label: "Files",
        icon: "files",
        current: leaf === "files",
      },
    );
  } else if (kind === "vm") {
    access.push({
      href: `/workloads/${id}/console`,
      label: "Console",
      icon: "terminal",
      current: leaf === "console",
    });
    if (guestOk) {
      access.push(
        {
          href: `/workloads/${id}/terminal`,
          label: "Terminal",
          icon: "terminal",
          current: leaf === "terminal",
        },
        {
          href: `/workloads/${id}/files`,
          label: "Files",
          icon: "files",
          current: leaf === "files",
        },
      );
    }
  }
  if (access.length > 0) {
    groups.push({ label: "Access", items: access });
  }

  const machine: WorkloadNavLink[] = [];
  if (kind === "vm" && mutate) {
    machine.push({
      href: `/workloads/${id}/machine`,
      label: "USB",
      icon: "node",
      current: leaf === "machine",
    });
  }
  if (mutate && kind) {
    machine.push({
      href: `/workloads/${id}/gpus`,
      label: "GPUs",
      icon: "node",
      current: leaf === "gpus",
    });
  }
  if (machine.length > 0) {
    groups.push({ label: "Machine", items: machine });
  }

  if (kind === "system-container") {
    groups.push({
      label: "Storage",
      items: [
        {
          href: `/workloads/${id}/storage`,
          label: "Storage",
          icon: "storage",
          current: leaf === "storage",
        },
      ],
    });
    groups.push({
      label: "Software",
      items: [
        {
          href: `/workloads/${id}/extensions`,
          label: "Components",
          icon: "settings",
          current: leaf === "extensions",
        },
      ],
    });
  }

  if (kind === "system-container" || kind === "vm") {
    groups.push({
      label: "Protection",
      items: [
        {
          href: `/workloads/${id}/snapshots`,
          label: "Snapshots",
          icon: "snapshots",
          current: leaf === "snapshots",
        },
      ],
    });
  }

  if (mutate) {
    groups.push({
      label: "Operations",
      items: [
        {
          href: `/workloads/${id}/clone`,
          label: "Clone",
          icon: "create",
          current: leaf === "clone" || leaf === "operations",
        },
        {
          href: `/workloads/${id}/migrate`,
          label: "Migrate",
          icon: "network",
          current: leaf === "migrate" || leaf === "operations",
        },
      ],
    });
  }

  if (dockerOn) {
    groups.push({
      label: "Integrations",
      items: [
        {
          href: "/docker",
          label: "Docker",
          icon: "workloads",
          current: false,
        },
      ],
    });
  }

  return groups;
}
