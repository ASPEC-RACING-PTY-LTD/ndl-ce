import { useCallback, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { useFloatingPanel } from "../ui/floating";
import { Icon } from "./Icon";

export type ActionItem = {
  label: string;
  onClick: () => void;
  danger?: boolean;
};

/**
 * ActionMenu is the "…" menu. Its panel floats above the page (rendered on
 * document.body), so a table or card that scrolls or clips never cuts it
 * off. It opens downwards, or upwards when there is no room below.
 */
export function ActionMenu({
  label = "More actions",
  items,
}: {
  label?: string;
  items: ActionItem[];
}) {
  const [open, setOpen] = useState(false);
  const root = useRef<HTMLDivElement>(null);
  const button = useRef<HTMLButtonElement>(null);
  const panel = useRef<HTMLDivElement>(null);

  useFloatingPanel(open, root, panel, useCallback(() => setOpen(false), []));

  if (items.length === 0) {
    return null;
  }

  return (
    <div className="menu" ref={root}>
      <button
        ref={button}
        className="btn btn-ghost btn-sm btn-icon"
        type="button"
        aria-label={label}
        aria-expanded={open}
        aria-haspopup="true"
        title={label}
        onClick={() => setOpen((v) => !v)}
      >
        <Icon name="more" />
      </button>
      {open
        ? createPortal(
            <div ref={panel} className="menu-panel is-floating" role="menu" aria-label={label}>
              {items.map((item) => (
                <button
                  key={item.label}
                  type="button"
                  role="menuitem"
                  className={item.danger ? "is-danger" : undefined}
                  onClick={() => {
                    setOpen(false);
                    item.onClick();
                  }}
                >
                  {item.label}
                </button>
              ))}
            </div>,
            document.body,
          )
        : null}
    </div>
  );
}
