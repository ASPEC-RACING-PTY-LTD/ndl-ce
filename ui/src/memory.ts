export const MIB_BYTES = 1024 * 1024;
export const GIB_BYTES = 1024 * MIB_BYTES;

export function bytesFromGB(gb: number): number {
  return gb * GIB_BYTES;
}

export function gbFromBytes(bytes?: number, fallbackGB = 1): string {
  if (bytes == null || !Number.isFinite(bytes) || bytes <= 0) {
    return String(fallbackGB);
  }
  const gb = bytes / GIB_BYTES;
  if (Number.isInteger(gb)) {
    return String(gb);
  }
  return String(gb);
}

export function parseMemoryGB(value: string, fallbackGB: number): number {
  const n = Number(value);
  if (!Number.isFinite(n) || n <= 0) {
    return fallbackGB;
  }
  return n;
}
