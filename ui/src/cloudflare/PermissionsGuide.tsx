import { useEffect, useState } from "react";
import { getCfPermissionsGuide, type CfCredential, type CfGuide, type CfGuideUseCase } from "../api/client";
import { LoadingState } from "../components/EmptyState";
import { StatusBadge } from "../components/StatusBadge";
import { formatRelative } from "../format";

// For each thing No-dal does with Cloudflare, shows whether a token can do it
// and, when it cannot, exactly which permission to add and how. Permission
// names, descriptions and the token steps come from Cloudflare's current
// documentation, read by the server (see internal/cloudflare/guide.go).

export type UseCaseState = "ok" | "partial" | "missing" | "unchecked" | "not-needed";

/** caseState says whether a token's saved scope covers a use case. */
export function caseState(uc: CfGuideUseCase, cred: CfCredential): UseCaseState {
  const accounts = cred.accounts ?? [];
  if (uc.user_tokens_only && cred.account_owned) {
    return "not-needed";
  }
  const some = (f: (a: (typeof accounts)[number]) => boolean) => accounts.some(f);
  const all = (f: (a: (typeof accounts)[number]) => boolean) => accounts.length > 0 && accounts.every(f);
  const grade = (f: (a: (typeof accounts)[number]) => boolean): UseCaseState => (all(f) ? "ok" : some(f) ? "partial" : "missing");
  switch (uc.check) {
    case "accounts":
      return grade((a) => Boolean(a.listed));
    case "tunnels":
      return grade((a) => a.tunnels);
    case "zones":
      return grade((a) => a.zones > 0);
    case "dns":
      return grade((a) => a.dns);
  }
  // Write access is only reported by Cloudflare when it is used. Without the
  // matching read access it is certainly missing.
  if (uc.id === "tunnels_edit" && !some((a) => a.tunnels)) {
    return "missing";
  }
  if (uc.id === "dns_edit" && !some((a) => a.dns)) {
    return "missing";
  }
  return "unchecked";
}

const LABEL: Record<UseCaseState, { status: string; label: string }> = {
  ok: { status: "healthy", label: "Allowed" },
  partial: { status: "warning", label: "Some accounts" },
  missing: { status: "critical", label: "Missing" },
  unchecked: { status: "unknown", label: "Checked when used" },
  "not-needed": { status: "unknown", label: "Not needed" },
};

let guideCache: CfGuide | null = null;

/** CfPermissions is the permissions dropdown on an account card. */
export function CfPermissions({ cred, open, onToggle, onCheckAgain }: { cred: CfCredential; open: boolean; onToggle: () => void; onCheckAgain?: () => void }) {
  const [guide, setGuide] = useState<CfGuide | null>(guideCache);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!open || guide) {
      return;
    }
    getCfPermissionsGuide()
      .then((g) => {
        guideCache = g;
        setGuide(g);
      })
      .catch((err) => setError(err instanceof Error ? err.message : "The guide could not be loaded"));
  }, [open, guide]);

  const states = guide ? guide.use_cases.map((uc) => ({ uc, state: caseState(uc, cred) })) : [];
  const missing = states.filter((s) => s.state === "missing" || s.state === "partial");
  const perms = new Map((guide?.permissions ?? []).map((p) => [p.id, p]));
  const toAdd = [...new Set(missing.flatMap((s) => s.uc.needs))]
    .map((id) => perms.get(id))
    .filter((p): p is NonNullable<typeof p> => Boolean(p));
  const accountID = cred.account_hint || cred.accounts[0]?.id || "";
  const dashboard = cred.account_owned && accountID ? `https://dash.cloudflare.com/${accountID}/api-tokens` : guide?.dashboard_url;

  return (
    <div className="cf-perms">
      <button type="button" className="cf-perms-toggle linkish" aria-expanded={open} onClick={onToggle}>
        Permissions{cred.status === "limited" ? " (some missing)" : ""}
      </button>
      {open ? (
        <div className="cf-perms-body">
          {error ? <p className="tunnel-reason">{error}</p> : null}
          {!guide && !error ? <LoadingState label="Reading Cloudflare's permission list" /> : null}
          {guide ? (
            <>
              <ul className="cf-perms-list" aria-label="What this token can do">
                {states.map(({ uc, state }) => (
                  <li key={uc.id}>
                    <StatusBadge status={LABEL[state].status} label={LABEL[state].label} />
                    <span>
                      <strong>{uc.title}</strong>
                      <span className="meta"> {uc.why}</span>
                    </span>
                  </li>
                ))}
              </ul>
              {toAdd.length > 0 ? (
                <div className="cf-perms-fix">
                  <h4>Add these permissions to the token</h4>
                  <table className="cf-perms-table">
                    <thead>
                      <tr>
                        <th>Type</th>
                        <th>Permission</th>
                        <th>Access</th>
                        <th>Resources</th>
                      </tr>
                    </thead>
                    <tbody>
                      {toAdd.map((p) => (
                        <tr key={p.id}>
                          <td>{p.scope}</td>
                          <td>
                            {p.group}
                            <div className="meta">{p.description}</div>
                            {!p.in_docs ? <div className="tunnel-reason">Not in Cloudflare's current list; it may have been renamed.</div> : null}
                          </td>
                          <td>{p.level}</td>
                          <td className="meta">{missing.find((m) => m.uc.needs.includes(p.id))?.uc.resources}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                  <h4>How</h4>
                  <ol className="cf-perms-steps">
                    <li>
                      Open{" "}
                      <a href={dashboard} target="_blank" rel="noopener noreferrer">
                        API Tokens in the Cloudflare dashboard
                      </a>
                      {cred.account_owned ? " for this account" : ""} and find the token ending {cred.token_last4}. Select Edit (or create a new
                      token).
                    </li>
                    <li>Under Permissions, add a row for each permission above: choose the type, then the permission, then the access.</li>
                    <li>Include the accounts and zones No-dal should manage, then select Continue to summary and save.</li>
                    <li>
                      Editing keeps the same token, so select Check again here. If you created a new token, use Replace token.
                      {onCheckAgain ? (
                        <>
                          {" "}
                          <button type="button" className="btn btn-sm btn-secondary" onClick={onCheckAgain}>
                            Check again
                          </button>
                        </>
                      ) : null}
                    </li>
                  </ol>
                  <details className="cf-perms-more">
                    <summary>Cloudflare's own steps for creating a token</summary>
                    <ol>
                      {guide.steps.map((s) => (
                        <li key={s}>{s}</li>
                      ))}
                    </ol>
                  </details>
                </div>
              ) : (
                <p className="banner banner-ok">Nothing is missing for what No-dal can check. Write access is confirmed the first time it is used.</p>
              )}
              <p className="meta cf-perms-source">
                {guide.source === "cloudflare-docs"
                  ? `From Cloudflare's documentation${guide.docs_updated ? `, updated ${guide.docs_updated}` : ""}; read ${formatRelative(guide.fetched_at)}.`
                  : `Built-in list; Cloudflare's documentation could not be read (${guide.error || "unavailable"}).`}{" "}
                <a href={guide.docs_url} target="_blank" rel="noopener noreferrer">
                  All token permissions
                </a>
                {" · "}
                <a href={guide.account_tokens_url} target="_blank" rel="noopener noreferrer">
                  Account-owned tokens
                </a>
              </p>
            </>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}
