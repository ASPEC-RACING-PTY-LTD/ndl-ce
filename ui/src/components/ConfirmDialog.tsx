import { useEffect, useRef, type ReactNode } from "react";

export function ConfirmDialog({
  open,
  title,
  children,
  confirmLabel = "Confirm",
  danger = false,
  onConfirm,
  onClose,
}: {
  open: boolean;
  title: string;
  children: ReactNode;
  confirmLabel?: string;
  danger?: boolean;
  onConfirm: () => void;
  onClose: () => void;
}) {
  const ref = useRef<HTMLDialogElement>(null);

  useEffect(() => {
    const node = ref.current;
    if (!node) {
      return;
    }
    if (open && !node.open) {
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
    }
  }, [open]);

  return (
    <dialog
      ref={ref}
      className="dialog-backdrop"
      aria-labelledby="confirm-title"
      onClose={onClose}
      onClick={(event) => {
        if (event.target === ref.current) {
          onClose();
        }
      }}
    >
      <form
        className="dialog-panel stack"
        method="dialog"
        onSubmit={(event) => {
          event.preventDefault();
          onConfirm();
        }}
      >
        <h2 id="confirm-title">{title}</h2>
        {children}
        <div className="btn-row">
          <button className="btn btn-ghost" type="button" onClick={onClose}>
            Cancel
          </button>
          <button className={danger ? "btn btn-danger" : "btn btn-primary"} type="submit">
            {confirmLabel}
          </button>
        </div>
      </form>
    </dialog>
  );
}
