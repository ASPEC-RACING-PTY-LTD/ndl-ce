import { useEffect, useRef, useState } from "react";
import { Icon } from "./Icon";

export type ActionItem = {
  label: string;
  onClick: () => void;
  danger?: boolean;
};

export function ActionMenu({
  label = "More actions",
  items,
}: {
  label?: string;
  items: ActionItem[];
}) {
  const [open, setOpen] = useState(false);
  const root = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) {
      return;
    }
    function onDoc(event: MouseEvent) {
      if (root.current && !root.current.contains(event.target as Node)) {
        setOpen(false);
      }
    }
    function onKey(event: KeyboardEvent) {
      if (event.key === "Escape") {
        setOpen(false);
      }
    }
    document.addEventListener("mousedown", onDoc);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onDoc);
      document.removeEventListener("keydown", onKey);
    };
  }, [open]);

  if (items.length === 0) {
    return null;
  }

  return (
    <div className="menu" ref={root}>
      <button
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
      {open ? (
        <div className="menu-panel" role="menu">
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
        </div>
      ) : null}
    </div>
  );
}
