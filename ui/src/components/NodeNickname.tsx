import { useState } from "react";
import { setNodeNickname } from "../api/client";
import {
  canEditNodeNicknames,
  nodeNickname,
  useNodeNicknames,
} from "../nodeNames";
import { invalidateQueries } from "../query";
import { Dialog } from "../ui/Dialog";
import { Icon } from "./Icon";

type NamedNode = { id: string; name: string; real_name?: string };

/** NodeName shows a node under its nickname, with its own name in the tooltip when they differ. */
export function NodeName({ node }: { node: NamedNode }) {
  const label = useNodeNicknames();
  const real = node.real_name ?? node.name;
  const shown = label(node.id, real);
  return (
    <span title={shown !== real ? `Node name: ${real}` : undefined}>
      {shown}
    </span>
  );
}

/**
 * NodeNicknameButton renames a node in the UI only. Everyone in the cluster
 * sees the nickname; the node's host name, ID and CLI name do not change.
 */
export function NodeNicknameButton({ node }: { node: NamedNode }) {
  useNodeNicknames();
  const [open, setOpen] = useState(false);
  const [value, setValue] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const real = node.real_name ?? node.name;
  if (!canEditNodeNicknames()) {
    return null;
  }
  const save = async (next: string) => {
    setBusy(true);
    setError(null);
    try {
      await setNodeNickname(node.id, next);
      invalidateQueries();
      setOpen(false);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <>
      <button
        className="btn btn-sm btn-ghost node-nickname-btn"
        type="button"
        aria-label={`Rename ${label(node, real)}`}
        title="Rename (shown in the UI only)"
        onClick={() => {
          setValue(nodeNickname(node.id) ?? "");
          setError(null);
          setOpen(true);
        }}
      >
        <Icon name="edit" size={14} />
      </button>
      <Dialog
        open={open}
        title={`Rename ${real}`}
        onClose={() => setOpen(false)}
        footer={
          <div className="btn-row">
            {nodeNickname(node.id) ? (
              <button
                className="btn btn-ghost"
                type="button"
                disabled={busy}
                onClick={() => void save("")}
              >
                Use node name
              </button>
            ) : null}
            <button
              className="btn btn-ghost"
              type="button"
              onClick={() => setOpen(false)}
            >
              Cancel
            </button>
            <button
              className="btn btn-primary"
              type="button"
              disabled={busy}
              onClick={() => void save(value)}
            >
              Save
            </button>
          </div>
        }
      >
        <form
          className="stack"
          onSubmit={(e) => {
            e.preventDefault();
            void save(value);
          }}
        >
          <label className="field">
            <span className="field-label">Nickname</span>
            <input
              className="field-input"
              value={value}
              maxLength={48}
              placeholder={real}
              autoFocus
              onChange={(e) => setValue(e.target.value)}
            />
          </label>
          <p className="meta">
            Everyone sees this name across the UI. The node keeps its own name{" "}
            <code>{real}</code> for the CLI, the API and its host name. Leave it
            empty to use the node name.
          </p>
          {error ? <p className="banner banner-warn">{error}</p> : null}
        </form>
      </Dialog>
    </>
  );
}

function label(node: NamedNode, real: string): string {
  return nodeNickname(node.id) ?? real;
}
