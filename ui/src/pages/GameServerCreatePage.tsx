import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { ApiError, listNodes } from "../api/client";
import { Link } from "../components/Link";
import { PageHeader } from "../components/PageHeader";
import { LoadingState } from "../components/EmptyState";
import { formatRam } from "../gameservers/caps";
import { defaultImageLabel, showCreateVariable } from "../gameservers/catalogue";
import { CatalogueBrowser } from "../gameservers/CatalogueBrowser";
import { TemplateSummary, portLabel } from "../gameservers/TemplateSummary";
import { useCataloguePrefs } from "../gameservers/useCataloguePrefs";
import {
  createGameServer,
  getGameTemplate,
  importGameTemplate,
  listGameCatalogue,
  preflightGameServer,
  refreshGameCatalogue,
} from "../gameservers/api";
import type { CatalogueItem, GameCreateBody, GameTemplate, PreflightResult } from "../gameservers/types";
import { GS_ROOT, gameServerHref } from "../nav/gameServers";
import { navigate, usePath } from "../router";

const STEPS = ["Game", "Options", "Placement", "Review"];
const PREFLIGHT_DELAY_MS = 250;

type NodeOption = { id: string; name?: string; memory_bytes?: number };

type PreflightState =
  | { key: string; status: "ok"; result: PreflightResult }
  | { key: string; status: "unsupported" }
  | { key: string; status: "error"; message: string };

export function GameServerCreatePage() {
  const path = usePath();
  const catalogueOnly = path === `${GS_ROOT}/catalogue`;
  const [step, setStep] = useState(0);
  const [items, setItems] = useState<CatalogueItem[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [template, setTemplate] = useState<GameTemplate | null>(null);
  const [name, setName] = useState("");
  const [env, setEnv] = useState<Record<string, string>>({});
  const [imageLabel, setImageLabel] = useState("");
  const [nodeId, setNodeId] = useState("");
  const [nodes, setNodes] = useState<NodeOption[]>([]);
  const [cpus, setCpus] = useState(2);
  const [memoryMb, setMemoryMb] = useState(2048);
  const [diskMb, setDiskMb] = useState(8192);
  const [busy, setBusy] = useState(false);
  const [importUrl, setImportUrl] = useState("");
  const [preflight, setPreflight] = useState<PreflightState | null>(null);
  // The game whose server types are open, kept while moving between steps.
  const [game, setGame] = useState<string | null>(null);
  const [picked, setPicked] = useState<CatalogueItem | null>(null);
  const prefs = useCataloguePrefs();
  const { markRecent } = prefs;

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

  const createBody = useMemo<GameCreateBody | null>(() => {
    if (!template) {
      return null;
    }
    return {
      name,
      template_id: template.id,
      ...(imageLabel ? { image_label: imageLabel } : {}),
      node_id: nodeId,
      env,
      cpus,
      memory_bytes: memoryMb * 1024 * 1024,
      disk_bytes: diskMb * 1024 * 1024,
    };
  }, [template, name, imageLabel, nodeId, env, cpus, memoryMb, diskMb]);
  const bodyKey = createBody ? JSON.stringify(createBody) : "";
  const wantPreflight = step >= 2 && bodyKey !== "";

  useEffect(() => {
    if (!wantPreflight) {
      return;
    }
    let alive = true;
    const timer = setTimeout(() => {
      preflightGameServer(JSON.parse(bodyKey) as GameCreateBody)
        .then((result) => {
          if (alive) {
            setPreflight({ key: bodyKey, status: "ok", result });
          }
        })
        .catch((err: unknown) => {
          if (!alive) {
            return;
          }
          if (err instanceof ApiError && (err.status === 404 || err.status === 405 || err.status === 501)) {
            setPreflight({ key: bodyKey, status: "unsupported" });
          } else {
            setPreflight({ key: bodyKey, status: "error", message: err instanceof Error ? err.message : "Preflight check failed" });
          }
        });
    }, PREFLIGHT_DELAY_MS);
    return () => {
      alive = false;
      clearTimeout(timer);
    };
  }, [wantPreflight, bodyKey]);

  // The last answer we got, and whether it matches the current settings.
  const current = preflight && preflight.key === bodyKey ? preflight : null;
  const lastResult = preflight?.status === "ok" ? preflight.result : null;
  const preflightPending = wantPreflight && !current && preflight?.status !== "unsupported";
  const preflightErrors = current?.status === "ok" ? (current.result.errors ?? []) : [];
  const preflightBlocks = current?.status === "ok" && (current.result.ok === false || preflightErrors.length > 0);

  const pick = useCallback(
    async (item: CatalogueItem) => {
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
        setImageLabel(Object.keys(tmpl.images ?? {}).length > 1 ? defaultImageLabel(tmpl.images, tmpl.default_image) : "");
        setCpus(tmpl.default_cpus || 2);
        setMemoryMb(tmpl.default_memory_mb || 2048);
        setDiskMb(tmpl.default_disk_mb || 8192);
        setPreflight(null);
        markRecent(item.id);
        setPicked(item);
        setStep(1);
      } catch (err) {
        setError(err instanceof ApiError ? err.message : "Could not open that template");
      } finally {
        setBusy(false);
      }
    },
    [markRecent],
  );

  // Keep the card callback identity stable so memoised cards do not re-render.
  const busyRef = useRef(busy);
  useEffect(() => {
    busyRef.current = busy;
  }, [busy]);
  const onPickCard = useCallback(
    (item: CatalogueItem) => {
      if (!busyRef.current) {
        void pick(item);
      }
    },
    [pick],
  );

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
    if (!createBody || preflightBlocks) {
      return;
    }
    setBusy(true);
    try {
      const created = await createGameServer(createBody);
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

  const images = Object.entries(template?.images ?? {});
  const selectedNode = nodes.find((n) => n.id === nodeId);
  const pfNode = lastResult?.node && (!nodeId || lastResult.node.id === nodeId) ? lastResult.node : null;
  const nodeArch = pfNode?.architecture;
  const nodeTotal = pfNode?.memory_total_bytes || selectedNode?.memory_bytes;
  const nodeCommitted = pfNode?.memory_committed_bytes;
  const minMb = template?.min_memory_mb ?? 0;
  const recMb = template?.default_memory_mb ?? 0;
  const archMismatch = Boolean(nodeArch && template?.architectures?.length && !template.architectures.includes(nodeArch));

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
          <li key={label} className={i === step ? "is-on" : i < step ? "is-done" : ""} aria-current={i === step ? "step" : undefined}>
            {label}
          </li>
        ))}
      </ol>
      {error ? <p className="banner banner-error">{error}</p> : null}

      {step === 0 ? (
        <div className="gs-create" aria-busy={busy}>
          <CatalogueBrowser
            items={items ?? []}
            favorites={prefs.favorites}
            recents={prefs.recents}
            game={game}
            onGameChange={setGame}
            onPick={onPickCard}
            onToggleFavorite={prefs.toggleFavorite}
          />
          {game ? null : (
            <details className="gs-catalogue-import">
              <summary>Import a Pelican or Pterodactyl egg</summary>
              <div className="gs-catalogue-tools">
                <input className="field-input" value={importUrl} placeholder="Paste an egg JSON HTTPS URL" aria-label="Egg JSON URL" onChange={(e) => setImportUrl(e.target.value)} />
                <button type="button" className="btn btn-ghost" disabled={busy} onClick={() => void onImport()}>
                  Import
                </button>
                <button type="button" className="btn btn-ghost" disabled={busy} onClick={() => void onRefresh()}>
                  Refresh catalogue
                </button>
              </div>
            </details>
          )}
        </div>
      ) : null}

      {step === 1 && template ? (
        <div className="gs-options">
          <div className="gs-form">
            <label>
              Name in No-DAL
              <span className="field-hint">How this server appears in your server list. Players see the in-game name below.</span>
              <input className="field-input" value={name} onChange={(e) => setName(e.target.value)} />
            </label>
            {images.length > 1 ? (
              <label>
                Runtime version
                <span className="field-hint">The container image the server runs on, for example the Java version.</span>
                <select className="field-input" value={imageLabel} onChange={(e) => setImageLabel(e.target.value)}>
                  {images.map(([label, image]) => (
                    <option key={label} value={label}>
                      {label}
                      {image === template.default_image ? " (default)" : ""}
                    </option>
                  ))}
                </select>
              </label>
            ) : null}
            {(template.variables ?? [])
              // Fixed values such as the Steam app ID are shown in the summary, not as inputs.
              .filter((v) => v.editable !== false && showCreateVariable(v.env, v.viewable, v.required, template.start_requires))
              .map((v) => {
                const generated = v.generate === "password";
                const onChange = (value: string) => setEnv((cur) => ({ ...cur, [v.env]: value }));
                return (
                  <label key={v.env}>
                    {v.name}
                    {v.description ? <span className="field-hint">{v.description}</span> : null}
                    {generated ? <span className="field-hint">Leave empty to generate one.</span> : null}
                    {v.field_type === "toggle" ? (
                      <select className="field-input" value={env[v.env] ?? ""} onChange={(e) => onChange(e.target.value)}>
                        <option value="true">Yes</option>
                        <option value="false">No</option>
                      </select>
                    ) : v.field_type === "select" && v.options?.length ? (
                      <select className="field-input" value={env[v.env] ?? ""} required={v.required} onChange={(e) => onChange(e.target.value)}>
                        {!v.options.includes(env[v.env] ?? "") ? <option value={env[v.env] ?? ""}>{env[v.env] ? env[v.env] : "Choose"}</option> : null}
                        {v.options.map((opt) => (
                          <option key={opt} value={opt}>
                            {opt}
                          </option>
                        ))}
                      </select>
                    ) : (
                      <input
                        className="field-input"
                        type={v.secret || v.field_type === "password" ? "password" : v.field_type === "number" ? "number" : "text"}
                        value={env[v.env] ?? ""}
                        placeholder={generated ? "Generated when empty" : undefined}
                        onChange={(e) => onChange(e.target.value)}
                        required={v.required && !generated}
                        readOnly={v.editable === false}
                        aria-readonly={v.editable === false ? true : undefined}
                      />
                    )}
                  </label>
                );
              })}
            <div className="btn-row">
              <button type="button" className="btn btn-ghost" onClick={() => setStep(0)}>
                Back
              </button>
              <button type="button" className="btn btn-primary" onClick={() => setStep(2)}>
                Continue
              </button>
            </div>
          </div>
          <TemplateSummary template={template} art={picked} />
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
          {nodeArch || nodeTotal ? (
            <p className="gs-meta gs-node-facts">
              {nodeArch ? `Architecture: ${nodeArch}` : ""}
              {nodeArch && nodeTotal ? " · " : ""}
              {nodeTotal
                ? nodeCommitted !== undefined
                  ? `Memory: ${formatRam(nodeCommitted)} committed of ${formatRam(nodeTotal)}`
                  : `Memory: ${formatRam(nodeTotal)}`
                : ""}
            </p>
          ) : null}
          {archMismatch ? (
            <p className="banner banner-warn">
              This node is {nodeArch}. {template.name} supports {template.architectures?.join(", ")}.
            </p>
          ) : null}
          <label>
            CPUs
            <input className="field-input" type="number" min={1} value={cpus} onChange={(e) => setCpus(Number(e.target.value))} />
          </label>
          <label>
            RAM (MiB)
            <input className="field-input" type="number" min={256} value={memoryMb} onChange={(e) => setMemoryMb(Number(e.target.value))} />
            {recMb > 0 || minMb > 0 ? (
              <span className="field-hint">
                {recMb ? `Recommended ${formatRam(recMb * 1024 * 1024)}` : ""}
                {recMb && minMb ? ", " : ""}
                {minMb ? `minimum ${formatRam(minMb * 1024 * 1024)}` : ""}.
              </span>
            ) : null}
          </label>
          {minMb > 0 && memoryMb < minMb ? (
            <p className="banner banner-warn" role="status">
              {formatRam(memoryMb * 1024 * 1024)} is below the {formatRam(minMb * 1024 * 1024)} minimum for {template.name}. The server is unlikely to start.
            </p>
          ) : recMb > 0 && memoryMb < recMb ? (
            <p className="banner banner-warn" role="status">
              {formatRam(memoryMb * 1024 * 1024)} is below the recommended {formatRam(recMb * 1024 * 1024)}. Expect lag with more players or mods.
            </p>
          ) : null}
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
              {template.name}
              {imageLabel ? ` (${imageLabel})` : ""} on {selectedNode?.name || nodeId || "this node"} · {cpus} CPU · {formatRam(memoryMb * 1024 * 1024)}
            </p>
            <ul>
              {(template.variables ?? [])
                .filter((v) => v.viewable !== false)
                .map((v) => (
                  <li key={v.env}>
                    {v.name}: {v.secret ? "hidden" : env[v.env] || v.default || (v.generate === "password" ? "generated" : "default")}
                  </li>
                ))}
            </ul>
          </article>
          <PreflightPanel state={current} pending={preflightPending} />
          <div className="btn-row">
            <button type="button" className="btn btn-ghost" onClick={() => setStep(2)}>
              Back
            </button>
            <button type="button" className="btn btn-primary" disabled={busy || preflightPending || preflightBlocks} onClick={() => void onCreate()}>
              Install server
            </button>
          </div>
        </div>
      ) : null}
    </section>
  );
}

function PreflightPanel({ state, pending }: { state: PreflightState | null; pending: boolean }) {
  if (pending) {
    return (
      <p className="gs-meta" role="status">
        Checking node, ports, and requirements...
      </p>
    );
  }
  if (!state || state.status === "unsupported") {
    return null;
  }
  if (state.status === "error") {
    return (
      <p className="banner banner-warn" role="status">
        Preflight check failed: {state.message}. Install still runs the same checks on the server.
      </p>
    );
  }
  const r = state.result;
  const errors = r.errors ?? [];
  const warnings = r.warnings ?? [];
  const ports = r.ports ?? [];
  const envUpdates = Object.entries(r.env_updates ?? {});
  const deps = r.dependencies ?? [];
  return (
    <section className="gs-preflight" aria-label="Preflight check">
      {errors.length > 0 ? (
        <div className="banner banner-error" role="alert">
          <strong>Fix before installing</strong>
          <ul>
            {errors.map((e) => (
              <li key={e}>{e}</li>
            ))}
          </ul>
        </div>
      ) : (
        <p className="banner banner-ok">Preflight passed. The node, ports, and requirements look good.</p>
      )}
      {warnings.length > 0 ? (
        <div className="banner banner-warn">
          <strong>Warnings</strong>
          <ul>
            {warnings.map((w) => (
              <li key={w}>{w}</li>
            ))}
          </ul>
        </div>
      ) : null}
      <dl className="gs-summary-list">
        {r.image ? (
          <>
            <dt>Runtime image</dt>
            <dd>
              <code>{r.image}</code>
            </dd>
          </>
        ) : null}
        {r.install_method ? (
          <>
            <dt>Install method</dt>
            <dd>{r.install_method}</dd>
          </>
        ) : null}
        {r.update_procedure ? (
          <>
            <dt>Updates</dt>
            <dd>{r.update_procedure}</dd>
          </>
        ) : null}
        {ports.length > 0 ? (
          <>
            <dt>Ports</dt>
            <dd>
              {ports.map((p) => (
                <span key={`${p.container_port}/${p.protocol ?? "tcp"}/${p.host_port ?? ""}`} className="gs-badge">
                  {portLabel(p)}
                </span>
              ))}
            </dd>
          </>
        ) : null}
        {envUpdates.length > 0 ? (
          <>
            <dt>Settings changed to fit the ports</dt>
            <dd>
              {envUpdates.map(([k, v]) => (
                <span key={k} className="gs-badge">
                  {k} = {v}
                </span>
              ))}
            </dd>
          </>
        ) : null}
        {deps.length > 0 ? (
          <>
            <dt>Dependencies</dt>
            <dd>{deps.join(", ")}</dd>
          </>
        ) : null}
      </dl>
    </section>
  );
}
