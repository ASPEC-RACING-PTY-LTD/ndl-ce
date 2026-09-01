import { useEffect, useMemo, useState } from "react";
import { filterPaletteActions, visiblePaletteActions } from "../palette";
import { navigate } from "../router";
import { useSession } from "../session";

type CommandPaletteProps = {
  open: boolean;
  onClose: () => void;
};

export function CommandPalette({ open, onClose }: CommandPaletteProps) {
  const session = useSession();
  const roles = session.status === "ready" ? session.user?.roles : undefined;
  const actions = useMemo(() => visiblePaletteActions(roles), [roles]);
  const [query, setQuery] = useState("");
  const [active, setActive] = useState(0);
  const filtered = filterPaletteActions(actions, query);

  useEffect(() => {
    if (!open) {
      return;
    }
    setQuery("");
    setActive(0);
  }, [open]);

  useEffect(() => {
    setActive(0);
  }, [query]);

  if (!open) {
    return null;
  }

  function go(href: string) {
    onClose();
    navigate(href);
  }

  return (
    <div className="palette-backdrop" onClick={onClose}>
      <div
        className="palette"
        role="dialog"
        aria-modal="true"
        aria-labelledby="palette-heading"
        onClick={(event) => event.stopPropagation()}
        onKeyDown={(event) => {
          if (event.key === "Escape") {
            event.preventDefault();
            onClose();
          }
          if (event.key === "ArrowDown") {
            event.preventDefault();
            setActive((n) => Math.min(n + 1, Math.max(filtered.length - 1, 0)));
          }
          if (event.key === "ArrowUp") {
            event.preventDefault();
            setActive((n) => Math.max(n - 1, 0));
          }
          if (event.key === "Enter" && filtered[active]) {
            event.preventDefault();
            go(filtered[active].href);
          }
        }}
      >
        <h2 id="palette-heading" className="palette-heading">
          Command palette
        </h2>
        <label className="field-label" htmlFor="palette-search">
          Search
        </label>
        <input
          id="palette-search"
          className="field-input"
          autoFocus
          value={query}
          placeholder="Jump to a page or action"
          onChange={(event) => setQuery(event.target.value)}
        />
        <ul className="palette-list">
          {filtered.length === 0 ? (
            <li className="muted">No matching actions.</li>
          ) : (
            filtered.map((action, index) => (
              <li key={action.id}>
                <button
                  type="button"
                  className={index === active ? "palette-item palette-item-active" : "palette-item"}
                  onClick={() => go(action.href)}
                  onMouseEnter={() => setActive(index)}
                >
                  {action.label}
                </button>
              </li>
            ))
          )}
        </ul>
        <p className="field-hint">Actions are filtered by your roles. Expert mode does not add any.</p>
      </div>
    </div>
  );
}
