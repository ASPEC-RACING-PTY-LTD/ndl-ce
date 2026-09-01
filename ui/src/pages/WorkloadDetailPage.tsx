import { useEffect, useState } from "react";
import { getWorkload, patchWorkload, workloadAction } from "../api/client";
import type { Workload } from "../api/phase5";
import { Field } from "../components/Field";
import { formatBytes, honestStatus } from "../format";
import { currentPath, navigate } from "../router";
import { useSession } from "../session";

function canMutate(roles: string[] | undefined): boolean {
  return Boolean(roles?.includes("admin") || roles?.includes("operator"));
}

function workloadIDFromPath(): string {
  const parts = currentPath().split("/").filter(Boolean);
  return parts[0] === "workloads" ? parts[1] ?? "" : "";
}

export function WorkloadDetailPage() {
  const session = useSession();
  const roles = session.status === "ready" ? session.user?.roles : undefined;
  const mutate = canMutate(roles);
  const id = workloadIDFromPath();
  const [item, setItem] = useState<Workload | null>(null);
  const [cpus, setCpus] = useState("1");
  const [memoryMiB, setMemoryMiB] = useState("256");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function reload() {
    const w = await getWorkload(id);
    setItem(w);
    setCpus(String(w.cpus ?? 1));
    setMemoryMiB(String(Math.round((w.memory_bytes ?? 256 * 1024 * 1024) / (1024 * 1024))));
  }

  useEffect(() => {
    let cancelled = false;
    void reload().catch((err) => {
      if (!cancelled) {
        setError(err instanceof Error ? err.message : "Unavailable");
      }
    });
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id]);

  async function onAction(action: "start" | "stop" | "restart" | "delete" | "clone") {
    setBusy(true);
    setError(null);
    try {
      const next = await workloadAction(id, action);
      if (action === "clone") {
        navigate(`/workloads/${next.id}`);
        return;
      }
      await reload();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Action failed");
    } finally {
      setBusy(false);
    }
  }

  async function onSave() {
    setBusy(true);
    setError(null);
    try {
      await patchWorkload(id, {
        cpus: Number(cpus) || 1,
        memory_bytes: (Number(memoryMiB) || 256) * 1024 * 1024,
      });
      await reload();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Update failed");
    } finally {
      setBusy(false);
    }
  }

  if (!item) {
    return (
      <section className="page">
        <h1>Workload</h1>
        {error ? (
          <p className="banner banner-error" role="alert">
            {error}
          </p>
        ) : (
          <p>Collecting</p>
        )}
      </section>
    );
  }

  const ipv4 = item.nics?.[0]?.ipv4;
  const mac = item.nics?.[0]?.mac;

  return (
    <section className="page page-wide" aria-labelledby="workload-heading">
      <header className="page-header">
        <h1 id="workload-heading">{item.name}</h1>
        <p className="page-kicker">{item.kind}</p>
      </header>
      {error ? (
        <p className="banner banner-error" role="alert">
          {error}
        </p>
      ) : null}
      <article className="panel">
        <h2>Summary</h2>
        <dl className="definition-list">
          <div>
            <dt>Status</dt>
            <dd>{honestStatus(item.status)}</dd>
          </div>
          <div>
            <dt>Reason</dt>
            <dd>{item.reason || "None"}</dd>
          </div>
          <div>
            <dt>Image</dt>
            <dd>
              {item.image_pin || "Not reported"} {item.image_verified ? "(verified)" : "(not verified)"}
            </dd>
          </div>
          <div>
            <dt>PID</dt>
            <dd>{item.pid ?? "Not reported"}</dd>
          </div>
          <div>
            <dt>Unit</dt>
            <dd>{item.unit_active ? "active" : "inactive"}</dd>
          </div>
          <div>
            <dt>Memory</dt>
            <dd>{formatBytes(item.memory_bytes)}</dd>
          </div>
          <div>
            <dt>Privileged</dt>
            <dd>{item.privileged ? "yes" : "no"}</dd>
          </div>
          <div>
            <dt>IPv4</dt>
            <dd>{ipv4 || "Not reported"}</dd>
          </div>
          <div>
            <dt>MAC</dt>
            <dd>{mac || "Not reported"}</dd>
          </div>
          <div>
            <dt>Migrate ready</dt>
            <dd>{item.migrate_ready ? "yes" : "no"}</dd>
          </div>
        </dl>
      </article>
      {mutate ? (
        <article className="panel">
          <h2>Lifecycle</h2>
          <div className="btn-row">
            <button className="btn" type="button" disabled={busy} onClick={() => void onAction("start")}>
              Start
            </button>
            <button className="btn" type="button" disabled={busy} onClick={() => void onAction("stop")}>
              Stop
            </button>
            <button className="btn" type="button" disabled={busy} onClick={() => void onAction("restart")}>
              Restart
            </button>
            <button className="btn" type="button" disabled={busy} onClick={() => void onAction("clone")}>
              Clone
            </button>
            <button className="btn" type="button" disabled={busy} onClick={() => void onAction("delete")}>
              Delete
            </button>
          </div>
        </article>
      ) : null}
      {mutate ? (
        <article className="panel">
          <h2>Spec</h2>
          <Field id="wl-cpus" label="CPUs" type="number" min={1} value={cpus} onChange={(e) => setCpus(e.target.value)} />
          <Field
            id="wl-mem"
            label="Memory (MiB)"
            type="number"
            min={64}
            value={memoryMiB}
            onChange={(e) => setMemoryMiB(e.target.value)}
          />
          <div className="btn-row">
            <button className="btn btn-primary" type="button" disabled={busy} onClick={() => void onSave()}>
              Save spec
            </button>
          </div>
        </article>
      ) : null}
    </section>
  );
}
