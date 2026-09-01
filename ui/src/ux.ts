export type UXLevel = "guided" | "advanced" | "expert";

export function uxLevel(user: { ux_level?: string } | null | undefined): UXLevel {
  if (user?.ux_level === "advanced" || user?.ux_level === "expert") {
    return user.ux_level;
  }
  return "guided";
}

export function canMutate(roles: string[] | undefined): boolean {
  return Boolean(roles?.includes("admin") || roles?.includes("operator"));
}

