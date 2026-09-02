import { describe, expect, it } from "vitest";
import { decodeFrame, encodeFrame, encodeResize } from "./frames";

describe("terminal frames", () => {
  it("round-trips payload and resize", () => {
    const payload = new TextEncoder().encode("hi");
    const frame = encodeFrame(1, payload);
    const decoded = decodeFrame(frame);
    expect(decoded?.type).toBe(1);
    expect(new TextDecoder().decode(decoded?.payload)).toBe("hi");
    const resize = encodeResize(24, 80);
    expect(resize.byteLength).toBe(4);
    const view = new DataView(resize.buffer);
    expect(view.getUint16(0)).toBe(24);
    expect(view.getUint16(2)).toBe(80);
  });
});
