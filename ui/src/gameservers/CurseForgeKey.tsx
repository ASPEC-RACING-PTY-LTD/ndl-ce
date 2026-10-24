import { useState } from "react";
import { ErrorNotice } from "../components/ErrorNotice";
import { putGameSettings } from "./api";

/**
 * CurseForgeKey asks for the CurseForge API key once. CurseForge serves its
 * catalogue only with a key; it is saved sealed for the whole cluster.
 */
export function CurseForgeKey({ onSaved }: { onSaved: () => void }) {
  const [key, setKey] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function save() {
    setBusy(true);
    setError(null);
    try {
      await putGameSettings({ curseforge_key: key.trim() });
      setKey("");
      onSaved();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not save the key");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="gs-cf-key">
      <p>
        CurseForge needs an API key. Create one at{" "}
        <a href="https://console.curseforge.com/" target="_blank" rel="noreferrer noopener">
          console.curseforge.com
        </a>{" "}
        (API keys), paste it here, and every game server can use CurseForge.
      </p>
      {error ? <ErrorNotice error={error} /> : null}
      <form
        className="gs-toolbar"
        onSubmit={(e) => {
          e.preventDefault();
          if (key.trim()) {
            void save();
          }
        }}
      >
        <input
          className="field-input"
          type="password"
          autoComplete="off"
          aria-label="CurseForge API key"
          placeholder="CurseForge API key"
          value={key}
          onChange={(e) => setKey(e.target.value)}
        />
        <button className="btn btn-sm btn-primary" type="submit" disabled={busy || !key.trim()}>
          {busy ? "Checking" : "Save key"}
        </button>
      </form>
    </div>
  );
}
