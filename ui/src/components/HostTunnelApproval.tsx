import { useRef, useState } from "react";
import { ApiError } from "../api/client";
import { ConfirmDialog } from "./ConfirmDialog";

type Pending = { message: string; advice: string; canApprove: boolean };

/** HOST_TUNNEL_LEFT_ALONE is the error when the approval is declined. */
export const HOST_TUNNEL_LEFT_ALONE = "Left the host's tunnel alone.";

function hostRefusal(err: unknown): Pending | null {
  if (!(err instanceof ApiError) || err.body?.code !== "host_tunnel_protected") {
    return null;
  }
  return {
    message: err.message,
    advice: typeof err.body.advice === "string" ? err.body.advice : "",
    canApprove: err.body.can_approve === true,
  };
}

/**
 * useHostTunnelApproval runs a tunnel change and, when the server refuses it
 * because it would touch the host's own cloudflared, shows what to do by
 * hand and lets a SuperAdmin approve that one change.
 */
export function useHostTunnelApproval() {
  const [pending, setPending] = useState<Pending | null>(null);
  const answer = useRef<((approved: boolean) => void) | null>(null);

  async function guard<T>(run: (approveHost: boolean) => Promise<T>): Promise<T> {
    try {
      return await run(false);
    } catch (err) {
      const refusal = hostRefusal(err);
      if (!refusal) {
        throw err;
      }
      const approved = await new Promise<boolean>((resolve) => {
        answer.current = resolve;
        setPending(refusal);
      });
      if (!approved) {
        throw new Error(HOST_TUNNEL_LEFT_ALONE);
      }
      return run(true);
    }
  }

  function close(approved: boolean) {
    setPending(null);
    answer.current?.(approved);
    answer.current = null;
  }

  const dialog = (
    <ConfirmDialog
      open={pending !== null}
      title="Host cloudflared tunnel"
      danger
      confirmLabel="Approve and continue"
      confirmDisabled={!pending?.canApprove}
      onConfirm={() => close(true)}
      onClose={() => close(false)}
    >
      <p>{pending?.message}</p>
      <p>
        No-dal leaves the host&apos;s own tunnel alone because it usually carries this panel. Changing it briefly drops
        every connection through it, including this page.
      </p>
      {pending?.advice ? <p className="field-hint">{pending.advice}</p> : null}
      {pending && !pending.canApprove ? <p className="field-hint">Only a SuperAdmin can approve this change.</p> : null}
    </ConfirmDialog>
  );

  return { guard, dialog };
}
