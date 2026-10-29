import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ItemAccessPanel } from "./ItemAccessPanel";
import { accessSummary, dockerItemID, permissionSections, presetPermissions } from "./itemAccess";

const suggested = {
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

const permissions = [
  "*",
  "compute.read",
  "compute.lifecycle",
  "compute.start",
  "compute.stop",
  "compute.delete",
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
  "node.read",
  "node.host",
  "gameserver.read",
  "gameserver.power",
  "gameserver.console",
  "gameserver.files",
  "gameserver.config",
  "gameserver.content",
  "gameserver.backup",
  "gameserver.schedule",
  "gameserver.reinstall",
  "permissions.assign",
  "permissions.assign.override",
  "deploy.configure",
];

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
  {
    id: "g4",
    subject_kind: "role",
    subject_id: "user",
    resource_kind: "cluster",
    resource_id: "",
    permissions: ["deploy.configure"],
  },
];

function json(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

function mockApi(putError?: string) {
  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
    const path = new URL(url, "http://localhost").pathname;
    const method = (init?.method ?? "GET").toUpperCase();
    if (path === "/api/v1/iam/resource-access") {
      if (method === "PUT" && putError) {
        return json(403, { error: putError });
      }
      return json(200, { grants, suggested, permissions });
    }
    if (path === "/api/v1/iam/access") {
      return json(200, {
        roles: [
          { name: "superadmin", title: "SuperAdmin", permissions: ["*"] },
          { name: "admin", title: "Admin", permissions: [] },
          { name: "user", title: "User", permissions: [] },
          { name: "support", title: "Support desk", permissions: [] },
        ],
        groups: [],
        users: [],
        permissions: [],
        can_order: false,
        can_manage_roles: false,
      });
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
    expect(presetPermissions("docker", "view", suggested.docker)).toEqual(["compute.read"]);
    expect(presetPermissions("docker", "operate", suggested.docker)).toEqual(suggested.docker);
    expect(presetPermissions("gameserver", "operate", suggested.gameserver)).toEqual([
      "gameserver.read",
      "gameserver.power",
      "gameserver.console",
    ]);
    expect(presetPermissions("workload", "view", suggested.workload)).toEqual([
      "compute.read",
      "files.read",
      "backup.read",
      "metrics.read",
      "events.read",
    ]);
    expect(presetPermissions("node", "view", suggested.node)).toEqual(["node.read", "metrics.read", "events.read"]);
    expect(presetPermissions("node", "operate", suggested.node)).toEqual(["node.read", "metrics.read", "events.read", "terminal.open"]);
    expect(presetPermissions("workload", "full", suggested.workload)).toEqual(suggested.workload);
  });

  it("summarises access by preset or by name", () => {
    expect(accessSummary("docker", ["compute.read"], suggested.docker)).toBe("View");
    expect(accessSummary("workload", suggested.workload, suggested.workload)).toBe("Full");
    expect(accessSummary("workload", ["compute.read", "compute.lifecycle", "terminal.open", "files.upload", "events.read"], suggested.workload)).toBe(
      "View, Power, Terminal +2",
    );
    expect(accessSummary("cluster", ["*"], [])).toBe("Everything");
    expect(accessSummary("workload", ["compute.read", "*"], suggested.workload)).toBe("Everything");
    expect(accessSummary("cluster", ["deploy.configure"], [])).toBe("Configure deployments");
  });

  it("puts an item's own permissions first", () => {
    const sections = permissionSections("docker", permissions, suggested.docker);
    expect(sections[0]).toEqual({ group: "This item", perms: suggested.docker });
    expect(sections.flatMap((s) => s.perms).filter((p) => p === "compute.read")).toHaveLength(1);
    expect(permissionSections("cluster", permissions, suggested.docker)[0].group).toBe("All");
  });

  it("names a Docker container by machine and name", () => {
    expect(dockerItemID("m1", "/web")).toBe("m1/web");
  });
});

async function openDialog(superAdmin = false) {
  render(<ItemAccessPanel superAdmin={superAdmin} />);
  await screen.findByRole("table");
  fireEvent.click(screen.getByRole("button", { name: "Give permissions" }));
  return screen.findByRole("dialog");
}

function putBody(fetchMock: ReturnType<typeof mockApi>) {
  const put = fetchMock.mock.calls.find(([, init]) => init?.method === "PUT");
  return JSON.parse(String(put?.[1]?.body));
}

describe("ItemAccessPanel", () => {
  it("lists grants with resolved names", async () => {
    mockApi();
    render(<ItemAccessPanel />);
    const table = await screen.findByRole("table");
    await waitFor(() => expect(within(table).getByText("Minecraft VM")).toBeInTheDocument());
    expect(screen.getByRole("heading", { name: "Direct permissions" })).toBeInTheDocument();
    expect(within(table).getAllByText("alice")).toHaveLength(2);
    expect(within(table).getByText("Ops")).toBeInTheDocument();
    expect(within(table).getByText("Group")).toBeInTheDocument();
    expect(within(table).getByText("web on box")).toBeInTheDocument();
    expect(within(table).getByText("gone")).toBeInTheDocument();
    expect(within(table).getByText(/\(missing\)/)).toBeInTheDocument();
    expect(within(table).getAllByText("View").length).toBeGreaterThan(0);
    await waitFor(() => expect(within(table).getByText("User")).toBeInTheDocument());
    expect(within(table).getByText("Role")).toBeInTheDocument();
    expect(within(table).getByText("Everywhere")).toBeInTheDocument();
    expect(within(table).getByText("Configure deployments")).toBeInTheDocument();
  });

  it("gives a group access to a Docker container", async () => {
    const fetchMock = mockApi();
    const dialog = await openDialog();
    fireEvent.click(within(dialog).getByRole("button", { name: "Group" }));
    await waitFor(() => expect(within(dialog).getByRole("option", { name: "Ops" })).toBeInTheDocument());
    fireEvent.change(within(dialog).getByLabelText("Group"), { target: { value: "grp1" } });
    fireEvent.click(within(dialog).getByRole("button", { name: "Docker container" }));
    fireEvent.change(within(dialog).getByLabelText("Docker container"), { target: { value: "m1/db" } });
    fireEvent.click(within(dialog).getByRole("button", { name: "Operate" }));
    fireEvent.click(within(dialog).getByRole("button", { name: "Give permissions" }));
    await waitFor(() => expect(fetchMock.mock.calls.some(([, init]) => init?.method === "PUT")).toBe(true));
    expect(putBody(fetchMock)).toEqual({
      subject_kind: "group",
      subject_id: "grp1",
      resource_kind: "docker",
      resource_id: "m1/db",
      permissions: ["compute.read", "compute.lifecycle", "terminal.open"],
    });
  });

  it("gives a role a permission everywhere", async () => {
    const fetchMock = mockApi();
    const dialog = await openDialog();
    fireEvent.click(within(dialog).getByRole("button", { name: "Role" }));
    await waitFor(() => expect(within(dialog).getByRole("option", { name: "Support desk" })).toBeInTheDocument());
    fireEvent.change(within(dialog).getByLabelText("Role"), { target: { value: "support" } });
    fireEvent.click(within(dialog).getByRole("button", { name: "Everywhere" }));
    expect(within(dialog).getByRole("button", { name: "Give permissions" })).toBeDisabled();
    fireEvent.change(within(dialog).getByLabelText("Filter permissions"), { target: { value: "delete workloads" } });
    fireEvent.click(within(dialog).getByLabelText("Delete workloads"));
    fireEvent.click(within(dialog).getByRole("button", { name: "Give permissions" }));
    await waitFor(() => expect(fetchMock.mock.calls.some(([, init]) => init?.method === "PUT")).toBe(true));
    expect(putBody(fetchMock)).toEqual({
      subject_kind: "role",
      subject_id: "support",
      resource_kind: "cluster",
      resource_id: "",
      permissions: ["compute.delete"],
    });
  });

  it("hides Everything and the override from an Admin", async () => {
    mockApi();
    const dialog = await openDialog(false);
    fireEvent.click(within(dialog).getByRole("button", { name: "Everywhere" }));
    expect(within(dialog).queryByLabelText("Everything")).not.toBeInTheDocument();
    expect(within(dialog).queryByLabelText("Give permissions without rank limits")).not.toBeInTheDocument();
    fireEvent.click(within(dialog).getByLabelText("Give permissions"));
    expect(within(dialog).getByText(/hand out the permissions they hold/i)).toBeInTheDocument();
  });

  it("shows Everything to a SuperAdmin, with a warning", async () => {
    mockApi();
    const dialog = await openDialog(true);
    fireEvent.click(within(dialog).getByRole("button", { name: "Everywhere" }));
    expect(within(dialog).getByLabelText("Give permissions without rank limits")).toBeInTheDocument();
    fireEvent.click(within(dialog).getByLabelText("Everything"));
    expect(within(dialog).getByText(/full SuperAdmin power/i)).toBeInTheDocument();
  });

  it("shows the server's error in the dialog", async () => {
    mockApi("you can only assign to users, groups and roles ranked below you");
    const dialog = await openDialog();
    await waitFor(() => expect(within(dialog).getByRole("option", { name: "alice" })).toBeInTheDocument());
    fireEvent.change(within(dialog).getByLabelText("User"), { target: { value: "u1" } });
    fireEvent.change(within(dialog).getByLabelText("Workload"), { target: { value: "w1" } });
    fireEvent.click(within(dialog).getByRole("button", { name: "Give permissions" }));
    expect(await within(dialog).findByText("you can only assign to users, groups and roles ranked below you")).toBeInTheDocument();
  });

  it("warns that node terminal and files are root access", async () => {
    mockApi();
    const dialog = await openDialog();
    fireEvent.click(within(dialog).getByRole("button", { name: "Node" }));
    expect(within(dialog).getByText(/root access to that machine/i)).toBeInTheDocument();
    expect(within(dialog).getByLabelText("Root terminal on the host")).not.toBeChecked();
  });
});
