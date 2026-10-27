import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { setAutoConfirm } from "../confirm";
import { LeftoverContainers } from "./LeftoverContainers";

const leftovers = [
  {
    id: "2bfff17a-0f19-4f3b-96a7-ba94fad9f0d1",
    name: "old-web",
    unit_enabled: true,
    unit_active: false,
    has_config: true,
    has_state: true,
    rootfs: "/pool/a",
    rootfs_bytes: 1048576,
    modified: "2026-09-13T19:31:00Z",
  },
  { id: "3193d754-6f83-4c13-806e-70a3fb0f0249", unit_enabled: true, unit_active: false, has_config: true, has_state: false, modified: "2026-09-13T16:07:00Z" },
];

function mock() {
  const calls: { method: string; path: string; body?: string; confirm?: string | null }[] = [];
  let items = leftovers;
  vi.stubGlobal("fetch", async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(typeof input === "string" ? input : input instanceof URL ? input.href : input.url, "http://localhost");
    calls.push({
      method: (init?.method ?? "GET").toUpperCase(),
      path: url.pathname,
      body: typeof init?.body === "string" ? init.body : undefined,
      confirm: new Headers(init?.headers).get("X-Nodal-Confirm"),
    });
    let out: unknown = { items };
    if (url.pathname.endsWith("/remove")) {
      out = { results: items.map((i) => ({ id: i.id, removed_data: true })) };
      items = [];
    }
    return new Response(JSON.stringify(out), { status: 200, headers: { "Content-Type": "application/json" } });
  });
  return calls;
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  setAutoConfirm(false);
});

describe("Leftover containers", () => {
  it("warns, lists them with sizes and removes the selected ones", async () => {
    setAutoConfirm(true);
    const calls = mock();
    render(<LeftoverContainers canRemove />);
    expect(await screen.findByText("2 leftover container(s) on this node.")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Review" }));
    expect(await screen.findByText("old-web")).toBeTruthy();
    expect(screen.getAllByText("yes, failing").length).toBe(2);
    fireEvent.click(screen.getByRole("checkbox", { name: "Select 3193d754-6f83-4c13-806e-70a3fb0f0249" }));
    fireEvent.click(screen.getByRole("button", { name: "Remove 1 selected" }));
    await waitFor(() => expect(calls.some((c) => c.path === "/api/v1/workloads/leftovers/remove")).toBe(true));
    const rm = calls.find((c) => c.path === "/api/v1/workloads/leftovers/remove")!;
    expect(rm.confirm).toBe("remove-leftovers");
    expect(rm.body).toBe('{"ids":["2bfff17a-0f19-4f3b-96a7-ba94fad9f0d1"]}');
    expect(await screen.findByText(/Removed 2 leftover container/)).toBeTruthy();
  });

  it("shows nothing when the node is clean", async () => {
    vi.stubGlobal("fetch", async () => new Response(JSON.stringify({ items: [] }), { status: 200, headers: { "Content-Type": "application/json" } }));
    const { container } = render(<LeftoverContainers canRemove />);
    await new Promise((r) => setTimeout(r, 20));
    expect(container.textContent).toBe("");
  });
});
