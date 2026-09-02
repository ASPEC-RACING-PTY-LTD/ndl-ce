import { useEffect, useState } from "react";
import { ApiError, assignGpu, listGpus, unassignGpu } from "../api/client";
import type { GPUListResponse } from "../generated/openapi";
import { currentPath } from "../router";

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
  const nested = currentPath().startsWith("/node");
  const heading = nested ? (
    <h2 id="gpu-heading">GPUs</h2>
  ) : (
    <header className="page-header">
      <h1 id="gpu-heading">GPUs</h1>
      <p className="page-kicker">
        Workloads receive a GPU only when assigned. gpu=all is refused. ACS override is refused.
        Store GPU picker is Phase 36.
      </p>
    </header>
  );
  const inner = (
    <>
      {heading}
      {error ? (
        <p className="banner banner-error" role="alert">
          {error}
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
                  <button
                    className="btn"
                    type="button"
                    onClick={() => {
                      void unassignGpu(a.id)
                        .then(() => reload())
                        .catch((err) => setError(err instanceof ApiError ? err.message : "Unassign failed"));
                    }}
                  >
                    Unassign
                  </button>
                </li>
              ))}
            </ul>
          )}
        </article>
      ))}
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
