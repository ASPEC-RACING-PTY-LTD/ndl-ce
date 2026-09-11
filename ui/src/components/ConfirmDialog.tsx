import type { ReactNode } from "react";
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
      {children}
    </Dialog>
  );
}
