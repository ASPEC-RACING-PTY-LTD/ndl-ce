import { afterEach, describe, expect, it, vi } from "vitest";
import { applyNetwork, createNetwork } from "./client";

const confirmBody = {
  error: "confirmation_required",
  code: "confirmation_required",
  danger: "dangerous",
  typed_ifname: "enp6s0",
  confirm_token: "hmac-confirm-token",
  message: "Enslaving the management NIC requires typing the interface name and sending X-Nodal-Confirm.",
};

const created = {
  id: "net-1",
  name: "lan",
  kind: "lan-bridge",
  status: "available",
  uplink_ifname: "enp6s0",
};

function headerOf(init: RequestInit | undefined, name: string): string {
  return new Headers(init?.headers).get(name) ?? "";
}

function mockInit(mock: { mock: { calls: unknown[][] } }, index: number): RequestInit | undefined {
  const call = mock.mock.calls[index];
  if (!call || call.length < 2) {
    return undefined;
  }
  return call[1] as RequestInit;
}

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("createNetwork dangerous LAN-bridge confirm", () => {
  it("retries create with X-Nodal-Confirm after typing the management NIC", async () => {
    const fetchMock = vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
      if (headerOf(init, "X-Nodal-Confirm") === confirmBody.confirm_token) {
        return jsonResponse(201, created);
      }
      return jsonResponse(409, confirmBody);
    });
    vi.stubGlobal("fetch", fetchMock);

    const result = await createNetwork({
      name: "lan",
      kind: "lan-bridge",
      uplink_ifname: "enp6s0",
      confirm_ifname: "enp6s0",
    });

    expect(result).toMatchObject(created);
    expect(fetchMock).toHaveBeenCalledTimes(2);
    expect(headerOf(mockInit(fetchMock, 0), "X-Nodal-Confirm")).toBe("");
    expect(headerOf(mockInit(fetchMock, 1), "X-Nodal-Confirm")).toBe("hmac-confirm-token");
    expect(JSON.parse(String(mockInit(fetchMock, 1)?.body))).toMatchObject({
      confirm_ifname: "enp6s0",
      uplink_ifname: "enp6s0",
      kind: "lan-bridge",
    });
  });

  it("does not send X-Nodal-Confirm or retry on dry-run", async () => {
    const preview = {
      kind: "lan-bridge",
      danger: "dangerous",
      requires_confirm: true,
      typed_ifname: "enp6s0",
      uplink_ifname: "enp6s0",
      dry_run: true,
      management_ifname: "enp6s0",
    };
    const fetchMock = vi.fn(async () => jsonResponse(200, preview));
    vi.stubGlobal("fetch", fetchMock);

    const result = await createNetwork({
      name: "lan",
      kind: "lan-bridge",
      uplink_ifname: "enp6s0",
      dry_run: true,
    });

    expect(result).toMatchObject(preview);
    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(headerOf(mockInit(fetchMock, 0), "X-Nodal-Confirm")).toBe("");
  });

  it("does not retry when the typed interface does not match", async () => {
    const fetchMock = vi.fn(async () => jsonResponse(409, confirmBody));
    vi.stubGlobal("fetch", fetchMock);

    const result = await createNetwork({
      name: "lan",
      kind: "lan-bridge",
      uplink_ifname: "enp6s0",
      confirm_ifname: "eth0",
    });

    expect(result).toMatchObject({ code: "confirmation_required" });
    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(headerOf(mockInit(fetchMock, 0), "X-Nodal-Confirm")).toBe("");
  });

  it("does not retry when the interface was not typed", async () => {
    const fetchMock = vi.fn(async () => jsonResponse(409, confirmBody));
    vi.stubGlobal("fetch", fetchMock);

    const result = await createNetwork({
      name: "lan",
      kind: "lan-bridge",
      uplink_ifname: "enp6s0",
    });

    expect(result).toMatchObject({ code: "confirmation_required" });
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });
});

describe("applyNetwork dangerous confirm", () => {
  it("retries apply with X-Nodal-Confirm when the typed NIC matches", async () => {
    const fetchMock = vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
      if (headerOf(init, "X-Nodal-Confirm") === confirmBody.confirm_token) {
        return jsonResponse(200, created);
      }
      return jsonResponse(409, confirmBody);
    });
    vi.stubGlobal("fetch", fetchMock);

    const result = await applyNetwork("net-1", false, "enp6s0");
    expect(result).toMatchObject(created);
    expect(fetchMock).toHaveBeenCalledTimes(2);
    expect(headerOf(mockInit(fetchMock, 1), "X-Nodal-Confirm")).toBe("hmac-confirm-token");
  });

  it("does not retry apply dry-run", async () => {
    const fetchMock = vi.fn(async () =>
      jsonResponse(200, { kind: "lan-bridge", dry_run: true, requires_confirm: true, typed_ifname: "enp6s0" }),
    );
    vi.stubGlobal("fetch", fetchMock);

    await applyNetwork("net-1", true, "enp6s0");
    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(headerOf(mockInit(fetchMock, 0), "X-Nodal-Confirm")).toBe("");
  });
});
