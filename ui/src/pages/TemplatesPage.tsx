import { useEffect, useState } from "react";
import { createTemplate, deployTemplate, listTemplates, listWorkloads } from "../api/client";
import type { VMTemplate } from "../api/client";
import type { Workload } from "../api/phase5";
import { Field } from "../components/Field";
import { Link } from "../components/Link";
import { navigate } from "../router";
import { useSession } from "../session";
import { canMutate } from "../ux";

export function TemplatesPage() {
  const session = useSession();
  const roles = session.status === "ready" ? session.user?.roles : undefined;
  const mutate = canMutate(roles);
  const [items, setItems] = useState<VMTemplate[]>([]);
  const [workloads, setWorkloads] = useState<Workload[]>([]);
  const [sourceID, setSourceID] = useState("");
  const [name, setName] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function reload() {
    const [listed, wls] = await Promise.all([listTemplates(), listWorkloads()]);
    setItems(listed.items ?? []);
    const vms = (wls.items ?? []).filter((w) => w.kind === "vm");
    setWorkloads(vms);
    if (!sourceID && vms[0]?.id) {
      setSourceID(vms[0].id);
    }
  }

  useEffect(() => {
    void reload().catch((err) => setError(err instanceof Error ? err.message : "Unavailable"));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return (
    <section className="page page-wide" aria-labelledby="templates-heading">
      <header className="page-header">
        <h1 id="templates-heading">VM templates</h1>
        <p className="page-kicker">
          A template is a volume snapshot plus a redacted spec. Deploying creates a new VM with new UUIDs and a new
          MAC.
        </p>
      </header>
      {error ? (
        <p className="banner banner-error" role="alert">
          {error}
        </p>
      ) : null}
      {mutate ? (
        <article className="panel">
          <h2>Save as template</h2>
          <label htmlFor="tmpl-src">
            Source VM
            <select id="tmpl-src" className="field-input" value={sourceID} onChange={(e) => setSourceID(e.target.value)}>
              {workloads.length === 0 ? <option value="">No VMs</option> : null}
              {workloads.map((w) => (
                <option key={w.id} value={w.id}>
                  {w.name}
                </option>
              ))}
            </select>
          </label>
          <Field id="tmpl-name" label="Name" value={name} onChange={(e) => setName(e.target.value)} />
          <button
            className="btn btn-primary"
            type="button"
            disabled={busy || !sourceID}
            onClick={() => {
              setBusy(true);
              setError(null);
              void createTemplate({ workload_id: sourceID, name })
                .then(() => reload())
                .catch((err) => setError(err instanceof Error ? err.message : "Create failed"))
                .finally(() => setBusy(false));
            }}
          >
            Create template
          </button>
        </article>
      ) : null}
      <article className="panel">
        <h2>Library</h2>
        {items.length === 0 ? <p>No templates</p> : null}
        <ul className="plain-list">
          {items.map((t) => (
            <li key={t.id}>
              {t.name} {t.snapshot_id ? "(snapshot recorded)" : "(snapshot unavailable; deploy clones the source)"}{" "}
              {mutate ? (
                <button
                  className="btn"
                  type="button"
                  disabled={busy}
                  onClick={() => {
                    setBusy(true);
                    setError(null);
                    void deployTemplate(t.id)
                      .then((wl) => navigate(`/workloads/${wl.id}`))
                      .catch((err) => setError(err instanceof Error ? err.message : "Deploy failed"))
                      .finally(() => setBusy(false));
                  }}
                >
                  Deploy
                </button>
              ) : null}
            </li>
          ))}
        </ul>
      </article>
      <p>
        <Link href="/workloads/import">Import a disk image</Link>
      </p>
    </section>
  );
}
