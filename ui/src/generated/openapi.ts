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

export interface StoragePoolListResponse {
  items: StoragePool[];
  default_path?: string;
}

export interface StoragePool {
  id: string;
  name: string;
  backend_type: string;
  status: string;
  locator?: string;
  reason?: string;
  warnings?: string[];
  warning_text?: string[];
  usable_bytes?: number;
  allocated_bytes?: number;
  provisioned_bytes?: number;
}

export interface CreateStoragePoolRequest {
  name?: string;
  path?: string;
  create?: boolean;
}

export interface StorageVolumeListResponse {
  items: StorageVolume[];
}

export interface StorageVolume {
  id: string;
  pool_id: string;
  class: string;
  backend_ref?: string;
  size_bytes?: number;
}

export interface CreateStorageVolumeRequest {
  pool_id: string;
  class: string;
  size_bytes: number;
  format?: string;
}

export interface StorageImageListResponse {
  items: StorageImage[];
}

export interface StorageImage {
  id: string;
  kind: string;
  display_name?: string;
  checksum_sha256?: string;
  size_bytes?: number;
}

export interface NetworkListResponse {
  items: Network[];
  first_run?: boolean;
  nics?: Record<string, unknown>[];
}

export interface Network {
  id: string;
  name: string;
  kind: string;
  status: string;
  danger?: string;
  bridge_name?: string;
  dhcp?: boolean;
}

export interface CreateNetworkRequest {
  name?: string;
  kind?: string;
  ipv4_cidr?: string;
  uplink_ifname?: string;
  confirm_ifname?: string;
  dry_run?: boolean;
}

export interface ApplyNetworkRequest {
  confirm_ifname?: string;
  uplink_ifname?: string;
  dry_run?: boolean;
}

export interface NetworkPreview {
  danger?: string;
  requires_confirm?: boolean;
  typed_ifname?: string;
  dry_run?: boolean;
}

export interface ConfirmationRequired {
  error: string;
  code?: string;
  danger?: string;
  typed_ifname?: string;
  confirm_token?: string;
  message?: string;
}

export interface ReservationListResponse {
  items: Reservation[];
}

export interface Reservation {
  id: string;
  mac: string;
  ipv4: string;
  hostname?: string;
}

export interface CreateReservationRequest {
  mac: string;
  ipv4: string;
  hostname?: string;
}

export interface WorkloadListResponse {
  items: Workload[];
  image_pins?: string[];
}

export interface Workload {
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
  pid?: number;
  unit_active?: boolean;
  migrate_ready?: boolean;
  ipv4?: string;
  autostart?: boolean;
  pending_restart?: boolean;
  firmware?: string;
  spec?: Record<string, unknown>;
}

export interface CreateWorkloadRequest {
  name: string;
  kind?: string;
  image_pin?: string;
  cpus?: number;
  memory_bytes?: number;
  pool_id?: string;
  network_id: string;
  volume_id?: string;
  privileged?: boolean;
  desired_power?: string;
  firmware?: string;
  autostart?: boolean;
  cloud_image_id?: string;
  iso_library_id?: string;
  nocloud?: Record<string, unknown>;
}

export interface UpdateWorkloadRequest {
  name?: string;
  cpus?: number;
  memory_bytes?: number;
  desired_power?: string;
  autostart?: boolean;
  firmware?: string;
}

export interface CloneWorkloadRequest {
  name?: string;
}

export interface CreateIOSessionRequest {
  cwd?: string;
  mode?: string;
}

export interface IOSession {
  id: string;
  target_kind: string;
  target_id: string;
  kind: string;
  cwd?: string;
  state: string;
  ticket?: string;
  ws_path?: string;
}

export interface FileEntry {
  name: string;
  type: string;
  size?: number;
  path?: string;
}

export interface FileListResponse {
  path?: string;
  entries: FileEntry[];
}

export interface FileMutationRequest {
  path?: string;
  dest_path?: string;
  mode?: number;
}

export interface FileMutationResponse {
  ok?: boolean;
  path?: string;
}

export interface StartLabQemuProtoRequest {
  workload_id?: string;
  pool_id?: string;
  volume_id?: string;
  size_bytes?: number;
  autostart?: boolean;
}

export interface LabQemuLastApplied {
  schema_version?: string;
  volume_id?: string;
  disk_path?: string;
  disk_format?: string;
  machine?: string;
  accel?: string;
  autostart?: boolean;
  memory_bytes?: number;
  cpus?: number;
}

export interface LabQemuProtoResponse {
  id?: string;
  name: string;
  kind: string;
  status: string;
  reason?: string;
  desired_power?: string;
  volume_id?: string;
  disk_path?: string;
  disk_format?: string;
  machine?: string;
  accel?: string;
  autostart?: boolean;
  cpus?: number;
  memory_bytes?: number;
  unit: string;
  unit_status?: string;
  observe_status?: string;
  unit_active?: boolean;
  running_as?: string;
  qmp?: string;
  serial_socket?: string;
  vnc_socket?: string;
  qga_socket?: string;
  last_applied?: LabQemuLastApplied;
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

export type ListStoragePoolsPath = "/api/v1/storage/pools";

export type GetStoragePoolPath = "/api/v1/storage/pools/{id}";

export type ListStorageVolumesPath = "/api/v1/storage/volumes";

export type GetStorageVolumePath = "/api/v1/storage/volumes/{id}";

export type ListStorageImagesPath = "/api/v1/storage/images";

export type GetStorageImagePath = "/api/v1/storage/images/{id}";

export type ListNetworksPath = "/api/v1/networks";

export type GetNetworkPath = "/api/v1/networks/{id}";

export type ApplyNetworkPath = "/api/v1/networks/{id}/apply";

export type ListNetworkReservationsPath = "/api/v1/networks/{id}/reservations";

export type ListWorkloadsPath = "/api/v1/workloads";

export type GetWorkloadPath = "/api/v1/workloads/{id}";

export type StartWorkloadPath = "/api/v1/workloads/{id}/start";

export type StopWorkloadPath = "/api/v1/workloads/{id}/stop";

export type RestartWorkloadPath = "/api/v1/workloads/{id}/restart";

export type ForceStopWorkloadPath = "/api/v1/workloads/{id}/force-stop";

export type DeleteWorkloadPath = "/api/v1/workloads/{id}/delete";

export type CloneWorkloadPath = "/api/v1/workloads/{id}/clone";

export type CreateNodeTerminalSessionPath = "/api/v1/nodes/{id}/terminal/sessions";

export type CreateWorkloadConsoleSessionPath = "/api/v1/workloads/{id}/console/sessions";

export type CreateWorkloadTerminalSessionPath = "/api/v1/workloads/{id}/terminal/sessions";

export type GetIOSessionPath = "/api/v1/io/sessions/{id}";

export type ConnectIOSessionPath = "/api/v1/io/sessions/{id}/ws";

export type ListNodeFilesPath = "/api/v1/nodes/{id}/files";

export type StatNodeFilePath = "/api/v1/nodes/{id}/files/stat";

export type DownloadNodeFilePath = "/api/v1/nodes/{id}/files/download";

export type UploadNodeFilePath = "/api/v1/nodes/{id}/files/upload";

export type MkdirNodeFilePath = "/api/v1/nodes/{id}/files/mkdir";

export type DeleteNodeFilePath = "/api/v1/nodes/{id}/files/delete";

export type MoveNodeFilePath = "/api/v1/nodes/{id}/files/move";

export type ListWorkloadFilesPath = "/api/v1/workloads/{id}/files";

export type StatWorkloadFilePath = "/api/v1/workloads/{id}/files/stat";

export type DownloadWorkloadFilePath = "/api/v1/workloads/{id}/files/download";

export type UploadWorkloadFilePath = "/api/v1/workloads/{id}/files/upload";

export type MkdirWorkloadFilePath = "/api/v1/workloads/{id}/files/mkdir";

export type DeleteWorkloadFilePath = "/api/v1/workloads/{id}/files/delete";

export type MoveWorkloadFilePath = "/api/v1/workloads/{id}/files/move";

export type GetLabQemuProtoPath = "/api/v1/lab/qemu-proto";

export type StopLabQemuProtoPath = "/api/v1/lab/qemu-proto/stop";

export type KillLabQemuProtoPath = "/api/v1/lab/qemu-proto/kill";
