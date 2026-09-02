import { useState, type FormEvent } from "react";
import { ApiError } from "../api/client";
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
  const [formError, setFormError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function onSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setFormError(null);
    if (!username.trim() || !password) {
      setFormError("Username and password are required.");
      return;
    }
    setBusy(true);
    try {
      await session.signIn({
        username: username.trim(),
        password,
      });
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
        <h1 id="login-heading">Sign in</h1>
        <p className="lede">Sign in with the administrator account created during setup.</p>
        <form className="form" onSubmit={(event) => void onSubmit(event)} noValidate>
          {formError ? (
            <p className="banner banner-error" role="alert">
              {formError}
            </p>
          ) : null}
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
          <button className="btn btn-primary" type="submit" disabled={busy}>
            {busy ? "Signing in" : "Sign in"}
          </button>
        </form>
        {setupOpen ? (
          <p className="auth-alt">
            First-time appliance? <Link href="/setup">Create the first administrator</Link>
          </p>
        ) : null}
      </main>
    </div>
  );
}
