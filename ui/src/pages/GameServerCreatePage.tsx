import { num, str, strMap, useTemplateFromURL } from "../instanceTemplates";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { ApiError, listNodes } from "../api/client";
import { Link } from "../components/Link";
import { PageHeader } from "../components/PageHeader";
import { LoadingState } from "../components/EmptyState";
import { formatRam } from "../gameservers/caps";
import { defaultImageLabel, showCreateVariable } from "../gameservers/catalogue";
import { CatalogueBrowser } from "../gameservers/CatalogueBrowser";
import { ModpackBrowser } from "../gameservers/ModpackBrowser";
import { TemplateSummary, portLabel } from "../gameservers/TemplateSummary";
import { useCataloguePrefs } from "../gameservers/useCataloguePrefs";
import {
  createGameServer,
  getGameTemplate,
  importGameTemplate,
  listGameCatalogue,
  modpackVersions,
  preflightGameServer,
  refreshGameCatalogue,
  type Modpack,
  type ModpackVersion,
} from "../gameservers/api";
import type { CatalogueItem, GameCreateBody, GameTemplate, GameVariable, PreflightResult } from "../gameservers/types";
import { GS_ROOT, gameServerHref } from "../nav/gameServers";
import { navigate } from "../router";
import { ErrorNotice } from "../components/ErrorNotice";
import { Segmented } from "../ui/Segmented";

const STEPS = ["Choose", "Set up"];
const PREFLIGHT_DELAY_MS = 250;
/** Variables a modpack pick fills in; the pack's own controls replace them. */
const MODPACK_VARS = new Set(["MODPACK_ID", "MODPACK_VERSION"]);

type NodeOption = { id: string; name?: string; memory_bytes?: number; local?: boolean };
type Source = "games" | "modpacks";
type PickedPack = { pack: Modpack; versions: ModpackVersion[] };

type PreflightState =
  | { key: string; status: "ok"; result: PreflightResult }
  | { key: string; status: "unsupported" }
  | { key: string; status: "error"; message: string };

export function GameServerCreatePage() {
  const [step, setStep] = useState(0);
  const [source, setSource] = useState<Source>(() =>
    new URLSearchParams(window.location.search).get("source") === "modpacks" ? "modpacks" : "games",
  );
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
  const { template: preset } = useTemplateFromURL("game-server");
  const [picked, setPicked] = useState<CatalogueItem | null>(null);
  const [pack, setPack] = useState<PickedPack | null>(null);
  const prefs = useCataloguePrefs();
  const { markRecent } = prefs;

  useEffect(() => {
    void (async () => {
      try {
        const [cat, nodeList] = await Promise.all([listGameCatalogue(), listNodes().catch(() => [])]);
        setItems(cat.items ?? []);
        // Create server's quick picker sends the chosen game along.
        const wanted = new URLSearchParams(window.location.search).get("game");
        if (wanted) {
          setGame(wanted);
        }
        const list = nodeList ?? [];
        setNodes(list);
        // Game servers run on the control node, the one No-DAL runs on.
        const home = list.find((n) => n.local) ?? list[0];
        if (home?.id) {
          setNodeId(home.id);
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
  const wantPreflight = step >= 1 && bodyKey !== "";

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
            setPreflight({
              key: bodyKey,
              status: "error",
              message: err instanceof Error ? err.message : "Preflight check failed",
            });
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

  /** applyTemplate loads a template's defaults into the form. */
  const applyTemplate = useCallback(
    (tmpl: GameTemplate, presetFor?: string) => {
      setTemplate(tmpl);
      setName(tmpl.name);
      const next: Record<string, string> = {};
      for (const v of tmpl.variables ?? []) {
        next[v.env] = v.default ?? "";
      }
      setImageLabel(Object.keys(tmpl.images ?? {}).length > 1 ? defaultImageLabel(tmpl.images, tmpl.default_image) : "");
      setCpus(tmpl.default_cpus || 2);
      setMemoryMb(tmpl.default_memory_mb || 2048);
      setDiskMb(tmpl.default_disk_mb || 8192);
      // A settings template for this game overrides the game's defaults.
      if (preset && presetFor && str(preset.spec, "game_template_id") === presetFor) {
        const c = num(preset.spec, "cpus");
        const mem = num(preset.spec, "memory_bytes");
        const disk = num(preset.spec, "disk_bytes");
        if (c) setCpus(c);
        if (mem) setMemoryMb(Math.round(mem / 1024 / 1024));
        if (disk) setDiskMb(Math.round(disk / 1024 / 1024));
        const presetEnv = strMap(preset.spec, "env");
        for (const k of Object.keys(presetEnv)) {
          if (k in next) next[k] = presetEnv[k];
        }
      }
      setEnv(next);
      setPreflight(null);
      return next;
    },
    [preset],
  );

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
        applyTemplate(tmpl, item.id);
        setPack(null);
        markRecent(item.id);
        setPicked(item);
        setStep(1);
      } catch (err) {
        setError(err instanceof ApiError ? err.message : "Could not open that template");
      } finally {
        setBusy(false);
      }
    },
    [markRecent, applyTemplate],
  );

  /** chooseVersion points the form at one modpack version and its Java. */
  const chooseVersion = useCallback((v: ModpackVersion | undefined) => {
    if (!v) {
      return;
    }
    setEnv((cur) => ({ ...cur, MODPACK_VERSION: v.id }));
    if (v.image_label) {
      setImageLabel(v.image_label);
    }
  }, []);

  const pickPack = useCallback(
    async (p: Modpack) => {
      setBusy(true);
      setError(null);
      try {
        const ref = p.provider === "curseforge" ? p.id : p.slug || p.id;
        const vers = await modpackVersions(p.provider, ref);
        const tmpl = await getGameTemplate(vers.template_id);
        const next = applyTemplate(tmpl);
        const chosen = vers.items.find((v) => v.release) ?? vers.items[0];
        setEnv({ ...next, MODPACK_ID: ref, MODPACK_VERSION: chosen?.id ?? "latest" });
        if (chosen?.image_label) {
          setImageLabel(chosen.image_label);
        }
        setName(p.name);
        setPack({ pack: p, versions: vers.items });
        setPicked(null);
        setStep(1);
      } catch (err) {
        setError(err instanceof ApiError ? err.message : "Could not open that modpack");
      } finally {
        setBusy(false);
      }
    },
    [applyTemplate],
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
      await pick({
        id: tmpl.id,
        name: tmpl.name,
        game: tmpl.game,
        builtin: false,
      });
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
  const visible = (template?.variables ?? []).filter(
    (v) => v.editable !== false && !(pack && MODPACK_VARS.has(v.env)) && showCreateVariable(v.env, v.viewable, v.required, template?.start_requires),
  );
  // What a server needs to start goes first; the rest waits under Advanced.
  const essential = visible.filter(
    (v) => v.required || v.field_type === "toggle" || v.field_type === "select" || template?.start_requires?.includes(v.env),
  );
  const optional = visible.filter((v) => !essential.includes(v));
  const packVersion = pack?.versions.find((v) => v.id === env.MODPACK_VERSION);

  const variableField = (v: GameVariable) => {
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
          />
        )}
      </label>
    );
  };

  return (
    <section className="page gs-page" aria-labelledby="gs-create-title">
      <PageHeader
        id="gs-create-title"
        title="Create game server"
        kicker="Pick a game or a modpack. No-DAL picks the image, installer, Java version and ports."
        actions={
          <div className="btn-row is-flush">
            <Link className="btn btn-ghost" href={GS_ROOT}>
              ← Back to Game Servers
            </Link>
            <Link className="btn btn-tinted" href={GS_ROOT}>
              Cancel
            </Link>
          </div>
        }
      />
      <ol className="gs-steps">
        {STEPS.map((label, i) => (
          <li key={label} className={i === step ? "is-on" : i < step ? "is-done" : ""} aria-current={i === step ? "step" : undefined}>
            {label}
          </li>
        ))}
      </ol>
      {error ? <ErrorNotice error={error} /> : null}

      {step === 0 ? (
        <div className="gs-create" aria-busy={busy}>
          <Segmented<Source>
            value={source}
            ariaLabel="What to install"
            options={[
              { id: "games", label: "Games" },
              { id: "modpacks", label: "Minecraft modpacks" },
            ]}
            onChange={setSource}
          />
          {source === "modpacks" ? (
            <ModpackBrowser busy={busy} onPick={(p) => void pickPack(p)} />
          ) : (
            <>
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
            </>
          )}
        </div>
      ) : null}

      {step === 1 && template ? (
        <div className="gs-options">
          <div className="gs-form">
            {pack ? (
              <div className="gs-pack-head">
                {pack.pack.icon_url ? <img src={pack.pack.icon_url} alt="" referrerPolicy="no-referrer" /> : null}
                <div>
                  <strong>{pack.pack.name}</strong>
                  <span className="gs-meta">{pack.pack.provider === "curseforge" ? "CurseForge modpack" : "Modrinth modpack"}</span>
                </div>
              </div>
            ) : null}
            <label>
              Name in No-DAL
              <span className="field-hint">How this server appears in your server list.</span>
              <input className="field-input" value={name} onChange={(e) => setName(e.target.value)} />
            </label>
            {pack ? (
              <label>
                Modpack version
                <span className="field-hint">
                  {packVersion
                    ? [packVersion.game_version ? `Minecraft ${packVersion.game_version}` : "", packVersion.loader ?? "", packVersion.image_label ? `runs on ${packVersion.image_label}` : ""]
                        .filter(Boolean)
                        .join(" · ")
                    : "The release to install."}
                </span>
                <select
                  className="field-input"
                  value={env.MODPACK_VERSION ?? ""}
                  onChange={(e) => chooseVersion(pack.versions.find((v) => v.id === e.target.value))}
                >
                  {pack.versions.map((v) => (
                    <option key={v.id} value={v.id}>
                      {v.name}
                      {v.game_version ? ` (MC ${v.game_version})` : ""}
                      {v.release ? "" : " · beta"}
                    </option>
                  ))}
                </select>
              </label>
            ) : null}
            {images.length > 1 ? (
              <label>
                Runtime version
                <span className="field-hint">{pack ? "Chosen for the pack's Minecraft version." : "The container image the server runs on, for example the Java version."}</span>
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
            {essential.map(variableField)}

            <fieldset className="gs-resources">
              <legend>Resources</legend>
              <label>
                RAM (MiB)
                <input className="field-input" type="number" min={256} step={512} value={memoryMb} onChange={(e) => setMemoryMb(Number(e.target.value))} />
                {recMb > 0 || minMb > 0 ? (
                  <span className="field-hint">
                    {recMb ? `Recommended ${formatRam(recMb * 1024 * 1024)}` : ""}
                    {recMb && minMb ? ", " : ""}
                    {minMb ? `minimum ${formatRam(minMb * 1024 * 1024)}` : ""}.
                  </span>
                ) : null}
              </label>
              <label>
                CPUs
                <input className="field-input" type="number" min={1} value={cpus} onChange={(e) => setCpus(Number(e.target.value))} />
              </label>
            </fieldset>
            {minMb > 0 && memoryMb < minMb ? (
              <p className="banner banner-warn" role="status">
                {formatRam(memoryMb * 1024 * 1024)} is below the {formatRam(minMb * 1024 * 1024)} minimum for {template.name}. The server is unlikely to start.
              </p>
            ) : recMb > 0 && memoryMb < recMb ? (
              <p className="banner banner-warn" role="status">
                {formatRam(memoryMb * 1024 * 1024)} is below the recommended {formatRam(recMb * 1024 * 1024)}. Expect lag with more players or mods.
              </p>
            ) : null}
            <p className="gs-meta gs-node-facts">
              Runs on {selectedNode?.name || "this node"}
              {nodeArch ? ` · Architecture: ${nodeArch}` : ""}
              {nodeTotal
                ? nodeCommitted !== undefined
                  ? ` · Memory: ${formatRam(nodeCommitted)} committed of ${formatRam(nodeTotal)}`
                  : ` · Memory: ${formatRam(nodeTotal)}`
                : ""}
            </p>
            {archMismatch ? (
              <p className="banner banner-warn">
                This node is {nodeArch}. {template.name} supports {template.architectures?.join(", ")}.
              </p>
            ) : null}

            <details className="gs-advanced">
              <summary>Advanced settings</summary>
              <label>
                Disk (MiB)
                <input className="field-input" type="number" min={1024} value={diskMb} onChange={(e) => setDiskMb(Number(e.target.value))} />
              </label>
              {optional.map(variableField)}
            </details>

            <PreflightPanel state={current} pending={preflightPending} />
            <div className="btn-row">
              <button
                type="button"
                className="btn btn-ghost"
                onClick={() => {
                  setStep(0);
                  setPack(null);
                }}
              >
                Back
              </button>
              <button type="button" className="btn btn-primary" disabled={busy || preflightPending || preflightBlocks || !name.trim()} onClick={() => void onCreate()}>
                {busy ? "Installing" : "Install server"}
              </button>
            </div>
          </div>
          {pack ? null : <TemplateSummary template={template} art={picked} />}
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
        <p className="banner banner-ok">Ready to install. The node, ports, and requirements look good.</p>
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
      <details className="gs-preflight-details">
        <summary>What will be installed</summary>
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
      </details>
    </section>
  );
}
