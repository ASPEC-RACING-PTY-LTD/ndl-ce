import { cleanup, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

vi.mock("../api/client", async (orig) => ({
  ...(await orig<typeof import("../api/client")>()),
  getUpdates: vi.fn(async () => ({
    channel: "stable",
    host_supported: true,
    host_reason: "Debian 13",
    repository_configured: true,
    packages: [{ name: "nodal", version: "1.2.3", status: "current" }],
    rollback: { available: false, reason: "No earlier version is kept." },
  })),
  getLiveUpdates: vi.fn(async () => ({ enabled: false, history: [] })),
  listFeatures: vi.fn(async () => ({
    base_install: "light",
    gpu_optional: true,
    items: [
      {
        id: "docker",
        title: "Docker",
        enabled: true,
        installed: true,
        core: false,
        package_status: "installed",
        runtime_status: "running",
        starts_runtime: true,
        kubelet_started: false,
        workload_count: 0,
      },
    ],
  })),
}));

vi.mock("../session", () => ({
  useSession: () => ({ status: "ready", user: { user_id: "u1", username: "u", roles: ["superadmin"], grants: ["*"] } }),
}));

vi.mock("../nav/NavDisclosure", async (orig) => ({
  ...(await orig<typeof import("../nav/NavDisclosure")>()),
  useCapabilityOn: () => true,
  useNavDisclosure: () => ({
    prefs: { enabled: [] },
    featureEnabled: { docker: true },
    setUiEnabled: () => undefined,
    reloadFeatures: async () => undefined,
  }),
}));

import { FeaturesPage } from "./FeaturesPage";
import { UpdatesPage } from "./UpdatesPage";

afterEach(() => {
  cleanup();
});

describe("Updates page", () => {
  it("lists the actions in order with the new names", async () => {
    render(<UpdatesPage />);
    const actions = await screen.findByRole("heading", { name: "Actions" });
    const panel = actions.closest("article") as HTMLElement;
    const names = within(panel)
      .getAllByRole("button")
      .map((b) => b.textContent);
    expect(names).toEqual(["Check for updates", "Dry run", "Create checkpoint", "Roll back", "Apply update"]);
    expect(screen.queryByRole("button", { name: "Run preflight" })).toBeNull();
    expect(await screen.findByText("1.2.3")).toBeTruthy();
  });
});

describe("Add Features page", () => {
  it("shows one table with no Grid or Table switch", async () => {
    render(<FeaturesPage />);
    expect(await screen.findByText("Docker")).toBeTruthy();
    expect(screen.queryByRole("group", { name: "View" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Grid" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Table" })).toBeNull();
    expect(screen.getAllByRole("table").length).toBeGreaterThan(0);
    expect(screen.getAllByRole("button", { name: "Disable" }).length).toBeGreaterThan(0);
  });
});
