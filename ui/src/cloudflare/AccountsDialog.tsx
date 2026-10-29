import { confirmAction } from "../confirm";
import { deleteCfCredential, validateCfCredential, type CfCredential } from "../api/client";
import { ActionMenu } from "../components/ActionMenu";
import { StatusBadge } from "../components/StatusBadge";
import { formatRelative, formatWhen } from "../format";
import { Dialog } from "../ui/Dialog";
import { CfPermissions } from "./PermissionsGuide";

type Props = {
  open: boolean;
  creds: CfCredential[];
  canManage: boolean;
  canCreds: boolean;
  busy: string | null;
  fetchedAt?: string;
  permsOpen: Set<string>;
  onTogglePerms: (id: string) => void;
  onClose: () => void;
  onConnect: () => void;
  onReplace: (c: CfCredential) => void;
  onSync: () => void;
  /** act runs fn, shows done when it succeeds and reloads from Cloudflare. */
  act: (label: string, fn: () => Promise<unknown>, done?: string) => void;
};

/** AccountsDialog manages the saved Cloudflare tokens and the accounts they reach. */
export function AccountsDialog({ open, creds, canManage, canCreds, busy, fetchedAt, permsOpen, onTogglePerms, onClose, onConnect, onReplace, onSync, act }: Props) {
  const syncing = busy === "Syncing from Cloudflare";
  return (
    <Dialog open={open} title="Manage accounts" wide onClose={onClose}>
      <div className="cf-accounts-bar">
        {canCreds ? (
          <button className="btn btn-sm btn-primary" type="button" onClick={onConnect}>
            Connect account
          </button>
        ) : null}
        <button className="btn btn-sm btn-secondary" type="button" disabled={busy !== null} onClick={onSync}>
          {syncing ? "Syncing" : "Force sync from Cloudflare"}
        </button>
        <span className="meta">
          {syncing ? "Reading every account again" : fetchedAt ? `Last read from Cloudflare ${formatRelative(fetchedAt)}` : ""}
        </span>
      </div>
      <p className="field-hint">
        Force sync checks every saved token again and reads tunnels, hostnames and DNS straight from Cloudflare. Use it after fixing something in the
        Cloudflare dashboard.
      </p>
      {creds.length === 0 ? <p className="meta">No tokens saved yet.</p> : null}
      <div className="cf-accounts-grid">
        {creds.map((c) => (
          <article key={c.id} className={"docker-card" + (c.status === "invalid" || c.status === "error" ? " is-issue" : "")} aria-label={`Token ${c.name}`}>
            <header className="docker-card-head">
              <strong className="docker-head-name">{c.name}</strong>
              <StatusBadge status={c.status === "active" ? "healthy" : c.status === "limited" ? "warning" : "critical"} label={c.status} />
              <ActionMenu
                label={`${c.name} actions`}
                items={[
                  ...(canManage ? [{ label: "Check again", onClick: () => act("Checking token", () => validateCfCredential(c.id), `${c.name} checked again.`) }] : []),
                  ...(canCreds
                    ? [
                        { label: "Replace token", onClick: () => onReplace(c) },
                        {
                          label: "Remove",
                          danger: true,
                          onClick: () => {
                            if (confirmAction(`Remove ${c.name} from No-dal? Nothing changes in Cloudflare.`)) {
                              act("Removing token", () => deleteCfCredential(c.id), `${c.name} removed. Cloudflare is unchanged.`);
                            }
                          },
                        },
                      ]
                    : []),
                ]}
              />
            </header>
            <p className="meta">
              {c.account_owned ? "Account token" : "User token"} ending {c.token_last4}
              {c.last_verified_at ? ` · checked ${formatWhen(c.last_verified_at)}` : ""}
              {c.expires_at ? ` · expires ${formatWhen(c.expires_at)}` : ""}
            </p>
            {c.status_reason ? <p className="tunnel-reason">{c.status_reason}</p> : null}
            <ul className="plain-list">
              {c.accounts.map((a) => (
                <li key={a.id}>
                  <strong>{a.name}</strong>: {a.zones} zone(s), tunnels {a.tunnels ? "yes" : "no"}, DNS {a.dns ? "yes" : "no"}
                  {(a.problems ?? []).map((p) => (
                    <div key={p} className="tunnel-reason">
                      {p}
                    </div>
                  ))}
                </li>
              ))}
            </ul>
            <CfPermissions
              cred={c}
              open={permsOpen.has(c.id)}
              onToggle={() => onTogglePerms(c.id)}
              onCheckAgain={canManage ? () => act("Checking token", () => validateCfCredential(c.id), `${c.name} checked again.`) : undefined}
            />
          </article>
        ))}
      </div>
    </Dialog>
  );
}
