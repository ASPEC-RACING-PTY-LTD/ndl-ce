import { describe, expect, it } from "vitest";
import {
  hrefForTarget,
  isCreatePath,
  isManagePath,
  isWorkloadsContext,
  resolveView,
  selectedTargetFromPath,
  viewFromPath,
} from "./match";
import type { NavTarget } from "./types";

const alpine: NavTarget = {
  kind: "workload",
  id: "wl-a",
  name: "Alpine",
  group: "system-container",
  typeLabel: "System container",
  status: "running",
  terminalReady: true,
  filesReady: true,
};

const stopped: NavTarget = {
  kind: "workload",
  id: "wl-stop",
  name: "Test VM",
  group: "vm",
  typeLabel: "Virtual machine",
  status: "stopped",
  terminalReady: false,
  filesReady: false,
};

const host: NavTarget = {
  kind: "node",
  id: "node-1",
  name: "no-dal-01",
  group: "host",
  typeLabel: "Host",
  status: "available",
  terminalReady: true,
  filesReady: true,
};

describe("isWorkloadsContext", () => {
  it("covers workload and host-operate routes only", () => {
    expect(isWorkloadsContext("/workloads")).toBe(true);
    expect(isWorkloadsContext("/workloads/wl-a")).toBe(true);
    expect(isWorkloadsContext("/workloads/wl-a/terminal")).toBe(true);
    expect(isWorkloadsContext("/nodes/node-1")).toBe(true);
    expect(isWorkloadsContext("/nodes/node-1/terminal")).toBe(true);
    expect(isWorkloadsContext("/node")).toBe(false);
    expect(isWorkloadsContext("/storage")).toBe(false);
    expect(isWorkloadsContext("/network")).toBe(false);
    expect(isWorkloadsContext("/terminal")).toBe(false);
    expect(isWorkloadsContext("/")).toBe(false);
  });
});

describe("selectedTargetFromPath", () => {
  it("ignores create and manage destinations", () => {
    expect(selectedTargetFromPath("/workloads")).toBeNull();
    expect(selectedTargetFromPath("/workloads/new/vm")).toBeNull();
    expect(selectedTargetFromPath("/workloads/import")).toBeNull();
    expect(selectedTargetFromPath("/workloads/wl-a")).toEqual({ kind: "workload", id: "wl-a" });
    expect(selectedTargetFromPath("/workloads/wl-a/terminal")).toEqual({ kind: "workload", id: "wl-a" });
    expect(selectedTargetFromPath("/nodes/node-1/files")).toEqual({ kind: "node", id: "node-1" });
  });
});

describe("view and href", () => {
  it("keeps terminal when the next target can accept it", () => {
    expect(viewFromPath("/workloads/wl-a/terminal")).toBe("terminal");
    expect(hrefForTarget(alpine, "terminal")).toBe("/workloads/wl-a/terminal");
    expect(hrefForTarget(stopped, "terminal")).toBe("/workloads/wl-stop");
    expect(hrefForTarget(host, "terminal")).toBe("/nodes/node-1/terminal");
    expect(hrefForTarget(host, "snapshots")).toBe("/nodes/node-1");
    expect(resolveView(stopped, "files")).toBe("summary");
  });
});

describe("create and manage paths", () => {
  it("recognizes existing create flows", () => {
    expect(isCreatePath("/workloads/new/system-container")).toBe(true);
    expect(isCreatePath("/workloads/new/vm")).toBe(true);
    expect(isManagePath("/workloads")).toBe(true);
    expect(isManagePath("/workloads/wl-a")).toBe(false);
  });
});
