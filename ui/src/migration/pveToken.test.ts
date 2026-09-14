import { describe, expect, it } from "vitest";
import { PVE_TOKEN_EXAMPLE, PVE_TOKEN_FORMAT, pveTokenError } from "./pveToken";

describe("pveTokenError", () => {
  it("accepts the full Proxmox token", () => {
    expect(pveTokenError("root@pam!nodal=aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")).toBeNull();
    expect(pveTokenError("  user@pve!backup=secret  ")).toBeNull();
  });

  it("rejects the secret alone with the required format", () => {
    const err = pveTokenError("SECRET-TOKEN-VALUE");
    expect(err).toMatch(/not the secret alone/i);
    expect(err).toContain(PVE_TOKEN_FORMAT);
    expect(err).toContain(PVE_TOKEN_EXAMPLE);
  });

  it("rejects a UUID-only paste", () => {
    expect(pveTokenError("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")).toMatch(/not the secret alone/i);
  });
});
