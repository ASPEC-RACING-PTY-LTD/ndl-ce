import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { DeployProject, DeployProjectList, Deployment } from "../api/client";

const running: Deployment = {
  id: "d1",
  project_id: "p1",
  status: "running",
  trigger: "manual",
  actor: "admin",
  started_at: new Date(Date.now() - 30_000).toISOString(),
  step_count: 2,
  current_step: 2,
  steps: [
    { command: "git pull", started_at: "2026-10-17T00:00:00Z", finished_at: "2026-10-17T00:00:01Z", exit_code: 0 },
    { command: "docker compose up -d --build", started_at: "2026-10-17T00:00:01Z" },
  ],
};

const project = (over: Partial<DeployProject> = {}): DeployProject => ({
  id: "p1",
  workload_id: "w1",
  workload_name: "apps",
  name: "shop",
  working_dir: "/srv/shop",
  config: { commands: [{ run: "git pull origin main && docker compose up -d --build --force-recreate" }], detect: true },
  state: { checked_at: "2026-10-17T00:00:00Z", behind: 3, due: true, remote_subject: "Fix cart" },
  ...over,
});

const list = (over: Partial<DeployProjectList> = {}): DeployProjectList => ({
  projects: [project({ active_deployment: running, estimate_sec: 90 }), project({ id: "p2", name: "blog", state: { checked_at: "x", behind: 0 } })],
  discovered: [{ workload_id: "w2", workload_name: "web", name: "wiki", working_dir: "/opt/wiki", services: 2, running: 2 }],
  settings: { default_commands: [{ run: "git pull origin main && docker compose up -d --build --force-recreate" }], default_timeout_sec: 1800, detect_interval_sec: 600 },
  running: 1,
  queued: 0,
  can_configure: true,
  can_deploy: true,
  ...over,
});

const api = vi.hoisted(() => ({
  listDeployProjects: vi.fn(),
  deployProject: vi.fn(),
  createDeployProject: vi.fn(),
  listWorkloads: vi.fn(),
  listProjectDeployments: vi.fn(),
  bulkDeploy: vi.fn(),
  checkDeployProject: vi.fn(),
  listAlertChannels: vi.fn(),
  setDeployWebhook: vi.fn(),
}));

vi.mock("../api/client", async (orig) => ({
  ...(await orig<typeof import("../api/client")>()),
  ...api,
}));

import { setAutoConfirm } from "../confirm";
import { DeployPage } from "./DeployPage";

beforeEach(() => {
  api.listDeployProjects.mockResolvedValue(list());
  api.listWorkloads.mockResolvedValue({ items: [{ id: "w1", name: "apps", kind: "system-container" }, { id: "w2", name: "web", kind: "system-container" }] });
  api.deployProject.mockResolvedValue({ ...running, id: "d2", project_id: "p2", status: "queued" });
  api.listProjectDeployments.mockResolvedValue({ deployments: [] });
  api.bulkDeploy.mockResolvedValue({ batch_id: "b1", queued: [running], skipped: [{ id: "p1", name: "shop", reason: "shop is already deploying" }] });
  api.checkDeployProject.mockResolvedValue(project());
  api.listAlertChannels.mockResolvedValue({ items: [{ id: "c1", name: "Discord", kind: "webhook" }] });
  api.setDeployWebhook.mockResolvedValue({ enabled: true, secret: "s".repeat(64), path: "/api/v1/hooks/deploy/p2" });
  api.createDeployProject.mockImplementation(async (b) => ({ ...project(), id: "p9", name: b.name }));
});

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe("DeployPage", () => {
  it("shows updates, progress and projects found in containers", async () => {
    render(<DeployPage />);
    expect(await screen.findByText("shop")).toBeTruthy();
    expect(screen.getAllByText("3 behind").length).toBeGreaterThan(0);
    expect(screen.getByText("Up to date")).toBeTruthy();
    expect(screen.getByText("Command 2 of 2")).toBeTruthy();
    expect(screen.getByText("wiki")).toBeTruthy();
  });

  it("deploys one project", async () => {
    render(<DeployPage />);
    await screen.findByText("blog");
    fireEvent.click(screen.getByRole("button", { name: "Deploy" }));
    await waitFor(() => expect(api.deployProject).toHaveBeenCalledWith("p2"));
  });

  it("adds a found project with the default commands", async () => {
    render(<DeployPage />);
    await screen.findByText("wiki");
    fireEvent.click(screen.getByRole("button", { name: "Add wiki" }));
    expect((screen.getByLabelText("Working folder") as HTMLInputElement).value).toBe("/opt/wiki");
    expect((screen.getByLabelText("Command 1") as HTMLTextAreaElement).value).toContain("docker compose up -d --build --force-recreate");
    fireEvent.click(screen.getByRole("button", { name: "Add command" }));
    fireEvent.change(screen.getByLabelText("Command 2"), { target: { value: "docker image prune -f" } });
    const submits = screen.getAllByRole("button", { name: "Add project" });
    fireEvent.click(submits[submits.length - 1]);
    await waitFor(() => expect(api.createDeployProject).toHaveBeenCalled());
    const body = api.createDeployProject.mock.calls[0][0];
    expect(body.workload_id).toBe("w2");
    expect(body.config.commands.map((c: { run: string }) => c.run)).toEqual([
      "git pull origin main && docker compose up -d --build --force-recreate",
      "docker image prune -f",
    ]);
  });

  it("deploys every project with an update, or the chosen ones", async () => {
    api.listDeployProjects.mockResolvedValue(
      list({ projects: [project({ id: "p1", name: "shop" }), project({ id: "p2", name: "blog", state: { checked_at: "x", behind: 0 } })] }),
    );
    setAutoConfirm(true);
    render(<DeployPage />);
    await screen.findByText("blog");
    fireEvent.click(screen.getByRole("button", { name: "Deploy all updates (1)" }));
    await waitFor(() => expect(api.bulkDeploy).toHaveBeenCalledWith({ due: true }));
    expect(await screen.findByText(/1 queued; skipped shop: shop is already deploying/)).toBeTruthy();
    fireEvent.click(screen.getByLabelText("Select blog"));
    fireEvent.click(screen.getByRole("button", { name: "Deploy selected (1)" }));
    await waitFor(() => expect(api.bulkDeploy).toHaveBeenLastCalledWith({ project_ids: ["p2"] }));
    setAutoConfirm(false);
  });

  it("checks one project for updates", async () => {
    render(<DeployPage />);
    await screen.findByText("blog");
    fireEvent.click(screen.getByRole("button", { name: "Check" }));
    await waitFor(() => expect(api.checkDeployProject).toHaveBeenCalledWith("p2"));
  });

  it("turns a project's webhook on and shows the secret once", async () => {
    render(<DeployPage />);
    await screen.findByText("blog");
    fireEvent.click(screen.getByRole("button", { name: "blog" }));
    fireEvent.click(await screen.findByRole("tab", { name: "Settings" }));
    expect(await screen.findByText("Discord")).toBeTruthy();
    expect((screen.getByLabelText("A deployment fails") as HTMLInputElement).checked).toBe(false);
    fireEvent.click(screen.getByRole("button", { name: "Turn on" }));
    await waitFor(() => expect(api.setDeployWebhook).toHaveBeenCalledWith("p2", true));
    expect(((await screen.findByLabelText(/Secret/)) as HTMLInputElement).value).toBe("s".repeat(64));
  });

  it("hides Owner controls from Operators", async () => {
    api.listDeployProjects.mockResolvedValue(list({ can_configure: false }));
    render(<DeployPage />);
    await screen.findByText("shop");
    expect(screen.queryByRole("button", { name: "Add project" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Add" })).toBeNull();
    expect(screen.getByRole("button", { name: "Deploy" })).toBeTruthy();
  });
});
