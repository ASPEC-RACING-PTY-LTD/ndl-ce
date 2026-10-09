import { useEffect, useState } from "react";
import { PageHeader } from "../components/PageHeader";
import { ApiError, assignGpu, listGpus, unassignGpu } from "../api/client";
import type { GPUListResponse } from "../generated/openapi";
import { currentPath } from "../router";
import { ErrorNotice } from "../components/ErrorNotice";

function workloadIDFromPath(): string {
  const parts = currentPath().split("/").filter(Boolean);
  return parts[0] === "workloads" ? parts[1] ?? "" : "";
}

export function GpuPage() {
  const workloadID = workloadIDFromPath();
  const [data, setData] = useState<GPUListResponse | null>(null);
  const [gpuId, setGpuId] = useState("");
  const [mode, setMode] = useState("render");
  const [exclusive, setExclusive] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  function onUnassign(id: string) {
    setError(null);
    setNotice(null);
    void unassignGpu(id)
      .then((res) => {
        if (res.restart_required) {
          setNotice(res.message || "Restart the workload to remove the GPU devices from the running container.");
        }
        return reload();
      })
      .catch((err) => setError(err instanceof ApiError ? err.message : "Unassign failed"));
  }

  async function reload() {
    const body = await listGpus();
    setData(body);
    if (!gpuId && body.items?.[0]?.id) {
      setGpuId(body.items[0].id);
    }
  }

  useEffect(() => {
    void reload().catch((err) => setError(err instanceof Error ? err.message : "Unavailable"));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const items = data?.items ?? [];
  const orphaned = data?.orphaned_assignments ?? [];
  const nested = currentPath().startsWith("/node");
  const heading = nested ? (
    <h2 id="gpu-heading">GPUs</h2>
  ) : (
    <PageHeader
      id="gpu-heading"
      title="GPUs"
      kicker="Workloads receive a GPU only when assigned. gpu=all is refused. ACS override is refused. A Store-driven GPU picker is not part of this release."
    />
  );
  const inner = (
    <>
      {heading}
      {error ? (
        <ErrorNotice error={error} />
      ) : null}
      {notice ? (
        <p className="banner banner-warn" role="status">
          {notice}
        </p>
      ) : null}
      {data?.runtime && data.runtime.host_supported === false ? (
        <p className="banner" role="status">
          GPU runtime install is Unsupported on this host. {data.runtime.reason}
        </p>
      ) : null}
      {items.length === 0 ? <p>None detected</p> : null}
      {items.map((g) => (
        <article className="panel" key={g.id}>
          <h2>
            {g.vendor || "GPU"} {g.pci}
          </h2>
          <p>IOMMU group {g.iommu_group || "not reported"}</p>
          <ul>
            {(g.group_members ?? []).map((m) => (
              <li key={m.pci}>
                {m.pci} {m.kind}
              </li>
            ))}
          </ul>
          {(g.assignments ?? []).length === 0 ? <p>Unassigned</p> : (
            <ul>
              {g.assignments?.map((a) => (
                <li key={a.id}>
                  {a.mode} {a.workload_id}{" "}
                  <button className="btn" type="button" onClick={() => onUnassign(a.id)}>
                    Unassign
                  </button>
                </li>
              ))}
            </ul>
          )}
        </article>
      ))}
      {orphaned.length > 0 ? (
        <article className="panel">
          <h2>Assigned GPUs not on this node</h2>
          <p className="banner banner-warn" role="status">
            These GPUs are no longer reported by the node. The card was removed or its driver is not loaded. Unassign
            them, or refresh inventory once the GPU is back.
          </p>
          <ul>
            {orphaned.map((a) => (
              <li key={a.id}>
                {a.gpu_id} {a.mode} {a.workload_id}{" "}
                <button className="btn" type="button" onClick={() => onUnassign(a.id)}>
                  Unassign
                </button>
              </li>
            ))}
          </ul>
        </article>
      ) : null}
      {workloadID ? (
        <form
          className="form"
          onSubmit={(event) => {
            event.preventDefault();
            void assignGpu({ gpu_id: gpuId, workload_id: workloadID, mode, exclusive })
              .then(() => reload())
              .catch((err) => setError(err instanceof ApiError ? err.message : "Assign failed"));
          }}
        >
          <label htmlFor="gpu-id">GPU id</label>
          <input id="gpu-id" value={gpuId} onChange={(e) => setGpuId(e.target.value)} />
          <label htmlFor="gpu-mode">Mode</label>
          <select id="gpu-mode" value={mode} onChange={(e) => setMode(e.target.value)}>
            <option value="render">render</option>
            <option value="compute">compute</option>
            <option value="encode">encode</option>
            <option value="vfio">vfio</option>
          </select>
          <label>
            <input type="checkbox" checked={exclusive} onChange={(e) => setExclusive(e.target.checked)} /> Exclusive
          </label>
          <button className="btn btn-primary" type="submit">
            Assign GPU
          </button>
        </form>
      ) : (
        <p>Open a workload GPU tab to assign. Creating a workload without a GPU does not attach /dev/dri.</p>
      )}
    </>
  );
  if (nested) {
    return <div className="stack">{inner}</div>;
  }
  return (
    <section className="page page-wide" aria-labelledby="gpu-heading">
      {inner}
    </section>
  );
}
