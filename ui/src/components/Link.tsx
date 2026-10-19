import type { AnchorHTMLAttributes, MouseEvent } from "react";
import { capabilityForHref } from "../nav/modules";
import { useCapabilityOn } from "../nav/NavDisclosure";
import { navigate } from "../router";

type LinkProps = AnchorHTMLAttributes<HTMLAnchorElement> & {
  href: string;
  replace?: boolean;
};

export function Link({ href, replace, onClick, className, children, ...props }: LinkProps) {
  // A link into a disabled feature renders as inert text, never a link.
  const featureOn = useCapabilityOn(capabilityForHref(href));
  const handleClick = (event: MouseEvent<HTMLAnchorElement>) => {
    onClick?.(event);
    if (
      event.defaultPrevented ||
      event.button !== 0 ||
      event.metaKey ||
      event.altKey ||
      event.ctrlKey ||
      event.shiftKey
    ) {
      return;
    }
    event.preventDefault();
    navigate(href, { replace });
  };

  if (!featureOn) {
    return (
      <span
        className={(className ? className + " " : "") + "is-feature-off"}
        aria-disabled="true"
        title="This feature is disabled"
        id={props.id}
        role={props.role}
      >
        {children}
      </span>
    );
  }
  return (
    <a href={href} onClick={handleClick} className={className} {...props}>
      {children}
    </a>
  );
}
