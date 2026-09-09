import { describe, expect, it } from "vitest";
import { bytesFromGB, gbFromBytes, GIB_BYTES, MIB_BYTES } from "./memory";

describe("memory GB conversion", () => {
  it("uses 1 GB = 1024 MiB", () => {
    expect(bytesFromGB(1)).toBe(1024 * MIB_BYTES);
    expect(bytesFromGB(2)).toBe(2048 * MIB_BYTES);
    expect(bytesFromGB(4)).toBe(4096 * MIB_BYTES);
    expect(bytesFromGB(8)).toBe(8192 * MIB_BYTES);
    expect(bytesFromGB(1)).toBe(GIB_BYTES);
  });

  it("does not round exact GiB values", () => {
    expect(gbFromBytes(bytesFromGB(16))).toBe("16");
    expect(gbFromBytes(256 * MIB_BYTES)).toBe(String(256 / 1024));
  });
});
