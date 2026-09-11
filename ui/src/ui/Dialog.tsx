import { useEffect, useId, useRef, type ReactNode } from "react";

type CloseReason = "backdrop" | "escape" | "close";

export function Dialog({
  open,
  title,
  children,
  footer,
  wide = false,
  drawer = false,
  dismissOnBackdrop = true,
  unsaved = false,
  onClose,
}: {
  open: boolean;
  title: string;
  children: ReactNode;
  footer?: ReactNode;
  wide?: boolean;
  drawer?: boolean;
  dismissOnBackdrop?: boolean;
  unsaved?: boolean;
  onClose: () => void;
}) {
  const ref = useRef<HTMLDialogElement>(null);
  const downOnBackdrop = useRef(false);
  const titleId = useId();
  const previousFocus = useRef<HTMLElement | null>(null);

  useEffect(() => {
    const node = ref.current;
    if (!node) {
      return;
    }
    if (open && !node.open) {
      previousFocus.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
      if (typeof node.showModal === "function") {
        node.showModal();
      } else {
        node.setAttribute("open", "");
      }
    }
    if (!open && node.open) {
      if (typeof node.close === "function") {
        node.close();
      } else {
        node.removeAttribute("open");
      }
      previousFocus.current?.focus?.();
    }
  }, [open]);

  function requestClose(reason: CloseReason) {
    if (unsaved && reason !== "close") {
      const ok = window.confirm("Discard unsaved changes?");
      if (!ok) {
        return;
      }
    }
    onClose();
  }

  return (
    <dialog
      ref={ref}
      className="dialog-backdrop"
      aria-labelledby={titleId}
      onCancel={(event) => {
        event.preventDefault();
        requestClose("escape");
      }}
      onPointerDown={(event) => {
        downOnBackdrop.current = event.target === event.currentTarget;
      }}
      onPointerUp={(event) => {
        const upOnBackdrop = event.target === event.currentTarget;
        const selected = window.getSelection()?.toString();
        if (selected) {
          downOnBackdrop.current = false;
          return;
        }
        if (dismissOnBackdrop && downOnBackdrop.current && upOnBackdrop) {
          requestClose("backdrop");
        }
        downOnBackdrop.current = false;
      }}
    >
      {open ? (
        <div
          className={
            drawer ? "dialog-panel dialog-drawer stack" : wide ? "dialog-panel dialog-wide stack" : "dialog-panel stack"
          }
          role="document"
        >
          <div className="dialog-head">
            <h2 id={titleId}>{title}</h2>
            <button className="btn btn-ghost btn-icon" type="button" aria-label="Close" onClick={() => requestClose("close")}>
              Close
            </button>
          </div>
          {children}
          {footer}
        </div>
      ) : null}
    </dialog>
  );
}
