import type { ReactNode } from "react";
import type { IconName } from "../components/Icon";
import { PageNav } from "../components/PageNav";
import { SectionScopeProvider } from "../components/PageHeader";
import { hasGrant } from "../rbac";
import { useSession } from "../session";

type Section = { href: string; label: string; icon: IconName; permissions: string[]; also?: string[] };

const SECTIONS: Section[] = [
  // Users holds approval requests too.
  { href: "/users", label: "Users", icon: "users", permissions: ["users.read", "approvals.view"], also: ["/iam/approvals"] },
  // Access control holds API access too (tokens and service accounts).
  {
    href: "/iam/access",
    label: "Access control",
    icon: "shield",
    permissions: ["roles.manage", "identity.group.manage", "api_access.manage", "identity.token.create"],
    also: ["/api-access"],
  },
];

/** IAM sections the user may open, in bar order. */
export function iamSections(user: { roles?: string[]; grants?: string[] } | null): { href: string; label: string }[] {
  return SECTIONS.filter((s) => s.permissions.some((p) => hasGrant(user, p)));
}

/**
 * IAM pages share a page bar of sections across the top, so each page
 * gets the full width below it.
 */
export function IAMLayout({ children }: { children: ReactNode }) {
  const session = useSession();
  const user = session.status === "ready" ? session.user : null;
  const items = SECTIONS.filter((s) => s.permissions.some((p) => hasGrant(user, p))).map((s) => ({
    id: s.href,
    label: s.label,
    href: s.href,
    icon: s.icon,
    match: (p: string) => p === s.href || Boolean(s.also?.includes(p)),
  }));
  return (
    <section className="page page-wide iam-page" aria-label="Identity and Access Management">
      <PageNav id="iam" label="IAM" items={items} className="is-sub" />
      <SectionScopeProvider>{children}</SectionScopeProvider>
    </section>
  );
}
