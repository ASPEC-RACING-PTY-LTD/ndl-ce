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
