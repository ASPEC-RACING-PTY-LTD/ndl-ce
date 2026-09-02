import type { ReactNode } from "react";

export type IconName =
  | "dashboard"
  | "workloads"
  | "node"
  | "storage"
  | "network"
  | "tasks"
  | "events"
  | "start"
  | "stop"
  | "restart"
  | "terminal"
  | "files"
  | "snapshots"
  | "settings"
  | "create"
  | "delete"
  | "search"
  | "more"
  | "success"
  | "warning"
  | "error"
  | "info"
  | "account"
  | "activity"
  | "collapse"
  | "expand"
  | "mark-ok"
  | "mark-warn"
  | "mark-bad"
  | "mark-info"
  | "mark-neutral";

const PATHS: Record<IconName, ReactNode> = {
  dashboard: (
    <>
      <rect x="2" y="2" width="5" height="5" rx="0.8" />
      <rect x="9" y="2" width="5" height="5" rx="0.8" />
      <rect x="2" y="9" width="5" height="5" rx="0.8" />
      <rect x="9" y="9" width="5" height="5" rx="0.8" />
    </>
  ),
  workloads: (
    <>
      <rect x="3" y="3.5" width="10" height="3" rx="0.6" />
      <rect x="3" y="8.5" width="10" height="3" rx="0.6" />
    </>
  ),
  node: (
    <>
      <rect x="2.5" y="2.5" width="11" height="11" rx="1.2" />
      <path d="M5 6h6M5 8.5h6M5 11h3.5" />
    </>
  ),
  storage: (
    <>
      <ellipse cx="8" cy="4.2" rx="5" ry="1.8" />
      <path d="M3 4.2v7.6c0 1 2.2 1.8 5 1.8s5-.8 5-1.8V4.2" />
      <path d="M3 8c0 1 2.2 1.8 5 1.8s5-.8 5-1.8" />
    </>
  ),
  network: (
    <>
      <circle cx="4" cy="8" r="1.6" />
      <circle cx="12" cy="4.5" r="1.6" />
      <circle cx="12" cy="11.5" r="1.6" />
      <path d="M5.4 7.3 10.5 5.1M5.4 8.7 10.5 10.9" />
    </>
  ),
  tasks: (
    <>
      <path d="M3.5 4.5h9M3.5 8h9M3.5 11.5h6" />
      <path d="M11.2 10.4 12.2 11.5 14.2 9.2" />
    </>
  ),
  events: (
    <>
      <circle cx="8" cy="8" r="2" />
      <circle cx="8" cy="8" r="5" />
    </>
  ),
  start: <path d="M5 3.5v9l8-4.5z" />,
  stop: <rect x="4.2" y="4.2" width="7.6" height="7.6" rx="0.8" />,
  restart: <path d="M12.5 8A4.5 4.5 0 1 1 11 4.2M12.5 2.8v3.2H9.4" />,
  terminal: <path d="M3.5 5 6.5 8 3.5 11M8.5 11.5h4" />,
  files: (
    <>
      <path d="M2.8 5.2V12a1 1 0 0 0 1 1h8.4a1 1 0 0 0 1-1V6.2a1 1 0 0 0-1-1H8.2L7 3.8H3.8a1 1 0 0 0-1 1.4z" />
    </>
  ),
  snapshots: (
    <>
      <rect x="3" y="5.5" width="10" height="7" rx="1" />
      <path d="M5 5.5V4.4A1.4 1.4 0 0 1 6.4 3h3.2A1.4 1.4 0 0 1 11 4.4V5.5" />
    </>
  ),
  settings: (
    <>
      <circle cx="8" cy="8" r="2" />
      <path d="M8 2.6v1.6M8 11.8v1.6M2.6 8h1.6M11.8 8h1.6M4.1 4.1l1.1 1.1M10.8 10.8l1.1 1.1M11.9 4.1l-1.1 1.1M5.2 10.8l-1.1 1.1" />
    </>
  ),
  create: <path d="M8 3.2v9.6M3.2 8h9.6" />,
  delete: (
    <>
      <path d="M3.4 5h9.2M6 5V3.8h4V5M5 5l.5 7.2h5L11 5" />
    </>
  ),
  search: (
    <>
      <circle cx="7" cy="7" r="3.4" />
      <path d="m10 10 3 3" />
    </>
  ),
  more: (
    <>
      <circle cx="4" cy="8" r="0.9" fill="currentColor" stroke="none" />
      <circle cx="8" cy="8" r="0.9" fill="currentColor" stroke="none" />
      <circle cx="12" cy="8" r="0.9" fill="currentColor" stroke="none" />
    </>
  ),
  success: <path d="M3.4 8.2 6.4 11.2 12.6 4.8" />,
  warning: <path d="M8 2.8 14.2 13.4H1.8L8 2.8zM8 6.6v3.2M8 11.4v.8" />,
  error: (
    <>
      <circle cx="8" cy="8" r="5.2" />
      <path d="m5.6 5.6 4.8 4.8M10.4 5.6 5.6 10.4" />
    </>
  ),
  info: (
    <>
      <circle cx="8" cy="8" r="5.2" />
      <path d="M8 7.2V11M8 5.2v.6" />
    </>
  ),
  account: (
    <>
      <circle cx="8" cy="6" r="2.2" />
      <path d="M3.4 13c.6-2.4 2.3-3.6 4.6-3.6s4 1.2 4.6 3.6" />
    </>
  ),
  activity: (
    <>
      <circle cx="8" cy="8" r="5.2" />
      <path d="M8 4.8V8l2.2 1.4" />
    </>
  ),
  collapse: <path d="M10 3.5 5.5 8 10 12.5" />,
  expand: <path d="M6 3.5 10.5 8 6 12.5" />,
  "mark-ok": <circle cx="8" cy="8" r="4" fill="currentColor" stroke="none" />,
  "mark-warn": <path d="M8 3.2 13.4 12.6H2.6L8 3.2z" fill="currentColor" stroke="none" />,
  "mark-bad": <rect x="4" y="4" width="8" height="8" rx="1" fill="currentColor" stroke="none" />,
  "mark-info": <circle cx="8" cy="8" r="4" fill="currentColor" stroke="none" />,
  "mark-neutral": <circle cx="8" cy="8" r="3.2" />,
};

export function Icon({
  name,
  size = 16,
  className,
}: {
  name: IconName;
  size?: number;
  className?: string;
}) {
  return (
    <svg
      className={className ? `icon ${className}` : "icon"}
      width={size}
      height={size}
      viewBox="0 0 16 16"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.5"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      {PATHS[name]}
    </svg>
  );
}

export function navIcon(label: string): IconName {
  switch (label) {
    case "Dashboard":
      return "dashboard";
    case "Workloads":
      return "workloads";
    case "Terminal":
      return "terminal";
    case "Node":
      return "node";
    case "Storage":
      return "storage";
    case "Network":
      return "network";
    case "Tasks":
      return "tasks";
    case "Events":
      return "events";
    default:
      return "info";
  }
}
