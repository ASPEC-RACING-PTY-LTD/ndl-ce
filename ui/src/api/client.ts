import type { HealthResponse } from "../generated/openapi";
import type {
  ErrorResponse,
  LoginRequest,
  MeResponse,
  SetupClaimRequest,
  SetupStatusResponse,
} from "./types";

const API_PREFIX = "/api/v1";

export class ApiError extends Error {
  readonly status: number;

  constructor(status: number, message: string) {
    super(message);
    this.name = "ApiError";
    this.status = status;
  }
}

async function readErrorMessage(res: Response): Promise<string> {
  try {
    const body = (await res.json()) as ErrorResponse;
    if (body && typeof body.error === "string" && body.error.length > 0) {
      return body.error;
    }
  } catch {
    // Response was not JSON.
  }
  return `Request failed (${res.status})`;
}

async function request(path: string, init: RequestInit = {}): Promise<Response> {
  const headers = new Headers(init.headers);
  if (init.body !== undefined && !headers.has("Content-Type")) {
    headers.set("Content-Type", "application/json");
  }

  return fetch(`${API_PREFIX}${path}`, {
    ...init,
    credentials: "include",
    headers,
  });
}

async function readJson<T>(res: Response): Promise<T> {
  if (!res.ok) {
    throw new ApiError(res.status, await readErrorMessage(res));
  }
  return (await res.json()) as T;
}

export async function getHealth(): Promise<HealthResponse> {
  return readJson<HealthResponse>(await request("/health"));
}

export async function getSetupStatus(): Promise<SetupStatusResponse> {
  return readJson<SetupStatusResponse>(await request("/setup/status"));
}

export async function claimSetup(body: SetupClaimRequest): Promise<MeResponse> {
  return readJson<MeResponse>(
    await request("/setup/claim", {
      method: "POST",
      body: JSON.stringify(body),
    }),
  );
}

export async function login(body: LoginRequest): Promise<MeResponse | import("../generated/openapi").MFAChallengeResponse> {
  const data = await readJson<MeResponse | import("../generated/openapi").MFAChallengeResponse>(
    await request("/auth/login", {
      method: "POST",
      body: JSON.stringify(body),
    }),
  );
  return data;
}

export async function logout(): Promise<void> {
  const res = await request("/auth/logout", { method: "POST" });
  if (res.status === 204) {
    return;
  }
  if (!res.ok) {
    throw new ApiError(res.status, await readErrorMessage(res));
  }
}

export async function getMe(): Promise<MeResponse | null> {
  const res = await request("/me");
  if (res.status === 401) {
    return null;
  }
  return readJson<MeResponse>(res);
}

export async function patchMe(body: {
  ux_level?: "guided" | "advanced" | "expert";
  expert_ack?: boolean;
}): Promise<MeResponse> {
  return readJson<MeResponse>(
    await request("/me", {
      method: "PATCH",
      body: JSON.stringify(body),
    }),
  );
}

export async function listNodes(): Promise<import("./phase2").NodeSummary[]> {
  const body = await readJson<{ items: import("./phase2").NodeSummary[] }>(await request("/nodes"));
  return body.items ?? [];
}

export async function getNode(id: string): Promise<import("./phase2").NodeSummary> {
  return readJson(await request(`/nodes/${id}`));
}

export type WGPeer = {
  id: string;
  name?: string;
  role?: string;
  public_key?: string;
  listen_port?: number;
  address_cidr?: string;
  endpoint?: string;
  locator?: string;
  status?: string;
  reason?: string;
  last_handshake_unix?: number;
};

export type WGStatus = {
  items?: WGPeer[];
  nodes?: import("./phase2").NodeSummary[];
  join?: string;
};

export type WGPeerCreateResult = {
  id: string;
  name?: string;
  local?: WGPeer;
  worker?: WGPeer;
  node?: import("./phase2").NodeSummary;
  worker_private_key?: string;
  pairing_token?: string;
  desired?: Record<string, unknown>;
  warning?: string;
};

export async function listWG(): Promise<WGStatus> {
  return readJson(await request("/cluster/wg"));
}

export async function createWGPeer(body: {
  name: string;
  endpoint?: string;
  local_address?: string;
  worker_address?: string;
}): Promise<WGPeerCreateResult> {
  return readJson(await request("/cluster/wg/peers", { method: "POST", body: JSON.stringify(body) }));
}

export type ClusterNode = import("./phase2").NodeSummary & {
  hostname?: string;
  revoked?: boolean;
  revoked_at?: string;
};

export type ClusterInventory = {
  id: string;
  name?: string;
  nodes?: ClusterNode[];
  writer?: boolean;
  lease_holder?: string;
};

export type JoinTokenCreateResult = {
  id: string;
  token: string;
  expires_at: string;
  warning?: string;
};

export async function getCluster(): Promise<ClusterInventory> {
  return readJson(await request("/cluster"));
}

export async function createJoinToken(): Promise<JoinTokenCreateResult> {
  return readJson(await request("/cluster/join-tokens", { method: "POST", body: JSON.stringify({}) }));
}

export async function revokeClusterNode(id: string): Promise<{ id: string; revoked: boolean }> {
  return readJson(await request(`/cluster/nodes/${id}/revoke`, { method: "POST", body: JSON.stringify({}) }));
}

export async function getClusterHA(): Promise<import("../generated/openapi").HAStatus> {
  return readJson(await request("/cluster/ha"));
}

export async function configureHAReplica(
  body: import("../generated/openapi").ConfigureHAReplicaRequest,
): Promise<import("../generated/openapi").HAStatus> {
  return readJson(await request("/cluster/ha/replica", { method: "POST", body: JSON.stringify(body) }));
}

export async function fenceClusterHA(): Promise<import("../generated/openapi").HAStatus> {
  return readJson(
    await request("/cluster/ha/fence", {
      method: "POST",
      headers: { "X-Nodal-Confirm": "fence" },
      body: JSON.stringify({}),
    }),
  );
}

export async function promoteClusterHA(): Promise<import("../generated/openapi").HAStatus> {
  return readJson(
    await request("/cluster/ha/promote", {
      method: "POST",
      headers: { "X-Nodal-Confirm": "promote" },
      body: JSON.stringify({}),
    }),
  );
}

export async function getClusterUpdate(): Promise<import("../generated/openapi").RollingUpdatePreview> {
  return readJson(await request("/cluster/update"));
}

export async function runClusterUpdate(): Promise<import("../generated/openapi").RollingPlan> {
  return readJson(
    await request("/cluster/update", {
      method: "POST",
      headers: { "X-Nodal-Confirm": "cluster-update" },
      body: JSON.stringify({}),
    }),
  );
}

export async function listFeatures(): Promise<import("../generated/openapi").FeatureList> {
  return readJson(await request("/features"));
}

export async function getKubernetes(): Promise<import("../generated/openapi").KubernetesStatus> {
  return readJson(await request("/kubernetes"));
}

export async function startKubernetes(): Promise<import("../generated/openapi").KubernetesStatus> {
  return readJson(
    await request("/kubernetes/start", {
      method: "POST",
      headers: { "X-Nodal-Confirm": "start-kubelet" },
      body: JSON.stringify({}),
    }),
  );
}

export async function stopKubernetes(): Promise<import("../generated/openapi").KubernetesStatus> {
  return readJson(
    await request("/kubernetes/stop", {
      method: "POST",
      body: JSON.stringify({}),
    }),
  );
}

export async function enableFeature(
  id: string,
  confirm?: string,
): Promise<import("../generated/openapi").Feature> {
  const headers: Record<string, string> = {};
  if (confirm) {
    headers["X-Nodal-Confirm"] = confirm;
  }
  return readJson(
    await request(`/features/${id}/enable`, {
      method: "POST",
      headers,
      body: JSON.stringify({}),
    }),
  );
}

export async function disableFeature(
  id: string,
  confirm?: string,
): Promise<import("../generated/openapi").Feature> {
  const headers: Record<string, string> = {};
  if (confirm) {
    headers["X-Nodal-Confirm"] = confirm;
  }
  return readJson(
    await request(`/features/${id}/disable`, {
      method: "POST",
      headers,
      body: JSON.stringify({}),
    }),
  );
}

export async function listStoreApps(): Promise<import("../generated/openapi").StoreAppList> {
  return readJson(await request("/store/apps"));
}

export async function getStoreApp(id: string): Promise<import("../generated/openapi").StoreApp> {
  return readJson(await request(`/store/apps/${id}`));
}

export async function installStoreApp(
  id: string,
  body: import("../generated/openapi").StoreInstallRequest,
): Promise<import("../generated/openapi").StoreInstallation> {
  return readJson(
    await request(`/store/apps/${id}/install`, {
      method: "POST",
      body: JSON.stringify(body),
    }),
  );
}

export async function verifyStoreApp(id: string): Promise<import("../generated/openapi").StoreVerifyResult> {
  return readJson(
    await request(`/store/apps/${id}/verify`, {
      method: "POST",
      body: JSON.stringify({}),
    }),
  );
}

export async function getStoreAppScans(id: string): Promise<import("../generated/openapi").StoreScanList> {
  return readJson(await request(`/store/apps/${id}/scans`));
}

export async function getStorePolicy(): Promise<import("../generated/openapi").StorePolicy> {
  return readJson(await request("/store/policy"));
}

export async function setStorePolicy(
  body: import("../generated/openapi").StorePolicy,
): Promise<import("../generated/openapi").StorePolicy> {
  return readJson(
    await request("/store/policy", {
      method: "PUT",
      body: JSON.stringify(body),
    }),
  );
}

export async function listAutomationPolicies(): Promise<import("../generated/openapi").AutomationPolicyList> {
  return readJson(await request("/policies"));
}

export async function listAutomationPolicyRuns(): Promise<import("../generated/openapi").AutomationPolicyRunList> {
  return readJson(await request("/policy-runs"));
}

export async function createAutomationPolicy(
  body: import("../generated/openapi").AutomationPolicyCreateRequest,
): Promise<import("../generated/openapi").AutomationPolicy> {
  return readJson(
    await request("/policies", {
      method: "POST",
      body: JSON.stringify(body),
    }),
  );
}

export async function applyAutomationPolicy(
  id: string,
  confirm?: string,
): Promise<import("../generated/openapi").AutomationPolicyRun> {
  const headers: Record<string, string> = {};
  if (confirm) {
    headers["X-Nodal-Confirm"] = confirm;
  }
  return readJson(
    await request(`/policies/${id}/apply`, {
      method: "POST",
      headers,
      body: JSON.stringify({}),
    }),
  );
}

export async function askAI(
  body: import("../generated/openapi").AIAskRequest,
): Promise<import("../generated/openapi").AIAskResponse> {
  return readJson(
    await request("/ai/ask", {
      method: "POST",
      body: JSON.stringify(body),
    }),
  );
}

export async function listAIPlans(): Promise<import("../generated/openapi").AIPlanList> {
  return readJson(await request("/ai/plans"));
}

export async function createAIPlan(
  body: import("../generated/openapi").AIPlanCreateRequest,
): Promise<import("../generated/openapi").AIPlan> {
  return readJson(
    await request("/ai/plans", {
      method: "POST",
      body: JSON.stringify(body),
    }),
  );
}

export async function approveAIPlan(id: string): Promise<import("../generated/openapi").AIPlan> {
  return readJson(
    await request(`/ai/plans/${id}/approve`, {
      method: "POST",
      headers: { "X-Nodal-Confirm": "approve-plan" },
      body: JSON.stringify({}),
    }),
  );
}

export async function getLicense(): Promise<import("../generated/openapi").LicenseStatus> {
  return readJson(await request("/settings/license"));
}

export async function activateLicense(key: string): Promise<import("../generated/openapi").LicenseStatus> {
  return readJson(
    await request("/settings/license", {
      method: "POST",
      headers: { "X-Nodal-Confirm": "activate-license" },
      body: JSON.stringify({ key }),
    }),
  );
}

export async function clearLicense(): Promise<import("../generated/openapi").LicenseStatus> {
  return readJson(
    await request("/settings/license/clear", {
      method: "POST",
      headers: { "X-Nodal-Confirm": "clear-license" },
      body: JSON.stringify({}),
    }),
  );
}

export async function getNodeHardware(id: string): Promise<import("./phase2").HardwareResponse> {
  return readJson(await request(`/nodes/${id}/hardware`));
}

export async function getNodeCapabilities(id: string): Promise<{
  capabilities: import("./phase2").Capability[];
  stale?: boolean;
}> {
  return readJson(await request(`/nodes/${id}/capabilities`));
}

export async function getNodeMetrics(id: string, params?: { from?: string; to?: string; minutes?: number }): Promise<import("./phase2").MetricsResponse> {
  const q = new URLSearchParams();
  if (params?.from) q.set("from", params.from);
  if (params?.to) q.set("to", params.to);
  if (params?.minutes) q.set("minutes", String(params.minutes));
  const suffix = q.size ? `?${q.toString()}` : "";
  return readJson(await request(`/nodes/${id}/metrics${suffix}`));
}

export async function getNodeLogs(id: string, unit = "ndl-agent.service"): Promise<{
  status: string;
  unit?: string;
  lines: string[];
  message?: string;
}> {
  return readJson(await request(`/nodes/${id}/logs?unit=${encodeURIComponent(unit)}`));
}

export async function getNodeSmart(id: string): Promise<{ status: string; items: { name: string; smart_status: string }[] }> {
  return readJson(await request(`/nodes/${id}/smart`));
}

export async function getNodeCapacity(id: string): Promise<{
  status: string;
  message?: string;
  samples?: number;
  last_bytes?: number;
  hours_to_zero?: number;
}> {
  return readJson(await request(`/nodes/${id}/capacity`));
}

export async function getTimeline(): Promise<{ items: { kind: string; id: string; title: string; created_at: string; result?: string; state?: string; message?: string }[] }> {
  return readJson(await request("/timeline"));
}

export async function listAlerts(): Promise<{ items: import("../generated/openapi").AlertRule[] }> {
  return readJson(await request("/alerts"));
}

export async function createAlert(body: { name: string; metric: string; op: string; threshold: number }): Promise<import("../generated/openapi").AlertRule> {
  return readJson(await request("/alerts", { method: "POST", body: JSON.stringify(body) }));
}

export async function listAlertChannels(): Promise<{ items: import("../generated/openapi").NotificationChannel[] }> {
  return readJson(await request("/alerts/channels"));
}

export async function createAlertChannel(body: Record<string, unknown>): Promise<import("../generated/openapi").NotificationChannel> {
  return readJson(await request("/alerts/channels", { method: "POST", body: JSON.stringify(body) }));
}

export async function listTasks(): Promise<import("./phase2").TaskItem[]> {
  const body = await readJson<{ items: import("./phase2").TaskItem[] }>(await request("/tasks"));
  return body.items ?? [];
}

export async function listEvents(): Promise<import("./phase2").EventItem[]> {
  const body = await readJson<{ items: import("./phase2").EventItem[] }>(await request("/events"));
  return body.items ?? [];
}

export async function listPools(): Promise<import("./phase3").PoolListResponse> {
  return readJson(await request("/storage/pools"));
}

export async function createPool(body: { name: string; path: string }): Promise<import("./phase3").StoragePool> {
  return readJson(
    await request("/storage/pools", {
      method: "POST",
      body: JSON.stringify(body),
    }),
  );
}

export async function zfsRuntime(): Promise<import("./phase3").ZFSRuntime> {
  return readJson(await request("/storage/zfs"));
}

export async function importZFS(body: { guid: string; name?: string }): Promise<import("./phase3").StoragePool> {
  return readJson(
    await request("/storage/zfs/import", {
      method: "POST",
      body: JSON.stringify(body),
    }),
  );
}

export async function createZFS(body: { name: string; disks: string[] }): Promise<import("./phase3").StoragePool> {
  return readJson(
    await request("/storage/zfs/create", {
      method: "POST",
      body: JSON.stringify(body),
    }),
  );
}

export async function lvmRuntime(): Promise<import("./phase3").LVMRuntime> {
  return readJson(await request("/storage/lvm"));
}

export async function createLVM(body: { name: string; disks: string[] }): Promise<import("./phase3").StoragePool> {
  return readJson(
    await request("/storage/lvm/create", {
      method: "POST",
      body: JSON.stringify(body),
    }),
  );
}

export async function datastoreRuntime(): Promise<import("./phase3").DatastoreRuntime> {
  return readJson(await request("/storage/datastores"));
}

export async function createNFS(body: { name: string; locator: string }): Promise<import("./phase3").StoragePool> {
  return readJson(
    await request("/storage/nfs", {
      method: "POST",
      body: JSON.stringify(body),
    }),
  );
}

export async function createSMB(body: {
  name: string;
  locator: string;
  username?: string;
  password?: string;
}): Promise<import("./phase3").StoragePool> {
  return readJson(
    await request("/storage/smb", {
      method: "POST",
      body: JSON.stringify(body),
    }),
  );
}

export async function createISCSI(body: { name: string; iqn: string; portal: string }): Promise<import("./phase3").StoragePool> {
  return readJson(
    await request("/storage/iscsi", {
      method: "POST",
      body: JSON.stringify(body),
    }),
  );
}

export async function distributedRuntime(): Promise<import("./phase3").DistributedRuntime> {
  return readJson(await request("/storage/distributed"));
}

export async function attachDistributed(body: {
  name: string;
  locator: string;
  user?: string;
  cephx_key: string;
}): Promise<import("./phase3").StoragePool> {
  return readJson(
    await request("/storage/distributed", {
      method: "POST",
      body: JSON.stringify(body),
    }),
  );
}

export async function createDistributedOSD(body: { disk: string; pool_id?: string }): Promise<{
  id: string;
  disk: string;
  status: string;
  reason?: string;
  osd_started?: boolean;
}> {
  return readJson(
    await request("/storage/distributed/osds", {
      method: "POST",
      headers: { "X-Nodal-Confirm": "start-ceph-osd" },
      body: JSON.stringify(body),
    }),
  );
}

export async function getPool(id: string): Promise<import("./phase3").StoragePool> {
  return readJson(await request(`/storage/pools/${id}`));
}

export async function listVolumes(poolId?: string): Promise<import("./phase3").StorageVolume[]> {
  const q = poolId ? `?pool_id=${encodeURIComponent(poolId)}` : "";
  const body = await readJson<{ items: import("./phase3").StorageVolume[] }>(await request(`/storage/volumes${q}`));
  return body.items ?? [];
}

export async function createVolume(body: {
  pool_id: string;
  class: string;
  size_bytes: number;
  format?: string;
}): Promise<import("./phase3").StorageVolume> {
  return readJson(
    await request("/storage/volumes", {
      method: "POST",
      body: JSON.stringify(body),
    }),
  );
}

export async function listImages(poolId?: string): Promise<import("./phase3").LibraryItem[]> {
  const q = poolId ? `?pool_id=${encodeURIComponent(poolId)}` : "";
  const body = await readJson<{ items: import("./phase3").LibraryItem[] }>(await request(`/storage/images${q}`));
  return body.items ?? [];
}

export async function uploadImage(poolId: string, kind: string, file: File): Promise<import("./phase3").LibraryItem> {
  const data = new FormData();
  data.append("pool_id", poolId);
  data.append("kind", kind);
  data.append("filename", file.name);
  data.append("file", file);
  const res = await fetch("/api/v1/storage/images", {
    method: "POST",
    credentials: "include",
    body: data,
  });
  if (!res.ok) {
    throw new ApiError(res.status, await readErrorMessage(res));
  }
  return (await res.json()) as import("./phase3").LibraryItem;
}

export async function listNetworks(): Promise<import("./phase4").NetworkListResponse> {
  return readJson(await request("/networks"));
}

export async function createNetwork(
  body: {
    name: string;
    kind: string;
    ipv4_cidr?: string;
    uplink_ifname?: string;
    confirm_ifname?: string;
    dry_run?: boolean;
  },
  confirmToken?: string,
): Promise<import("./phase4").Network | import("./phase4").NetworkPreview | import("./phase4").ConfirmRequired> {
  const headers = new Headers();
  if (confirmToken) {
    headers.set("X-Nodal-Confirm", confirmToken);
  }
  const res = await request("/networks", {
    method: "POST",
    headers,
    body: JSON.stringify(body),
  });
  const parsed = (await res.json().catch(() => ({ error: `Request failed (${res.status})` }))) as
    | import("./phase4").Network
    | import("./phase4").NetworkPreview
    | import("./phase4").ConfirmRequired;
  if (res.status === 409) {
    return parsed;
  }
  if (!res.ok) {
    const message =
      parsed && "error" in parsed && typeof parsed.error === "string" ? parsed.error : `Request failed (${res.status})`;
    throw new ApiError(res.status, message);
  }
  return parsed;
}

export async function applyNetwork(
  id: string,
  dryRun: boolean,
  confirmIfName?: string,
  confirmToken?: string,
): Promise<import("./phase4").Network | import("./phase4").NetworkPreview | import("./phase4").ConfirmRequired> {
  const headers = new Headers();
  if (confirmToken) {
    headers.set("X-Nodal-Confirm", confirmToken);
  }
  const q = dryRun ? "?dry_run=true" : "";
  const res = await request(`/networks/${id}/apply${q}`, {
    method: "POST",
    headers,
    body: JSON.stringify({ confirm_ifname: confirmIfName, dry_run: dryRun }),
  });
  const parsed = (await res.json().catch(() => ({ error: `Request failed (${res.status})` }))) as
    | import("./phase4").Network
    | import("./phase4").NetworkPreview
    | import("./phase4").ConfirmRequired;
  if (res.status === 409) {
    return parsed;
  }
  if (!res.ok) {
    const message =
      parsed && "error" in parsed && typeof parsed.error === "string" ? parsed.error : `Request failed (${res.status})`;
    throw new ApiError(res.status, message);
  }
  return parsed;
}

export async function createVLAN(body: {
  name?: string;
  network_id?: string;
  vlan_id: number;
  parent_ifname?: string;
  access_ifname?: string;
  mode?: string;
  confirm_ifname?: string;
}): Promise<import("./phase4").NetworkVLAN> {
  return readJson(await request("/networks/vlans", { method: "POST", body: JSON.stringify(body) }));
}

export async function createBond(body: {
  name: string;
  mode?: string;
  members: string[];
  confirm_ifname?: string;
}): Promise<import("./phase4").NetworkBond> {
  return readJson(await request("/networks/bonds", { method: "POST", body: JSON.stringify(body) }));
}

export async function createPolicy(body: {
  name: string;
  action?: string;
  src_workload_id: string;
  dst_workload_id: string;
}): Promise<import("./phase4").NetworkPolicy> {
  return readJson(await request("/networks/policies", { method: "POST", body: JSON.stringify(body) }));
}

export async function applyPolicy(id: string): Promise<import("./phase4").NetworkPolicy> {
  return readJson(await request(`/networks/policies/${id}/apply`, { method: "POST", body: "{}" }));
}

export async function createOverlay(body: { name?: string; vni: number }): Promise<import("./phase4").NetworkOverlay> {
  return readJson(await request("/networks/overlays", { method: "POST", body: JSON.stringify(body) }));
}

export async function listWorkloads(): Promise<import("./phase5").WorkloadListResponse> {
  return readJson(await request("/workloads"));
}

export async function getWorkload(id: string): Promise<import("./phase5").Workload> {
  return readJson(await request(`/workloads/${id}`));
}

export type GuestChannelState = {
  state: "ok" | "not_installed" | "stale" | "unavailable";
  version?: string;
  reason?: string;
};

export type WorkloadGuest = {
  workload_id: string;
  qemu_ga: GuestChannelState;
  nodal_ga: GuestChannelState;
  guest_os?: string | null;
  guest_arch?: string | null;
  ipv4?: string[];
  observed_at: string;
  install?: { linux?: string; windows?: string };
};

export async function getWorkloadGuest(id: string): Promise<WorkloadGuest> {
  return readJson(await request(`/workloads/${id}/guest`));
}

export async function createWorkload(
  body: {
    name: string;
    kind: string;
    image_pin?: string;
    cpus?: number;
    memory_bytes?: number;
    pool_id?: string;
    network_id?: string;
    registry_id?: string;
    volume_ids?: string[];
    health?: { http_path?: string; port?: number };
    privileged?: boolean;
    firmware?: string;
    autostart?: boolean;
    cloud_image_id?: string;
    iso_library_id?: string;
    nocloud?: {
      enable?: boolean;
      hostname?: string;
      username?: string;
      ssh_authorized_keys?: string[];
      user_data?: string;
    };
  },
  idempotencyKey?: string,
): Promise<import("./phase5").Workload> {
  const headers = new Headers();
  if (idempotencyKey) {
    headers.set("Idempotency-Key", idempotencyKey);
  }
  return readJson(
    await request("/workloads", {
      method: "POST",
      headers,
      body: JSON.stringify(body),
    }),
  );
}

export type Registry = {
  id: string;
  name: string;
  url: string;
  insecure?: boolean;
  has_credentials?: boolean;
  status?: string;
};

export async function listRegistries(): Promise<{ items: Registry[] }> {
  return readJson(await request("/registries"));
}

export type StackMember = {
  id: string;
  service_name: string;
  status: string;
  sort_order?: number;
  workload_id?: string | null;
  reason?: string;
  desired?: Record<string, unknown>;
  workload?: {
    id: string;
    name?: string;
    kind?: string;
    status?: string;
    image_pin?: string;
    health?: { status?: string; message?: string };
  };
};

export type Stack = {
  id: string;
  name: string;
  status: string;
  created_at?: string;
  updated_at?: string;
  has_source_compose?: boolean;
  members?: StackMember[];
  desired?: Record<string, unknown>;
};

export async function listStacks(): Promise<{ items: Stack[] }> {
  return readJson(await request("/stacks"));
}

export async function getStack(id: string): Promise<Stack> {
  return readJson(await request(`/stacks/${id}`));
}

export async function importStack(body: {
  name: string;
  compose: string;
  pool_id?: string;
  network_id?: string;
  registry_id?: string;
  apply?: boolean;
}): Promise<Stack> {
  return readJson(
    await request("/stacks/import", {
      method: "POST",
      body: JSON.stringify(body),
    }),
  );
}

export async function applyStack(id: string): Promise<Stack> {
  return readJson(
    await request(`/stacks/${id}/apply`, {
      method: "POST",
      body: JSON.stringify({}),
    }),
  );
}

export async function patchStackMember(
  stackId: string,
  memberId: string,
  body: {
    name?: string;
    image_pin?: string;
    env?: { name: string; value?: string }[];
    privileged?: boolean;
    cpus?: number;
    memory_bytes?: number;
  },
): Promise<Stack> {
  return readJson(
    await request(`/stacks/${stackId}/members/${memberId}`, {
      method: "PATCH",
      body: JSON.stringify(body),
    }),
  );
}

export async function getWorkloadLogs(id: string, lines = 200): Promise<{ status: string; unit: string; lines: string[]; message?: string }> {
  return readJson(await request(`/workloads/${id}/logs?lines=${lines}`));
}

export async function patchWorkload(
  id: string,
  body: {
    cpus?: number;
    memory_bytes?: number;
    desired_power?: string;
    autostart?: boolean;
    firmware?: string;
    name?: string;
  },
): Promise<import("./phase5").Workload> {
  return readJson(
    await request(`/workloads/${id}`, {
      method: "PATCH",
      body: JSON.stringify(body),
    }),
  );
}

export async function createTerminalSession(
  kind: "node" | "workload",
  id: string,
  cwd = "/",
): Promise<import("./phase6").IOSession> {
  const prefix = kind === "node" ? "nodes" : "workloads";
  return readJson(
    await request(`/${prefix}/${id}/terminal/sessions`, {
      method: "POST",
      body: JSON.stringify({ cwd }),
    }),
  );
}

export async function listIOSessions(): Promise<{ items: import("./phase6").IOSession[] }> {
  return readJson(await request("/io/sessions"));
}

export async function listFiles(
  kind: "node" | "workload",
  id: string,
  path = "/",
): Promise<import("./phase6").FileList> {
  const prefix = kind === "node" ? "nodes" : "workloads";
  return readJson(await request(`/${prefix}/${id}/files?path=${encodeURIComponent(path)}`));
}

export async function statFile(kind: "node" | "workload", id: string, path: string): Promise<import("./phase6").FileEntry> {
  const prefix = kind === "node" ? "nodes" : "workloads";
  return readJson(await request(`/${prefix}/${id}/files/stat?path=${encodeURIComponent(path)}`));
}

export async function readFileContent(
  kind: "node" | "workload",
  id: string,
  path: string,
): Promise<import("./phase6").FileContent> {
  const prefix = kind === "node" ? "nodes" : "workloads";
  return readJson(await request(`/${prefix}/${id}/files/content?path=${encodeURIComponent(path)}`));
}

async function mutateFile(
  kind: "node" | "workload",
  id: string,
  action: "mkdir" | "delete" | "move" | "copy" | "chmod" | "chown",
  body: import("./phase6").FileMutation,
): Promise<void> {
  const prefix = kind === "node" ? "nodes" : "workloads";
  await readJson(
    await request(`/${prefix}/${id}/files/${action}`, {
      method: "POST",
      body: JSON.stringify(body),
    }),
  );
}

export async function mkdirFile(kind: "node" | "workload", id: string, path: string): Promise<void> {
  await mutateFile(kind, id, "mkdir", { path });
}

export async function deleteFile(
  kind: "node" | "workload",
  id: string,
  path: string,
  expectedMtime?: string,
): Promise<void> {
  await mutateFile(kind, id, "delete", { path, expected_mtime: expectedMtime });
}

export async function moveFile(
  kind: "node" | "workload",
  id: string,
  path: string,
  destPath: string,
): Promise<void> {
  await mutateFile(kind, id, "move", { path, dest_path: destPath });
}

export async function copyFile(
  kind: "node" | "workload",
  id: string,
  path: string,
  destPath: string,
): Promise<void> {
  await mutateFile(kind, id, "copy", { path, dest_path: destPath });
}

export async function chmodFile(
  kind: "node" | "workload",
  id: string,
  path: string,
  mode: number,
): Promise<void> {
  await mutateFile(kind, id, "chmod", { path, mode });
}

export async function chownFile(
  kind: "node" | "workload",
  id: string,
  path: string,
  uid: number,
  gid: number,
): Promise<void> {
  await mutateFile(kind, id, "chown", { path, uid, gid });
}

export type UploadProgress = { loaded: number; total: number };

export async function uploadFile(
  kind: "node" | "workload",
  id: string,
  path: string,
  file: File,
  opts?: { expectedMtime?: string; signal?: AbortSignal; onProgress?: (p: UploadProgress) => void },
): Promise<void> {
  const prefix = kind === "node" ? "nodes" : "workloads";
  const data = new FormData();
  data.append("path", path);
  data.append("file", file);
  if (opts?.expectedMtime) {
    data.append("expected_mtime", opts.expectedMtime);
  }
  await new Promise<void>((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open("POST", `/api/v1/${prefix}/${id}/files/upload`);
    xhr.withCredentials = true;
    xhr.upload.onprogress = (ev) => {
      opts?.onProgress?.({ loaded: ev.loaded, total: ev.total || file.size });
    };
    xhr.onload = () => {
      if (xhr.status >= 200 && xhr.status < 300) {
        resolve();
        return;
      }
      let message = `Request failed (${xhr.status})`;
      try {
        const parsed = JSON.parse(xhr.responseText) as { error?: string };
        if (parsed.error) {
          message = parsed.error;
        }
      } catch {
        // not JSON
      }
      reject(new ApiError(xhr.status, message));
    };
    xhr.onerror = () => reject(new ApiError(0, "Upload failed"));
    xhr.onabort = () => reject(new ApiError(0, "Upload cancelled"));
    if (opts?.signal) {
      if (opts.signal.aborted) {
        xhr.abort();
        return;
      }
      opts.signal.addEventListener("abort", () => xhr.abort(), { once: true });
    }
    xhr.send(data);
  });
}

export async function downloadFile(kind: "node" | "workload", id: string, path: string, filename: string): Promise<void> {
  const prefix = kind === "node" ? "nodes" : "workloads";
  const res = await fetch(`/api/v1/${prefix}/${id}/files/download?path=${encodeURIComponent(path)}`, {
    credentials: "include",
  });
  if (!res.ok) {
    throw new ApiError(res.status, await readErrorMessage(res));
  }
  const blob = await res.blob();
  const href = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = href;
  a.download = filename;
  a.click();
  URL.revokeObjectURL(href);
}

export async function createConsoleSession(id: string, mode: "serial" | "vnc"): Promise<import("./phase6").IOSession> {
  return readJson(
    await request(`/workloads/${id}/console/sessions`, {
      method: "POST",
      body: JSON.stringify({ mode }),
    }),
  );
}

export async function migrateWorkload(
  id: string,
  body: { dest_node_id: string; mode?: "live" | "offline" },
): Promise<{
  id?: string;
  state?: string;
  reason?: string;
  dest_node_id?: string;
  source_running?: boolean;
  dest_running?: boolean;
  epoch?: number;
}> {
  return readJson(
    await request(`/workloads/${id}/migrate`, {
      method: "POST",
      body: JSON.stringify(body),
    }),
  );
}

export async function workloadAction(
  id: string,
  action: "start" | "stop" | "restart" | "delete" | "clone" | "force-stop",
  body?: { name?: string },
  confirm?: string,
): Promise<import("./phase5").Workload> {
  const headers = new Headers();
  if (confirm) {
    headers.set("X-Nodal-Confirm", confirm);
  }
  return readJson(
    await request(`/workloads/${id}/${action}`, {
      method: "POST",
      headers,
      body: JSON.stringify(body ?? {}),
    }),
  );
}

export async function getCerts(): Promise<import("../generated/openapi").CertificateStatus> {
  return readJson(await request("/certs"));
}

export async function generateCert(
  body: import("../generated/openapi").GenerateCertRequest,
  confirm = "enable-tls",
): Promise<import("../generated/openapi").CertificateStatus> {
  const headers = new Headers();
  headers.set("X-Nodal-Confirm", confirm);
  return readJson(
    await request("/certs/generate", {
      method: "POST",
      headers,
      body: JSON.stringify(body),
    }),
  );
}

export async function importCert(
  body: import("../generated/openapi").ImportCertRequest,
  confirm = "enable-tls",
): Promise<import("../generated/openapi").CertificateStatus> {
  const headers = new Headers();
  headers.set("X-Nodal-Confirm", confirm);
  return readJson(
    await request("/certs/import", {
      method: "POST",
      headers,
      body: JSON.stringify(body),
    }),
  );
}

export async function acmeCert(
  body: import("../generated/openapi").AcmeCertRequest,
  confirm = "enable-tls",
): Promise<import("../generated/openapi").CertificateStatus> {
  const headers = new Headers();
  headers.set("X-Nodal-Confirm", confirm);
  return readJson(
    await request("/certs/acme", {
      method: "POST",
      headers,
      body: JSON.stringify(body),
    }),
  );
}

export async function listWorkloadSnapshots(
  id: string,
): Promise<import("../generated/openapi").SnapshotListResponse> {
  return readJson(await request(`/workloads/${id}/snapshots`));
}

export async function createWorkloadSnapshot(
  id: string,
  body: import("../generated/openapi").CreateSnapshotRequest,
): Promise<import("../generated/openapi").Snapshot> {
  return readJson(
    await request(`/workloads/${id}/snapshots`, {
      method: "POST",
      body: JSON.stringify(body),
    }),
  );
}

export async function rollbackSnapshot(
  id: string,
  confirm = "rollback",
): Promise<import("../generated/openapi").Snapshot> {
  const headers = new Headers();
  headers.set("X-Nodal-Confirm", confirm);
  return readJson(
    await request(`/snapshots/${id}/rollback`, {
      method: "POST",
      headers,
      body: JSON.stringify({}),
    }),
  );
}

export async function flattenWorkloadSnapshots(
  id: string,
  confirm = "flatten",
): Promise<import("../generated/openapi").SnapshotListResponse> {
  const headers = new Headers();
  headers.set("X-Nodal-Confirm", confirm);
  return readJson(
    await request(`/workloads/${id}/snapshots/flatten`, {
      method: "POST",
      headers,
      body: JSON.stringify({}),
    }),
  );
}

export async function listBackupTargets(): Promise<import("../generated/openapi").BackupTargetListResponse> {
  return readJson(await request("/backups/targets"));
}

export async function createBackupTarget(
  body: import("../generated/openapi").CreateBackupTargetRequest,
): Promise<import("../generated/openapi").BackupTarget> {
  return readJson(
    await request("/backups/targets", {
      method: "POST",
      body: JSON.stringify(body),
    }),
  );
}

export async function listBackupPolicies(): Promise<import("../generated/openapi").BackupPolicyListResponse> {
  return readJson(await request("/backups/policies"));
}

export async function createBackupPolicy(
  body: import("../generated/openapi").CreateBackupPolicyRequest,
): Promise<import("../generated/openapi").BackupPolicy> {
  return readJson(
    await request("/backups/policies", {
      method: "POST",
      body: JSON.stringify(body),
    }),
  );
}

export async function listBackupRuns(): Promise<import("../generated/openapi").BackupRunListResponse> {
  return readJson(await request("/backups/runs"));
}

export async function listBackupArtifacts(): Promise<import("../generated/openapi").BackupArtifactListResponse> {
  return readJson(await request("/backups/artifacts"));
}

export async function runBackup(
  body: import("../generated/openapi").RunBackupRequest,
): Promise<import("../generated/openapi").BackupRun> {
  return readJson(
    await request("/backups/run", {
      method: "POST",
      body: JSON.stringify(body),
    }),
  );
}

export async function restoreBackupArtifact(
  id: string,
  body: import("../generated/openapi").RestoreBackupRequest,
): Promise<import("../generated/openapi").BackupRun> {
  const headers = new Headers();
  if (body.mode === "replace") {
    headers.set("X-Nodal-Confirm", "restore");
  }
  return readJson(
    await request(`/backups/artifacts/${id}/restore`, {
      method: "POST",
      headers,
      body: JSON.stringify(body),
    }),
  );
}

export async function exportBackupDR(): Promise<import("../generated/openapi").DRExportResponse> {
  return readJson(await request("/backups/dr-export"));
}

export async function verifyBackupArtifact(
  id: string,
  body: import("../generated/openapi").VerifyBackupRequest,
): Promise<import("../generated/openapi").BackupArtifact> {
  return readJson(
    await request(`/backups/artifacts/${id}/verify`, {
      method: "POST",
      body: JSON.stringify(body ?? {}),
    }),
  );
}

export async function restoreBackupFile(
  id: string,
  body: import("../generated/openapi").RestoreBackupFileRequest,
): Promise<import("../generated/openapi").RestoreBackupFileResponse> {
  return readJson(
    await request(`/backups/artifacts/${id}/restore-file`, {
      method: "POST",
      body: JSON.stringify(body),
    }),
  );
}

export async function getUpdates(): Promise<import("../generated/openapi").UpdateStatus> {
  return readJson(await request("/updates"));
}

export async function checkUpdates(): Promise<import("../generated/openapi").UpdatePreview> {
  return readJson(
    await request("/updates/check", {
      method: "POST",
      body: JSON.stringify({}),
    }),
  );
}

export async function preflightUpdates(): Promise<import("../generated/openapi").UpdatePreflight> {
  return readJson(
    await request("/updates/preflight", {
      method: "POST",
      body: JSON.stringify({}),
    }),
  );
}

export async function checkpointUpdates(): Promise<import("../generated/openapi").UpdateCheckpoint> {
  return readJson(
    await request("/updates/checkpoint", {
      method: "POST",
      body: JSON.stringify({}),
    }),
  );
}

export async function applyUpdates(
  confirm = "apply-update",
): Promise<import("../generated/openapi").UpdateOperation> {
  const headers = new Headers();
  headers.set("X-Nodal-Confirm", confirm);
  return readJson(
    await request("/updates/apply", {
      method: "POST",
      headers,
      body: JSON.stringify({}),
    }),
  );
}

export async function rollbackUpdates(
  confirm = "rollback-update",
): Promise<import("../generated/openapi").UpdateOperation> {
  const headers = new Headers();
  headers.set("X-Nodal-Confirm", confirm);
  return readJson(
    await request("/updates/rollback", {
      method: "POST",
      headers,
      body: JSON.stringify({}),
    }),
  );
}

export async function verifyMfa(body: import("../generated/openapi").MFAVerifyRequest) {
  return readJson<MeResponse>(
    await request("/auth/mfa/verify", { method: "POST", body: JSON.stringify(body) }),
  );
}

export async function getMfa() {
  return readJson<import("../generated/openapi").MFAStatus>(await request("/mfa"));
}

export async function enrollMfa() {
  return readJson<import("../generated/openapi").MFAEnrollResponse>(
    await request("/mfa/enroll", { method: "POST", body: JSON.stringify({}) }),
  );
}

export async function confirmMfa(code: string) {
  return readJson<import("../generated/openapi").MFAStatus>(
    await request("/mfa/confirm", { method: "POST", body: JSON.stringify({ code }) }),
  );
}

export async function listAudit() {
  return readJson<import("../generated/openapi").AuditListResponse>(await request("/audit"));
}

export async function listGroups() {
  return readJson<import("../generated/openapi").GroupListResponse>(await request("/groups"));
}

export async function createGroup(name: string) {
  return readJson<import("../generated/openapi").Group>(
    await request("/groups", { method: "POST", body: JSON.stringify({ name }) }),
  );
}

export async function addGroupMember(id: string, userId: string) {
  return readJson(
    await request(`/groups/${id}/members`, { method: "POST", body: JSON.stringify({ user_id: userId }) }),
  );
}

export async function bindGroupRole(id: string, role: string) {
  return readJson(
    await request(`/groups/${id}/roles`, { method: "POST", body: JSON.stringify({ role }) }),
  );
}

export async function listGpus() {
  return readJson<import("../generated/openapi").GPUListResponse>(await request("/gpus"));
}

export async function assignGpu(body: import("../generated/openapi").GPUAssignRequest) {
  return readJson<import("../generated/openapi").GPUAssignment>(
    await request("/gpus/assign", { method: "POST", body: JSON.stringify(body) }),
  );
}

export async function unassignGpu(id: string) {
  return readJson(await request("/gpus/unassign", { method: "POST", body: JSON.stringify({ id }) }));
}

export type VMTemplate = {
  id: string;
  name: string;
  source_workload_id?: string;
  snapshot_id?: string;
  created_at?: string;
};

export async function listTemplates() {
  return readJson<{ items: VMTemplate[] }>(await request("/templates"));
}

export async function createTemplate(body: { workload_id: string; name?: string }) {
  return readJson<VMTemplate>(await request("/templates", { method: "POST", body: JSON.stringify(body) }));
}

export async function deployTemplate(id: string, name?: string) {
  return readJson<import("./phase5").Workload>(
    await request(`/templates/${id}/deploy`, { method: "POST", body: JSON.stringify({ name }) }),
  );
}

export async function importWorkload(body: {
  name?: string;
  library_id: string;
  pool_id?: string;
  network_id: string;
  firmware?: string;
}) {
  return readJson<import("./phase5").Workload>(
    await request("/workloads/import", { method: "POST", body: JSON.stringify(body) }),
  );
}

export async function exportWorkload(id: string, displayName?: string) {
  return readJson<{ id: string; kind: string; display_name: string }>(
    await request(`/workloads/${id}/export`, { method: "POST", body: JSON.stringify({ display_name: displayName }) }),
  );
}

export type USBDeviceRow = {
  address: string;
  vendor?: string;
  product?: string;
  name?: string;
  claimed_by?: string;
};

export async function listNodeUSB(nodeId: string) {
  return readJson<{ items: USBDeviceRow[] }>(await request(`/nodes/${nodeId}/usb`));
}

export async function listNodePCI(nodeId: string) {
  return readJson<{ items: Array<{ id: string; vendor?: string; device?: string; class?: string; iommu_group?: string; claimed_by?: string }> }>(
    await request(`/nodes/${nodeId}/pci`),
  );
}

export async function attachWorkloadUSB(id: string, address: string) {
  return readJson(await request(`/workloads/${id}/usb`, { method: "POST", body: JSON.stringify({ address }) }));
}

export async function attachWorkloadPCI(id: string, pci: string) {
  return readJson(await request(`/workloads/${id}/pci`, { method: "POST", body: JSON.stringify({ pci }) }));
}
