import { useEffect, useState, type MouseEvent as ReactMouseEvent } from "react";
import { changeWorkloadExtensions, getWorkload, listWorkloadExtensions, type WorkloadExtension } from "../api/client";
import { ActionMenu } from "../components/ActionMenu";
import { ContextMenu, useContextMenu, type ContextItem } from "../components/ContextMenu";
import { LoadingState } from "../components/EmptyState";
import { ErrorNotice } from "../components/ErrorNotice";
import { Link } from "../components/Link";
import { PageHeader } from "../components/PageHeader";
import { StatusBadge } from "../components/StatusBadge";
import { formatWhen } from "../format";
import { usePoll } from "../query";
import { canMutate } from "../rbac";
import { usePath } from "../router";
import { useSession } from "../session";

type Action = "install" | "update" | "reinstall";

const STATE: Record<string, { status: string; label: string }> = {
  installed: { status: "healthy", label: "Installed" },
  installing: { status: "pending", label: "Working" },
  failed: { status: "critical", label: "Failed" },
  missing: { status: "unknown", label: "Not installed" },
};

/** WorkloadExtensionsPage lists a container's optional extras and installs, updates or reinstalls them. */
export function WorkloadExtensionsPage() {
  const path = usePath();
  const id = decodeURIComponent(path.split("/")[2] ?? "");
  const session = useSession();
  const mutate = canMutate(session.status === "ready" ? session.user?.roles : undefined);
  const ctx = useContextMenu();
  const [name, setName] = useState("");
  const [items, setItems] = useState<WorkloadExtension[] | null>(null);
  const [busy, setBusy] = useState(false);
  const [note, setNote] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [checking, setChecking] = useState(false);

  async function reload(refresh = false) {
    try {
      const res = await listWorkloadExtensions(id, refresh);
      setItems(res.items);
      setBusy(res.busy);
      setNote(res.note ?? null);
      setError(null);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not read extensions");
      setItems((cur) => cur ?? []);
    }
  }

  useEffect(() => {
    void getWorkload(id)
      .then((w) => setName(w.name))
      .catch(() => undefined);
    void reload(true);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id]);
  usePoll(() => reload(false), busy ? 3000 : 30000);

  async function run(action: Action, extras: string[] | "all") {
    setError(null);
    try {
      const res = await changeWorkloadExtensions(id, extras === "all" ? { action, all: true } : { action, extras });
      const verb = { install: "Installing", update: "Updating", reinstall: "Reinstalling" }[action];
      setNotice(`${verb} ${res.extras.join(", ")} in the background. You can leave this page.`);
      setSelected(new Set());
      await reload(false);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not start");
    }
  }

  function rowItems(x: WorkloadExtension): ContextItem[] {
    if (!mutate || !x.available) {
      return [];
    }
    const installed = x.state === "installed";
    return [
      { label: installed ? "Install again" : "Install", onClick: () => void run("install", [x.id]), disabled: busy },
      { label: "Update", onClick: () => void run("update", [x.id]), disabled: busy || !installed },
      { label: "Reinstall", onClick: () => void run("reinstall", [x.id]), disabled: busy || !installed },
    ];
  }

  if (!items) {
    return (
      <section className="page">
        <LoadingState label="Checking extensions" />
      </section>
    );
  }
  const installedCount = items.filter((x) => x.state === "installed").length;
  const chosen = items.filter((x) => selected.has(x.id));
  return (
    <section className="page page-wide" aria-labelledby="ext-heading">
      <PageHeader
        id="ext-heading"
        title={`Extensions${name ? ` on ${name}` : ""}`}
        kicker={
          <>
            Optional software inside this container. Changes run in the background and do not depend on this page staying open.{" "}
            <Link href={`/workloads/${id}`}>Back to the workload</Link>
          </>
        }
        actions={
          <div className="btn-row is-flush">
            <button
              className="btn btn-secondary"
              type="button"
              disabled={checking || busy}
              onClick={() => {
                setChecking(true);
                void reload(true).finally(() => setChecking(false));
              }}
            >
              {checking ? "Checking" : "Check what's installed"}
            </button>
            {mutate ? (
              <>
                <button className="btn btn-secondary" type="button" disabled={busy || installedCount === 0} onClick={() => void run("update", "all")}>
                  Update all
                </button>
                <button
                  className="btn btn-ghost"
                  type="button"
                  disabled={busy || installedCount === 0}
                  onClick={() => {
                    if (window.confirm("Reinstall every installed extension? Docker restarts while it is reinstalled.")) {
                      void run("reinstall", "all");
                    }
                  }}
                >
                  Reinstall all
                </button>
              </>
            ) : null}
          </div>
        }
      />
      {error ? <ErrorNotice error={error} /> : null}
      {note ? <p className="banner banner-warn">{note}</p> : null}
      {busy ? (
        <p className="banner" role="status">
          A change is running. This page updates when it finishes.
        </p>
      ) : notice ? (
        <p className="banner banner-ok" role="status">
          {notice}
        </p>
      ) : null}
      {mutate && chosen.length > 0 ? (
        <div className="toolbar">
          <div className="toolbar-group">
            <span className="meta">{chosen.length} selected</span>
            <button className="btn btn-sm btn-primary" type="button" disabled={busy} onClick={() => void run("install", chosen.map((x) => x.id))}>
              Install
            </button>
            <button className="btn btn-sm btn-secondary" type="button" disabled={busy} onClick={() => void run("update", chosen.map((x) => x.id))}>
              Update
            </button>
            <button className="btn btn-sm btn-secondary" type="button" disabled={busy} onClick={() => void run("reinstall", chosen.map((x) => x.id))}>
              Reinstall
            </button>
          </div>
        </div>
      ) : null}
      <div className="table-wrap">
        <table aria-label="Extensions">
          <thead>
            <tr>
              {mutate ? <th /> : null}
              <th>Extension</th>
              <th>Status</th>
              <th>Version</th>
              <th>Notes</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {items.map((x) => {
              const st = STATE[x.state] ?? { status: "unknown", label: x.state };
              const menu = rowItems(x);
              return (
                <tr key={x.id} onContextMenu={menu.length ? (e: ReactMouseEvent) => ctx.openAt(e, x.name, menu) : undefined}>
                  {mutate ? (
                    <td>
                      <input
                        type="checkbox"
                        aria-label={`Select ${x.name}`}
                        disabled={!x.available}
                        checked={selected.has(x.id)}
                        onChange={() =>
                          setSelected((cur) => {
                            const next = new Set(cur);
                            if (next.has(x.id)) {
                              next.delete(x.id);
                            } else {
                              next.add(x.id);
                            }
                            return next;
                          })
                        }
                      />
                    </td>
                  ) : null}
                  <td>
                    <strong>{x.name}</strong>
                    <div className="meta">
                      {x.description}
                      {x.requires?.length ? ` · needs ${x.requires.join(", ")}` : ""}
                    </div>
                  </td>
                  <td>
                    <StatusBadge status={st.status} label={st.label} />
                    {x.requested && x.state !== "installed" ? <div className="meta">chosen for this container</div> : null}
                  </td>
                  <td>{x.version ? <code>{x.version}</code> : <span className="meta">-</span>}</td>
                  <td>
                    {!x.available ? <span className="meta">{x.unavailable_reason}</span> : null}
                    {x.message ? <div className="tunnel-reason">{x.message}</div> : null}
                    {x.detail ? <div className="meta">{x.detail}</div> : null}
                    {x.updated_at ? <div className="meta">checked {formatWhen(x.updated_at)}</div> : null}
                  </td>
                  <td>
                    {mutate && x.available ? (
                      x.state === "installed" ? (
                        <ActionMenu label={`${x.name} actions`} items={menu.filter((i): i is Exclude<ContextItem, "sep"> => i !== "sep" && !i.disabled)} />
                      ) : (
                        <button className="btn btn-sm btn-secondary" type="button" disabled={busy} onClick={() => void run("install", [x.id])}>
                          {x.state === "failed" ? "Retry" : "Install"}
                        </button>
                      )
                    ) : null}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
      <ContextMenu menu={ctx.menu} onClose={ctx.close} />
    </section>
  );
}
