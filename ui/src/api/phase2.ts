export type NodeSummary = {
  id: string;
  name: string;
  status: string;
  role?: string;
  reason?: string;
  listen_addr?: string;
  wg_peer_id?: string;
  wg_public_key?: string;
  last_seen_at?: string;
  last_handshake_unix?: number;
  stale?: boolean;
  observed_at?: string;
  host_os?: string;
  support_tier?: string;
  host_platform?: string;
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
};

export type Capability = {
  id: string;
  status: string;
  detail?: string;
};

export type HardwareResponse = {
  node_id?: string;
  observed_at?: string;
  stale?: boolean;
  status: string;
  message?: string;
  inventory?: Record<string, unknown>;
};

export type MetricPoint = {
  time: string;
  value: number;
};

export type MetricSeries = {
  name: string;
  status: string;
  unit?: string;
  points: MetricPoint[];
};

export type MetricsResponse = {
  status: string;
  series: MetricSeries[];
};

export type TaskItem = {
  id: string;
  kind: string;
  state: string;
  stage?: string;
  message?: string;
  progress?: number;
  created_at?: string;
  updated_at?: string;
};

export type EventItem = {
  id: string;
  type: string;
  node_id?: string;
  payload?: Record<string, unknown>;
  created_at: string;
};
