import { useEffect, useMemo, useState } from "react";
import { ApiError, listNodes } from "../api/client";
import { Link } from "../components/Link";
import { PageHeader } from "../components/PageHeader";
import { LoadingState } from "../components/EmptyState";
import { familyTone, formatRam } from "../gameservers/caps";
import { CATALOGUE_GROUPS, catalogueMark, filterCatalogue, showCreateVariable, type CatalogueGroup } from "../gameservers/catalogue";
import { createGameServer, getGameTemplate, importGameTemplate, listGameCatalogue, refreshGameCatalogue } from "../gameservers/api";
import type { CatalogueItem, GameTemplate } from "../gameservers/types";
import { GS_ROOT, gameServerHref } from "../nav/gameServers";
import { navigate, usePath } from "../router";

const STEPS = ["Game", "Options", "Placement", "Review"];

export function GameServerCreatePage() {
  const path = usePath();
  const catalogueOnly = path === `${GS_ROOT}/catalogue`;
  const [step, setStep] = useState(0);
  const [q, setQ] = useState("");
  const [group, setGroup] = useState<CatalogueGroup>("all");
  const [items, setItems] = useState<CatalogueItem[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [template, setTemplate] = useState<GameTemplate | null>(null);
  const [name, setName] = useState("");
  const [env, setEnv] = useState<Record<string, string>>({});
  const [nodeId, setNodeId] = useState("");
  const [nodes, setNodes] = useState<{ id: string; name?: string }[]>([]);
  const [cpus, setCpus] = useState(2);
  const [memoryMb, setMemoryMb] = useState(2048);
  const [diskMb, setDiskMb] = useState(8192);
  const [busy, setBusy] = useState(false);
  const [importUrl, setImportUrl] = useState("");

  useEffect(() => {
    void (async () => {
      try {
        const [cat, nodeList] = await Promise.all([listGameCatalogue(), listNodes().catch(() => [])]);
        setItems(cat.items ?? []);
        const list = nodeList ?? [];
        setNodes(list);
        if (list[0]?.id) {
          setNodeId(list[0].id);
        }
      } catch (err) {
        setError(err instanceof ApiError ? err.message : "Could not load the catalogue");
      }
    })();
  }, []);

  const filtered = useMemo(() => filterCatalogue(items ?? [], q, group), [items, q, group]);

  async function pick(item: CatalogueItem) {
    setBusy(true);
    setError(null);
    try {
      let tmpl: GameTemplate;
      if (item.builtin || item.id.startsWith("ndl-")) {
        tmpl = await getGameTemplate(item.id);
      } else if (item.import_url) {
        tmpl = await importGameTemplate({ url: item.import_url });
      } else {
        tmpl = await getGameTemplate(item.id);
      }
      setTemplate(tmpl);
      setName(tmpl.name);
      const next: Record<string, string> = {};
      for (const v of tmpl.variables ?? []) {
        next[v.env] = v.default ?? "";
      }
      setEnv(next);
      setCpus(tmpl.default_cpus || 2);
      setMemoryMb(tmpl.default_memory_mb || 2048);
      setDiskMb(tmpl.default_disk_mb || 8192);
      setStep(1);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Could not open that template");
    } finally {
      setBusy(false);
    }
  }

  async function onRefresh() {
    setBusy(true);
    try {
      const cat = await refreshGameCatalogue();
      setItems(cat.items ?? []);
      if (cat.errors?.length) {
        setError(cat.errors.join(" "));
      }
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Catalogue refresh failed");
    } finally {
      setBusy(false);
    }
  }

  async function onImport() {
    if (!importUrl.trim()) {
      return;
    }
    setBusy(true);
    try {
      const tmpl = await importGameTemplate({ url: importUrl.trim() });
      setItems((cur) => [
        {
          id: tmpl.id,
          name: tmpl.name,
          game: tmpl.game,
          summary: tmpl.summary,
          builtin: false,
          tags: tmpl.tags,
          capabilities: tmpl.capabilities,
        },
        ...(cur ?? []),
      ]);
      await pick({ id: tmpl.id, name: tmpl.name, game: tmpl.game, builtin: false });
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Import failed");
    } finally {
      setBusy(false);
    }
  }

  async function onCreate() {
    if (!template) {
      return;
    }
    setBusy(true);
    try {
      const created = await createGameServer({
        name,
        template_id: template.id,
        node_id: nodeId,
        env,
        cpus,
        memory_bytes: memoryMb * 1024 * 1024,
        disk_bytes: diskMb * 1024 * 1024,
      });
      navigate(gameServerHref(created.id));
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Create failed");
    } finally {
      setBusy(false);
    }
  }

  if (!items && !error) {
    return <LoadingState label="Loading games" />;
  }

  return (
    <section className="page gs-page" aria-labelledby="gs-create-title">
      <PageHeader
        id="gs-create-title"
        title={catalogueOnly ? "Catalogue" : "Create game server"}
        kicker={
          catalogueOnly
            ? "Browse built-in games and imported eggs, then install one."
            : "Answer a few questions. No-DAL picks the image, installer, and ports."
        }
        actions={
          <Link className="btn btn-ghost" href={GS_ROOT}>
            Cancel
          </Link>
        }
      />
      <ol className="gs-steps">
        {STEPS.map((label, i) => (
          <li key={label} className={i === step ? "is-on" : i < step ? "is-done" : ""}>
            {label}
          </li>
        ))}
      </ol>
      {error ? <p className="banner banner-error">{error}</p> : null}

      {step === 0 ? (
        <div className="gs-create">
          <label className="gs-catalogue-search">
            <span>Available game templates</span>
            <input
              className="field-input"
              type="search"
              value={q}
              placeholder="Search name, alias, or type. Try mc, pz, cs2, gmod, fivem"
              aria-label="Search available game templates"
              onChange={(e) => setQ(e.target.value)}
            />
          </label>
          <div className="gs-catalogue-groups" role="tablist" aria-label="Catalogue filters">
            {CATALOGUE_GROUPS.map((item) => (
              <button
                key={item.id}
                type="button"
                role="tab"
                className={"gs-chip" + (group === item.id ? " is-on" : "")}
                aria-selected={group === item.id}
                onClick={() => setGroup(item.id)}
              >
                {item.label}
              </button>
            ))}
          </div>
          <p className="gs-catalogue-count">
            {filtered.length} {filtered.length === 1 ? "template" : "templates"}
          </p>
          <div className="gs-catalogue-grid">
            {filtered.map((item) => (
              <button key={item.id} type="button" className={"gs-catalogue-item " + familyTone(item.family)} onClick={() => void pick(item)}>
                <span className="gs-catalogue-mark" aria-hidden="true">
                  {catalogueMark(item)}
                </span>
                <span className="gs-catalogue-copy">
                  <h3>{item.name}</h3>
                  <span className="gs-catalogue-type">
                    {item.builtin ? "Built in" : "Imported"}
                    {item.runtime_kind ? ` · ${item.runtime_kind}` : ""}
                  </span>
                  {item.hint ? <span className="gs-catalogue-hint">{item.hint}</span> : null}
                </span>
              </button>
            ))}
          </div>
          {filtered.length === 0 ? <p className="gs-meta">No templates match that search.</p> : null}
          <div className="gs-catalogue-tools">
            <button type="button" className="btn btn-ghost" disabled={busy} onClick={() => void onRefresh()}>
              Refresh catalogue
            </button>
            <input className="field-input" value={importUrl} placeholder="Paste an egg JSON HTTPS URL" aria-label="Egg JSON URL" onChange={(e) => setImportUrl(e.target.value)} />
            <button type="button" className="btn btn-ghost" disabled={busy} onClick={() => void onImport()}>
              Import
            </button>
          </div>
        </div>
      ) : null}

      {step === 1 && template ? (
        <div className="gs-form">
          <label>
            Server name
            <input className="field-input" value={name} onChange={(e) => setName(e.target.value)} />
          </label>
          {(template.variables ?? [])
            .filter((v) => showCreateVariable(v.env, v.viewable, v.required, template.start_requires))
            .map((v) => (
              <label key={v.env}>
                {v.name}
                <span className="field-hint">{v.description}</span>
                {v.field_type === "toggle" ? (
                  <select className="field-input" value={env[v.env] ?? ""} onChange={(e) => setEnv((cur) => ({ ...cur, [v.env]: e.target.value }))}>
                    <option value="true">Yes</option>
                    <option value="false">No</option>
                  </select>
                ) : (
                  <input
                    className="field-input"
                    type={v.secret || v.field_type === "password" ? "password" : v.field_type === "number" ? "number" : "text"}
                    value={env[v.env] ?? ""}
                    onChange={(e) => setEnv((cur) => ({ ...cur, [v.env]: e.target.value }))}
                    required={v.required}
                  />
                )}
              </label>
            ))}
          <div className="btn-row">
            <button type="button" className="btn btn-ghost" onClick={() => setStep(0)}>
              Back
            </button>
            <button type="button" className="btn btn-primary" onClick={() => setStep(2)}>
              Continue
            </button>
          </div>
        </div>
      ) : null}

      {step === 2 && template ? (
        <div className="gs-form">
          <label>
            Node
            <select className="field-input" value={nodeId} onChange={(e) => setNodeId(e.target.value)}>
              {nodes.length === 0 ? <option value="">This node</option> : null}
              {nodes.map((n) => (
                <option key={n.id} value={n.id}>
                  {n.name || n.id}
                </option>
              ))}
            </select>
            <span className="field-hint">Pick a node that already has Docker Engine and enough free RAM.</span>
          </label>
          <label>
            CPUs
            <input className="field-input" type="number" min={1} value={cpus} onChange={(e) => setCpus(Number(e.target.value))} />
          </label>
          <label>
            RAM (MiB)
            <input className="field-input" type="number" min={256} value={memoryMb} onChange={(e) => setMemoryMb(Number(e.target.value))} />
          </label>
          <label>
            Disk (MiB)
            <input className="field-input" type="number" min={1024} value={diskMb} onChange={(e) => setDiskMb(Number(e.target.value))} />
          </label>
          <div className="btn-row">
            <button type="button" className="btn btn-ghost" onClick={() => setStep(1)}>
              Back
            </button>
            <button type="button" className="btn btn-primary" onClick={() => setStep(3)}>
              Review
            </button>
          </div>
        </div>
      ) : null}

      {step === 3 && template ? (
        <div className="gs-form">
          <article className="gs-review">
            <h2>{name}</h2>
            <p>
              {template.name} on {nodeId || "this node"} · {cpus} CPU · {formatRam(memoryMb * 1024 * 1024)}
            </p>
            <ul>
              {(template.variables ?? [])
                .filter((v) => v.viewable !== false)
                .map((v) => (
                  <li key={v.env}>
                    {v.name}: {v.secret ? "hidden" : env[v.env] || v.default || "default"}
                  </li>
                ))}
            </ul>
          </article>
          <div className="btn-row">
            <button type="button" className="btn btn-ghost" onClick={() => setStep(2)}>
              Back
            </button>
            <button type="button" className="btn btn-primary" disabled={busy} onClick={() => void onCreate()}>
              Install server
            </button>
          </div>
        </div>
      ) : null}
    </section>
  );
}
