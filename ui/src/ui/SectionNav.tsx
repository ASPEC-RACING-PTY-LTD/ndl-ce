import { Icon, type IconName } from "../components/Icon";
import { Link } from "../components/Link";

export type SectionNavItem = {
  href: string;
  label: string;
  icon?: IconName;
  current: boolean;
  count?: number;
};

export type SectionNavGroup = { label: string; items: SectionNavItem[] };

/** The in-page sidebar used by areas with several sections (for example IAM). */
export function SectionNav({ label, groups }: { label: string; groups: SectionNavGroup[] }) {
  return (
    <nav className="ctx-page-nav" aria-label={label}>
      {groups
        .filter((group) => group.items.length > 0)
        .map((group) => (
          <div className="ctx-page-group" key={group.label}>
            <p className="ctx-page-group-label">{group.label}</p>
            {group.items.map((item) => (
              <Link
                key={item.href}
                href={item.href}
                className="ctx-page-link"
                aria-current={item.current ? "page" : undefined}
              >
                {item.icon ? <Icon name={item.icon} size={15} /> : null}
                <span>{item.label}</span>
                {item.count != null ? <span className="ctx-page-count">{item.count}</span> : null}
              </Link>
            ))}
          </div>
        ))}
    </nav>
  );
}
