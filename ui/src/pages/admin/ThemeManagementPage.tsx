import { useEffect, useState } from "react";
import { getAppearanceSettings, getIAMAccess, putAppearanceSettings, type AppearanceSettings, type IAMAccess } from "../../api/client";
import { loadAppearanceDefaults } from "../../appearance/state";
import { CodeThemeEditor, ColourEditor, StyleEditor, ThemePreview } from "../../appearance/ThemeEditor";
import { ErrorNotice } from "../../components/ErrorNotice";
import { DEFAULT_THEME, parseThemePrefs, previewTheme, savedTheme, type ThemePrefs } from "../../theme";
import { Segmented } from "../../ui/Segmented";

type Target = { kind: "global" } | { kind: "role"; id: string; label: string } | { kind: "group"; id: string; label: string };

function key(t: Target): string {
  return t.kind === "global" ? "global" : `${t.kind}:${t.id}`;
}

/**
 * ThemeManagementPage sets the look everyone starts from: a global theme,
 * themes per role or group, the default navigation and Instance layouts,
 * and (SuperAdmin only) how many design profiles people may keep.
 */
export function ThemeManagementPage() {
  const [data, setData] = useState<AppearanceSettings | null>(null);
  const [iam, setIam] = useState<IAMAccess | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [target, setTarget] = useState<Target>({ kind: "global" });
  const [draft, setDraft] = useState<ThemePrefs>(DEFAULT_THEME);
  const [mode, setMode] = useState<"pick" | "code">("pick");
  const [saved, setSaved] = useState("");

  useEffect(() => {
    void (async () => {
      try {
        const [a, access] = await Promise.all([getAppearanceSettings(), getIAMAccess().catch(() => null)]);
        setData(a);
        setIam(access);
      } catch (err) {
        setError(err instanceof Error ? err.message : String(err));
      }
    })();
    return () => previewTheme(null);
  }, []);

  function themeFor(t: Target, a: AppearanceSettings | null): unknown {
    if (!a) {
      return null;
    }
    if (t.kind === "global") {
      return a.theme ?? null;
    }
    const map = (t.kind === "role" ? a.role_themes : a.group_themes) as Record<string, unknown> | undefined;
    return map?.[t.id] ?? null;
  }

  useEffect(() => {
    const raw = themeFor(target, data);
    setDraft(raw ? parseThemePrefs(raw) : DEFAULT_THEME);
    setSaved("");
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key(target), data]);

  async function save(body: Partial<AppearanceSettings>, msg: string) {
    setBusy(true);
    setError("");
    try {
      const next = await putAppearanceSettings(body);
      setData(next);
      setSaved(msg);
      await loadAppearanceDefaults();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }

  function saveTheme(t: ThemePrefs | null) {
    if (!data) {
      return;
    }
    const value = t as unknown as Record<string, unknown> | null;
    if (target.kind === "global") {
      // null clears the global theme.
      void save({ theme: value as Record<string, unknown> }, value ? "Global theme saved." : "Global theme cleared.");
      return;
    }
    const field = target.kind === "role" ? "role_themes" : "group_themes";
    const map = { ...((data[field] as Record<string, unknown> | undefined) ?? {}) };
    if (value) {
      map[target.id] = value;
    } else {
      delete map[target.id];
    }
    void save({ [field]: map }, value ? `Theme for ${target.label} saved.` : `Theme for ${target.label} cleared.`);
  }

  const roleThemes = (data?.role_themes as Record<string, unknown> | undefined) ?? {};
  const groupThemes = (data?.group_themes as Record<string, unknown> | undefined) ?? {};
  const has = Boolean(themeFor(target, data));
  return (
    <section className="page page-wide" aria-labelledby="theme-mgmt-heading">
      <header className="page-header">
        <h1 id="theme-mgmt-heading">Theme management</h1>
        <p className="page-lede">
          The look people start from. A person&apos;s own choice wins, then their group&apos;s theme, then their role&apos;s, then the global one.
        </p>
      </header>
      {error ? <ErrorNotice error={error} /> : null}
      {saved ? (
        <p className="banner banner-ok" role="status">
          {saved}
        </p>
      ) : null}
      {!data ? (
        <p className="muted">Loading</p>
      ) : (
        <>
          <div className="admin-grid">
            <div className="panel admin-box">
              <h2>Defaults</h2>
              <div className="theme-row">
                <span className="field-label">Navigation</span>
                <Segmented<string>
                  ariaLabel="Default navigation"
                  value={data.nav_layout ?? "modern"}
                  onChange={(v) => void save({ nav_layout: v as "modern" | "classic" }, "Default navigation saved.")}
                  options={[
                    { id: "modern", label: "Modern" },
                    { id: "classic", label: "Classic" },
                  ]}
                />
              </div>
              <div className="theme-row">
                <span className="field-label">Compute pages</span>
                <Segmented<string>
                  ariaLabel="Default compute pages"
                  value={data.instance_layout ?? "instance"}
                  onChange={(v) => void save({ instance_layout: v as "instance" | "classic" }, "Default compute layout saved.")}
                  options={[
                    { id: "instance", label: "Instance" },
                    { id: "classic", label: "Classic" },
                  ]}
                />
              </div>
              <p className="field-hint">People who picked their own layout in Theme keep it.</p>
            </div>
            <div className="panel admin-box">
              <h2>Design profiles</h2>
              <div className="theme-row">
                <span className="field-label">Profiles per person</span>
                <Segmented<string>
                  ariaLabel="Design profiles per person"
                  value={String(data.profile_limit ?? 3)}
                  onChange={(v) => void save({ profile_limit: Number(v) }, "Design profile limit saved.")}
                  options={[1, 2, 3].map((n) => ({ id: String(n), label: String(n), disabled: !data.can_set_profile_limit }))}
                />
                <p className="field-hint">
                  At most 3 for everyone. Only a SuperAdmin can lower it; no permission changes that. People over a lowered limit keep their extra profiles
                  read-only until they delete them.
                </p>
              </div>
            </div>
          </div>
          <div className="panel admin-box">
            <h2>Themes</h2>
            <div className="theme-targets" role="tablist" aria-label="Whose theme">
              <button type="button" role="tab" aria-selected={target.kind === "global"} className="chip" onClick={() => setTarget({ kind: "global" })}>
                Global{data.theme ? " ●" : ""}
              </button>
              {(iam?.roles ?? []).map((r) => (
                <button
                  key={r.name}
                  type="button"
                  role="tab"
                  aria-selected={target.kind === "role" && target.id === r.name}
                  className="chip"
                  onClick={() => setTarget({ kind: "role", id: r.name, label: r.title || r.name })}
                >
                  Role: {r.title || r.name}
                  {roleThemes[r.name] ? " ●" : ""}
                </button>
              ))}
              {(iam?.groups ?? []).map((g) => (
                <button
                  key={g.id}
                  type="button"
                  role="tab"
                  aria-selected={target.kind === "group" && target.id === g.id}
                  className="chip"
                  onClick={() => setTarget({ kind: "group", id: g.id, label: g.name })}
                >
                  Group: {g.name}
                  {groupThemes[g.id] ? " ●" : ""}
                </button>
              ))}
            </div>
            <div className="theme-code-bar">
              <Segmented<"pick" | "code">
                ariaLabel="Editor"
                value={mode}
                onChange={setMode}
                options={[
                  { id: "pick", label: "Pick" },
                  { id: "code", label: "Code editor" },
                ]}
              />
              <span className="theme-spacer" />
              <span className="meta">{has ? "A theme is set here." : "Nothing set here; it inherits."}</span>
            </div>
            {mode === "pick" ? (
              <div className="theme-layout">
                <div className="theme-editor">
                  <ColourEditor draft={draft} onChange={setDraft} />
                  <StyleEditor draft={draft} onChange={setDraft} />
                </div>
                <aside className="theme-aside">
                  <ThemePreview theme={draft} />
                  <div className="theme-apply-bar">
                    <button className="btn btn-ghost btn-sm" type="button" disabled={busy || !has} onClick={() => saveTheme(null)}>
                      Clear
                    </button>
                    <span className="theme-spacer" />
                    <button className="btn btn-secondary btn-sm" type="button" onClick={() => previewTheme(draft)}>
                      Preview on me
                    </button>
                    <button className="btn btn-secondary btn-sm" type="button" onClick={() => previewTheme(null)}>
                      Stop preview
                    </button>
                    <button className="btn btn-primary btn-sm" type="button" disabled={busy} onClick={() => saveTheme(draft)}>
                      Save
                    </button>
                  </div>
                </aside>
              </div>
            ) : (
              <CodeThemeEditor live={has ? draft : savedTheme()} onPreview={(t) => previewTheme(t)} onApply={(t) => saveTheme(t)} applyLabel="Save" />
            )}
          </div>
        </>
      )}
    </section>
  );
}
