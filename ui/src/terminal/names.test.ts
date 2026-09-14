import { describe, expect, it } from "vitest";
import { defaultTabTitle } from "./names";

describe("defaultTabTitle", () => {
  it("uses the target name then numbers siblings", () => {
    expect(defaultTabTitle("Alpine", [])).toBe("Alpine");
    expect(defaultTabTitle("Alpine", ["Alpine"])).toBe("Alpine (2)");
    expect(defaultTabTitle("Alpine", ["Alpine", "Alpine (2)"])).toBe("Alpine (3)");
    expect(defaultTabTitle("Ubuntu", ["Alpine", "Alpine (2)"])).toBe("Ubuntu");
  });
});
