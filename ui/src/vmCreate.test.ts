import { describe, expect, it } from "vitest";
import { buildVmCreateBody, type VmCreateFields } from "./vmCreate";

const fields: VmCreateFields = {
  name: "vm-1",
  cpus: "2",
  memoryMiB: "2048",
  networkID: "net",
  poolID: "pool",
  firmware: "bios",
  autostart: false,
  cloudImageID: "",
  isoID: "",
  hostname: "vm-1",
  username: "debian",
  sshKeys: "",
};

describe("VM create body", () => {
  it("is identical for Guided and Advanced from the same fields", () => {
    const guided = buildVmCreateBody(fields);
    const advanced = buildVmCreateBody(fields);
    expect(JSON.stringify(guided)).toBe(JSON.stringify(advanced));
    expect(guided.kind).toBe("vm");
  });
});
