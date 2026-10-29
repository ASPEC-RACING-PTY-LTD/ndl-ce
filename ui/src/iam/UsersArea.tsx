import { useEffect } from "react";
import { hasGrant } from "../rbac";
import { useSession } from "../session";
import { UsersPage } from "../pages/UsersPage";
import { ApprovalsPage } from "./ApprovalsPage";
import { ItemAccessPanel } from "./ItemAccessPanel";

/** UsersArea is IAM > Users: the user list, then direct permissions, then approval requests. */
export function UsersArea({ focus }: { focus?: "approvals" }) {
  const session = useSession();
  const user = session.status === "ready" ? session.user : null;
  const canUsers = hasGrant(user, "users.read");
  const canApprovals = hasGrant(user, "approvals.view");
  const canItemAccess = hasGrant(user, "permissions.assign");
  const superAdmin = Boolean(user?.roles?.includes("superadmin"));

  useEffect(() => {
    if (focus !== "approvals") {
      return;
    }
    const timer = setTimeout(() => document.getElementById("approvals")?.scrollIntoView({ block: "start" }), 300);
    return () => clearTimeout(timer);
  }, [focus]);

  return (
    <div className="users-area">
      {canUsers ? <UsersPage /> : null}
      {canItemAccess ? (
        <div className="users-area-section">
          <ItemAccessPanel superAdmin={superAdmin} />
        </div>
      ) : null}
      {canApprovals ? (
        <div id="approvals" className="users-area-approvals">
          <ApprovalsPage />
        </div>
      ) : null}
    </div>
  );
}
