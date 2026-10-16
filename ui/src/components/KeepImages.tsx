import { useEffect, useState } from "react";
import { ApiError, listBackupWorkloadOptions, setBackupWorkloadOptions } from "../api/client";
import { ErrorNotice } from "./ErrorNotice";

type Workload = { id: string; name: string; kind?: string };

/**
 * KeepImages chooses, per workload, whether its backups keep a copy of the
 * container images it runs, so it can be restored without the registry or
 * a rebuild. Smart backups otherwise record images by digest only.
 */
export function KeepImages({ workloads, mutate }: { workloads: Workload[]; mutate: boolean }) {
  const [keep, setKeep] = useState<Record<string, boolean>>({});
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    listBackupWorkloadOptions()
      .then((res) => setKeep(Object.fromEntries((res.items ?? []).map((o) => [o.workload_id ?? "", !!o.keep_images]))))
      .catch(() => setKeep({}));
  }, []);

  async function toggle(id: string, on: boolean) {
    setError(null);
    setKeep((k) => ({ ...k, [id]: on }));
    try {
      await setBackupWorkloadOptions(id, { keep_images: on });
    } catch (err) {
      setKeep((k) => ({ ...k, [id]: !on }));
      setError(err instanceof ApiError || err instanceof Error ? err.message : "Could not save");
    }
  }

  const containers = workloads.filter((w) => w.kind === "system-container");
  if (!containers.length) {
    return null;
  }
  return (
    <details className="section-block">
      <summary>Container image copies</summary>
      <p className="field-hint">
        Smart backups record each Docker image by its exact digest and pull it again on restore. Keep a copy when an image
        may disappear from its registry or was built by hand: the backup grows by the image's size, deduplicated.
      </p>
      {error ? <ErrorNotice error={error} /> : null}
      {containers.map((w) => (
        <label className="field-check" key={w.id}>
          <input type="checkbox" checked={!!keep[w.id]} disabled={!mutate} onChange={(e) => void toggle(w.id, e.target.checked)} />
          Keep a copy of {w.name}'s images
        </label>
      ))}
    </details>
  );
}
