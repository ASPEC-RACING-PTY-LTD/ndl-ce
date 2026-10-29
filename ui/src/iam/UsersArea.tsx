import { useEffect } from "react";
import { hasGrant } from "../rbac";
import { useSession } from "../session";
import { UsersPage } from "../pages/UsersPage";
import { ApprovalsPage } from "./ApprovalsPage";

/** UsersArea is IAM > Users: the user list, with approval requests below it. */
export function UsersArea({ focus }: { focus?: "approvals" }) {
  const session = useSession();
  const user = session.status === "ready" ? session.user : null;
  const canUsers = hasGrant(user, "users.read");
  const canApprovals = hasGrant(user, "approvals.view");

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
      {canApprovals ? (
        <div id="approvals" className="users-area-approvals">
          <ApprovalsPage />
        </div>
      ) : null}
    </div>
  );
}
