import { describe, expect, it } from "vitest";
import { statusTone } from "./caps";

describe("statusTone", () => {
  it("is red only for a failed server", () => {
    expect(statusTone("running")).toBe("gs-state-ok");
    expect(statusTone("failed")).toBe("gs-state-bad");
    for (const s of ["pending", "installing", "starting", "stopping"]) {
      expect(statusTone(s)).toBe("gs-state-busy");
    }
    for (const s of ["stopped", "ready", "offline", undefined]) {
      expect(statusTone(s)).toBe("gs-state-idle");
    }
  });
});
