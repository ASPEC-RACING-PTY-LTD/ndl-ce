import { useEffect, useRef, type ReactNode } from "react";
import { autoConfirms } from "../confirm";
import { Dialog } from "../ui/Dialog";

export function ConfirmDialog({
  open,
  title,
  children,
  confirmLabel = "Confirm",
  danger = false,
  wide = false,
  hideFooter = false,
  confirmDisabled = false,
  unsaved = false,
  onConfirm,
  onClose,
}: {
  open: boolean;
  title: string;
  children: ReactNode;
  confirmLabel?: string;
  danger?: boolean;
  wide?: boolean;
  hideFooter?: boolean;
  confirmDisabled?: boolean;
  unsaved?: boolean;
  onConfirm: () => void;
  onClose: () => void;
}) {
  // A plain "are you sure" dialog (nothing to fill in) confirms itself for
  // a SuperAdmin; see confirm.ts.
  const body = useRef<HTMLDivElement>(null);
  const fired = useRef(false);
  useEffect(() => {
    if (!open) {
      fired.current = false;
      return;
    }
    if (fired.current || hideFooter || confirmDisabled || !autoConfirms()) {
      return;
    }
    if (body.current && !body.current.querySelector("input, select, textarea")) {
      fired.current = true;
      onConfirm();
    }
  }, [open, hideFooter, confirmDisabled, onConfirm]);
  return (
    <Dialog
      open={open}
      title={title}
      wide={wide}
      unsaved={unsaved}
      onClose={onClose}
      footer={
        hideFooter ? undefined : (
          <div className="btn-row">
            <button className="btn btn-ghost" type="button" onClick={onClose}>
              Cancel
            </button>
            <button
              className={danger ? "btn btn-danger" : "btn btn-primary"}
              type="button"
              disabled={confirmDisabled}
              onClick={onConfirm}
            >
              {confirmLabel}
            </button>
          </div>
        )
      }
    >
      <div ref={body}>{children}</div>
    </Dialog>
  );
}
