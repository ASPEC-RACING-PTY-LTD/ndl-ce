import { useState } from "react";
import { createR2Bucket, deleteR2Bucket, listR2Buckets, type R2Account, type R2Bucket } from "../api/client";
import { useQuery } from "../query";
import { ConfirmDialog } from "./ConfirmDialog";
import { ErrorNotice } from "./ErrorNotice";

const locations: [string, string][] = [
  ["", "Automatic"],
  ["oc", "Oceania"],
  ["apac", "Asia-Pacific"],
  ["wnam", "Western North America"],
  ["enam", "Eastern North America"],
  ["weur", "Western Europe"],
  ["eeur", "Eastern Europe"],
];

/** R2BucketsPanel manages R2 buckets with the tokens saved on the Cloudflare page. */
export function R2BucketsPanel({ mutate }: { mutate: boolean }) {
  const q = useQuery("r2-buckets", listR2Buckets);
  const accounts: R2Account[] = q.data?.accounts ?? [];
  const [account, setAccount] = useState("");
  const [name, setName] = useState("");
  const [location, setLocation] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [deleting, setDeleting] = useState<{ account: string; bucket: R2Bucket } | null>(null);
  const acct = account || accounts[0]?.account_id || "";

  async function onCreate() {
    setBusy(true);
    setError(null);
    setNotice(null);
    try {
      await createR2Bucket(acct, name.trim(), location);
      setNotice(`Bucket ${name.trim()} created.`);
      setName("");
      await q.reload();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not create the bucket");
    } finally {
      setBusy(false);
    }
  }

  async function onDelete() {
    const d = deleting;
    setDeleting(null);
    if (!d) {
      return;
    }
    setBusy(true);
    setError(null);
    setNotice(null);
    try {
      await deleteR2Bucket(d.account, d.bucket.name);
      setNotice(`Bucket ${d.bucket.name} deleted.`);
      await q.reload();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not delete the bucket");
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="panel" aria-labelledby="r2-buckets-heading">
      <div className="panel-header">
        <h2 id="r2-buckets-heading">Cloudflare R2 buckets</h2>
        <button className="btn btn-ghost btn-sm" type="button" onClick={() => void q.reload()}>
          Refresh
        </button>
      </div>
      <p className="muted">
        Uses the API tokens saved on the Cloudflare page. Listing needs Workers R2 Storage Read; creating and deleting
        need Edit. A bucket that still holds objects is never deleted.
      </p>
      {q.error ? <ErrorNotice error={q.error} /> : null}
      {error ? <ErrorNotice error={error} /> : null}
      {notice ? (
        <p className="banner" role="status">
          {notice}
        </p>
      ) : null}
      {!q.loading && accounts.length === 0 ? <p className="muted">No Cloudflare token is saved yet. Add one on the Cloudflare page.</p> : null}
      {accounts.map((a) => (
        <div key={a.account_id} className="stack">
          <h3>{a.account_name || a.account_id}</h3>
          {a.error ? <ErrorNotice error={a.error} /> : null}
          {!a.error && a.buckets.length === 0 ? <p className="muted">No buckets.</p> : null}
          {a.buckets.length ? (
            <table className="table">
              <thead>
                <tr>
                  <th>Bucket</th>
                  <th>Location</th>
                  <th>Created</th>
                  {mutate ? <th>Actions</th> : null}
                </tr>
              </thead>
              <tbody>
                {a.buckets.map((b) => (
                  <tr key={b.name}>
                    <td>{b.name}</td>
                    <td>{b.location || "Automatic"}</td>
                    <td>{b.creation_date ? new Date(b.creation_date).toLocaleDateString() : ""}</td>
                    {mutate ? (
                      <td>
                        <button
                          className="btn btn-sm btn-ghost btn-danger-text"
                          type="button"
                          disabled={busy}
                          aria-label={`Delete bucket ${b.name}`}
                          onClick={() => setDeleting({ account: a.account_id, bucket: b })}
                        >
                          Delete
                        </button>
                      </td>
                    ) : null}
                  </tr>
                ))}
              </tbody>
            </table>
          ) : null}
        </div>
      ))}
      {mutate && accounts.length ? (
        <div className="btn-row">
          {accounts.length > 1 ? (
            <select className="field-input" aria-label="Account" value={acct} onChange={(e) => setAccount(e.target.value)}>
              {accounts.map((a) => (
                <option key={a.account_id} value={a.account_id}>
                  {a.account_name || a.account_id}
                </option>
              ))}
            </select>
          ) : null}
          <input className="field-input" aria-label="New bucket name" placeholder="new-bucket-name" value={name} onChange={(e) => setName(e.target.value.toLowerCase())} />
          <select className="field-input" aria-label="Location" value={location} onChange={(e) => setLocation(e.target.value)}>
            {locations.map(([v, l]) => (
              <option key={v} value={v}>
                {l}
              </option>
            ))}
          </select>
          <button className="btn btn-primary btn-sm" type="button" disabled={busy || !acct || name.trim().length < 3} onClick={() => void onCreate()}>
            Create bucket
          </button>
        </div>
      ) : null}
      <ConfirmDialog
        open={deleting != null}
        title={`Delete bucket ${deleting?.bucket.name ?? ""}?`}
        confirmLabel="Delete"
        danger
        onConfirm={() => void onDelete()}
        onClose={() => setDeleting(null)}
      >
        <p>Cloudflare refuses to delete a bucket that still holds objects. Backup targets using this bucket stop working.</p>
      </ConfirmDialog>
    </section>
  );
}
