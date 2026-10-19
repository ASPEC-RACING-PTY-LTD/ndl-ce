import { useEffect, useState, type FormEvent } from "react";
import { ApiError, getHealth, getRecoveryStatus, recoverConnect, recoverRestore, recoverUnlock } from "../api/client";
import type { RecoveryConnectResponse, RecoveryPlan, RecoveryRestoreResponse, RecoveryStatus } from "../generated/openapi";
import { AuthBrand } from "../components/AuthBrand";
import { ErrorNotice } from "../components/ErrorNotice";
import { Field } from "../components/Field";
import { formatWhen } from "../format";
import { navigate } from "../router";

type Step = "connect" | "unlock" | "map" | "restoring" | "done";

function message(err: unknown, fallback: string): string {
  return err instanceof ApiError || err instanceof Error ? err.message : fallback;
}

/**
 * RecoverPage rebuilds a lost NDL-CE on this unclaimed host from its
 * platform backup: connect the bucket with new credentials, unlock with an
 * SuperAdmin's password, map pools and networks onto this host, restore.
 */
export function RecoverPage() {
  const [status, setStatus] = useState<RecoveryStatus | null>(null);
  const [step, setStep] = useState<Step>("connect");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [token, setToken] = useState("");
  const [accountId, setAccountId] = useState("");
  const [bucket, setBucket] = useState("");
  const [auth, setAuth] = useState<"token" | "keys">("token");
  const [apiToken, setApiToken] = useState("");
  const [accessKey, setAccessKey] = useState("");
  const [secretKey, setSecretKey] = useState("");
  const [found, setFound] = useState<RecoveryConnectResponse | null>(null);
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [plan, setPlan] = useState<RecoveryPlan | null>(null);
  const [pools, setPools] = useState<Record<string, string>>({});
  const [networks, setNetworks] = useState<Record<string, string>>({});
  const [result, setResult] = useState<RecoveryRestoreResponse | null>(null);

  useEffect(() => {
    getRecoveryStatus()
      .then(setStatus)
      .catch(() => setStatus({ available: false }));
  }, []);

  async function run(fn: () => Promise<void>, fallback: string) {
    setBusy(true);
    setError(null);
    try {
      await fn();
    } catch (err) {
      setError(message(err, fallback));
    } finally {
      setBusy(false);
    }
  }

  function onConnect(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    void run(async () => {
      const res = await recoverConnect({
        token: token.trim(),
        kind: "r2",
        account_id: accountId.trim(),
        bucket: bucket.trim(),
        ...(auth === "token" ? { api_token: apiToken.trim() } : { access_key_id: accessKey.trim(), secret_access_key: secretKey.trim() }),
      });
      setFound(res);
      setStep("unlock");
    }, "The bucket could not be read.");
  }

  function onUnlock(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    void run(async () => {
      const res = await recoverUnlock({ token: token.trim(), session_id: found?.session_id ?? "", username: username.trim(), password });
      setPassword("");
      setPlan(res);
      setPools(Object.fromEntries((res.pools ?? []).map((p) => [p.id ?? "", p.path ?? ""])));
      setNetworks(Object.fromEntries((res.networks ?? []).map((n) => [n.id ?? "", n.bridge ?? ""])));
      setStep("map");
    }, "The backup could not be unlocked.");
  }

  function onRestore(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setStep("restoring");
    void run(async () => {
      try {
        const res = await recoverRestore({ token: token.trim(), session_id: found?.session_id ?? "", pools, networks });
        setResult(res);
        // The control plane restarts with the restored database.
        for (let i = 0; i < 60; i++) {
          await new Promise((r) => setTimeout(r, 2000));
          try {
            await getHealth();
            break;
          } catch {
            // still restarting
          }
        }
        setStep("done");
      } catch (err) {
        setStep("map");
        throw err;
      }
    }, "The restore failed.");
  }

  if (status && !status.available && step === "connect") {
    return (
      <div className="auth-screen">
        <AuthBrand />
        <main className="panel auth-panel" aria-labelledby="recover-heading">
          <h1 id="recover-heading">Recover an existing NDL-CE</h1>
          <p className="lede">
            {status.locked
              ? "A recovery is already running on this host."
              : "Recovery is only possible on a host that has not been set up yet."}
          </p>
        </main>
      </div>
    );
  }

  return (
    <div className="auth-screen">
      <AuthBrand />
      <main className="panel auth-panel" aria-labelledby="recover-heading">
        <h1 id="recover-heading">Recover an existing NDL-CE</h1>
        {error ? <ErrorNotice error={error} /> : null}

        {step === "connect" ? (
          <form className="form" onSubmit={onConnect} noValidate>
            <p className="lede">
              Connect the R2 bucket that holds the platform backup. Use new Cloudflare credentials: the old ones should
              have been revoked with the lost host. Only reading the bucket is needed to find the backup; it stays locked
              until a SuperAdmin's password opens it.
            </p>
            <Field id="recover-token" label="Setup token" type="text" autoComplete="off" spellCheck={false} value={token} onChange={(e) => setToken(e.target.value)} />
            <Field id="recover-account" label="Cloudflare account id" type="text" autoComplete="off" spellCheck={false} value={accountId} onChange={(e) => setAccountId(e.target.value)} />
            <Field id="recover-bucket" label="Bucket" type="text" autoComplete="off" spellCheck={false} value={bucket} onChange={(e) => setBucket(e.target.value)} />
            <fieldset className="field">
              <legend className="field-label">Credentials</legend>
              <label className="field-check">
                <input type="radio" name="recover-auth" checked={auth === "token"} onChange={() => setAuth("token")} /> API token with R2 access
              </label>
              <label className="field-check">
                <input type="radio" name="recover-auth" checked={auth === "keys"} onChange={() => setAuth("keys")} /> R2 access key id and secret
              </label>
            </fieldset>
            {auth === "token" ? (
              <Field id="recover-api-token" label="API token" type="password" autoComplete="off" value={apiToken} onChange={(e) => setApiToken(e.target.value)} />
            ) : (
              <>
                <Field id="recover-access-key" label="Access key id" type="text" autoComplete="off" spellCheck={false} value={accessKey} onChange={(e) => setAccessKey(e.target.value)} />
                <Field id="recover-secret-key" label="Secret access key" type="password" autoComplete="off" value={secretKey} onChange={(e) => setSecretKey(e.target.value)} />
              </>
            )}
            <button className="btn btn-primary" type="submit" disabled={busy}>
              {busy ? "Looking for the backup" : "Find the backup"}
            </button>
            <button className="btn btn-ghost" type="button" onClick={() => navigate("/setup")}>
              Set up a new NDL-CE instead
            </button>
          </form>
        ) : null}

        {step === "unlock" && found ? (
          <form className="form" onSubmit={onUnlock} noValidate>
            <p className="lede">
              Found {found.snapshots} platform backup{found.snapshots === 1 ? "" : "s"}
              {found.newest ? `, the newest from ${formatWhen(found.newest)}` : ""}. Unlock it with the username and
              password of a SuperAdmin of the lost NDL-CE.
            </p>
            {found.schema_level != null && found.local_schema_level != null && found.schema_level > found.local_schema_level ? (
              <p className="banner banner-error">This NDL-CE is older than the backup. Install the same or a newer version first.</p>
            ) : null}
            <Field id="recover-username" label="SuperAdmin username" type="text" autoComplete="username" value={username} onChange={(e) => setUsername(e.target.value)} />
            <Field id="recover-password" label="Password" type="password" autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} />
            <button className="btn btn-primary" type="submit" disabled={busy}>
              {busy ? "Unlocking" : "Unlock"}
            </button>
          </form>
        ) : null}

        {(step === "map" || step === "restoring") && plan ? (
          <form className="form" onSubmit={onRestore} noValidate>
            <p className="lede">
              Backup of {plan.hostname} from {formatWhen(plan.created_at ?? "")}: {plan.workloads?.length ?? 0} workloads.
              Their definitions, accounts, settings and secrets come back now. Workloads stay stopped until their data is
              restored from Backups. This host gets a new identity and certificates.
            </p>
            {plan.pools?.length ? <h2>Storage pools on this host</h2> : null}
            {(plan.pools ?? []).map((p) => (
              <Field
                key={p.id}
                id={`recover-pool-${p.id}`}
                label={`${p.name} (${p.backend})`}
                type="text"
                value={pools[p.id ?? ""] ?? ""}
                onChange={(e) => setPools((cur) => ({ ...cur, [p.id ?? ""]: e.target.value }))}
                hint={p.path ? `Was ${p.path}` : undefined}
              />
            ))}
            {plan.networks?.length ? <h2>Networks on this host</h2> : null}
            {(plan.networks ?? []).map((n) => (
              <Field
                key={n.id}
                id={`recover-net-${n.id}`}
                label={`${n.name} bridge`}
                type="text"
                value={networks[n.id ?? ""] ?? ""}
                onChange={(e) => setNetworks((cur) => ({ ...cur, [n.id ?? ""]: e.target.value }))}
                hint={n.bridge ? `Was ${n.bridge}${n.cidr ? ` (${n.cidr})` : ""}` : undefined}
              />
            ))}
            <button className="btn btn-primary" type="submit" disabled={busy}>
              {busy ? "Restoring; this host restarts the control plane" : "Restore"}
            </button>
          </form>
        ) : null}

        {step === "done" && result ? (
          <div className="form">
            <p className="lede">NDL-CE was restored. Sign in with your SuperAdmin account from the lost host.</p>
            {result.connection_verified ? (
              <p className="banner" role="status">
                The new bucket connection was saved in place of the old credentials and tested.
              </p>
            ) : (
              <p className="banner banner-error">
                The new bucket connection was saved but its test failed{result.connection_error ? `: ${result.connection_error}` : ""}. Fix it in
                Backups before restoring workloads.
              </p>
            )}
            <button className="btn btn-primary" type="button" onClick={() => navigate("/login", { replace: true })}>
              Sign in
            </button>
          </div>
        ) : null}
      </main>
    </div>
  );
}
