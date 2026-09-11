type BrandMarkSize = "sidebar" | "auth";

const SIZE_PX: Record<BrandMarkSize, number> = {
  sidebar: 28,
  auth: 72,
};

export function BrandMark({
  size,
  decorative = false,
}: {
  size: BrandMarkSize;
  decorative?: boolean;
}) {
  const px = SIZE_PX[size];
  return (
    <img
      className={`brand-logo brand-logo-${size}`}
      src="/logo.png"
      width={px}
      height={px}
      alt={decorative ? "" : "No-dal"}
      decoding="async"
    />
  );
}
