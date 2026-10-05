import type { ReactNode } from "react";
import { PageHeader, SectionScopeProvider } from "../components/PageHeader";
import { hasGrant } from "../rbac";
import { usePath } from "../router";
import { useSession } from "../session";
import { SectionNav, type SectionNavGroup } from "../ui/SectionNav";

type Section = { href: string; label: string; icon: SectionNavGroup["items"][number]["icon"]; permission: string };

const GROUPS: { label: string; items: Section[] }[] = [
  {
    label: "Identities",
    items: [
      { href: "/users", label: "Users", icon: "users", permission: "users.read" },
      { href: "/groups", label: "Groups", icon: "users", permission: "identity.group.manage" },
    ],
  },
  {
    label: "Access control",
    items: [
      { href: "/roles", label: "Roles", icon: "shield", permission: "roles.manage" },
      { href: "/roles/permissions", label: "Permissions", icon: "lock", permission: "roles.manage" },
      { href: "/roles/assignments", label: "Assignments", icon: "account", permission: "roles.manage" },
    ],
  },
  {
    label: "Programmatic access",
    items: [{ href: "/api-access", label: "API Access", icon: "key", permission: "api_access.manage" }],
  },
  {
    label: "Policy",
    items: [{ href: "/settings/security", label: "Security", icon: "settings", permission: "settings.security.manage" }],
  },
];

/** IAM sections the user may open, in sidebar order. */
export function iamSections(user: { roles?: string[]; grants?: string[] } | null): Section[] {
  return GROUPS.flatMap((g) => g.items).filter((item) => hasGrant(user, item.permission));
}

/** IAM: Users, Groups, Roles, Permissions, Assignments, API Access and Security in one area. */
export function IAMLayout({ children }: { children: ReactNode }) {
  const path = usePath();
  const session = useSession();
  const user = session.status === "ready" ? session.user : null;
  const groups: SectionNavGroup[] = GROUPS.map((group) => ({
    label: group.label,
    items: group.items
      .filter((item) => hasGrant(user, item.permission))
      .map((item) => ({ href: item.href, label: item.label, icon: item.icon, current: path === item.href })),
  }));
  return (
    <section className="page page-wide" aria-labelledby="iam-heading">
      <PageHeader
        id="iam-heading"
        icon="shield"
        title="Identity and Access Management"
        kicker="Accounts, roles, API access and authentication policy. Authorization is always enforced by the control plane."
      />
      <div className="ctx-page">
        <SectionNav label="IAM sections" groups={groups} />
        <div className="ctx-page-body">
          <SectionScopeProvider>{children}</SectionScopeProvider>
        </div>
      </div>
    </section>
  );
}
