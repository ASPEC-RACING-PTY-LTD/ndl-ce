import { type ReactNode } from "react";
import { hasGrant } from "../rbac";
import { useSession } from "../session";
import { ForbiddenPage } from "../pages/ForbiddenPage";

export function RequireGrant({ permission, anyPermission, children }: { permission?: string; anyPermission?: string[]; children: ReactNode }) {
  const session = useSession();
  const user = session.status === "ready" ? session.user : null;
  const wanted = anyPermission ?? (permission ? [permission] : []);
  if (!wanted.some((p) => hasGrant(user, p))) {
    return <ForbiddenPage />;
  }
  return <>{children}</>;
}
