export function canMutate(roles: string[] | undefined): boolean {
  return Boolean(roles?.includes("superadmin") || roles?.includes("admin"));
}

export function isAdmin(roles: string[] | undefined): boolean {
  return Boolean(roles?.includes("superadmin"));
}

export function mutateHint(roles: string[] | undefined): string | null {
  return canMutate(roles) ? null : "Requires Admin or SuperAdmin.";
}

export type GrantUser = {
  roles?: string[];
  grants?: string[];
};

export function hasGrant(user: GrantUser | null | undefined, perm: string): boolean {
  if (!user) {
    return false;
  }
  const grants = user.grants ?? [];
  if (grants.includes("*") || grants.includes(perm)) {
    return true;
  }
  for (const grant of grants) {
    if (grant.endsWith(".*") && perm.startsWith(grant.slice(0, -1))) {
      return true;
    }
  }
  if (user.roles?.includes("superadmin")) {
    return true;
  }
  if (user.roles?.includes("admin")) {
    return (
      perm === "api_access.manage" ||
      perm === "updates.manage" ||
      perm === "feature.manage" ||
      perm === "identity.token.create" ||
      perm === "identity.group.manage" ||
      perm === "node.update" ||
      perm === "settings.license.read"
    );
  }
  return false;
}

export function canSeeManagement(user: GrantUser | null | undefined): boolean {
  return (
    hasGrant(user, "users.read") ||
    hasGrant(user, "roles.manage") ||
    hasGrant(user, "api_access.manage") ||
    hasGrant(user, "feature.manage") ||
    hasGrant(user, "updates.manage") ||
    hasGrant(user, "settings.license.manage") ||
    hasGrant(user, "audit.read") ||
    hasGrant(user, "settings.security.manage") ||
    hasGrant(user, "identity.group.manage") ||
    hasGrant(user, "settings.tls.manage")
  );
}
