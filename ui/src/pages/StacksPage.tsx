import { useEffect, useState } from "react";
import { applyStack, getStack, importStack, listPools, listStacks, patchStackMember, type Stack, type StackMember } from "../api/client";
import type { StoragePool } from "../api/phase3";
import { Link } from "../components/Link";
import { honestStatus } from "../format";
import { navigate, usePath } from "../router";
import { useSession } from "../session";
import { canMutate } from "../ux";

export function StacksPage() {
  const session = useSession();
  const roles = session.status === "ready" ? session.user?.roles : undefined;
  const mutate = canMutate(roles);
  const [items, setItems] = useState<Stack[]>([]);
  const [pools, setPools] = useState<StoragePool[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [name, setName] = useState("demo");
  const [compose, setCompose] = useState(`services:
  web:
    image: nginx:alpine
    ports:
      - "8080:80"
    volumes:
      - webdata:/usr/share/nginx/html
volumes:
  webdata:
`);
  const [poolID, setPoolID] = useState("");
  const [busy, setBusy] = useState(false);

  function refresh() {
    return listStacks()
      .then((listed) => setItems(listed.items ?? []))
      .catch((err) => setError(err instanceof Error ? err.message : "Unavailable"));
  }

  useEffect(() => {
    let cancelled = false;
    void Promise.all([listStacks(), listPools()])
      .then(([listed, poolsRes]) => {
        if (cancelled) {
          return;
        }
        setItems(listed.items ?? []);
        const ready = (poolsRes.items ?? []).filter((p) => p.status === "available" || p.status === "warning");
        setPools(ready);
        if (!poolID && ready[0]) {
          setPoolID(ready[0].id);
        }
      })
      .catch((err) => {
        if (!cancelled) {
          setError(err instanceof Error ? err.message : "Unavailable");
        }
      });
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  async function onImport() {
    setBusy(true);
    setError(null);
    try {
      const created = await importStack({
        name,
        compose,
        pool_id: poolID || undefined,
      });
      await refresh();
      navigate(`/stacks/${created.id}`);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Import failed");
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="page page-wide" aria-labelledby="stacks-heading">
      <header className="page-header">
        <h1 id="stacks-heading">Stacks</h1>
        <p className="page-kicker">Multi-container apps as inspectable No-dal objects. Compose is import only.</p>
      </header>
      {error ? (
        <p className="banner banner-error" role="alert">
          {error}
        </p>
      ) : null}
      <article className="panel">
        <h2>On this node</h2>
        {items.length === 0 ? (
          <div className="empty-panel">
            <p className="empty-title">No stacks yet</p>
            <p>Import a compose file to create editable stack members. Apply turns each member into an OCI workload.</p>
          </div>
        ) : (
          <ul className="plain-list">
            {items.map((s) => (
              <li key={s.id}>
                <Link href={`/stacks/${s.id}`}>{s.name}</Link> {honestStatus(s.status)}
              </li>
            ))}
          </ul>
        )}
      </article>
      {mutate ? (
        <article className="panel">
          <h2>Import Compose</h2>
            <p>Named volumes become Directory volumes or mapped No-dal volume UUIDs. Creating volumes requires storage.volume.create. Privileged requires admin.</p>
          <div className="field">
            <label className="field-label" htmlFor="stack-name">
              Name
            </label>
            <input id="stack-name" className="field-input" value={name} onChange={(e) => setName(e.target.value)} />
          </div>
          <div className="field">
            <label className="field-label" htmlFor="stack-pool">
              Storage pool
            </label>
            <select id="stack-pool" className="field-input" value={poolID} onChange={(e) => setPoolID(e.target.value)}>
              <option value="">Select pool</option>
              {pools.map((p) => (
                <option key={p.id} value={p.id}>
                  {p.name}
                </option>
              ))}
            </select>
          </div>
          <div className="field">
            <label className="field-label" htmlFor="stack-compose">
              compose.yml
            </label>
            <textarea id="stack-compose" className="field-input" rows={12} value={compose} onChange={(e) => setCompose(e.target.value)} />
          </div>
          <p className="btn-row">
            <button className="btn btn-primary" type="button" disabled={busy || !name.trim()} onClick={() => void onImport()}>
              Import stack
            </button>
          </p>
        </article>
      ) : null}
    </section>
  );
}

export function StackDetailPage() {
  const session = useSession();
  const roles = session.status === "ready" ? session.user?.roles : undefined;
  const mutate = canMutate(roles);
  const admin = Boolean(roles?.includes("admin"));
  const path = usePath();
  const id = path.split("/").pop() ?? "";
  const [stack, setStack] = useState<Stack | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    let cancelled = false;
    void getStack(id)
      .then((row) => {
        if (!cancelled) {
          setStack(row);
        }
      })
      .catch((err) => {
        if (!cancelled) {
          setError(err instanceof Error ? err.message : "Unavailable");
        }
      });
    return () => {
      cancelled = true;
    };
  }, [id]);

  async function onApply() {
    setBusy(true);
    setError(null);
    try {
      const updated = await applyStack(id);
      setStack(updated);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Apply failed");
    } finally {
      setBusy(false);
    }
  }

  if (!stack && !error) {
    return (
      <section className="page">
        <p role="status">Loading</p>
      </section>
    );
  }

  return (
    <section className="page page-wide" aria-labelledby="stack-detail-heading">
      <header className="page-header">
        <p>
          <Link href="/stacks">Stacks</Link>
        </p>
        <h1 id="stack-detail-heading">{stack?.name ?? "Stack"}</h1>
        <p className="page-kicker">
          Status {stack ? honestStatus(stack.status) : "unavailable"}. Members are editable No-dal objects; Compose is not the runtime source of truth.
        </p>
      </header>
      {error ? (
        <p className="banner banner-error" role="alert">
          {error}
        </p>
      ) : null}
      {mutate ? (
        <p className="btn-row">
          <button className="btn btn-primary" type="button" disabled={busy} onClick={() => void onApply()}>
            Apply stack
          </button>
        </p>
      ) : null}
      <article className="panel">
        <h2>Members</h2>
        {!stack?.members?.length ? (
          <p>No members.</p>
        ) : (
          <ul className="plain-list">
            {stack.members.map((m) => (
              <li key={m.id}>
                <MemberEditor
                  stackId={id}
                  member={m}
                  mutate={mutate}
                  admin={admin}
                  onSaved={setStack}
                  onError={setError}
                />
              </li>
            ))}
          </ul>
        )}
      </article>
    </section>
  );
}

function envToText(env: unknown): string {
  if (!Array.isArray(env)) {
    return "";
  }
  return env
    .map((item) => {
      if (!item || typeof item !== "object") {
        return "";
      }
      const row = item as { name?: unknown; value?: unknown };
      const name = typeof row.name === "string" ? row.name : "";
      const value = typeof row.value === "string" ? row.value : "";
      if (!name) {
        return "";
      }
      return `${name}=${value}`;
    })
    .filter(Boolean)
    .join("\n");
}

function parseEnvText(text: string): { name: string; value: string }[] {
  return text
    .split("\n")
    .map((line) => line.trim())
    .filter(Boolean)
    .map((line) => {
      const idx = line.indexOf("=");
      if (idx < 0) {
        return { name: line, value: "" };
      }
      return { name: line.slice(0, idx), value: line.slice(idx + 1) };
    });
}

function MemberEditor({
  stackId,
  member,
  mutate,
  admin,
  onSaved,
  onError,
}: {
  stackId: string;
  member: StackMember;
  mutate: boolean;
  admin: boolean;
  onSaved: (stack: Stack) => void;
  onError: (message: string | null) => void;
}) {
  const desired = member.desired ?? {};
  const [image, setImage] = useState(typeof desired.image_pin === "string" ? desired.image_pin : "");
  const [envText, setEnvText] = useState(envToText(desired.env));
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    setImage(typeof member.desired?.image_pin === "string" ? member.desired.image_pin : "");
    setEnvText(envToText(member.desired?.env));
  }, [member]);

  async function onSave() {
    setBusy(true);
    onError(null);
    try {
      const updated = await patchStackMember(stackId, member.id, {
        image_pin: image,
        env: parseEnvText(envText),
      });
      onSaved(updated);
    } catch (err) {
      onError(err instanceof Error ? err.message : "Save failed");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div>
      <p>
        <strong>{member.service_name}</strong> {honestStatus(member.status)}
        {member.workload_id ? (
          <>
            {" "}
            <Link href={`/workloads/${member.workload_id}`}>{member.workload?.name ?? member.workload_id}</Link>
            {member.workload?.kind ? ` (${member.workload.kind})` : ""}
            {member.workload?.health?.status ? ` health ${honestStatus(member.workload.health.status)}` : ""}
          </>
        ) : (
          <span> not applied</span>
        )}
        {member.reason ? ` ${member.reason}` : ""}
      </p>
      {member.workload_id ? (
        <p className="field-hint">
          Applied members keep this desired state for inspection. Edit the linked OCI workload for the running unit.
        </p>
      ) : null}
      {mutate ? (
        <>
          <div className="field">
            <label className="field-label" htmlFor={`member-image-${member.id}`}>
              Image
            </label>
            <input
              id={`member-image-${member.id}`}
              className="field-input"
              value={image}
              onChange={(e) => setImage(e.target.value)}
            />
          </div>
          <div className="field">
            <label className="field-label" htmlFor={`member-env-${member.id}`}>
              Environment
            </label>
            <textarea
              id={`member-env-${member.id}`}
              className="field-input"
              rows={4}
              value={envText}
              onChange={(e) => setEnvText(e.target.value)}
            />
          </div>
          {admin && desired.privileged ? <p>Privileged (admin import).</p> : null}
          <p className="btn-row">
            <button className="btn" type="button" disabled={busy || !image.trim()} onClick={() => void onSave()}>
              Save member
            </button>
          </p>
        </>
      ) : null}
    </div>
  );
}
