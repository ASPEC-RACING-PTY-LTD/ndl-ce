import { ApiError } from "../api/client";

// The Helix integration's settings (SuperAdmin) and kill switch (Admin and
// above). Helix itself uses /api/v1/helix/v1 with its own credential.

export const HELIX_CAPS = ["read", "logs", "write", "execute", "deploy", "power"] as const;
export type HelixCap = (typeof HELIX_CAPS)[number];

export type HelixGrant = {
  machine_id: string;
  caps: HelixCap[];
  protected?: boolean;
  destructive?: boolean;
  run_as?: string;
  exec_users?: string[];
};

export type HelixMachine = { id: string; name: string; type: string; status: string; exec_capable: boolean; grant?: HelixGrant };

export type HelixCredential = {
  id: string;
  name: string;
  prefix: string;
  created_at: string;
  created_by: string;
  active: boolean;
  expires_at?: string;
  revoked_at?: string;
  revoked_by?: string;
  last_used_at?: string;
  last_used_ip?: string;
};

export type HelixApproval = {
  id: string;
  op: string;
  machine_id: string;
  machine_name: string;
  summary: string;
  reasons: string[];
  status: string;
  requested_at: string;
  requested_by: string;
  expires_at: string;
  note?: string;
  decided_at?: string;
  decided_by?: string;
  used_at?: string;
};

export type HelixKill = { engaged: boolean; at?: string; by?: string; reason?: string };

export type HelixView = {
  enabled: boolean;
  kill: HelixKill;
  credentials: HelixCredential[];
  allowed_ips: string[];
  global_events: boolean;
  thresholds: { cpu_percent: number; memory_percent: number };
  machines: HelixMachine[];
  approvals: HelixApproval[];
  pending_approvals: number;
  running: { id: string; op: string; machine_id: string; summary: string; started_at: string }[];
  listening: number;
  latest_event: number;
  api_base: string;
};

export type HelixActivity = { id: string; at: string; action: string; result: string; actor: string; ip: string; detail?: { detail?: string; credential?: string } };

async function call<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers);
  if (init.body !== undefined) {
    headers.set("Content-Type", "application/json");
  }
  const res = await fetch(`/api/v1${path}`, { cache: "no-store", ...init, credentials: "include", headers });
  if (!res.ok) {
    let message = `Request failed (${res.status})`;
    try {
      const body = (await res.json()) as { error?: string };
      if (body?.error) {
        message = body.error;
      }
    } catch {
      // keep the status
    }
    throw new ApiError(res.status, message);
  }
  return (await res.json()) as T;
}

const json = (method: string, body: unknown): RequestInit => ({ method, body: JSON.stringify(body) });

export const getHelix = () => call<HelixView>("/helix");
export const putHelixSettings = (body: Partial<{ enabled: boolean; allowed_ips: string[]; global_events: boolean; thresholds: HelixView["thresholds"] }>) =>
  call<HelixView>("/helix/settings", json("PUT", body));
export const putHelixMachines = (grants: HelixGrant[]) => call<HelixView>("/helix/machines", json("PUT", { grants }));
export const createHelixCredential = (name: string, ttlDays: number) =>
  call<{ token: string; credential: HelixCredential; view: HelixView }>("/helix/credentials", json("POST", { name, ttl_days: ttlDays }));
export const revokeHelixCredential = (id: string) => call<HelixView>(`/helix/credentials/${encodeURIComponent(id)}/revoke`, json("POST", {}));
export const decideHelixApproval = (id: string, decision: "approve" | "deny", note: string) =>
  call<HelixView>(`/helix/approvals/${encodeURIComponent(id)}/${decision}`, json("POST", { note }));
export const getHelixKill = () => call<{ enabled: boolean; kill: HelixKill; running: number }>("/helix/kill");
export const engageHelixKill = (reason: string, revokeCredentials: boolean) =>
  call<{ kill: HelixKill; stopped_operations: number; cancelled_deployments: number; revoked_credentials: number }>(
    "/helix/kill",
    json("POST", { reason, revoke_credentials: revokeCredentials }),
  );
export const releaseHelixKill = () => call<HelixView>("/helix/kill/release", json("POST", {}));
export const getHelixActivity = () => call<{ items: HelixActivity[] }>("/helix/activity");
