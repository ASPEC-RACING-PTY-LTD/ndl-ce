export function canMutate(roles: string[] | undefined): boolean {
  return Boolean(roles?.includes("admin") || roles?.includes("operator"));
}

export function isAdmin(roles: string[] | undefined): boolean {
  return Boolean(roles?.includes("admin"));
}

export function mutateHint(roles: string[] | undefined): string | null {
  return canMutate(roles) ? null : "Requires operator or administrator.";
}
