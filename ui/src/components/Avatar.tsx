import { useState } from "react";
import { avatarURL } from "../api/client";

function initials(name: string): string {
  const parts = name.trim().split(/[\s._-]+/).filter(Boolean);
  if (parts.length === 0) {
    return "?";
  }
  return (parts[0][0] + (parts.length > 1 ? parts[parts.length - 1][0] : "")).toUpperCase();
}

/** Avatar shows a user's picture, or their initials when they have none. */
export function Avatar({
  userID,
  name,
  updatedAt,
  url,
  size = 32,
}: {
  userID: string;
  name: string;
  updatedAt?: string;
  /** url is a picture address the person gave; an uploaded picture wins. */
  url?: string;
  size?: number;
}) {
  const src = avatarURL(userID, updatedAt) || (url && url.startsWith("https://") ? url : undefined);
  const [broken, setBroken] = useState<string | null>(null);
  const style = { width: size, height: size, fontSize: Math.max(10, Math.round(size * 0.4)) };
  if (src && broken !== src) {
    return (
      <img className="avatar" src={src} alt="" width={size} height={size} style={style} referrerPolicy="no-referrer" onError={() => setBroken(src)} />
    );
  }
  return (
    <span className="avatar avatar-initials" style={style} aria-hidden="true">
      {initials(name)}
    </span>
  );
}

/** squareImage crops a picture to its centre square and scales it to px, as PNG. */
export async function squareImage(file: File, px = 256): Promise<Blob> {
  const url = URL.createObjectURL(file);
  try {
    const img = await new Promise<HTMLImageElement>((resolve, reject) => {
      const el = new Image();
      el.onload = () => resolve(el);
      el.onerror = () => reject(new Error("That file is not a picture."));
      el.src = url;
    });
    const side = Math.min(img.naturalWidth, img.naturalHeight);
    const canvas = document.createElement("canvas");
    canvas.width = px;
    canvas.height = px;
    const ctx = canvas.getContext("2d");
    if (!ctx) {
      throw new Error("Could not process the picture.");
    }
    ctx.drawImage(img, (img.naturalWidth - side) / 2, (img.naturalHeight - side) / 2, side, side, 0, 0, px, px);
    return await new Promise<Blob>((resolve, reject) =>
      canvas.toBlob((b) => (b ? resolve(b) : reject(new Error("Could not process the picture."))), "image/png"),
    );
  } finally {
    URL.revokeObjectURL(url);
  }
}
