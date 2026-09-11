import { useEffect, useState } from "react";
import { createTemplate, deployTemplate, listTemplates, listWorkloads } from "../api/client";
import type { VMTemplate } from "../api/client";
import type { Workload } from "../api/phase5";
import { ConfirmDialog } from "../components/ConfirmDialog";
import { Field } from "../components/Field";
import { Link } from "../components/Link";
import { PageHeader } from "../components/PageHeader";
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
  const [open, setOpen] = useState(false);

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
      <PageHeader
        id="templates-heading"
        title="VM templates"
        kicker="A template is a volume snapshot plus a redacted spec. Deploying creates a new VM with new UUIDs and a new MAC."
        actions={
          mutate ? (
            <button className="btn btn-primary" type="button" onClick={() => setOpen(true)}>
              Create template
            </button>
          ) : null
        }
      />
      {error ? (
        <p className="banner banner-error" role="alert">
          {error}
        </p>
      ) : null}
      <div className="card-grid">
        {items.length === 0 ? (
          <article className="panel dashboard-card empty-card">
            <p className="empty-title">No templates</p>
            {mutate ? (
              <button className="btn btn-primary" type="button" onClick={() => setOpen(true)}>
                Create template
              </button>
            ) : null}
          </article>
        ) : (
          items.map((t) => (
            <article className="panel dashboard-card" key={t.id}>
              <h3>{t.name}</h3>
              <p className="muted">
                {t.snapshot_id ? "Snapshot recorded" : "Snapshot unavailable; deploy clones the source"}
              </p>
              {mutate ? (
                <div className="btn-row">
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
                </div>
              ) : null}
            </article>
          ))
        )}
      </div>
      <p>
        <Link href="/workloads/import">Import a disk image</Link>
      </p>
      <ConfirmDialog
        open={open}
        title="Save as template"
        confirmLabel="Create template"
        confirmDisabled={busy || !sourceID}
        onClose={() => setOpen(false)}
        onConfirm={() => {
          setBusy(true);
          setError(null);
          void createTemplate({ workload_id: sourceID, name })
            .then(() => {
              setOpen(false);
              return reload();
            })
            .catch((err) => setError(err instanceof Error ? err.message : "Create failed"))
            .finally(() => setBusy(false));
        }}
      >
        <div className="field">
          <label className="field-label" htmlFor="tmpl-src">
            Source VM
          </label>
          <select id="tmpl-src" className="field-input" value={sourceID} onChange={(e) => setSourceID(e.target.value)}>
            {workloads.length === 0 ? <option value="">No VMs</option> : null}
            {workloads.map((w) => (
              <option key={w.id} value={w.id}>
                {w.name}
              </option>
            ))}
          </select>
        </div>
        <Field id="tmpl-name" label="Name" value={name} onChange={(e) => setName(e.target.value)} />
      </ConfirmDialog>
    </section>
  );
}
