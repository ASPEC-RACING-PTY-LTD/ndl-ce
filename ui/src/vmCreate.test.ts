import { describe, expect, it } from "vitest";
import { buildVmCreateBody, type VmCreateFields } from "./vmCreate";

const fields: VmCreateFields = {
  name: "vm-1",
  cpus: "2",
  memoryGB: "2",
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

  it("sends a physical boot disk without a storage pool", () => {
    const body = buildVmCreateBody({
      ...fields,
      bootDisk: "physical",
      physicalDeviceID: "ata-CT1000MX500SSD1_0001",
      firmware: "uefi",
    });
    expect(body.pool_id).toBeUndefined();
    expect(body.cloud_image_id).toBeUndefined();
    expect(body.spec?.disks).toEqual([
      {
        role: "boot",
        source: "physical",
        device_id: "ata-CT1000MX500SSD1_0001",
        format: "raw",
        bus: "ahci",
      },
    ]);
    expect(body.nocloud.enable).toBe(false);
  });
});
