import { useEffect, useState } from "react";
import { applyStack, getStack, importStack, listPools, listStacks, type Stack } from "../api/client";
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
          <p>Named volumes become Directory volumes or mapped No-dal volume UUIDs. Privileged requires admin.</p>
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
          Status {stack ? honestStatus(stack.status) : "unavailable"}. Members are No-dal objects; Compose is not the runtime source of truth.
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
                <strong>{m.service_name}</strong> {honestStatus(m.status)}
                {m.desired && typeof m.desired.image_pin === "string" ? ` ${m.desired.image_pin}` : ""}
                {m.workload_id ? (
                  <>
                    {" "}
                    <Link href={`/workloads/${m.workload_id}`}>{m.workload?.name ?? m.workload_id}</Link>
                    {m.workload?.kind ? ` (${m.workload.kind})` : ""}
                    {m.workload?.health?.status ? ` health ${honestStatus(m.workload.health.status)}` : ""}
                  </>
                ) : (
                  <span> not applied</span>
                )}
                {m.reason ? ` ${m.reason}` : ""}
              </li>
            ))}
          </ul>
        )}
      </article>
    </section>
  );
}
