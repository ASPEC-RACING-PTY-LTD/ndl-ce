import { useEffect, useState } from "react";
import { ApiError, getTrustedProxies, putTrustedProxies } from "../api/client";
import type { TrustedProxies } from "../generated/openapi";
import { ErrorNotice } from "./ErrorNotice";
import { LoadingState } from "./EmptyState";

type ManualRow = { cidr: string; kind: "cloudflared" | "generic"; note: string; enabled: boolean };

/**
 * TrustedProxiesPanel shows which proxies ndl-ce believes for the real client
 * address. What is in front of it (a cloudflared tunnel, Caddy or Nginx) is
 * detected and trusted without setup; admins add others or turn one off.
 */
export function TrustedProxiesPanel() {
  const [data, setData] = useState<TrustedProxies | null>(null);
  const [manual, setManual] = useState<ManualRow[]>([]);
  const [off, setOff] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  function load(next: TrustedProxies) {
    setData(next);
    setManual(next.manual.map((m) => ({ cidr: m.cidr, kind: m.kind, note: m.note ?? "", enabled: m.enabled })));
    setOff(next.detected.filter((d) => !d.enabled).map((d) => d.id));
  }

  useEffect(() => {
    void getTrustedProxies()
      .then(load)
      .catch((err) => setError(err instanceof Error ? err.message : "Unavailable"));
  }, []);

  async function save() {
    setBusy(true);
    setError(null);
    setNotice(null);
    try {
      load(
        await putTrustedProxies({
          manual: manual.filter((m) => m.cidr.trim()).map((m) => ({ ...m, cidr: m.cidr.trim(), note: m.note.trim() })),
          detected_off: off,
        }),
      );
      setNotice("Trusted proxies saved.");
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Save failed");
    } finally {
      setBusy(false);
    }
  }

  const current = (data?.current ?? {}) as { ip?: string; peer?: string; via_proxy?: boolean };

  return (
    <article className="panel stack">
      <h2>Trusted proxies</h2>
      <p className="lede">
        Client-address headers (CF-Connecting-IP, X-Forwarded-For) are only believed from these. Anything else is identified by its
        own connection, so a visitor cannot fake their address.
      </p>
      {error ? <ErrorNotice error={error} /> : null}
      {notice ? (
        <p className="banner banner-ok" role="status">
          {notice}
        </p>
      ) : null}
      {!data ? (
        <LoadingState />
      ) : (
        <>
          <p className="field-hint">
            This request: ndl-ce sees <code>{String(current.ip ?? "")}</code>
            {current.via_proxy ? ` through a trusted proxy at ${String(current.peer ?? "")}` : ` directly from ${String(current.peer ?? "")}`}.
          </p>
          <h3>Detected</h3>
          {data.detected.length === 0 ? (
            <p className="muted">Nothing detected. Turn on Cloudflare, Caddy or Nginx under Add Features if one is in front of ndl-ce.</p>
          ) : (
            <ul className="plain-list">
              {data.detected.map((d) => (
                <li key={d.id} className="inline-actions">
                  <label className="check-row">
                    <input
                      type="checkbox"
                      checked={!off.includes(d.id)}
                      onChange={(e) => setOff((cur) => (e.target.checked ? cur.filter((x) => x !== d.id) : [...cur, d.id]))}
                    />{" "}
                    <code>{d.cidr}</code> · {d.source} · {d.kind === "cloudflared" ? "CF-Connecting-IP" : "X-Forwarded-For"}
                  </label>
                </li>
              ))}
            </ul>
          )}
          <h3>Added by an admin</h3>
          {manual.length ? (
            <ul className="allowlist-rows">
              {manual.map((m, i) => (
                <li key={i}>
                  <input
                    className="field-input"
                    aria-label={`Proxy ${i + 1} address`}
                    placeholder="10.0.0.5 or 10.0.0.0/24"
                    value={m.cidr}
                    onChange={(e) => setManual((cur) => cur.map((x, j) => (j === i ? { ...x, cidr: e.target.value } : x)))}
                  />
                  <select
                    className="field-input"
                    aria-label={`Proxy ${i + 1} type`}
                    value={m.kind}
                    onChange={(e) =>
                      setManual((cur) => cur.map((x, j) => (j === i ? { ...x, kind: e.target.value as ManualRow["kind"] } : x)))
                    }
                  >
                    <option value="cloudflared">Cloudflare tunnel</option>
                    <option value="generic">Reverse proxy (X-Forwarded-For)</option>
                  </select>
                  <button className="btn btn-ghost btn-sm" type="button" onClick={() => setManual((cur) => cur.filter((_, j) => j !== i))}>
                    Remove
                  </button>
                </li>
              ))}
            </ul>
          ) : (
            <p className="muted">None.</p>
          )}
          <div className="btn-row is-flush">
            <button className="btn btn-ghost" type="button" onClick={() => setManual((cur) => [...cur, { cidr: "", kind: "generic", note: "", enabled: true }])}>
              Add proxy
            </button>
            <button className="btn btn-primary" type="button" disabled={busy} onClick={() => void save()}>
              Save trusted proxies
            </button>
          </div>
        </>
      )}
    </article>
  );
}
