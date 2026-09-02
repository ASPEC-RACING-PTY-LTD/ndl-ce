import { useState, type FormEvent } from "react";
import { ApiError } from "../api/client";
import { Field } from "../components/Field";
import { navigate } from "../router";
import { useSession } from "../session";

export function SetupPage() {
  const session = useSession();
  const [token, setToken] = useState("");
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [formError, setFormError] = useState<string | null>(null);
  const [passwordError, setPasswordError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function onSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setFormError(null);

    if (!token.trim() || !username.trim() || !password) {
      setFormError("All fields are required.");
      return;
    }
    if (password !== confirmPassword) {
      setPasswordError("Passwords do not match.");
      return;
    }
    setPasswordError(null);
    setBusy(true);
    try {
      await session.completeSetup({
        token: token.trim(),
        username: username.trim(),
        password,
      });
      navigate("/", { replace: true });
    } catch (err) {
      setFormError(err instanceof ApiError ? err.message : "Setup could not be completed.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="auth-screen">
      <header className="auth-brand">
        <p className="wordmark">No-dal</p>
        <p className="auth-edition">Community Edition</p>
      </header>
      <main className="panel auth-panel" aria-labelledby="setup-heading">
        <h1 id="setup-heading">Create the first administrator</h1>
        <p className="lede">
          Use the one-time setup token printed when the appliance was installed. This claim can be
          used only once.
        </p>
        <form className="form" onSubmit={(event) => void onSubmit(event)} noValidate>
          {formError ? (
            <p className="banner banner-error" role="alert">
              {formError}
            </p>
          ) : null}
          <Field
            id="setup-token"
            label="Setup token"
            name="token"
            type="text"
            autoComplete="off"
            spellCheck={false}
            required
            value={token}
            onChange={(event) => setToken(event.target.value)}
          />
          <Field
            id="setup-username"
            label="Username"
            name="username"
            type="text"
            autoComplete="username"
            required
            value={username}
            onChange={(event) => setUsername(event.target.value)}
          />
          <Field
            id="setup-password"
            label="Password"
            name="password"
            type="password"
            autoComplete="new-password"
            required
            value={password}
            onChange={(event) => setPassword(event.target.value)}
          />
          <Field
            id="setup-confirm-password"
            label="Confirm password"
            name="confirmPassword"
            type="password"
            autoComplete="new-password"
            required
            error={passwordError ?? undefined}
            value={confirmPassword}
            onChange={(event) => {
              setConfirmPassword(event.target.value);
              if (passwordError) {
                setPasswordError(null);
              }
            }}
          />
          <button className="btn btn-primary" type="submit" disabled={busy}>
            {busy ? "Creating administrator" : "Create administrator"}
          </button>
        </form>
      </main>
    </div>
  );
}
