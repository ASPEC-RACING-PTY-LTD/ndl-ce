import { useEffect, useState } from "react";

/** The build this page was loaded from; Vite stamps it. */
export const PAGE_BUILD = typeof __NDL_BUILD__ === "string" ? __NDL_BUILD__ : "";

/**
 * UpdateBanner notices when the host starts serving a newer web UI (a live
 * update or a package upgrade) and offers to reload, so nobody keeps
 * working in an old page without knowing.
 */
export function UpdateBanner({ intervalMs = 60_000 }: { intervalMs?: number }) {
  const [newer, setNewer] = useState(false);

  useEffect(() => {
    if (!PAGE_BUILD) {
      return;
    }
    let stopped = false;
    async function check() {
      try {
        const res = await fetch("/version.json", { cache: "no-store" });
        if (!res.ok) {
          return;
        }
        const body = (await res.json()) as { build?: string };
        if (!stopped && body.build && body.build !== PAGE_BUILD) {
          setNewer(true);
        }
      } catch {
        // Offline or restarting; try again on the next tick.
      }
    }
    const timer = window.setInterval(() => void check(), intervalMs);
    return () => {
      stopped = true;
      window.clearInterval(timer);
    };
  }, [intervalMs]);

  if (!newer) {
    return null;
  }
  return (
    <div className="banner banner-ok update-banner" role="status">
      <span>NDL-CE was updated. Reload to use the new version.</span>
      <button className="btn btn-sm btn-primary" type="button" onClick={() => window.location.reload()}>
        Reload
      </button>
    </div>
  );
}
