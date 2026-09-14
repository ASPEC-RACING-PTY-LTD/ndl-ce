import { type ReactNode } from "react";
import { hasGrant } from "../rbac";
import { useSession } from "../session";
import { ForbiddenPage } from "../pages/ForbiddenPage";

export function RequireGrant({ permission, children }: { permission: string; children: ReactNode }) {
  const session = useSession();
  const user = session.status === "ready" ? session.user : null;
  if (!hasGrant(user, permission)) {
    return <ForbiddenPage />;
  }
  return <>{children}</>;
}
