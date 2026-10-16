import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { useState } from "react";
import { describe, expect, it } from "vitest";
import { ApiError } from "../api/client";
import { useHostTunnelApproval } from "./HostTunnelApproval";

function refusal(canApprove: boolean) {
  return new ApiError(409, "This is the host's own cloudflared tunnel, which No-dal does not manage.", {
    code: "host_tunnel_protected",
    advice: "Check it on the host as root: systemctl status cloudflared.service",
    can_approve: canApprove,
  });
}

function Harness({ canApprove, calls }: { canApprove: boolean; calls: boolean[] }) {
  const gate = useHostTunnelApproval();
  const [result, setResult] = useState("");
  return (
    <div>
      {gate.dialog}
      <button
        type="button"
        onClick={() =>
          gate
            .guard(async (approve) => {
              calls.push(approve);
              if (!approve) throw refusal(canApprove);
              return "restarted";
            })
            .then(setResult, (err: Error) => setResult(err.message))
        }
      >
        Restart
      </button>
      <output>{result}</output>
    </div>
  );
}

describe("host tunnel approval", () => {
  it("shows the advice and retries only after an Owner approves", async () => {
    const calls: boolean[] = [];
    render(<Harness canApprove calls={calls} />);
    fireEvent.click(screen.getByRole("button", { name: "Restart" }));
    expect(await screen.findByText(/systemctl status cloudflared.service/)).toBeInTheDocument();
    expect(calls).toEqual([false]);
    fireEvent.click(screen.getByRole("button", { name: /approve and continue/i }));
    await waitFor(() => expect(document.querySelector("output")?.textContent).toBe("restarted"));
    expect(calls).toEqual([false, true]);
  });

  it("leaves the host alone when declined, and a non-Owner cannot approve", async () => {
    const calls: boolean[] = [];
    render(<Harness canApprove={false} calls={calls} />);
    fireEvent.click(screen.getByRole("button", { name: "Restart" }));
    expect(await screen.findByText(/only an owner can approve/i)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /approve and continue/i })).toBeDisabled();
    fireEvent.click(screen.getByRole("button", { name: /cancel/i }));
    await waitFor(() => expect(document.querySelector("output")?.textContent).toMatch(/left the host's tunnel alone/i));
    expect(calls).toEqual([false]);
  });
});
