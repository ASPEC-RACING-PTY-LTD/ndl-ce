import { useEffect, useState } from "react";
import {
  createPool,
  createVolume,
  listImages,
  listPools,
  listVolumes,
  uploadImage,
} from "../api/client";
import type { LibraryItem, StoragePool, StorageVolume } from "../api/phase3";
import { ErrorState } from "../components/EmptyState";
import { Field } from "../components/Field";
import { SelectField } from "../components/form/SelectField";
import { PageHeader } from "../components/PageHeader";
import { ResourceTable } from "../components/ResourceTable";
import { StatusBadge } from "../components/StatusBadge";
import { formatBytes } from "../format";
import { kindLabel } from "../labels";
import { canMutate } from "../rbac";
import { useSession } from "../session";

function capacityLabel(value: number | null | undefined, status: string): string {
  if (status === "unavailable") {
    return "Unavailable";
  }
  if (value == null) {
    return "Not reported";
  }
  return formatBytes(value);
}

export function StoragePage() {
  const session = useSession();
  const roles = session.status === "ready" ? session.user?.roles : undefined;
  const mutate = canMutate(roles);
  const [pools, setPools] = useState<StoragePool[]>([]);
  const [defaultPath, setDefaultPath] = useState("/var/lib/ndl/storage/local");
  const [selected, setSelected] = useState<string>("");
  const [volumes, setVolumes] = useState<StorageVolume[]>([]);
  const [images, setImages] = useState<LibraryItem[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [name, setName] = useState("local");
  const [path, setPath] = useState("/var/lib/ndl/storage/local");
  const [volClass, setVolClass] = useState("vm-disk");
  const [volSizeGiB, setVolSizeGiB] = useState("10");
  const [kind, setKind] = useState("iso");
  const [file, setFile] = useState<File | null>(null);
  const [busy, setBusy] = useState(false);
  const [loaded, setLoaded] = useState(false);

  async function reload() {
    const listed = await listPools();
    setPools(listed.items ?? []);
    if (listed.default_path) {
      setDefaultPath(listed.default_path);
      if (!path) {
        setPath(listed.default_path);
      }
    }
    const first = selected || listed.items?.[0]?.id || "";
    if (first && !selected) {
      setSelected(first);
    }
    const poolId = first;
    const [vols, imgs] = await Promise.all([listVolumes(poolId), listImages(poolId)]);
    setVolumes(vols);
    setImages(imgs);
    setLoaded(true);
  }

  useEffect(() => {
    let cancelled = false;
    void reload().catch((err) => {
      if (!cancelled) {
        setError(err instanceof Error ? err.message : "Unavailable");
        setLoaded(true);
      }
    });
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    if (!selected) {
      return;
    }
    void Promise.all([listVolumes(selected), listImages(selected)])
      .then(([vols, imgs]) => {
        setVolumes(vols);
        setImages(imgs);
      })
      .catch(() => undefined);
  }, [selected]);

  const pool = pools.find((p) => p.id === selected) ?? pools[0];
  const firstRun = loaded && pools.length === 0;

  async function onCreatePool() {
    setBusy(true);
    setError(null);
    try {
      const created = await createPool({ name, path: path || defaultPath });
      setSelected(created.id);
      await reload();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Create failed");
    } finally {
      setBusy(false);
    }
  }

  async function onCreateVolume() {
    if (!pool) {
      return;
    }
    setBusy(true);
    setError(null);
    try {
      const gib = Number(volSizeGiB);
      await createVolume({
        pool_id: pool.id,
        class: volClass,
        size_bytes: Math.round(gib * 1024 * 1024 * 1024),
      });
      await reload();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Create failed");
    } finally {
      setBusy(false);
    }
  }

  async function onUpload() {
    if (!pool || !file) {
      return;
    }
    setBusy(true);
    setError(null);
    try {
      await uploadImage(pool.id, kind, file);
      await reload();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Upload failed");
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="page" aria-labelledby="storage-heading">
      <PageHeader id="storage-heading" title="Storage" kicker="Directory pools, volumes, and the image library" />
      {error ? <ErrorState>{error}</ErrorState> : null}
      {firstRun || mutate ? (
        <article className="form-narrow">
          <h2>{firstRun ? "First-run storage pool" : "Create Directory pool"}</h2>
          {firstRun ? (
            <p className="lede">
              This installation has no usable storage pool yet. Create a Directory pool so images and
              disks can be stored.
            </p>
          ) : (
            <p className="lede">
              Add another Directory pool on a safe path. If the path shares the host root filesystem,
              a headroom warning is shown.
            </p>
          )}
          {mutate ? (
            <form
              className="form"
              onSubmit={(e) => {
                e.preventDefault();
                void onCreatePool();
              }}
            >
              <Field id="pool-name" label="Name" value={name} onChange={(e) => setName(e.target.value)} />
              <Field
                id="pool-path"
                label="Directory path"
                value={path}
                onChange={(e) => setPath(e.target.value)}
                hint="A safe default is /var/lib/ndl/storage/local. If this path shares the host root filesystem, a headroom warning is shown."
              />
              <button className="btn btn-primary" type="submit" disabled={busy}>
                Create Directory pool
              </button>
            </form>
          ) : (
            <p>An operator or administrator must create the first pool.</p>
          )}
        </article>
      ) : null}
      <section className="section">
        <h2>Pools</h2>
        <ResourceTable
          headers={["Name", "Status", "Backend", "Usable", "Allocated", "Snapshots"]}
          numeric={[3, 4]}
          selected={pools.findIndex((p) => p.id === selected)}
          empty={<p>No storage pools.</p>}
          rows={pools.map((p) => [
            <button key={p.id} className="btn btn-ghost btn-sm" type="button" onClick={() => setSelected(p.id)}>
              {p.name}
            </button>,
            <StatusBadge key="st" status={p.status} />,
            kindLabel(p.backend_type),
            capacityLabel(p.usable_bytes, p.status),
            capacityLabel(p.allocated_bytes, p.status),
            <StatusBadge key="snap" status={p.capabilities?.snapshots ? "available" : "unavailable"} />,
          ])}
        />
      </section>
      {pool ? (
        <section className="section">
          <h2>{pool.name}</h2>
          <dl className="definition-list compact">
            <div>
              <dt>Path</dt>
              <dd>
                <code>{pool.locator || "Not reported"}</code>
              </dd>
            </div>
            <div>
              <dt>Backend</dt>
              <dd>{kindLabel(pool.backend_type)}</dd>
            </div>
            <div>
              <dt>Status</dt>
              <dd>
                <StatusBadge status={pool.status} />
              </dd>
            </div>
            <div>
              <dt>Classes</dt>
              <dd>{(pool.storage_classes ?? []).map(kindLabel).join(", ") || "Not reported"}</dd>
            </div>
            <div>
              <dt>Snapshots</dt>
              <dd>
                <StatusBadge status={pool.capabilities?.snapshots ? "available" : "unavailable"} />
              </dd>
            </div>
          </dl>
          {(pool.warning_text ?? []).map((text) => (
            <p key={text} className="banner banner-warn" role="status">
              {text}
            </p>
          ))}
          {pool.status === "unavailable" ? (
            <p className="banner banner-error" role="alert">
              This pool is unavailable. Stored objects were not deleted.
              {pool.reason ? ` ${pool.reason}` : ""}
            </p>
          ) : null}
        </section>
      ) : null}
      {pool && mutate ? (
        <div className="split-grid">
          <section className="section">
            <h2>Create volume</h2>
            <form
              className="form"
              onSubmit={(e) => {
                e.preventDefault();
                void onCreateVolume();
              }}
            >
              <div className="field-row">
                <SelectField id="vol-class" label="Class" value={volClass} onChange={(e) => setVolClass(e.target.value)}>
                  <option value="vm-disk">{kindLabel("vm-disk")}</option>
                  <option value="container-root">{kindLabel("container-root")}</option>
                  <option value="template">{kindLabel("template")}</option>
                  <option value="backup-staging">{kindLabel("backup-staging")}</option>
                </SelectField>
                <Field
                  id="vol-size"
                  label="Size (GiB)"
                  type="number"
                  min={1}
                  value={volSizeGiB}
                  onChange={(e) => setVolSizeGiB(e.target.value)}
                />
              </div>
              <button className="btn btn-primary" type="submit" disabled={busy || pool.status === "unavailable"}>
                Create volume
              </button>
            </form>
          </section>
          <section className="section">
            <h2>Upload image</h2>
            <form
              className="form"
              onSubmit={(e) => {
                e.preventDefault();
                void onUpload();
              }}
            >
              <div className="field-row">
                <SelectField id="img-kind" label="Kind" value={kind} onChange={(e) => setKind(e.target.value)}>
                  <option value="iso">ISO</option>
                  <option value="cloud-image">{kindLabel("cloud-image")}</option>
                </SelectField>
                <label className="field">
                  <span className="field-label">File</span>
                  <input className="field-input" type="file" onChange={(e) => setFile(e.target.files?.[0] ?? null)} />
                </label>
              </div>
              <button className="btn btn-primary" type="submit" disabled={busy || !file || pool.status === "unavailable"}>
                Upload
              </button>
            </form>
          </section>
        </div>
      ) : null}
      <section className="section">
        <h2>Volumes</h2>
        <ResourceTable
          headers={["Class", "Status", "Provisioned", "Allocated"]}
          numeric={[2, 3]}
          empty={<p>No volumes.</p>}
          rows={volumes.map((v) => [
            kindLabel(v.class),
            <StatusBadge key={v.id} status={v.status} />,
            capacityLabel(v.size_bytes, v.status),
            capacityLabel(v.allocated_bytes, v.status),
          ])}
        />
      </section>
      <section className="section">
        <h2>Image library</h2>
        <ResourceTable
          headers={["Name", "Kind", "Size", "Status"]}
          empty={<p>No library items.</p>}
          rows={images.map((item) => [
            item.display_name,
            kindLabel(item.kind),
            capacityLabel(item.size_bytes, item.status),
            <StatusBadge key={item.id} status={item.status} />,
          ])}
        />
      </section>
    </section>
  );
}
