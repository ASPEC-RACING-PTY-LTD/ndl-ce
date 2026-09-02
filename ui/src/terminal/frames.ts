export function encodeFrame(type: number, payload: Uint8Array): Uint8Array {
  const out = new Uint8Array(5 + payload.length);
  out[0] = type;
  const view = new DataView(out.buffer);
  view.setUint32(1, payload.length);
  out.set(payload, 5);
  return out;
}

export function decodeFrame(raw: Uint8Array): { type: number; payload: Uint8Array } | null {
  if (raw.length < 5) {
    return null;
  }
  const view = new DataView(raw.buffer, raw.byteOffset, raw.byteLength);
  const n = view.getUint32(1);
  return { type: raw[0], payload: raw.slice(5, 5 + n) };
}

export function encodeResize(rows: number, cols: number): Uint8Array {
  const out = new Uint8Array(4);
  const view = new DataView(out.buffer);
  view.setUint16(0, rows);
  view.setUint16(2, cols);
  return out;
}
