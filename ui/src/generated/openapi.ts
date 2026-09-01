/* Generated from api/openapi/nodal.v1.yaml. Do not edit by hand. */

export interface HealthResponse {
  status: "ok" | "starting";
  service: "ndl-control";
  setup_open?: boolean;
}

export interface SetupStatusResponse {
  open: boolean;
}

export interface SetupClaimRequest {
  token: string;
  username: string;
  password: string;
}

export interface LoginRequest {
  username: string;
  password: string;
}

export interface MeResponse {
  user_id: string;
  username: string;
  roles: string[];
  edition: string;
  cluster_id?: string;
}

export interface CreateTokenRequest {
  name: string;
}

export interface CreateTokenResponse {
  id: string;
  prefix: string;
  token: string;
}

export interface RevokeTokenRequest {
  id: string;
}

export interface ErrorResponse {
  error: string;
}

export interface NodeListResponse {
  items: NodeSummary[];
}

export interface NodeSummary {
  id: string;
  name: string;
  status: string;
  stale?: boolean;
  observed_at?: string;
  host_os?: string;
  host_id?: string;
  host_version_id?: string;
  cpu_model?: string;
  cpu_sockets?: number;
  cpu_cores?: number;
  cpu_threads?: number;
  memory_bytes?: number;
  disk_count?: number;
  disk_bytes?: number;
  nic_count?: number;
  gpu_count?: number;
  gpu_present?: boolean;
}

export interface HardwareResponse {
  node_id?: string;
  observed_at?: string;
  stale?: boolean;
  status: string;
  message?: string;
  inventory?: Record<string, unknown>;
}

export interface CapabilitiesResponse {
  node_id?: string;
  stale?: boolean;
  capabilities: Capability[];
}

export interface Capability {
  id: string;
  status: string;
  detail?: string;
}

export interface MetricsResponse {
  status: string;
  series: MetricSeries[];
}

export interface MetricSeries {
  name: string;
  status: string;
  unit?: string;
  points: MetricPoint[];
}

export interface MetricPoint {
  time: string;
  value: number;
}

export interface TaskListResponse {
  items: TaskItem[];
}

export interface TaskItem {
  id: string;
  kind: string;
  state: string;
  stage?: string;
  message?: string;
  progress?: number;
  created_at?: string;
  updated_at?: string;
}

export interface EventListResponse {
  items: EventItem[];
}

export interface EventItem {
  id: string;
  type: string;
  node_id?: string;
  payload?: Record<string, unknown>;
  created_at: string;
}

export type GetHealthPath = "/api/v1/health";

export type GetSetupStatusPath = "/api/v1/setup/status";

export type ClaimSetupPath = "/api/v1/setup/claim";

export type LoginPath = "/api/v1/auth/login";

export type LogoutPath = "/api/v1/auth/logout";

export type GetMePath = "/api/v1/me";

export type CreateTokenPath = "/api/v1/tokens";

export type RevokeTokenPath = "/api/v1/tokens/revoke";

export type ListNodesPath = "/api/v1/nodes";

export type GetNodePath = "/api/v1/nodes/{id}";

export type GetNodeHardwarePath = "/api/v1/nodes/{id}/hardware";

export type GetNodeCapabilitiesPath = "/api/v1/nodes/{id}/capabilities";

export type GetNodeMetricsPath = "/api/v1/nodes/{id}/metrics";

export type ListTasksPath = "/api/v1/tasks";

export type ListEventsPath = "/api/v1/events";

export type StreamEventsPath = "/api/v1/events/stream";
