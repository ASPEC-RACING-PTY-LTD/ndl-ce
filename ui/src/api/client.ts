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

export async function login(body: LoginRequest): Promise<MeResponse> {
  return readJson<MeResponse>(
    await request("/auth/login", {
      method: "POST",
      body: JSON.stringify(body),
    }),
  );
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

export async function listNodes(): Promise<import("./phase2").NodeSummary[]> {
  const body = await readJson<{ items: import("./phase2").NodeSummary[] }>(await request("/nodes"));
  return body.items ?? [];
}

export async function getNode(id: string): Promise<import("./phase2").NodeSummary> {
  return readJson(await request(`/nodes/${id}`));
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

export async function getNodeMetrics(id: string): Promise<import("./phase2").MetricsResponse> {
  return readJson(await request(`/nodes/${id}/metrics`));
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

export async function listWorkloads(): Promise<import("./phase5").WorkloadListResponse> {
  return readJson(await request("/workloads"));
}

export async function getWorkload(id: string): Promise<import("./phase5").Workload> {
  return readJson(await request(`/workloads/${id}`));
}

export async function createWorkload(
  body: {
    name: string;
    kind: string;
    image_pin?: string;
    cpus?: number;
    memory_bytes?: number;
    pool_id?: string;
    network_id: string;
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

export async function listFiles(
  kind: "node" | "workload",
  id: string,
  path = "/",
): Promise<import("./phase6").FileList> {
  const prefix = kind === "node" ? "nodes" : "workloads";
  return readJson(await request(`/${prefix}/${id}/files?path=${encodeURIComponent(path)}`));
}

export async function mkdirFile(kind: "node" | "workload", id: string, path: string): Promise<void> {
  const prefix = kind === "node" ? "nodes" : "workloads";
  await readJson(
    await request(`/${prefix}/${id}/files/mkdir`, {
      method: "POST",
      body: JSON.stringify({ path }),
    }),
  );
}

export async function deleteFile(kind: "node" | "workload", id: string, path: string): Promise<void> {
  const prefix = kind === "node" ? "nodes" : "workloads";
  await readJson(
    await request(`/${prefix}/${id}/files/delete`, {
      method: "POST",
      body: JSON.stringify({ path }),
    }),
  );
}

export async function uploadFile(kind: "node" | "workload", id: string, path: string, file: File): Promise<void> {
  const prefix = kind === "node" ? "nodes" : "workloads";
  const data = new FormData();
  data.append("path", path);
  data.append("file", file);
  const res = await fetch(`/api/v1/${prefix}/${id}/files/upload`, {
    method: "POST",
    credentials: "include",
    body: data,
  });
  if (!res.ok) {
    throw new ApiError(res.status, await readErrorMessage(res));
  }
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
