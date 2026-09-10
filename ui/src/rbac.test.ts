import { describe, expect, it } from "vitest";
import { canSeeManagement, hasGrant } from "./rbac";

describe("hasGrant", () => {
  it("treats owner grants as unrestricted", () => {
    expect(hasGrant({ roles: ["admin"], grants: ["*"] }, "users.delete")).toBe(true);
  });

  it("denies user management to a standard user", () => {
    const viewer = { roles: ["viewer"], grants: ["identity.read"] };
    expect(hasGrant(viewer, "users.read")).toBe(false);
    expect(canSeeManagement(viewer)).toBe(false);
  });

  it("lets an admin manage API access but not users", () => {
    const operator = { roles: ["operator"], grants: ["api_access.manage", "feature.manage", "updates.manage"] };
    expect(hasGrant(operator, "api_access.manage")).toBe(true);
    expect(hasGrant(operator, "users.read")).toBe(false);
    expect(canSeeManagement(operator)).toBe(true);
  });
});
