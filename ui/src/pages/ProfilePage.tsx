import { useEffect, useRef, useState } from "react";
import { deleteMyAvatar, getMe, getMyProfile, getUserProfile, patchMyProfile, putMyAvatar, type PublicProfile } from "../api/client";
import type { AccountProfile } from "../generated/openapi";
import { Avatar, squareImage } from "../components/Avatar";
import { LoadingState } from "../components/EmptyState";
import { ErrorNotice } from "../components/ErrorNotice";
import { Link } from "../components/Link";
import { PageHeader } from "../components/PageHeader";
import { formatWhen } from "../format";
import { roleLabel } from "../labels";
import { useSession } from "../session";

const BIO_MAX = 600;

function errText(err: unknown, fallback: string): string {
  return err instanceof Error ? err.message : fallback;
}

/** validAvatarURL accepts an empty value or an https address. */
export function validAvatarURL(v: string): boolean {
  const t = v.trim();
  if (!t) {
    return true;
  }
  try {
    return new URL(t).protocol === "https:";
  } catch {
    return false;
  }
}

/** ProfilePage edits what other people see: picture, display name and bio. */
export function ProfilePage() {
  const session = useSession();
  const [profile, setProfile] = useState<AccountProfile | null>(null);
  const [displayName, setDisplayName] = useState("");
  const [bio, setBio] = useState("");
  const [pictureURL, setPictureURL] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const fileInput = useRef<HTMLInputElement>(null);

  function take(p: AccountProfile) {
    setProfile(p);
    setDisplayName(p.display_name);
    setBio(p.bio ?? "");
    setPictureURL(p.avatar_url ?? "");
  }

  useEffect(() => {
    getMyProfile()
      .then(take)
      .catch((err: unknown) => setError(errText(err, "Profile is unavailable")));
  }, []);

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

  async function run(fn: () => Promise<AccountProfile>, msg: string) {
    setBusy(true);
    setError(null);
    setNotice(null);
    try {
      take(await fn());
      setNotice(msg);
      void refreshUser();
    } catch (err) {
      setError(errText(err, "Save failed"));
    } finally {
      setBusy(false);
    }
  }

  if (!profile) {
    return (
      <section className="page page-wide">
        <h1>Profile</h1>
        {error ? <ErrorNotice error={error} /> : <LoadingState label="Loading profile" />}
      </section>
    );
  }
  const urlOk = validAvatarURL(pictureURL);
  const changed = displayName.trim() !== profile.display_name || bio !== (profile.bio ?? "") || pictureURL.trim() !== (profile.avatar_url ?? "");
  const shown = displayName.trim() || profile.username;

  return (
    <section className="page page-wide account-page" aria-labelledby="profile-heading">
      <div className="account-head">
        <Avatar userID={profile.user_id} name={shown} updatedAt={profile.avatar_updated_at} url={profile.avatar_url} size={56} />
        <PageHeader
          id="profile-heading"
          title="Profile"
          kicker="What other people see about you."
          actions={
            <div className="btn-row is-flush">
              <Link className="btn btn-ghost" href={`/people/${encodeURIComponent(profile.user_id)}`}>
                View as others see it
              </Link>
              <Link className="btn btn-ghost" href="/me">
                Settings
              </Link>
            </div>
          }
        />
      </div>
      {error ? <ErrorNotice error={error} /> : null}
      {notice ? (
        <p className="banner banner-ok" role="status">
          {notice}
        </p>
      ) : null}
      <div className="account-grid">
        <article className="panel stack">
          <h2>Picture</h2>
          <div className="inline-actions">
            <Avatar userID={profile.user_id} name={shown} updatedAt={profile.avatar_updated_at} url={profile.avatar_url} size={72} />
            <div className="btn-row is-flush">
              <button className="btn" type="button" disabled={busy} onClick={() => fileInput.current?.click()}>
                {profile.avatar_updated_at ? "Change picture" : "Upload picture"}
              </button>
              {profile.avatar_updated_at ? (
                <button
                  className="btn btn-ghost"
                  type="button"
                  disabled={busy}
                  onClick={() =>
                    void run(async () => {
                      await deleteMyAvatar();
                      return { ...profile, avatar_updated_at: undefined };
                    }, "Picture removed.")
                  }
                >
                  Remove
                </button>
              ) : null}
            </div>
            <input
              ref={fileInput}
              className="visually-hidden"
              type="file"
              accept="image/png,image/jpeg,image/webp"
              aria-label="Choose a picture"
              onChange={(e) => {
                const file = e.target.files?.[0];
                e.target.value = "";
                if (file) {
                  void run(async () => putMyAvatar(await squareImage(file)), "Picture updated.");
                }
              }}
            />
          </div>
          <label htmlFor="profile-url">Or a picture address</label>
          <input
            id="profile-url"
            className="field-input"
            type="url"
            placeholder="https://"
            value={pictureURL}
            aria-invalid={!urlOk}
            onChange={(e) => setPictureURL(e.target.value)}
          />
          <p className="field-hint">{urlOk ? "PNG, JPEG or WebP, cropped to a square. An uploaded picture wins over an address." : "Use an https:// address."}</p>
        </article>
        <article className="panel stack">
          <h2>About you</h2>
          <label htmlFor="profile-display">Display name</label>
          <input id="profile-display" className="field-input" value={displayName} maxLength={128} onChange={(e) => setDisplayName(e.target.value)} />
          <p className="field-hint">
            Shown instead of your username. Audit logs and admins still see <code>{profile.username}</code>.
          </p>
          <label htmlFor="profile-bio">Bio</label>
          <textarea id="profile-bio" rows={5} maxLength={BIO_MAX} value={bio} onChange={(e) => setBio(e.target.value)} />
          <p className="field-hint">
            {bio.length} of {BIO_MAX} characters.
          </p>
        </article>
      </div>
      <div className="sticky-actions">
        <button className="btn btn-secondary" type="button" disabled={!changed || busy} onClick={() => take(profile)}>
          Discard
        </button>
        <button
          className="btn btn-primary"
          type="button"
          disabled={!changed || busy || !urlOk}
          onClick={() =>
            void run(() => patchMyProfile({ display_name: displayName.trim(), bio, avatar_url: pictureURL.trim() }), "Profile saved.")
          }
        >
          Save profile
        </button>
      </div>
    </section>
  );
}

/** PublicProfilePage shows someone's profile the way other signed-in people see it. */
export function PublicProfilePage({ id }: { id: string }) {
  const [p, setP] = useState<PublicProfile | null>(null);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    getUserProfile(id)
      .then(setP)
      .catch((err: unknown) => setError(errText(err, "Profile is unavailable")));
  }, [id]);
  if (!p) {
    return (
      <section className="page page-wide">
        <h1>Profile</h1>
        {error ? <ErrorNotice error={error} /> : <LoadingState label="Loading profile" />}
      </section>
    );
  }
  const shown = p.display_name || p.username;
  return (
    <section className="page page-wide account-page" aria-labelledby="person-heading">
      <div className="account-head">
        <Avatar userID={p.user_id} name={shown} updatedAt={p.avatar_updated_at} url={p.avatar_url} size={72} />
        <PageHeader
          id="person-heading"
          title={shown}
          kicker={`${p.username}${p.roles.length ? ` · ${p.roles.map(roleLabel).join(", ")}` : ""}${p.disabled ? " · disabled" : ""}`}
        />
      </div>
      <article className="panel stack person-card">
        {p.bio ? <p className="person-bio">{p.bio}</p> : <p className="muted">No bio yet.</p>}
        {p.created_at ? <p className="meta">Member since {formatWhen(p.created_at)}</p> : null}
      </article>
    </section>
  );
}
