import { Link } from "../components/Link";
import { RequireGrant } from "../components/RequireGrant";
import { APIAccessPage } from "../pages/APIAccessPage";
import { AccessControlPage } from "../pages/AccessControlPage";
import { hasGrant } from "../rbac";
import { useSession } from "../session";

export const ACCESS_ROLES_PATH = "/iam/access";
export const ACCESS_API_PATH = "/api-access";

/**
 * AccessControlArea is Access control's two views: roles and groups, and
 * API access (tokens and service accounts). API access used to be its own
 * IAM section; its address still works and opens this view.
 */
export function AccessControlArea({ view }: { view: "roles" | "api" }) {
  const session = useSession();
  const user = session.status === "ready" ? session.user : null;
  const canRoles = hasGrant(user, "identity.read");
  const canAPI = hasGrant(user, "api_access.manage") || hasGrant(user, "identity.token.create");
  return (
    <>
      {canRoles && canAPI ? (
        <nav className="subnav iam-subnav" aria-label="Access control">
          <Link href={ACCESS_ROLES_PATH} aria-current={view === "roles" ? "page" : undefined}>
            Roles &amp; groups
          </Link>
          <Link href={ACCESS_API_PATH} aria-current={view === "api" ? "page" : undefined}>
            API access
          </Link>
        </nav>
      ) : null}
      {view === "roles" ? (
        <RequireGrant permission="identity.read">
          <AccessControlPage />
        </RequireGrant>
      ) : (
        <RequireGrant permission="api_access.manage">
          <APIAccessPage />
        </RequireGrant>
      )}
    </>
  );
}
