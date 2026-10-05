import { useState } from "react";
import {
  createAIProvider,
  createNodeGroup,
  createRegistry,
  listAIProviders,
  listNodeGroups,
  listNodes,
  listRegistries,
} from "../api/client";
import { useQuery } from "../query";
import { useSession } from "../session";
import { Dialog } from "../ui/Dialog";
import { canMutate } from "../ux";
import { DeleteButton } from "./DeleteButton";
import { EmptyState, ErrorState, LoadingState } from "./EmptyState";
import { Field } from "./Field";

function useMutate(): boolean {
  const session = useSession();
  return canMutate(session.status === "ready" ? session.user?.roles : undefined);
}

function errorText(err: unknown, fallback: string): string {
  return err instanceof Error ? err.message : fallback;
}

/** Container registries used by OCI workloads. */
export function RegistriesPanel() {
  const mutate = useMutate();
  const q = useQuery("registries", () => listRegistries());
  const items = q.data?.items ?? [];
  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  const [url, setUrl] = useState("");
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [insecure, setInsecure] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function onCreate() {
    setBusy(true);
    setError(null);
    try {
      await createRegistry({
        name: name.trim(),
        url: url.trim(),
        username: username.trim() || undefined,
        password: password || undefined,
        insecure,
      });
      setOpen(false);
      setName("");
      setUrl("");
      setUsername("");
      setPassword("");
      setInsecure(false);
      await q.reload();
    } catch (err) {
      setError(errorText(err, "Create failed"));
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="section" aria-labelledby="registries-heading">
      <div className="section-head">
        <h2 id="registries-heading">Registries</h2>
        {mutate ? (
          <button className="btn btn-primary btn-sm" type="button" onClick={() => setOpen(true)}>
            Add registry
          </button>
        ) : null}
      </div>
      {q.error ? <ErrorState>{q.error}</ErrorState> : null}
      {q.loading && !q.data ? <LoadingState label="Loading registries" /> : null}
      {q.data && items.length === 0 ? (
        <EmptyState icon="storage" title="No registries">
          Add a private registry to pull OCI images that need credentials.
        </EmptyState>
      ) : null}
      {items.length > 0 ? (
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>Name</th>
                <th>URL</th>
                <th>Credentials</th>
                <th className="col-tools">
                  <span className="visually-hidden">Actions</span>
                </th>
              </tr>
            </thead>
            <tbody>
              {items.map((r) => (
                <tr key={r.id}>
                  <td>
                    <strong>{r.name}</strong>
                    {r.insecure ? <span className="cell-sub">Insecure TLS</span> : null}
                  </td>
                  <td className="cell-mono">{r.url}</td>
                  <td>{r.has_credentials ? "Stored" : <span className="muted">None</span>}</td>
                  <td className="col-tools">
                    {mutate ? (
                      <DeleteButton
                        path={`/registries/${r.id}`}
                        name={r.name}
                        noun="registry"
                        description={<p>The registry and its stored credentials are removed. Running workloads keep their images.</p>}
                        onDeleted={() => q.reload()}
                      />
                    ) : null}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : null}
      <Dialog
        open={open}
        title="Add registry"
        unsaved={Boolean(name || url)}
        onClose={() => setOpen(false)}
        footer={
          <button className="btn btn-primary" type="submit" form="registry-create" disabled={busy || !name.trim() || !url.trim()}>
            Add registry
          </button>
        }
      >
        <form
          id="registry-create"
          className="form"
          onSubmit={(e) => {
            e.preventDefault();
            void onCreate();
          }}
        >
          {error ? <ErrorState>{error}</ErrorState> : null}
          <Field id="registry-name" label="Name" value={name} onChange={(e) => setName(e.target.value)} />
          <Field id="registry-url" label="URL" value={url} onChange={(e) => setUrl(e.target.value)} placeholder="registry.example.com" />
          <Field id="registry-user" label="Username" value={username} onChange={(e) => setUsername(e.target.value)} autoComplete="off" />
          <Field
            id="registry-pass"
            label="Password or token"
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            autoComplete="new-password"
          />
          <label className="check">
            <input type="checkbox" checked={insecure} onChange={(e) => setInsecure(e.target.checked)} /> Allow insecure TLS
          </label>
        </form>
      </Dialog>
    </section>
  );
}

const PROVIDER_KINDS = ["openai", "anthropic", "gemini", "ollama", "local", "openai_compatible", "private"];

/** Bring-your-own AI providers used by Ask and Plans. */
export function AIProvidersPanel() {
  const mutate = useMutate();
  const q = useQuery("ai-providers", () => listAIProviders());
  const items = q.data?.items ?? [];
  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  const [kind, setKind] = useState("openai");
  const [endpoint, setEndpoint] = useState("");
  const [model, setModel] = useState("");
  const [apiKey, setApiKey] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function onCreate() {
    setBusy(true);
    setError(null);
    try {
      await createAIProvider({
        name: name.trim(),
        kind,
        endpoint: endpoint.trim() || undefined,
        model: model.trim() || undefined,
        api_key: apiKey || undefined,
      });
      setOpen(false);
      setName("");
      setEndpoint("");
      setModel("");
      setApiKey("");
      await q.reload();
    } catch (err) {
      setError(errorText(err, "Create failed"));
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="section" aria-labelledby="ai-providers-heading">
      <div className="section-head">
        <h2 id="ai-providers-heading">Providers</h2>
        {mutate ? (
          <button className="btn btn-secondary btn-sm" type="button" onClick={() => setOpen(true)}>
            Add provider
          </button>
        ) : null}
      </div>
      {q.error ? <ErrorState>{q.error}</ErrorState> : null}
      {q.data && items.length === 0 ? <p className="muted">No providers configured. Ask works offline without one.</p> : null}
      {items.length > 0 ? (
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>Name</th>
                <th>Kind</th>
                <th>Model</th>
                <th>Credentials</th>
                <th className="col-tools">
                  <span className="visually-hidden">Actions</span>
                </th>
              </tr>
            </thead>
            <tbody>
              {items.map((pr) => (
                <tr key={pr.id}>
                  <td>
                    <strong>{pr.name}</strong>
                    {pr.endpoint ? <span className="cell-sub cell-mono">{pr.endpoint}</span> : null}
                  </td>
                  <td>{pr.kind ?? "Not set"}</td>
                  <td>{pr.model || <span className="muted">Default</span>}</td>
                  <td>{pr.has_credentials ? "Stored" : <span className="muted">None</span>}</td>
                  <td className="col-tools">
                    {mutate && pr.id ? (
                      <DeleteButton
                        path={`/ai/providers/${pr.id}`}
                        name={pr.name ?? "provider"}
                        noun="provider"
                        description={<p>The provider and its stored key are removed. Profiles that use it must be deleted first.</p>}
                        onDeleted={() => q.reload()}
                      />
                    ) : null}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : null}
      <Dialog
        open={open}
        title="Add provider"
        unsaved={Boolean(name || apiKey)}
        onClose={() => setOpen(false)}
        footer={
          <button className="btn btn-primary" type="submit" form="provider-create" disabled={busy || !name.trim()}>
            Add provider
          </button>
        }
      >
        <form
          id="provider-create"
          className="form"
          onSubmit={(e) => {
            e.preventDefault();
            void onCreate();
          }}
        >
          {error ? <ErrorState>{error}</ErrorState> : null}
          <Field id="provider-name" label="Name" value={name} onChange={(e) => setName(e.target.value)} />
          <label className="field" htmlFor="provider-kind">
            <span className="field-label">Kind</span>
            <select id="provider-kind" className="field-input" value={kind} onChange={(e) => setKind(e.target.value)}>
              {PROVIDER_KINDS.map((k) => (
                <option key={k} value={k}>
                  {k}
                </option>
              ))}
            </select>
          </label>
          <Field id="provider-endpoint" label="Endpoint" value={endpoint} onChange={(e) => setEndpoint(e.target.value)} hint="Optional for hosted providers." />
          <Field id="provider-model" label="Model" value={model} onChange={(e) => setModel(e.target.value)} />
          <Field
            id="provider-key"
            label="API key"
            type="password"
            value={apiKey}
            onChange={(e) => setApiKey(e.target.value)}
            autoComplete="new-password"
          />
        </form>
      </Dialog>
    </section>
  );
}

/** Node groups used for placement. */
export function NodeGroupsPanel() {
  const mutate = useMutate();
  const q = useQuery("node-groups", () => listNodeGroups());
  const nodesQ = useQuery("nodes", () => listNodes(), 15000);
  const items = q.data?.items ?? [];
  const nodeName = new Map((nodesQ.data ?? []).map((n) => [n.id, n.name]));
  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  const [members, setMembers] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function onCreate() {
    setBusy(true);
    setError(null);
    try {
      await createNodeGroup({ name: name.trim(), node_ids: members });
      setOpen(false);
      setName("");
      setMembers([]);
      await q.reload();
    } catch (err) {
      setError(errorText(err, "Create failed"));
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="section" aria-labelledby="node-groups-heading">
      <div className="section-head">
        <h2 id="node-groups-heading">Node groups</h2>
        {mutate ? (
          <button className="btn btn-secondary btn-sm" type="button" onClick={() => setOpen(true)}>
            Add node group
          </button>
        ) : null}
      </div>
      {q.error ? <ErrorState>{q.error}</ErrorState> : null}
      {q.data && items.length === 0 ? <p className="muted">No node groups yet.</p> : null}
      {items.length > 0 ? (
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>Name</th>
                <th>Members</th>
                <th className="col-tools">
                  <span className="visually-hidden">Actions</span>
                </th>
              </tr>
            </thead>
            <tbody>
              {items.map((g) => (
                <tr key={g.id}>
                  <td>
                    <strong>{g.name}</strong>
                  </td>
                  <td>
                    {g.members?.length ? g.members.map((id) => nodeName.get(id) ?? id.slice(0, 8)).join(", ") : <span className="muted">No members</span>}
                  </td>
                  <td className="col-tools">
                    {mutate ? (
                      <DeleteButton
                        path={`/node-groups/${g.id}`}
                        name={g.name}
                        noun="node group"
                        description={<p>The group is removed. Its nodes and their workloads are not affected.</p>}
                        onDeleted={() => q.reload()}
                      />
                    ) : null}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : null}
      <Dialog
        open={open}
        title="Add node group"
        unsaved={Boolean(name)}
        onClose={() => setOpen(false)}
        footer={
          <button className="btn btn-primary" type="submit" form="node-group-create" disabled={busy || !name.trim()}>
            Add node group
          </button>
        }
      >
        <form
          id="node-group-create"
          className="form"
          onSubmit={(e) => {
            e.preventDefault();
            void onCreate();
          }}
        >
          {error ? <ErrorState>{error}</ErrorState> : null}
          <Field id="node-group-name" label="Name" value={name} onChange={(e) => setName(e.target.value)} />
          <fieldset className="field">
            <legend className="field-label">Members</legend>
            {(nodesQ.data ?? []).map((n) => (
              <label key={n.id} className="check">
                <input
                  type="checkbox"
                  checked={members.includes(n.id)}
                  onChange={(e) => setMembers((cur) => (e.target.checked ? [...cur, n.id] : cur.filter((x) => x !== n.id)))}
                />{" "}
                {n.name}
              </label>
            ))}
          </fieldset>
        </form>
      </Dialog>
    </section>
  );
}
