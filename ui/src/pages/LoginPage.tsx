import { useState, type FormEvent } from "react";
import { ApiError, login, verifyMfa } from "../api/client";
import type { MFAChallengeResponse } from "../generated/openapi";
import { AuthBrand } from "../components/AuthBrand";
import { Field } from "../components/Field";
import { Link } from "../components/Link";
import { navigate } from "../router";
import { useSession } from "../session";

export function LoginPage() {
  const session = useSession();
  const setupOpen = session.status === "ready" ? session.setupOpen : false;
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [code, setCode] = useState("");
  const [challenge, setChallenge] = useState<MFAChallengeResponse | null>(null);
  const [formError, setFormError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function onSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setFormError(null);
    setBusy(true);
    try {
      if (challenge) {
        if (!code.trim()) {
          setFormError("Authenticator code is required.");
          return;
        }
        await verifyMfa({
          mfa_challenge_id: challenge.mfa_challenge_id,
          mfa_token: challenge.mfa_token,
          code: code.trim(),
        });
        await session.refresh();
        navigate("/", { replace: true });
        return;
      }
      if (!username.trim() || !password) {
        setFormError("Username and password are required.");
        return;
      }
      const result = await login({ username: username.trim(), password });
      if ("mfa_required" in result && result.mfa_required) {
        setChallenge(result);
        return;
      }
      await session.refresh();
      navigate("/", { replace: true });
    } catch (err) {
      setFormError(err instanceof ApiError ? err.message : "Sign in failed.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="auth-screen">
      <AuthBrand />
      <main className="panel auth-panel" aria-labelledby="login-heading">
        <h1 id="login-heading">{challenge ? "Authenticator" : "Sign in"}</h1>
        <p className="lede">
          {challenge
            ? "Enter a TOTP or recovery code. This is not a backup of the appliance."
            : "Sign in with the administrator account created during setup."}
        </p>
        <form className="form" onSubmit={(event) => void onSubmit(event)} noValidate>
          {formError ? (
            <p className="banner banner-error" role="alert">
              {formError}
            </p>
          ) : null}
          {challenge ? (
            <Field
              id="login-mfa"
              label="Authenticator code"
              name="otp"
              type="text"
              autoComplete="one-time-code"
              required
              value={code}
              onChange={(event) => setCode(event.target.value)}
            />
          ) : (
            <>
              <Field
                id="login-username"
                label="Username"
                name="username"
                type="text"
                autoComplete="username"
                required
                value={username}
                onChange={(event) => setUsername(event.target.value)}
              />
              <Field
                id="login-password"
                label="Password"
                name="password"
                type="password"
                autoComplete="current-password"
                required
                value={password}
                onChange={(event) => setPassword(event.target.value)}
              />
            </>
          )}
          <button className="btn btn-primary" type="submit" disabled={busy}>
            {busy ? "Signing in" : "Sign in"}
          </button>
        </form>
        {setupOpen && !challenge ? (
          <p className="auth-alt">
            First-time appliance? <Link href="/setup">Create the first administrator</Link>
          </p>
        ) : null}
      </main>
    </div>
  );
}
