import { describe, expect, it } from "vitest";
import { badgeText } from "./NotificationBell";

describe("badgeText", () => {
  it("shows nothing when everything is read", () => {
    expect(badgeText(0)).toBe("");
  });

  it("shows the count up to 99, then 99+", () => {
    expect(badgeText(1)).toBe("1");
    expect(badgeText(10)).toBe("10");
    expect(badgeText(99)).toBe("99");
    expect(badgeText(100)).toBe("99+");
    expect(badgeText(4321)).toBe("99+");
  });
});
