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

export function buildWorkloadNav(opts: {
  id: string;
  kind: string;
  leaf: string;
  guestOk: boolean;
  mutate: boolean;
  dockerOn: boolean;
}): WorkloadNavGroup[] {
  const { id, kind, leaf, guestOk, mutate, dockerOn } = opts;
  const groups: WorkloadNavGroup[] = [];
  const summaryCurrent = leaf === "summary";

  groups.push({
    label: "Overview",
    items: [
      {
        href: `/workloads/${id}`,
        label: "Summary",
        icon: "dashboard",
        current: summaryCurrent,
      },
    ],
  });

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
