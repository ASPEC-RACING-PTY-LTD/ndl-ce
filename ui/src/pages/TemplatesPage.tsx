import { useEffect, useState } from "react";
import { createInstanceTemplate, listInstanceTemplates, type InstanceTemplate } from "../api/client";
import { listGameCatalogue } from "../gameservers/api";
import type { CatalogueItem } from "../gameservers/types";
import { createHref, num, str } from "../instanceTemplates";
import { useFeatureOn } from "../nav/NavDisclosure";
import { formatBytes } from "../format";
import { DeleteButton } from "../components/DeleteButton";
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
import { ErrorNotice } from "../components/ErrorNotice";

type SettingsKind = "system-container" | "oci" | "game-server";

const KIND_TITLES: Record<SettingsKind, string> = {
  "system-container": "System container templates",
  oci: "OCI container templates",
  "game-server": "Game server templates",
};

function specSummary(t: InstanceTemplate): string {
  const parts = [str(t.spec, "image_pin") ?? str(t.spec, "image") ?? str(t.spec, "game_template_id")];
  const c = num(t.spec, "cpus");
  const m = num(t.spec, "memory_bytes");
  if (c) parts.push(`${c} vCPU`);
  if (m) parts.push(formatBytes(m));
  return parts.filter(Boolean).join(" · ") || "Defaults";
}

/**
 * SettingsTemplates lists and saves the settings templates for one
 * instance type. Using one opens that type's create page filled in.
 */
function SettingsTemplates({
  kind,
  items,
  mutate,
  onChanged,
}: {
  kind: SettingsKind;
  items: InstanceTemplate[];
  mutate: boolean;
  onChanged: () => void;
}) {
  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  const [source, setSource] = useState("");
  const [sources, setSources] = useState<Workload[]>([]);
  const [games, setGames] = useState<CatalogueItem[]>([]);
  const [game, setGame] = useState("");
  const [cpus, setCpus] = useState("");
  const [memGB, setMemGB] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!open) {
      return;
    }
    if (kind === "game-server") {
      listGameCatalogue()
        .then((c) => {
          setGames(c.items ?? []);
          setGame((cur) => cur || c.items?.[0]?.id || "");
        })
        .catch(() => setGames([]));
      return;
    }
    listWorkloads()
      .then((w) => {
        const list = (w.items ?? []).filter((x) => x.kind === kind);
        setSources(list);
      })
      .catch(() => setSources([]));
  }, [open, kind]);

  async function save() {
    setBusy(true);
    setError(null);
    try {
      if (kind === "game-server") {
        const spec: Record<string, unknown> = { game_template_id: game };
        if (Number(cpus) > 0) spec.cpus = Number(cpus);
        if (Number(memGB) > 0) spec.memory_bytes = Math.round(Number(memGB) * 1024 ** 3);
        await createInstanceTemplate({ kind, name: name.trim(), spec });
      } else if (source) {
        await createInstanceTemplate({
          kind,
          name: name.trim(),
          workload_id: source,
        });
      } else {
        const spec: Record<string, unknown> = {};
        if (Number(cpus) > 0) spec.cpus = Number(cpus);
        if (Number(memGB) > 0) spec.memory_bytes = Math.round(Number(memGB) * 1024 ** 3);
        await createInstanceTemplate({ kind, name: name.trim(), spec });
      }
      setOpen(false);
      setName("");
      onChanged();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Save failed");
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="stack" aria-label={KIND_TITLES[kind]}>
      <div className="components-head">
        <h2>{KIND_TITLES[kind]}</h2>
        {mutate ? (
          <button className="btn btn-secondary btn-sm" type="button" onClick={() => setOpen(true)}>
            New template
          </button>
        ) : null}
      </div>
      <div className="card-grid">
        {items.length === 0 ? <p className="muted">None yet.</p> : null}
        {items.map((t) => (
          <article className="panel dashboard-card" key={t.id}>
            <h3>{t.name}</h3>
            <p className="muted">{t.description || specSummary(t)}</p>
            <div className="btn-row">
              <Link className="btn" href={createHref(t)}>
                Use
              </Link>
              {mutate ? (
                <DeleteButton
                  path={`/instance-templates/${t.id}`}
                  name={t.name}
                  noun="template"
                  description={<p>The template is removed. Instances made from it are not changed.</p>}
                  onDeleted={onChanged}
                />
              ) : null}
            </div>
          </article>
        ))}
      </div>
      <ConfirmDialog
        open={open}
        title={`New ${KIND_TITLES[kind].replace(/ templates$/, "").toLowerCase()} template`}
        confirmLabel="Save template"
        confirmDisabled={busy || (!name.trim() && !source) || (kind === "game-server" && !game)}
        onClose={() => setOpen(false)}
        onConfirm={() => void save()}
      >
        {error ? <ErrorNotice error={error} /> : null}
        <Field id={`tmpl-${kind}-name`} label="Name" value={name} onChange={(e) => setName(e.target.value)} />
        {kind === "game-server" ? (
          <div className="field">
            <label className="field-label" htmlFor={`tmpl-${kind}-game`}>
              Game
            </label>
            <select id={`tmpl-${kind}-game`} className="field-input" value={game} onChange={(e) => setGame(e.target.value)}>
              {games.map((g) => (
                <option key={g.id} value={g.id}>
                  {g.name}
                </option>
              ))}
            </select>
          </div>
        ) : (
          <div className="field">
            <label className="field-label" htmlFor={`tmpl-${kind}-src`}>
              Copy settings from
            </label>
            <select id={`tmpl-${kind}-src`} className="field-input" value={source} onChange={(e) => setSource(e.target.value)}>
              <option value="">Nothing; set them below</option>
              {sources.map((w) => (
                <option key={w.id} value={w.id}>
                  {w.name}
                </option>
              ))}
            </select>
          </div>
        )}
        {!source ? (
          <div className="field-row">
            <Field id={`tmpl-${kind}-cpu`} label="vCPUs (optional)" value={cpus} onChange={(e) => setCpus(e.target.value)} />
            <Field id={`tmpl-${kind}-mem`} label="Memory in GB (optional)" value={memGB} onChange={(e) => setMemGB(e.target.value)} />
          </div>
        ) : null}
      </ConfirmDialog>
    </section>
  );
}

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
  const [settings, setSettings] = useState<InstanceTemplate[]>([]);
  const ociOn = useFeatureOn("oci");
  const gamesOn = useFeatureOn("gameservers");

  async function reloadSettings() {
    setSettings((await listInstanceTemplates()).items ?? []);
  }

  async function reload() {
    const [listed, wls] = await Promise.all([listTemplates(), listWorkloads(), reloadSettings()]);
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
        title="Templates"
        kicker="Reusable settings for every instance type. A VM template also keeps a disk snapshot; deploying makes a new VM with new UUIDs and a new MAC."
        actions={
          mutate ? (
            <button className="btn btn-primary" type="button" onClick={() => setOpen(true)}>
              Create VM template
            </button>
          ) : null
        }
      />
      <SettingsTemplates
        kind="system-container"
        items={settings.filter((t) => t.kind === "system-container")}
        mutate={mutate}
        onChanged={() => void reloadSettings()}
      />
      {ociOn ? (
        <SettingsTemplates kind="oci" items={settings.filter((t) => t.kind === "oci")} mutate={mutate} onChanged={() => void reloadSettings()} />
      ) : null}
      {gamesOn ? (
        <SettingsTemplates
          kind="game-server"
          items={settings.filter((t) => t.kind === "game-server")}
          mutate={mutate}
          onChanged={() => void reloadSettings()}
        />
      ) : null}
      <h2>VM templates</h2>
      {error ? <ErrorNotice error={error} /> : null}
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
                  <DeleteButton
                    path={`/templates/${t.id}`}
                    name={t.name}
                    noun="template"
                    description={<p>The template is removed. Workloads deployed from it are not changed.</p>}
                    onDeleted={() => reload()}
                  />
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
