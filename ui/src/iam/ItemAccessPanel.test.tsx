import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ItemAccessPanel } from "./ItemAccessPanel";
import { accessSummary, dockerItemID, presetPermissions } from "./itemAccess";

const grantable = {
  workload: [
    "compute.read",
    "compute.lifecycle",
    "compute.start",
    "compute.stop",
    "compute.console",
    "compute.snapshot",
    "terminal.open",
    "files.read",
    "files.download",
    "files.upload",
    "files.create",
    "files.modify",
    "files.delete",
    "backup.read",
    "backup.create",
    "metrics.read",
    "events.read",
  ],
  gameserver: [
    "gameserver.read",
    "gameserver.power",
    "gameserver.console",
    "gameserver.files",
    "gameserver.config",
    "gameserver.content",
    "gameserver.backup",
    "gameserver.schedule",
    "gameserver.reinstall",
  ],
  docker: ["compute.read", "compute.lifecycle", "terminal.open"],
  node: ["node.read", "metrics.read", "events.read", "terminal.open", "files.read", "files.download", "files.upload", "files.create", "files.modify", "files.delete"],
};

const grants = [
  {
    id: "g1",
    subject_kind: "user",
    subject_id: "u1",
    resource_kind: "workload",
    resource_id: "w1",
    permissions: ["compute.read", "metrics.read", "events.read", "files.read", "backup.read"],
  },
  {
    id: "g2",
    subject_kind: "group",
    subject_id: "grp1",
    resource_kind: "docker",
    resource_id: "m1/web",
    permissions: ["compute.read", "compute.lifecycle"],
  },
  {
    id: "g3",
    subject_kind: "user",
    subject_id: "u1",
    resource_kind: "gameserver",
    resource_id: "gone",
    permissions: ["gameserver.read"],
  },
];

function json(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

function mockApi() {
  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
    const path = new URL(url, "http://localhost").pathname;
    const method = (init?.method ?? "GET").toUpperCase();
    if (path === "/api/v1/iam/resource-access") {
      return json(200, { grants, grantable });
    }
    if (path === "/api/v1/users") {
      return json(200, { items: [{ id: "u1", username: "alice", roles: [] }], total: 1 });
    }
    if (path === "/api/v1/groups") {
      return json(200, { items: [{ id: "grp1", name: "Ops" }] });
    }
    if (path === "/api/v1/workloads") {
      return json(200, { items: [{ id: "w1", name: "Minecraft VM", kind: "vm", status: "running" }] });
    }
    if (path === "/api/v1/game-servers") {
      return json(200, { items: [{ id: "gs1", name: "Rust", status: "running", template_id: "t", game: "rust" }] });
    }
    if (path === "/api/v1/docker") {
      return json(200, {
        summary: {},
        machines: [],
        projects: [],
        containers: [
          { id: "c1", machine_id: "m1", machine_name: "box", container_id: "abc", name: "/web" },
          { id: "c2", machine_id: "m1", machine_name: "box", container_id: "def", name: "db" },
        ],
      });
    }
    if (path === "/api/v1/nodes") {
      return json(200, { items: [{ id: "m1", name: "box", status: "ready" }] });
    }
    return json(404, { error: `unmocked ${method} ${path}` });
  });
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("item access presets", () => {
  it("sets view, operate and full per kind", () => {
    expect(presetPermissions("docker", "view", grantable.docker)).toEqual(["compute.read"]);
    expect(presetPermissions("docker", "operate", grantable.docker)).toEqual(grantable.docker);
    expect(presetPermissions("gameserver", "operate", grantable.gameserver)).toEqual([
      "gameserver.read",
      "gameserver.power",
      "gameserver.console",
    ]);
    expect(presetPermissions("workload", "view", grantable.workload)).toEqual([
      "compute.read",
      "files.read",
      "backup.read",
      "metrics.read",
      "events.read",
    ]);
    expect(presetPermissions("node", "view", grantable.node)).toEqual(["node.read", "metrics.read", "events.read"]);
    expect(presetPermissions("node", "operate", grantable.node)).toEqual(["node.read", "metrics.read", "events.read", "terminal.open"]);
    expect(presetPermissions("workload", "full", grantable.workload)).toEqual(grantable.workload);
  });

  it("summarises access by preset or by name", () => {
    expect(accessSummary("docker", ["compute.read"], grantable.docker)).toBe("View");
    expect(accessSummary("workload", grantable.workload, grantable.workload)).toBe("Full");
    expect(accessSummary("workload", ["compute.read", "compute.lifecycle", "terminal.open", "files.upload", "events.read"], grantable.workload)).toBe(
      "View, Power, Terminal +2",
    );
  });

  it("names a Docker container by machine and name", () => {
    expect(dockerItemID("m1", "/web")).toBe("m1/web");
  });
});

describe("ItemAccessPanel", () => {
  it("lists grants with resolved names", async () => {
    mockApi();
    render(<ItemAccessPanel />);
    const table = await screen.findByRole("table");
    await waitFor(() => expect(within(table).getByText("Minecraft VM")).toBeInTheDocument());
    expect(within(table).getAllByText("alice")).toHaveLength(2);
    expect(within(table).getByText("Ops")).toBeInTheDocument();
    expect(within(table).getByText("Group")).toBeInTheDocument();
    expect(within(table).getByText("web on box")).toBeInTheDocument();
    expect(within(table).getByText("gone")).toBeInTheDocument();
    expect(within(table).getByText(/\(missing\)/)).toBeInTheDocument();
    expect(within(table).getAllByText("View").length).toBeGreaterThan(0);
  });

  it("gives a group access to a Docker container", async () => {
    const fetchMock = mockApi();
    render(<ItemAccessPanel />);
    await screen.findByRole("table");
    fireEvent.click(screen.getByRole("button", { name: "Give access" }));
    const dialog = await screen.findByRole("dialog");
    fireEvent.click(within(dialog).getByRole("button", { name: "Group" }));
    await waitFor(() => expect(within(dialog).getByRole("option", { name: "Ops" })).toBeInTheDocument());
    fireEvent.change(within(dialog).getByLabelText("Group"), { target: { value: "grp1" } });
    fireEvent.click(within(dialog).getByRole("button", { name: "Docker container" }));
    fireEvent.change(within(dialog).getByLabelText("Docker container"), { target: { value: "m1/db" } });
    fireEvent.click(within(dialog).getByRole("button", { name: "Operate" }));
    fireEvent.click(within(dialog).getByRole("button", { name: "Give access" }));
    await waitFor(() => expect(fetchMock.mock.calls.some(([, init]) => init?.method === "PUT")).toBe(true));
    const put = fetchMock.mock.calls.find(([, init]) => init?.method === "PUT");
    expect(JSON.parse(String(put?.[1]?.body))).toEqual({
      subject_kind: "group",
      subject_id: "grp1",
      resource_kind: "docker",
      resource_id: "m1/db",
      permissions: ["compute.read", "compute.lifecycle", "terminal.open"],
    });
  });

  it("warns that node terminal and files are root access", async () => {
    mockApi();
    render(<ItemAccessPanel />);
    await screen.findByRole("table");
    fireEvent.click(screen.getByRole("button", { name: "Give access" }));
    const dialog = await screen.findByRole("dialog");
    fireEvent.click(within(dialog).getByRole("button", { name: "Node" }));
    expect(within(dialog).getByText(/root access to that machine/i)).toBeInTheDocument();
    expect(within(dialog).getByLabelText("Root terminal on the host")).not.toBeChecked();
  });
});
