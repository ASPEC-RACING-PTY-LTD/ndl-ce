import { useCallback, useEffect, useState } from "react";
import {
  ApiError,
  changeMyPassword,
  confirmMfa,
  disableMyMfa,
  enrollMfa,
  getMe,
  getMyAllowlist,
  getMyIP,
  getMyProfile,
  patchMyProfile,
  putMyAllowlist,
  signOutOtherSessions,
} from "../api/client";
import type { AccountProfile, ClientIPInfo, IPAllowlist, MFAEnrollResponse } from "../generated/openapi";
import { Avatar } from "../components/Avatar";
import { Link } from "../components/Link";
import { NotificationsSection } from "./ThemePage";
import { ErrorNotice } from "../components/ErrorNotice";
import { LoadingState } from "../components/EmptyState";
import { PageHeader } from "../components/PageHeader";
import { formatWhen } from "../format";
import { navigate } from "../router";
import { useSession } from "../session";
import { Segmented } from "../ui/Segmented";

type Tab = "account" | "security" | "notifications";

function errText(err: unknown, fallback: string): string {
  return err instanceof ApiError ? err.message : err instanceof Error ? err.message : fallback;
}

function tabFromURL(): Tab {
  const t = new URLSearchParams(window.location.search).get("tab");
  return t === "security" || t === "notifications" ? t : "account";
}

export function MePage() {
  const session = useSession();
  const user = session.status === "ready" ? session.user : null;
  const [tab, setTabState] = useState<Tab>(tabFromURL);
  const [profile, setProfile] = useState<AccountProfile | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);

  const reload = useCallback(async () => {
    try {
      setProfile(await getMyProfile());
    } catch (err) {
      setLoadError(errText(err, "Account is unavailable"));
    }
  }, []);

  useEffect(() => {
    void reload();
  }, [reload]);

  // The interface level moved to Theme → Other.
  useEffect(() => {
    if (new URLSearchParams(window.location.search).get("tab") === "appearance") {
      navigate("/theme/layout", { replace: true });
    }
  }, []);

  function setTab(next: Tab) {
    setTabState(next);
    navigate(next === "account" ? "/me" : `/me?tab=${next}`, { replace: true });
  }

  async function refreshUser() {
    try {
      const me = await getMe();
      if (me) {
        session.applyUser(me);
      }
    } catch {
      // The page still shows the saved profile.
    }
  }

  if (!user) {
    return null;
  }
  const shown = profile?.display_name || user.display_name || user.username;

  return (
    <section className="page page-wide account-page" aria-labelledby="me-heading">
      <div className="account-head">
        <Avatar userID={user.user_id} name={shown} updatedAt={profile?.avatar_updated_at ?? user.avatar_updated_at} size={56} />
        <PageHeader
          id="me-heading"
          title="Settings"
          kicker={`${shown} · signed in as ${user.username}${user.roles.length ? ` · ${user.roles.join(", ")}` : ""}`}
          actions={
            <Link className="btn btn-ghost" href="/profile">
              Edit profile
            </Link>
          }
        />
      </div>
      <div className="hub-tabs">
        <Segmented<Tab>
          ariaLabel="Account sections"
          value={tab}
          onChange={setTab}
          options={[
            { id: "account", label: "Account" },
            { id: "security", label: "Security and Authenticator" },
            { id: "notifications", label: "Notifications" },
          ]}
        />
      </div>
      {loadError ? <ErrorNotice error={loadError} /> : null}
      {!profile && !loadError ? <LoadingState label="Loading account" /> : null}
      {profile && tab === "account" ? (
        <AccountTab
          profile={profile}
          onSaved={(p) => {
            setProfile(p);
            void refreshUser();
          }}
        />
      ) : null}
      {tab === "notifications" ? <NotificationsSection /> : null}
      {profile && tab === "security" ? <SecurityTab profile={profile} onChanged={() => void reload().then(refreshUser)} /> : null}
    </section>
  );
}

function AccountTab({ profile, onSaved }: { profile: AccountProfile; onSaved: (p: AccountProfile) => void }) {
  const [email, setEmail] = useState(profile.email);
  const [email2, setEmail2] = useState(profile.email_secondary);
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const emailChanged = email.trim() !== profile.email || email2.trim() !== profile.email_secondary;

  async function save() {
    setBusy(true);
    setError(null);
    setNotice(null);
    try {
      const next = await patchMyProfile({ email: email.trim(), email_secondary: email2.trim(), current_password: password });
      setPassword("");
      setNotice("Email saved.");
      onSaved(next);
    } catch (err) {
      setError(errText(err, "Save failed"));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="account-grid">
      {error ? <ErrorNotice error={error} /> : null}
      {notice ? (
        <p className="banner banner-ok" role="status">
          {notice}
        </p>
      ) : null}
      <article className="panel stack">
        <h2>Account</h2>
        <dl className="definition-list">
          <div>
            <dt>Username</dt>
            <dd>{profile.username}</dd>
          </div>
          <div>
            <dt>User ID</dt>
            <dd>
              <code>{profile.user_id}</code>
            </dd>
          </div>
          <div>
            <dt>Roles</dt>
            <dd>{profile.roles.length ? profile.roles.join(", ") : "None"}</dd>
          </div>
          {profile.created_at ? (
            <div>
              <dt>Created</dt>
              <dd>{formatWhen(profile.created_at)}</dd>
            </div>
          ) : null}
          {profile.last_login_at ? (
            <div>
              <dt>Last sign-in</dt>
              <dd>{formatWhen(profile.last_login_at)}</dd>
            </div>
          ) : null}
        </dl>
      </article>

      <article className="panel stack">
        <h2>Email</h2>
        <label htmlFor="acct-email">Email address</label>
        <input id="acct-email" className="field-input" type="email" value={email} onChange={(e) => setEmail(e.target.value)} />
        {profile.email ? <p className="field-hint">{profile.email_verified ? "Verified." : "Not verified."}</p> : null}
        <label htmlFor="acct-email2">Secondary email (optional)</label>
        <input id="acct-email2" className="field-input" type="email" value={email2} onChange={(e) => setEmail2(e.target.value)} />
        {emailChanged ? (
          <>
            <label htmlFor="acct-email-pw">Current password</label>
            <input
              id="acct-email-pw"
              className="field-input"
              type="password"
              autoComplete="current-password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
            />
            <p className="field-hint">Changing where account mail goes needs your password.</p>
          </>
        ) : null}
      </article>

      <div className="btn-row is-flush">
        <button className="btn btn-primary" type="button" disabled={busy || !emailChanged || !password} onClick={() => void save()}>
          Save email
        </button>
      </div>
    </div>
  );
}

function SecurityTab({ profile, onChanged }: { profile: AccountProfile; onChanged: () => void }) {
  return (
    <div className="account-grid">
      <PasswordPanel />
      <MfaPanel enabled={profile.mfa_enabled} allowlistOn={profile.ip_allowlist.enabled} onChanged={onChanged} />
      <AllowlistPanel mfaEnabled={profile.mfa_enabled} onChanged={onChanged} />
      <SessionsPanel />
    </div>
  );
}

function PasswordPanel() {
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [confirm, setConfirm] = useState("");
  const [others, setOthers] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [done, setDone] = useState(false);

  async function save() {
    setError(null);
    setDone(false);
    if (next !== confirm) {
      setError("The new passwords do not match.");
      return;
    }
    setBusy(true);
    try {
      await changeMyPassword({ current_password: current, new_password: next, sign_out_others: others });
      setCurrent("");
      setNext("");
      setConfirm("");
      setDone(true);
    } catch (err) {
      setError(errText(err, "Password change failed"));
    } finally {
      setBusy(false);
    }
  }

  return (
    <article className="panel stack">
      <h2>Password</h2>
      {error ? <ErrorNotice error={error} /> : null}
      {done ? (
        <p className="banner banner-ok" role="status">
          Password changed.
        </p>
      ) : null}
      <label htmlFor="pw-current">Current password</label>
      <input id="pw-current" className="field-input" type="password" autoComplete="current-password" value={current} onChange={(e) => setCurrent(e.target.value)} />
      <label htmlFor="pw-new">New password</label>
      <input id="pw-new" className="field-input" type="password" autoComplete="new-password" value={next} onChange={(e) => setNext(e.target.value)} />
      <label htmlFor="pw-confirm">Confirm new password</label>
      <input id="pw-confirm" className="field-input" type="password" autoComplete="new-password" value={confirm} onChange={(e) => setConfirm(e.target.value)} />
      <label className="check-row">
        <input type="checkbox" checked={others} onChange={(e) => setOthers(e.target.checked)} /> Sign out my other sessions
      </label>
      <div className="btn-row is-flush">
        <button className="btn btn-primary" type="button" disabled={busy || !current || !next} onClick={() => void save()}>
          Change password
        </button>
      </div>
    </article>
  );
}

function MfaPanel({ enabled, allowlistOn, onChanged }: { enabled: boolean; allowlistOn: boolean; onChanged: () => void }) {
  const [enroll, setEnroll] = useState<MFAEnrollResponse | null>(null);
  const [code, setCode] = useState("");
  const [password, setPassword] = useState("");
  const [removing, setRemoving] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function run(fn: () => Promise<unknown>, fallback: string) {
    setBusy(true);
    setError(null);
    try {
      await fn();
    } catch (err) {
      setError(errText(err, fallback));
    } finally {
      setBusy(false);
    }
  }

  return (
    <article className="panel stack">
      <h2>Multi-factor authentication</h2>
      {error ? <ErrorNotice error={error} /> : null}
      <p>
        Status: <strong>{enabled ? "On (authenticator app)" : "Off"}</strong>
      </p>
      {!enabled && !enroll ? (
        <div className="btn-row is-flush">
          <button className="btn btn-primary" type="button" disabled={busy} onClick={() => void run(async () => setEnroll(await enrollMfa()), "Enroll failed")}>
            Set up MFA
          </button>
        </div>
      ) : null}
      {enroll ? (
        <>
          <p className="field-hint">
            Add this account to your authenticator app with the key below (or <a href={enroll.otpauth_url}>open it in the app</a>), then
            enter the 6-digit code it shows.
          </p>
          <p>
            <code className="mfa-secret">{enroll.secret}</code>
          </p>
          <p className="field-hint">Recovery codes, shown once. Store them somewhere safe:</p>
          <ul className="mfa-codes">
            {enroll.recovery_codes.map((c) => (
              <li key={c}>
                <code>{c}</code>
              </li>
            ))}
          </ul>
          <label htmlFor="mfa-code">Code from the app</label>
          <input id="mfa-code" className="field-input" inputMode="numeric" autoComplete="one-time-code" value={code} onChange={(e) => setCode(e.target.value)} />
          <div className="btn-row is-flush">
            <button
              className="btn btn-primary"
              type="button"
              disabled={busy || !code.trim()}
              onClick={() =>
                void run(async () => {
                  await confirmMfa(code.trim());
                  setEnroll(null);
                  setCode("");
                  onChanged();
                }, "Confirm failed")
              }
            >
              Turn on MFA
            </button>
            <button className="btn btn-ghost" type="button" onClick={() => setEnroll(null)}>
              Cancel
            </button>
          </div>
        </>
      ) : null}
      {enabled && !removing ? (
        <div className="btn-row is-flush">
          <button
            className="btn btn-ghost"
            type="button"
            disabled={allowlistOn}
            title={allowlistOn ? "Turn off your IP allowlist first; it needs MFA" : undefined}
            onClick={() => setRemoving(true)}
          >
            Turn off MFA
          </button>
        </div>
      ) : null}
      {enabled && allowlistOn ? <p className="field-hint">Your IP allowlist needs MFA, so MFA stays on while the allowlist is on.</p> : null}
      {enabled && removing ? (
        <>
          <label htmlFor="mfa-off-pw">Password</label>
          <input id="mfa-off-pw" className="field-input" type="password" value={password} onChange={(e) => setPassword(e.target.value)} />
          <label htmlFor="mfa-off-code">Current code</label>
          <input id="mfa-off-code" className="field-input" inputMode="numeric" value={code} onChange={(e) => setCode(e.target.value)} />
          <div className="btn-row is-flush">
            <button
              className="btn btn-danger"
              type="button"
              disabled={busy || !password || !code}
              onClick={() =>
                void run(async () => {
                  await disableMyMfa({ password, code: code.trim() });
                  setRemoving(false);
                  setPassword("");
                  setCode("");
                  onChanged();
                }, "Could not turn off MFA")
              }
            >
              Turn off MFA
            </button>
            <button className="btn btn-ghost" type="button" onClick={() => setRemoving(false)}>
              Cancel
            </button>
          </div>
        </>
      ) : null}
    </article>
  );
}

type Row = { cidr: string; note: string };

function AllowlistPanel({ mfaEnabled, onChanged }: { mfaEnabled: boolean; onChanged: () => void }) {
  const [state, setState] = useState<IPAllowlist | null>(null);
  const [ipInfo, setIpInfo] = useState<ClientIPInfo | null>(null);
  const [rows, setRows] = useState<Row[]>([]);
  const [enabled, setEnabled] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  useEffect(() => {
    void Promise.all([getMyAllowlist(), getMyIP()])
      .then(([list, ip]) => {
        setState(list);
        setIpInfo(ip);
        setRows(list.entries.map((e) => ({ cidr: e.cidr, note: e.note ?? "" })));
        setEnabled(list.enabled);
      })
      .catch((err) => setError(errText(err, "Allowlist is unavailable")));
  }, []);

  const current = ipInfo?.ip ?? state?.current_ip ?? "";
  const hasCurrent = rows.some((r) => r.cidr.trim() === current || r.cidr.trim() === `${current}/32` || r.cidr.trim() === `${current}/128`);

  async function save(nextEnabled = enabled) {
    setBusy(true);
    setError(null);
    setNotice(null);
    try {
      const res = await putMyAllowlist({
        enabled: nextEnabled,
        entries: rows.filter((r) => r.cidr.trim()).map((r) => ({ cidr: r.cidr.trim(), note: r.note.trim() })),
      });
      setState(res);
      setRows(res.entries.map((e) => ({ cidr: e.cidr, note: e.note ?? "" })));
      setEnabled(res.enabled);
      setNotice(res.enabled ? "Allowlist saved and on." : "Allowlist saved. It is off.");
      onChanged();
    } catch (err) {
      setError(errText(err, "Save failed"));
    } finally {
      setBusy(false);
    }
  }

  if (!state) {
    return (
      <article className="panel stack">
        <h2>IP allowlist</h2>
        {error ? <ErrorNotice error={error} /> : <LoadingState />}
      </article>
    );
  }

  return (
    <article className="panel stack">
      <h2>IP allowlist</h2>
      <p className="lede">Only let this account sign in and use its sessions and tokens from these addresses.</p>
      {error ? <ErrorNotice error={error} /> : null}
      {notice ? (
        <p className="banner banner-ok" role="status">
          {notice}
        </p>
      ) : null}
      <p className="field-hint">
        Your address as ndl-ce sees it: <code>{current || "unknown"}</code>
        {ipInfo?.via_proxy && ipInfo.proxy ? ` (through ${ipInfo.proxy.source ?? "a trusted proxy"})` : ""}. If this is not your
        real public address, an admin needs to check Trusted proxies.
      </p>
      {!mfaEnabled ? (
        <p className="banner banner-warn" role="status">
          Set up MFA first. The allowlist needs it, so a lockout can be undone with your password and code.
        </p>
      ) : null}
      {rows.length > 0 ? (
        <ul className="allowlist-rows">
          {rows.map((r, i) => (
            <li key={i}>
              <input
                className="field-input"
                aria-label={`Address ${i + 1}`}
                placeholder="203.0.113.10 or 203.0.113.0/24"
                value={r.cidr}
                onChange={(e) => setRows((cur) => cur.map((x, j) => (j === i ? { ...x, cidr: e.target.value } : x)))}
              />
              <input
                className="field-input"
                aria-label={`Note ${i + 1}`}
                placeholder="Note (home, office…)"
                value={r.note}
                onChange={(e) => setRows((cur) => cur.map((x, j) => (j === i ? { ...x, note: e.target.value } : x)))}
              />
              <button className="btn btn-ghost btn-sm" type="button" aria-label={`Remove ${r.cidr || "row"}`} onClick={() => setRows((cur) => cur.filter((_, j) => j !== i))}>
                Remove
              </button>
            </li>
          ))}
        </ul>
      ) : (
        <p className="muted">No addresses yet.</p>
      )}
      <div className="btn-row is-flush">
        <button className="btn" type="button" disabled={!current || hasCurrent} onClick={() => setRows((cur) => [...cur, { cidr: current, note: "added from this browser" }])}>
          Add my current IP
        </button>
        <button className="btn btn-ghost" type="button" onClick={() => setRows((cur) => [...cur, { cidr: "", note: "" }])}>
          Add address
        </button>
      </div>
      <label className="check-row">
        <input
          type="checkbox"
          checked={enabled}
          disabled={!mfaEnabled && !enabled}
          onChange={(e) => setEnabled(e.target.checked)}
        />{" "}
        Only allow these addresses
      </label>
      {enabled && !hasCurrent ? <p className="field-hint">Your current address must be on the list before the allowlist can be on.</p> : null}
      <div className="btn-row is-flush">
        <button className="btn btn-primary" type="button" disabled={busy || (enabled && (!mfaEnabled || !hasCurrent))} onClick={() => void save()}>
          Save allowlist
        </button>
      </div>
      <p className="field-hint">
        Locked out? Sign in with your password and MFA code from the new address and it is added for you. Without MFA, ask an
        administrator; the root account can run <code>nodalctl recover-ip --username {"<you>"} --add-ip {"<address>"}</code> on the
        host.
      </p>
    </article>
  );
}

function SessionsPanel() {
  const [busy, setBusy] = useState(false);
  const [done, setDone] = useState(false);
  const [error, setError] = useState<string | null>(null);
  return (
    <article className="panel stack">
      <h2>Sessions</h2>
      <p className="lede">Sign out everywhere else, for example after using a shared computer.</p>
      {error ? <ErrorNotice error={error} /> : null}
      {done ? (
        <p className="banner banner-ok" role="status">
          Other sessions are signed out.
        </p>
      ) : null}
      <div className="btn-row is-flush">
        <button
          className="btn"
          type="button"
          disabled={busy}
          onClick={() => {
            setBusy(true);
            setError(null);
            void signOutOtherSessions()
              .then(() => setDone(true))
              .catch((err) => setError(errText(err, "Sign-out failed")))
              .finally(() => setBusy(false));
          }}
        >
          Sign out other sessions
        </button>
      </div>
    </article>
  );
}
