import { useEffect, useState } from "react";
import {
  createPool,
  createVolume,
  createISCSI,
  createLVM,
  createNFS,
  createSMB,
  createZFS,
  datastoreRuntime,
  importZFS,
  listImages,
  listPools,
  listVolumes,
  lvmRuntime,
  uploadImage,
  zfsRuntime,
} from "../api/client";
import type { DatastoreRuntime, LibraryItem, LVMRuntime, StoragePool, StorageVolume, ZFSRuntime } from "../api/phase3";
import { Field } from "../components/Field";
import { formatBytes } from "../format";
import { useSession } from "../session";

function canMutate(roles: string[] | undefined): boolean {
  return Boolean(roles?.includes("admin") || roles?.includes("operator"));
}

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
  const [zfs, setZfs] = useState<ZFSRuntime | null>(null);
  const [zfsName, setZfsName] = useState("tank");
  const [zfsGUID, setZfsGUID] = useState("");
  const [zfsDisk, setZfsDisk] = useState("");
  const [lvm, setLvm] = useState<LVMRuntime | null>(null);
  const [lvmName, setLvmName] = useState("ndlvg");
  const [lvmDisk, setLvmDisk] = useState("");
  const [datastores, setDatastores] = useState<DatastoreRuntime | null>(null);
  const [nfsName, setNfsName] = useState("nfs-iso");
  const [nfsLocator, setNfsLocator] = useState("");
  const [smbName, setSmbName] = useState("smb-iso");
  const [smbLocator, setSmbLocator] = useState("");
  const [smbUser, setSmbUser] = useState("");
  const [smbPass, setSmbPass] = useState("");
  const [iscsiName, setIscsiName] = useState("iscsi-lun");
  const [iscsiIQN, setIscsiIQN] = useState("");
  const [iscsiPortal, setIscsiPortal] = useState("");

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
    const [vols, imgs, runtime, lvmStatus, dsStatus] = await Promise.all([
      listVolumes(poolId),
      listImages(poolId),
      zfsRuntime().catch(() => null),
      lvmRuntime().catch(() => null),
      datastoreRuntime().catch(() => null),
    ]);
    setVolumes(vols);
    setImages(imgs);
    setZfs(runtime);
    setLvm(lvmStatus);
    setDatastores(dsStatus);
  }

  useEffect(() => {
    let cancelled = false;
    void reload()
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
  const firstRun = pools.length === 0;

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

  async function onImportZFS() {
    setBusy(true);
    setError(null);
    try {
      const created = await importZFS({ guid: zfsGUID, name: zfsName });
      setSelected(created.id);
      await reload();
    } catch (err) {
      setError(err instanceof Error ? err.message : "ZFS import failed");
    } finally {
      setBusy(false);
    }
  }

  async function onCreateZFS() {
    setBusy(true);
    setError(null);
    try {
      const created = await createZFS({ name: zfsName, disks: [zfsDisk] });
      setSelected(created.id);
      await reload();
    } catch (err) {
      setError(err instanceof Error ? err.message : "ZFS create failed");
    } finally {
      setBusy(false);
    }
  }

  async function onCreateLVM() {
    setBusy(true);
    setError(null);
    try {
      const created = await createLVM({ name: lvmName, disks: [lvmDisk] });
      setSelected(created.id);
      await reload();
    } catch (err) {
      setError(err instanceof Error ? err.message : "LVM create failed");
    } finally {
      setBusy(false);
    }
  }

  async function onCreateNFS() {
    setBusy(true);
    setError(null);
    try {
      const created = await createNFS({ name: nfsName, locator: nfsLocator });
      setSelected(created.id);
      await reload();
    } catch (err) {
      setError(err instanceof Error ? err.message : "NFS add failed");
    } finally {
      setBusy(false);
    }
  }

  async function onCreateSMB() {
    setBusy(true);
    setError(null);
    try {
      const created = await createSMB({ name: smbName, locator: smbLocator, username: smbUser, password: smbPass });
      setSmbPass("");
      setSelected(created.id);
      await reload();
    } catch (err) {
      setError(err instanceof Error ? err.message : "SMB add failed");
    } finally {
      setBusy(false);
    }
  }

  async function onCreateISCSI() {
    setBusy(true);
    setError(null);
    try {
      const created = await createISCSI({ name: iscsiName, iqn: iscsiIQN, portal: iscsiPortal });
      setSelected(created.id);
      await reload();
    } catch (err) {
      setError(err instanceof Error ? err.message : "iSCSI add failed");
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
    <section className="page page-wide" aria-labelledby="storage-heading">
      <header className="page-header">
        <h1 id="storage-heading">Storage</h1>
        <p className="page-kicker">
          Directory remains the default. ZFS, LVM-thin, and NFS/SMB/iSCSI are optional. Hosts
          without those tools keep Directory. zpool import -f and vgexport are refused. Passwords
          are stored in secrets, not unit files.
        </p>
      </header>
      {error ? (
        <p className="banner banner-error" role="alert">
          {error}
        </p>
      ) : null}
      {firstRun || mutate ? (
        <article className="panel">
          <h2>{firstRun ? "First-run storage pool" : "Create Directory pool"}</h2>
          {firstRun ? (
            <p className="lede">
              This installation has no usable storage pool yet. Create a Directory pool. Workloads
              cannot start until a later phase, but images and disks can be stored now.
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
      {mutate ? (
        <article className="panel">
          <h2>ZFS</h2>
          <p className="lede">
            Import by pool GUID or create on extra disks. The host root disk is refused. Incremental
            send is a ZFS capability. Directory incremental send stays no.
          </p>
          {zfs?.host_supported === false ? (
            <p className="banner banner-warn" role="status">
              {zfs.reason || "ZFS runtime install uses the Debian 13 adapter. Directory remains first-class."}
            </p>
          ) : null}
          {zfs?.status === "not_installed" ? (
            <p className="banner" role="status">
              ZFS userland is not installed. Directory storage remains the default. Optional package:{" "}
              {(zfs.packages ?? []).join(", ") || "zfsutils-linux"}.
            </p>
          ) : null}
          <form
            className="form"
            onSubmit={(e) => {
              e.preventDefault();
              void onImportZFS();
            }}
          >
            <Field id="zfs-name" label="Pool name" value={zfsName} onChange={(e) => setZfsName(e.target.value)} />
            <Field
              id="zfs-guid"
              label="zpool GUID"
              value={zfsGUID}
              onChange={(e) => setZfsGUID(e.target.value)}
              hint="Numeric pool GUID. Names are locators, not identity. Force import is refused."
            />
            <button className="btn btn-primary" type="submit" disabled={busy || !zfsGUID}>
              Import ZFS pool
            </button>
          </form>
          <form
            className="form"
            onSubmit={(e) => {
              e.preventDefault();
              void onCreateZFS();
            }}
          >
            <Field
              id="zfs-disk"
              label="Extra disk"
              value={zfsDisk}
              onChange={(e) => setZfsDisk(e.target.value)}
              hint="A by-id or extra-disk path such as /dev/disk/by-id/.... Not the host root disk."
            />
            <button className="btn" type="submit" disabled={busy || !zfsDisk}>
              Create ZFS pool
            </button>
          </form>
        </article>
      ) : null}
      {mutate ? (
        <article className="panel">
          <h2>LVM-thin</h2>
          <p className="lede">
            Create a volume group and thin pool on extra disks. The host root disk is refused.
            Incremental send is not an LVM capability. vgexport is refused.
          </p>
          {lvm?.host_supported === false ? (
            <p className="banner banner-warn" role="status">
              {lvm.reason || "LVM runtime install uses the Debian 13 adapter. Directory remains first-class."}
            </p>
          ) : null}
          {lvm?.status === "not_installed" ? (
            <p className="banner" role="status">
              LVM userland is not installed. Directory storage remains the default. Optional package:{" "}
              {(lvm.packages ?? []).join(", ") || "lvm2"}.
            </p>
          ) : null}
          <form
            className="form"
            onSubmit={(e) => {
              e.preventDefault();
              void onCreateLVM();
            }}
          >
            <Field id="lvm-name" label="Volume group name" value={lvmName} onChange={(e) => setLvmName(e.target.value)} />
            <Field
              id="lvm-disk"
              label="Extra disk"
              value={lvmDisk}
              onChange={(e) => setLvmDisk(e.target.value)}
              hint="A by-id or extra-disk path such as /dev/disk/by-id/.... Not the host root disk."
            />
            <button className="btn btn-primary" type="submit" disabled={busy || !lvmDisk}>
              Create LVM-thin pool
            </button>
          </form>
        </article>
      ) : null}
      {mutate ? (
        <article className="panel">
          <h2>Network storage</h2>
          <p className="lede">
            NFS and SMB are compute and library mounts. iSCSI is a raw LUN for one VM disk. If the
            share is down, volumes stay unavailable and are not deleted. Incremental send is not a
            network datastore capability. Backup destinations remain a separate Phase 11 target.
          </p>
          {datastores?.host_supported === false ? (
            <p className="banner banner-warn" role="status">
              {datastores.reason || "Network datastore runtime uses the Debian 13 adapter."}
            </p>
          ) : null}
          {datastores?.status === "not_installed" ? (
            <p className="banner" role="status">
              Network datastore tools are optional. Directory remains the default. Optional packages:{" "}
              {(datastores.packages ?? []).join(", ") || "nfs-common, cifs-utils, open-iscsi"}.
            </p>
          ) : null}
          <form
            className="form"
            onSubmit={(e) => {
              e.preventDefault();
              void onCreateNFS();
            }}
          >
            <Field id="nfs-name" label="NFS pool name" value={nfsName} onChange={(e) => setNfsName(e.target.value)} />
            <Field
              id="nfs-locator"
              label="NFS locator"
              value={nfsLocator}
              onChange={(e) => setNfsLocator(e.target.value)}
              hint="server:/export. This is a locator, not identity. The pool UUID is identity."
            />
            <button className="btn btn-primary" type="submit" disabled={busy || !nfsLocator}>
              Add NFS share
            </button>
          </form>
          <form
            className="form"
            onSubmit={(e) => {
              e.preventDefault();
              void onCreateSMB();
            }}
          >
            <Field id="smb-name" label="SMB pool name" value={smbName} onChange={(e) => setSmbName(e.target.value)} />
            <Field
              id="smb-locator"
              label="SMB locator"
              value={smbLocator}
              onChange={(e) => setSmbLocator(e.target.value)}
              hint="//server/share. The password is written to a 0600 credentials file, never to argv or a unit file."
            />
            <Field id="smb-user" label="Username" value={smbUser} onChange={(e) => setSmbUser(e.target.value)} />
            <Field
              id="smb-pass"
              label="Password"
              type="password"
              autoComplete="new-password"
              value={smbPass}
              onChange={(e) => setSmbPass(e.target.value)}
            />
            <button className="btn" type="submit" disabled={busy || !smbLocator}>
              Add SMB share
            </button>
          </form>
          <form
            className="form"
            onSubmit={(e) => {
              e.preventDefault();
              void onCreateISCSI();
            }}
          >
            <Field id="iscsi-name" label="iSCSI pool name" value={iscsiName} onChange={(e) => setIscsiName(e.target.value)} />
            <Field
              id="iscsi-iqn"
              label="Target IQN"
              value={iscsiIQN}
              onChange={(e) => setIscsiIQN(e.target.value)}
              hint="iqn.yyyy-mm.domain:name. One VM disk LUN per pool. Snapshots are not available."
            />
            <Field
              id="iscsi-portal"
              label="Portal"
              value={iscsiPortal}
              onChange={(e) => setIscsiPortal(e.target.value)}
              hint="host:3260"
            />
            <button className="btn" type="submit" disabled={busy || !iscsiIQN || !iscsiPortal}>
              Add iSCSI target
            </button>
          </form>
        </article>
      ) : null}
      <article className="panel">
        <h2>Pools</h2>
        {pools.length === 0 ? (
          <p>No storage pools.</p>
        ) : (
          <div className="table-wrap">
            <table>
              <thead>
                <tr>
                  <th>Name</th>
                  <th>Status</th>
                  <th>Backend</th>
                  <th>Usable</th>
                  <th>Allocated</th>
                  <th>Provisioned</th>
                </tr>
              </thead>
              <tbody>
                {pools.map((p) => (
                  <tr key={p.id}>
                    <td>
                      <button className="btn btn-ghost" type="button" onClick={() => setSelected(p.id)}>
                        {p.name}
                      </button>
                    </td>
                    <td>{p.status}</td>
                    <td>{p.backend_type}</td>
                    <td>{capacityLabel(p.usable_bytes, p.status)}</td>
                    <td>{capacityLabel(p.allocated_bytes, p.status)}</td>
                    <td>{capacityLabel(p.provisioned_bytes, p.status)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </article>
      {pool ? (
        <article className="panel">
          <h2>{pool.name}</h2>
          <dl className="definition-list">
            <div>
              <dt>Identity</dt>
              <dd>
                <code>{pool.id}</code>
              </dd>
            </div>
            <div>
              <dt>Locator</dt>
              <dd>
                <code>{pool.locator || "Not reported"}</code>
              </dd>
            </div>
            <div>
              <dt>Backend</dt>
              <dd>{pool.backend_type}</dd>
            </div>
            <div>
              <dt>Status</dt>
              <dd>{pool.status}</dd>
            </div>
            <div>
              <dt>Classes</dt>
              <dd>{(pool.storage_classes ?? []).join(", ")}</dd>
            </div>
            <div>
              <dt>Incremental send</dt>
              <dd>{pool.capabilities?.incremental_send ? "Yes" : "No"}</dd>
            </div>
            {pool.backend_type === "lvm" ? (
              <div>
                <dt>Thin pool metadata</dt>
                <dd>{pool.metadata_percent != null ? `${pool.metadata_percent.toFixed(1)}%` : "Not reported"}</dd>
              </div>
            ) : null}
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
        </article>
      ) : null}
      {pool && mutate ? (
        <div className="card-grid">
          <article className="panel">
            <h2>Create volume</h2>
            <form
              className="form"
              onSubmit={(e) => {
                e.preventDefault();
                void onCreateVolume();
              }}
            >
              <label className="field">
                <span className="field-label">Class</span>
                <select className="field-input" value={volClass} onChange={(e) => setVolClass(e.target.value)}>
                  <option value="vm-disk">vm-disk</option>
                  <option value="container-root">container-root</option>
                  <option value="template">template</option>
                  <option value="backup-staging">backup-staging</option>
                </select>
              </label>
              <Field
                id="vol-size"
                label="Size (GiB)"
                type="number"
                min={1}
                value={volSizeGiB}
                onChange={(e) => setVolSizeGiB(e.target.value)}
              />
              <button className="btn btn-primary" type="submit" disabled={busy || pool.status === "unavailable"}>
                Create volume
              </button>
            </form>
          </article>
          <article className="panel">
            <h2>Upload image</h2>
            <form
              className="form"
              onSubmit={(e) => {
                e.preventDefault();
                void onUpload();
              }}
            >
              <label className="field">
                <span className="field-label">Kind</span>
                <select className="field-input" value={kind} onChange={(e) => setKind(e.target.value)}>
                  <option value="iso">ISO</option>
                  <option value="cloud-image">cloud-image</option>
                  <option value="disk-image">disk-image</option>
                </select>
              </label>
              <label className="field">
                <span className="field-label">File</span>
                <input
                  className="field-input"
                  type="file"
                  onChange={(e) => setFile(e.target.files?.[0] ?? null)}
                />
              </label>
              <button className="btn btn-primary" type="submit" disabled={busy || !file || pool.status === "unavailable"}>
                Upload
              </button>
            </form>
          </article>
        </div>
      ) : null}
      <article className="panel">
        <h2>Volumes</h2>
        {volumes.length === 0 ? (
          <p>No volumes.</p>
        ) : (
          <div className="table-wrap">
            <table>
              <thead>
                <tr>
                  <th>UUID</th>
                  <th>Class</th>
                  <th>Status</th>
                  <th>Provisioned</th>
                  <th>Allocated</th>
                </tr>
              </thead>
              <tbody>
                {volumes.map((v) => (
                  <tr key={v.id}>
                    <td>
                      <code>{v.id}</code>
                    </td>
                    <td>{v.class}</td>
                    <td>{v.status}</td>
                    <td>{capacityLabel(v.size_bytes, v.status)}</td>
                    <td>{capacityLabel(v.allocated_bytes, v.status)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </article>
      <article className="panel">
        <h2>Image library</h2>
        {images.length === 0 ? (
          <p>No library items.</p>
        ) : (
          <div className="table-wrap">
            <table>
              <thead>
                <tr>
                  <th>UUID</th>
                  <th>Name</th>
                  <th>Kind</th>
                  <th>Size</th>
                  <th>Checksum</th>
                  <th>Status</th>
                </tr>
              </thead>
              <tbody>
                {images.map((item) => (
                  <tr key={item.id}>
                    <td>
                      <code>{item.id}</code>
                    </td>
                    <td>{item.display_name}</td>
                    <td>{item.kind}</td>
                    <td>{capacityLabel(item.size_bytes, item.status)}</td>
                    <td>
                      <code>{item.checksum_sha256.slice(0, 12)}</code>
                    </td>
                    <td>{item.status}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </article>
    </section>
  );
}
