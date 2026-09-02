/* Generated from api/openapi/nodal.v1.yaml. Do not edit by hand. */

export interface HealthResponse {
  status: "ok" | "starting";
  service: "ndl-control";
  setup_open?: boolean;
  tls_enabled?: boolean;
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
  aal?: number;
  mfa_enabled?: boolean;
  kind?: "person" | "service";
  ux_level: "guided" | "advanced" | "expert";
  expert_ack: boolean;
  expert_ack_at?: string;
}

export interface MePatchRequest {
  ux_level?: "guided" | "advanced" | "expert";
  expert_ack?: boolean;
}

export interface MFAChallengeResponse {
  mfa_required: true;
  mfa_challenge_id: string;
  mfa_token: string;
}

export interface CreateTokenRequest {
  name: string;
  permissions?: string[];
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
  support_tier?: string;
  host_platform?: string;
  role?: string;
  hostname?: string;
  revoked?: boolean;
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
  total_bytes?: number;
  metadata_percent?: number;
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
  vlans?: NetworkVLAN[];
  bonds?: NetworkBond[];
  policies?: NetworkPolicy[];
  overlays?: NetworkOverlay[];
}

export interface NetworkVLAN {
  id?: string;
  network_id?: string;
  name?: string;
  vlan_id?: number;
  parent_ifname?: string;
  access_ifname?: string;
  mode?: string;
  locator?: string;
  status?: string;
  reason?: string;
}

export interface NetworkBond {
  id?: string;
  name?: string;
  mode?: string;
  members?: string[];
  locator?: string;
  status?: string;
  reason?: string;
}

export interface NetworkPolicy {
  id?: string;
  name?: string;
  action?: string;
  src_workload_id?: string;
  dst_workload_id?: string;
  src_mac?: string;
  dst_mac?: string;
  status?: string;
  reason?: string;
}

export interface NetworkOverlay {
  id?: string;
  name?: string;
  vni?: number;
  locator?: string;
  status?: string;
  reason?: string;
}

export interface VLANCreateRequest {
  name?: string;
  network_id?: string;
  vlan_id: number;
  parent_ifname?: string;
  access_ifname?: string;
  mode?: string;
  confirm_ifname?: string;
}

export interface BondCreateRequest {
  name: string;
  mode?: string;
  members: string[];
  confirm_ifname?: string;
}

export interface PolicyCreateRequest {
  name: string;
  action?: string;
  src_workload_id: string;
  dst_workload_id: string;
}

export interface OverlayCreateRequest {
  name?: string;
  vni: number;
}

export interface WGPeer {
  id?: string;
  name?: string;
  role?: string;
  public_key?: string;
  listen_port?: number;
  address_cidr?: string;
  endpoint?: string;
  allowed_ips?: string;
  persistent_keepalive?: number;
  locator?: string;
  status?: string;
  reason?: string;
  last_handshake_unix?: number;
}

export interface WGStatusResponse {
  items: WGPeer[];
  nodes?: NodeSummary[];
  join?: string;
}

export interface WGPeerCreateRequest {
  name: string;
  endpoint?: string;
  local_address?: string;
  worker_address?: string;
  listen_port?: number;
}

export interface WGPeerCreateResponse {
  id: string;
  name?: string;
  role?: string;
  local?: WGPeer;
  worker?: WGPeer;
  warning?: string;
}

export interface ClusterInventoryResponse {
  id: string;
  name?: string;
  nodes: NodeSummary[];
  writer?: boolean;
  lease_holder?: string;
  lease_expires_at?: string;
}

export interface HAStatus {
  mode: "single-writer";
  writer: boolean;
  replica_status: "not_configured" | "unavailable";
  replica_endpoint?: string;
  fencing_mode: "operator";
  fencing_reason?: string;
  multi_master: boolean;
  reason?: string;
  fenced_holder?: string;
  fenced_at?: string;
  promoted_holder?: string;
  promoted_at?: string;
  lease_holder?: string;
  lease_expires_at?: string;
  lease_fenced?: boolean;
}

export interface ConfigureHAReplicaRequest {
  endpoint?: string;
  dsn?: string;
}

export interface RollingStep {
  id?: string;
  node_id: string;
  name?: string;
  ordinal: number;
  action: "drain" | "update";
  status?: string;
  reason?: string;
  update_operation_id?: string;
}

export interface RollingPlan {
  id: string;
  status: string;
  reason?: string;
  created_at?: string;
  finished_at?: string;
  steps: RollingStep[];
}

export interface RollingUpdatePreview {
  preview: RollingStep[];
  note?: string;
  plan?: RollingPlan;
}

export interface Feature {
  id: "vm" | "ct" | "oci" | "gpu" | "k8s" | "distributed_storage" | "ai";
  title: string;
  enabled: boolean;
  core: boolean;
  package?: string;
  package_status: "not_configured" | "unavailable" | "installed" | "removed";
  runtime_status: "not_started" | "installed";
  starts_runtime: boolean;
  kubelet_started: boolean;
  workload_count: number;
  tiny_node?: boolean;
  confirm?: string;
  reason?: string;
}

export interface FeatureList {
  items: Feature[];
  base_install: "light";
  gpu_optional: boolean;
  reason?: string;
}

export interface StoreApp {
  id: string;
  name: string;
  version: string;
  class: "community" | "verified" | "official";
  title?: string;
  summary?: string;
  unsigned?: boolean;
  warning?: string;
  gpu_optional?: boolean;
  deployment_kind?: string;
  image?: string;
  hooks?: Record<string, unknown>;
  ai_actions?: Record<string, unknown>[];
}

export interface StoreAppList {
  items: StoreApp[];
}

export interface StoreImportRequest {
  manifest: string;
}

export interface StoreInstallRequest {
  name?: string;
  pool_id?: string;
  network_id?: string;
  node_id?: string;
  cpus?: number;
  memory_bytes?: number;
  gpu_id?: string;
}

export interface StoreInstallation {
  id: string;
  package_id: string;
  status: "queued" | "running" | "installed" | "failed" | "rolled_back";
  stack_id?: string;
  workload_id?: string;
  kubelet_started?: boolean;
  reason?: string;
  warning?: string;
  node_id?: string;
  finished_at?: string;
  name?: string;
  class?: string;
}

export interface StoreInstallationList {
  items: StoreInstallation[];
}

export interface JoinTokenCreateRequest {
  ttl_seconds?: number;
}

export interface JoinTokenCreateResponse {
  id: string;
  token: string;
  expires_at: string;
  warning?: string;
}

export interface ClusterJoinRequest {
  token: string;
  hostname: string;
  host_platform?: Record<string, unknown>;
}

export interface ClusterJoinResponse {
  id: string;
  cluster_id: string;
  name?: string;
  hostname?: string;
  role: string;
  token_id?: string;
  ca_cert?: string;
  node_cert?: string;
  node_key?: string;
  warning?: string;
}

export interface ClusterNodeRevokeResponse {
  id: string;
  revoked: boolean;
}

export interface PlacementPreviewRequest {
  placement?: string;
  node_id?: string;
  require_gpu?: boolean;
}

export interface PlacementPreviewResponse {
  node_id: string;
  name?: string;
  role?: string;
  apply_local?: boolean;
}

export interface NodeGroupListResponse {
  items: NodeGroup[];
}

export interface NodeGroupCreateRequest {
  name: string;
  node_ids?: string[];
}

export interface NodeGroup {
  id: string;
  name: string;
  members?: string[];
}

export interface NodeMaintainResponse {
  id: string;
  maintenance?: boolean;
  warning?: string;
}

export interface NodeMaintainExitResponse {
  id: string;
  maintenance?: boolean;
}

export interface OpenClusterSessionRequest {
  peer_id: string;
  node_id?: string;
  cluster_id?: string;
  listen_addr?: string;
  wg_public_key?: string;
  handshake_unix?: number;
  pairing_token: string;
}

export interface OpenClusterSessionResponse {
  session_id?: string;
  accepted?: boolean;
  node_id?: string;
  status?: string;
  reason?: string;
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
  health?: Record<string, unknown>;
  unit?: string;
}

export interface CreateWorkloadRequest {
  name: string;
  kind?: string;
  image_pin?: string;
  cpus?: number;
  memory_bytes?: number;
  pool_id?: string;
  network_id?: string;
  volume_id?: string;
  volume_ids?: string[];
  registry_id?: string;
  ports?: Record<string, unknown>[];
  env?: Record<string, unknown>[];
  health?: Record<string, unknown>;
  privileged?: boolean;
  desired_power?: string;
  firmware?: string;
  autostart?: boolean;
  cloud_image_id?: string;
  iso_library_id?: string;
  nocloud?: Record<string, unknown>;
  placement?: string;
  node_id?: string;
  node_group_id?: string;
  require_gpu?: boolean;
  require_storage_class?: string;
  affinity_workload_id?: string;
  anti_affinity_workload_id?: string;
}

export interface Registry {
  id: string;
  name: string;
  url: string;
  insecure?: boolean;
  has_credentials?: boolean;
  status?: string;
  created_at?: string;
}

export interface RegistryListResponse {
  items: Registry[];
}

export interface CreateRegistryRequest {
  name: string;
  url: string;
  username?: string;
  password?: string;
  insecure?: boolean;
}

export interface Stack {
  id: string;
  name: string;
  status: string;
  created_at?: string;
  updated_at?: string;
  has_source_compose?: boolean;
  desired?: Record<string, unknown>;
  members?: StackMember[];
}

export interface StackMember {
  id: string;
  service_name: string;
  status: string;
  sort_order?: number;
  workload_id?: string;
  reason?: string;
  desired?: Record<string, unknown>;
  workload?: Record<string, unknown>;
}

export interface StackListResponse {
  items: Stack[];
}

export interface CreateStackRequest {
  name: string;
}

export interface ImportStackRequest {
  name: string;
  compose: string;
  pool_id?: string;
  network_id?: string;
  registry_id?: string;
  volume_map?: Record<string, unknown>;
  apply?: boolean;
}

export interface PatchStackRequest {
  name?: string;
}

export interface PatchStackMemberRequest {
  name?: string;
  image_pin?: string;
  env?: Record<string, unknown>[];
  ports?: Record<string, unknown>[];
  volumes?: Record<string, unknown>[];
  privileged?: boolean;
  command?: string[];
  health?: Record<string, unknown>;
  network_id?: string;
  registry_id?: string;
  cpus?: number;
  memory_bytes?: number;
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

export interface MigrateWorkloadRequest {
  dest_node_id?: string;
  mode?: "live" | "offline";
}

export interface MigrateWorkloadResponse {
  id?: string;
  workload_id?: string;
  operation_id?: string;
  source_node_id?: string;
  dest_node_id?: string;
  mode?: string;
  state?: string;
  source_running?: boolean;
  dest_running?: boolean;
  reason?: string;
  epoch?: number;
}

export interface ImportWorkloadRequest {
  name?: string;
  library_id: string;
  pool_id?: string;
  network_id: string;
  firmware?: string;
  cpus?: number;
  memory_bytes?: number;
}

export interface ExportWorkloadRequest {
  display_name?: string;
}

export interface AttachUSBRequest {
  address: string;
}

export interface AttachPCIRequest {
  pci: string;
}

export interface CreateTemplateRequest {
  workload_id: string;
  name?: string;
}

export interface VMTemplate {
  id: string;
  name: string;
  source_workload_id?: string;
  snapshot_id?: string;
  created_at?: string;
}

export interface TemplateListResponse {
  items: VMTemplate[];
}

export interface USBListResponse {
  items: Record<string, unknown>[];
}

export interface PCIListResponse {
  items: Record<string, unknown>[];
  iommu?: Record<string, unknown>;
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

export interface CertificateStatus {
  enabled: boolean;
  mode: "self_signed" | "imported" | "acme" | "";
  common_name: string;
  sans: string[];
  fingerprint: string;
  not_before?: string;
  not_after?: string;
  acme_directory?: string;
  acme_email?: string;
  acme_status: "not_configured" | "pending" | "issued" | "failed";
  next_renewal_at?: string;
  tls_listen?: string;
  http_listen?: string;
  https_url?: string;
  trust_note?: string;
  restart_required?: boolean;
}

export interface GenerateCertRequest {
  common_name: string;
  sans: string[];
}

export interface ImportCertRequest {
  cert_pem: string;
  key_pem: string;
}

export interface AcmeCertRequest {
  directory: string;
  email: string;
  domain: string;
}

export interface Snapshot {
  id: string;
  workload_id: string;
  volume_id: string;
  name: string;
  purpose_tag: string;
  mechanism: string;
  backend_ref: string;
  parent_id: string;
  chain_depth: number;
  status: string;
  created_at: string;
}

export interface SnapshotCapability {
  supported: boolean;
  mechanism: "qcow2-overlay" | "";
  chain_max: number;
  chain_depth: number;
  reason: string;
}

export interface SnapshotListResponse {
  items: Snapshot[];
  capability: SnapshotCapability;
}

export interface CreateSnapshotRequest {
  name: string;
}

export interface BackupTarget {
  id: string;
  name: string;
  kind: "local" | "nfs" | "smb" | "s3" | "r2" | "aws" | "b2" | "minio";
  locator: string;
  status: "available" | "unavailable" | "not_configured";
  username?: string;
  endpoint?: string;
  region?: string;
  bucket?: string;
  prefix?: string;
  no_check_bucket?: boolean;
  has_encryption_key?: boolean;
}

export interface BackupTargetListResponse {
  items: BackupTarget[];
}

export interface CreateBackupTargetRequest {
  name: string;
  kind: "local" | "nfs" | "smb" | "s3" | "r2" | "aws" | "b2" | "minio";
  locator?: string;
  username?: string;
  password?: string;
  endpoint?: string;
  region?: string;
  bucket?: string;
  prefix?: string;
  no_check_bucket?: boolean;
  encryption_key?: string;
}

export interface BackupPolicy {
  id: string;
  name: string;
  workload_id: string;
  target_id: string;
  schedule: "nightly";
  keep_daily: number;
  keep_weekly: number;
  keep_monthly: number;
  last_run_at?: string;
}

export interface BackupPolicyListResponse {
  items: BackupPolicy[];
}

export interface CreateBackupPolicyRequest {
  name: string;
  workload_id: string;
  target_id: string;
  schedule: "nightly";
  keep_daily: number;
  keep_weekly: number;
  keep_monthly: number;
}

export interface BackupRun {
  id: string;
  policy_id?: string;
  target_id: string;
  workload_id: string;
  snapshot_id?: string;
  status: "running" | "succeeded" | "failed";
  error?: string;
  restored_workload_id?: string;
  started_at: string;
  finished_at?: string;
  transferred_bytes?: number;
  incremental?: boolean;
}

export interface BackupRunListResponse {
  items: BackupRun[];
}

export interface RunBackupRequest {
  workload_id: string;
  target_id: string;
  policy_id?: string;
}

export interface BackupArtifact {
  id: string;
  run_id: string;
  workload_id: string;
  checksum_sha256: string;
  size_bytes: number;
  locator: string;
  format: string;
  created_at: string;
  encrypted?: boolean;
  transferred_bytes?: number;
  object_key?: string;
  parent_artifact_id?: string;
  verify_status?: "unverified" | "verified" | "failed";
  verify_error?: string;
  last_tested_at?: string;
  throwaway_workload_id?: string;
  locality?: "local" | "object" | "pull";
  pull_url?: string;
}

export interface BackupArtifactListResponse {
  items: BackupArtifact[];
}

export interface RestoreBackupRequest {
  mode: "new" | "replace";
  target_node_id?: string;
}

export interface DRExportNode {
  id: string;
  name: string;
  role: string;
  hostname?: string;
  revoked?: boolean;
  revoked_at?: string;
}

export interface DRExportWorkload {
  id: string;
  name: string;
  kind: string;
  node_id: string;
  owner_node_id?: string;
  desired_node_id?: string;
  status?: string;
}

export interface DRExportTarget {
  id: string;
  name: string;
  kind: string;
  status: string;
  locator?: string;
  endpoint?: string;
  region?: string;
  bucket?: string;
  prefix?: string;
}

export interface DRExportResponse {
  cluster_id: string;
  exported_at: string;
  nodes: DRExportNode[];
  workloads: DRExportWorkload[];
  artifacts: BackupArtifact[];
  targets: DRExportTarget[];
}

export interface VerifyBackupRequest {
  mode?: "open" | "throwaway";
}

export interface RestoreBackupFileRequest {
  path: string;
}

export interface RestoreBackupFileResponse {
  artifact_id: string;
  path: string;
  size_bytes: number;
  sha256: string;
  content_base64: string;
}

export interface UpdatePackage {
  name: "ndl-control" | "ndl-agent" | "ndl-ui" | "nodal" | "nodalctl";
  version: string;
  status: "current" | "update_available" | "unsupported" | "not_configured" | "not_reported";
}

export interface UpdateOperation {
  id: string;
  action: "check" | "preflight" | "checkpoint" | "apply" | "rollback";
  status: "running" | "succeeded" | "failed" | "unsupported";
  dry_run: boolean;
  error?: string;
  started_at: string;
  finished_at?: string;
  packages?: ("ndl-control" | "ndl-agent" | "ndl-ui" | "nodal" | "nodalctl")[];
}

export interface UpdateStatus {
  channel: "stable";
  host_supported: boolean;
  host_reason: string;
  packages: UpdatePackage[];
  last_operation?: UpdateOperation;
}

export interface UpdatePreviewItem {
  name: "ndl-control" | "ndl-agent" | "ndl-ui" | "nodal" | "nodalctl";
  current_version: string;
  candidate_version: string;
  action: "hold" | "upgrade" | "unsupported";
}

export interface UpdatePreview {
  channel: "stable";
  items: UpdatePreviewItem[];
  changelog: string;
  dry_run: true;
}

export interface UpdatePreflightCheck {
  name: string;
  status: "ok" | "warning" | "failed" | "unsupported";
  detail: string;
}

export interface UpdatePreflight {
  ok: boolean;
  checks: UpdatePreflightCheck[];
  kernel_ok: boolean;
  zfs_ok: boolean;
  nvidia_ok: boolean;
}

export interface UpdateCheckpoint {
  id: string;
  locator: string;
  postgres_dump: boolean;
  status: "succeeded" | "failed" | "unsupported";
}

export interface MFAVerifyRequest {
  mfa_challenge_id: string;
  mfa_token: string;
  code: string;
}

export interface MFAConfirmRequest {
  code: string;
}

export interface MFAStatus {
  enabled: boolean;
  kind: string;
}

export interface MFAEnrollResponse {
  kind: string;
  secret: string;
  otpauth_url: string;
  recovery_codes: string[];
  enabled: boolean;
}

export interface AuditListResponse {
  items: AuditEvent[];
}

export interface AuditEvent {
  id: string;
  action: string;
  result: string;
  created_at: string;
  actor_user_id?: string;
}

export interface GroupListResponse {
  items: Group[];
}

export interface Group {
  id: string;
  name: string;
  member_ids?: string[];
}

export interface CreateGroupRequest {
  name: string;
}

export interface AddGroupMemberRequest {
  user_id: string;
}

export interface BindGroupRoleRequest {
  role: "operator" | "viewer";
}

export interface ServicePrincipalListResponse {
  items: ServicePrincipal[];
}

export interface ServicePrincipal {
  id: string;
  name: string;
  user_id: string;
  kind: "service";
}

export interface CreateServicePrincipalRequest {
  name: string;
}

export interface ServicePrincipalCreated {
  id: string;
  name: string;
  user_id: string;
  token: string;
  kind: "service";
}

export interface GPUListResponse {
  items: GPU[];
  iommu?: Record<string, unknown>;
  runtime?: GPURuntime;
  acs_override: string;
  default_devices: string[];
  note: string;
}

export interface GPU {
  id: string;
  vendor?: string;
  model?: string;
  pci: string;
  driver?: string;
  iommu_group?: string;
  hint?: string;
  group_members?: GPUGroupMember[];
  assignments?: GPUAssignment[];
}

export interface GPUGroupMember {
  pci: string;
  class?: string;
  kind: string;
  driver?: string;
}

export interface GPUAssignment {
  id: string;
  gpu_id: string;
  workload_id: string;
  mode: string;
  exclusive: boolean;
  iommu_group?: string;
  pci_devices?: string[];
  device_nodes?: string[];
  status: string;
  reason?: string;
}

export interface GPUAssignmentListResponse {
  items: GPUAssignment[];
}

export interface GPUAssignRequest {
  gpu_id: string;
  workload_id: string;
  mode: string;
  exclusive?: boolean;
  acs_override?: boolean;
}

export interface GPUUnassignRequest {
  id: string;
}

export interface GPUInstallRequest {
  dry_run?: boolean;
}

export interface GPURuntime {
  host_supported: boolean;
  status: string;
  reason?: string;
  cuda: string;
  rocm: string;
  packages?: string[];
  argv?: string[];
  flags?: Record<string, unknown>;
}

export interface GPUAssignResult {
  status?: string;
  reason?: string;
  pci_devices?: string[];
  device_nodes?: string[];
  argv?: string[];
  cuda?: string;
  rocm?: string;
  host_supported?: boolean;
  packages?: string[];
}

export interface ZFSRuntime {
  backend?: string;
  incremental_send?: boolean;
  snapshots?: boolean;
  directory_default?: boolean;
  force_import?: string;
  host_supported?: boolean;
  status?: string;
  reason?: string;
  packages?: string[];
  argv?: string[];
}

export interface ZFSImportRequest {
  guid: string;
  name?: string;
  force?: boolean;
}

export interface ZFSCreateRequest {
  name: string;
  disks: string[];
  force?: boolean;
}

export interface LVMRuntime {
  backend?: string;
  incremental_send?: boolean;
  snapshots?: boolean;
  directory_default?: boolean;
  vgexport?: string;
  host_supported?: boolean;
  status?: string;
  reason?: string;
  packages?: string[];
  argv?: string[];
}

export interface LVMCreateRequest {
  name: string;
  disks: string[];
}

export interface DatastoreRuntime {
  nfs?: boolean;
  smb?: boolean;
  iscsi?: boolean;
  incremental_send?: boolean;
  directory_default?: boolean;
  passwords_in_unit_files?: boolean;
  host_supported?: boolean;
  status?: string;
  reason?: string;
  packages?: string[];
}

export interface NFSCreateRequest {
  name: string;
  locator: string;
}

export interface SMBCreateRequest {
  name: string;
  locator: string;
  username?: string;
  password?: string;
}

export interface ISCSICreateRequest {
  name: string;
  iqn: string;
  locator?: string;
  portal: string;
}

export interface LogsResponse {
  status: string;
  unit?: string;
  lines: string[];
  message?: string;
}

export interface SmartResponse {
  status: string;
  stale?: boolean;
  items: Record<string, unknown>[];
}

export interface CapacityResponse {
  status: string;
  message?: string;
  samples?: number;
  last_bytes?: number;
  hours_to_zero?: number;
}

export interface TimelineResponse {
  items: Record<string, unknown>[];
  from?: string;
  to?: string;
}

export interface AlertListResponse {
  items: AlertRule[];
}

export interface AlertRule {
  id: string;
  name: string;
  metric: string;
  op: string;
  threshold: number;
  for_minutes?: number;
  enabled: boolean;
  last_fired_at?: string;
  created_at?: string;
}

export interface AlertCreateRequest {
  name: string;
  metric: string;
  op: string;
  threshold: number;
  for_minutes?: number;
}

export interface ChannelListResponse {
  items: NotificationChannel[];
}

export interface NotificationChannel {
  id: string;
  name: string;
  kind: string;
  status: string;
  webhook_configured?: boolean;
  smtp_host?: string;
  smtp_port?: number;
  created_at?: string;
}

export interface ChannelCreateRequest {
  name: string;
  kind: string;
  url?: string;
  smtp_host?: string;
  smtp_port?: number;
  smtp_from?: string;
  smtp_username?: string;
  smtp_password?: string;
}

export interface WorkloadGuestResponse {
  workload_id: string;
  qemu_ga: GuestChannelState;
  nodal_ga: GuestChannelState;
  guest_os?: string;
  guest_arch?: string;
  ipv4?: string[];
  observed_at: string;
  install?: GuestInstallHints;
}

export interface GuestChannelState {
  state: "ok" | "not_installed" | "stale" | "unavailable";
  version?: string;
  reason?: string;
}

export interface GuestInstallHints {
  linux?: string;
  windows?: string;
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

export type GetNodeLogsPath = "/api/v1/nodes/{id}/logs";

export type GetNodeSmartPath = "/api/v1/nodes/{id}/smart";

export type GetNodeCapacityPath = "/api/v1/nodes/{id}/capacity";

export type GetWorkloadLogsPath = "/api/v1/workloads/{id}/logs";

export type GetTimelinePath = "/api/v1/timeline";

export type ListAlertsPath = "/api/v1/alerts";

export type ListAlertChannelsPath = "/api/v1/alerts/channels";

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

export type CreateNetworkVlanPath = "/api/v1/networks/vlans";

export type CreateNetworkBondPath = "/api/v1/networks/bonds";

export type CreateNetworkPolicyPath = "/api/v1/networks/policies";

export type ApplyNetworkPolicyPath = "/api/v1/networks/policies/{id}/apply";

export type CreateNetworkOverlayPath = "/api/v1/networks/overlays";

export type ListWireGuardPath = "/api/v1/cluster/wg";

export type CreateWireGuardPeerPath = "/api/v1/cluster/wg/peers";

export type OpenClusterSessionPath = "/api/v1/cluster/sessions";

export type GetClusterPath = "/api/v1/cluster";

export type GetClusterHAPath = "/api/v1/cluster/ha";

export type ConfigureHAReplicaPath = "/api/v1/cluster/ha/replica";

export type FenceClusterHAPath = "/api/v1/cluster/ha/fence";

export type PromoteClusterHAPath = "/api/v1/cluster/ha/promote";

export type GetClusterUpdatePath = "/api/v1/cluster/update";

export type ListFeaturesPath = "/api/v1/features";

export type EnableFeaturePath = "/api/v1/features/{id}/enable";

export type DisableFeaturePath = "/api/v1/features/{id}/disable";

export type ListStoreAppsPath = "/api/v1/store/apps";

export type ImportStoreAppPath = "/api/v1/store/apps/import";

export type GetStoreAppPath = "/api/v1/store/apps/{id}";

export type InstallStoreAppPath = "/api/v1/store/apps/{id}/install";

export type ListStoreInstallationsPath = "/api/v1/store/installations";

export type CreateJoinTokenPath = "/api/v1/cluster/join-tokens";

export type JoinClusterPath = "/api/v1/cluster/join";

export type RevokeClusterNodePath = "/api/v1/cluster/nodes/{id}/revoke";

export type PreviewPlacementPath = "/api/v1/placement/preview";

export type ListNodeGroupsPath = "/api/v1/node-groups";

export type MaintainNodePath = "/api/v1/nodes/{id}/maintain";

export type ExitNodeMaintenancePath = "/api/v1/nodes/{id}/maintain/exit";

export type ListWorkloadsPath = "/api/v1/workloads";

export type GetWorkloadPath = "/api/v1/workloads/{id}";

export type GetWorkloadGuestPath = "/api/v1/workloads/{id}/guest";

export type StartWorkloadPath = "/api/v1/workloads/{id}/start";

export type StopWorkloadPath = "/api/v1/workloads/{id}/stop";

export type RestartWorkloadPath = "/api/v1/workloads/{id}/restart";

export type ForceStopWorkloadPath = "/api/v1/workloads/{id}/force-stop";

export type DeleteWorkloadPath = "/api/v1/workloads/{id}/delete";

export type GetWorkloadMigratePath = "/api/v1/workloads/{id}/migrate";

export type CloneWorkloadPath = "/api/v1/workloads/{id}/clone";

export type ImportWorkloadPath = "/api/v1/workloads/import";

export type ExportWorkloadPath = "/api/v1/workloads/{id}/export";

export type AttachWorkloadUSBPath = "/api/v1/workloads/{id}/usb";

export type AttachWorkloadPCIPath = "/api/v1/workloads/{id}/pci";

export type ListTemplatesPath = "/api/v1/templates";

export type DeployTemplatePath = "/api/v1/templates/{id}/deploy";

export type ListNodeUSBPath = "/api/v1/nodes/{id}/usb";

export type ListNodePCIPath = "/api/v1/nodes/{id}/pci";

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

export type ListWorkloadSnapshotsPath = "/api/v1/workloads/{id}/snapshots";

export type FlattenWorkloadSnapshotsPath = "/api/v1/workloads/{id}/snapshots/flatten";

export type RollbackSnapshotPath = "/api/v1/snapshots/{id}/rollback";

export type ListBackupTargetsPath = "/api/v1/backups/targets";

export type ListBackupPoliciesPath = "/api/v1/backups/policies";

export type ListBackupRunsPath = "/api/v1/backups/runs";

export type ListBackupArtifactsPath = "/api/v1/backups/artifacts";

export type ExportBackupDRPath = "/api/v1/backups/dr-export";

export type RunBackupPath = "/api/v1/backups/run";

export type RestoreBackupArtifactPath = "/api/v1/backups/artifacts/{id}/restore";

export type VerifyBackupArtifactPath = "/api/v1/backups/artifacts/{id}/verify";

export type RestoreBackupFilePath = "/api/v1/backups/artifacts/{id}/restore-file";

export type GetCertsPath = "/api/v1/certs";

export type GenerateCertPath = "/api/v1/certs/generate";

export type ImportCertPath = "/api/v1/certs/import";

export type AcmeCertPath = "/api/v1/certs/acme";

export type GetUpdatesPath = "/api/v1/updates";

export type CheckUpdatesPath = "/api/v1/updates/check";

export type PreflightUpdatesPath = "/api/v1/updates/preflight";

export type CheckpointUpdatesPath = "/api/v1/updates/checkpoint";

export type ApplyUpdatesPath = "/api/v1/updates/apply";

export type RollbackUpdatesPath = "/api/v1/updates/rollback";

export type VerifyMfaPath = "/api/v1/auth/mfa/verify";

export type GetMfaPath = "/api/v1/mfa";

export type EnrollMfaPath = "/api/v1/mfa/enroll";

export type ConfirmMfaPath = "/api/v1/mfa/confirm";

export type ListAuditPath = "/api/v1/audit";

export type ListGroupsPath = "/api/v1/groups";

export type AddGroupMemberPath = "/api/v1/groups/{id}/members";

export type BindGroupRolePath = "/api/v1/groups/{id}/roles";

export type ListServicePrincipalsPath = "/api/v1/service-principals";

export type GetZfsRuntimePath = "/api/v1/storage/zfs";

export type ImportZfsPath = "/api/v1/storage/zfs/import";

export type CreateZfsPath = "/api/v1/storage/zfs/create";

export type GetLvmRuntimePath = "/api/v1/storage/lvm";

export type CreateLvmPath = "/api/v1/storage/lvm/create";

export type GetDatastoreRuntimePath = "/api/v1/storage/datastores";

export type CreateNfsPath = "/api/v1/storage/nfs";

export type CreateSmbPath = "/api/v1/storage/smb";

export type CreateIscsiPath = "/api/v1/storage/iscsi";

export type ListRegistriesPath = "/api/v1/registries";

export type ListStacksPath = "/api/v1/stacks";

export type ImportStackComposePath = "/api/v1/stacks/import";

export type GetStackPath = "/api/v1/stacks/{id}";

export type ApplyStackPath = "/api/v1/stacks/{id}/apply";

export type PatchStackMemberPath = "/api/v1/stacks/{id}/members/{memberId}";

export type ListGpusPath = "/api/v1/gpus";

export type GetGpuRuntimePath = "/api/v1/gpus/runtime";

export type InstallGpuRuntimePath = "/api/v1/gpus/runtime/install";

export type AssignGpuPath = "/api/v1/gpus/assign";

export type UnassignGpuPath = "/api/v1/gpus/unassign";

export type ListWorkloadGpusPath = "/api/v1/workloads/{id}/gpus";
