import { useEffect, useState } from "react";
import { importWorkload, listImages, listNetworks, listPools } from "../api/client";
import type { LibraryItem, StoragePool } from "../api/phase3";
import type { Network } from "../api/phase4";
import { Field } from "../components/Field";
import { navigate } from "../router";
import { useSession } from "../session";
import { canMutate } from "../ux";

export function ImportVMPage() {
  const session = useSession();
  const roles = session.status === "ready" ? session.user?.roles : undefined;
  const mutate = canMutate(roles);
  const admin = Boolean(roles?.includes("admin"));
  const [images, setImages] = useState<LibraryItem[]>([]);
  const [pools, setPools] = useState<StoragePool[]>([]);
  const [nets, setNets] = useState<Network[]>([]);
  const [name, setName] = useState("imported");
  const [libraryID, setLibraryID] = useState("");
  const [poolID, setPoolID] = useState("");
  const [networkID, setNetworkID] = useState("");
  const [firmware, setFirmware] = useState("bios");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    void Promise.all([listImages(), listPools(), listNetworks()])
      .then(([imgs, listedPools, listedNets]) => {
        const disks = imgs.filter((i) => i.kind === "disk-image" || i.kind === "cloud-image");
        setImages(disks);
        setPools(listedPools.items ?? []);
        setNets(listedNets.items ?? []);
        if (!libraryID && disks[0]?.id) {
          setLibraryID(disks[0].id);
        }
        if (!poolID && listedPools.items?.[0]?.id) {
          setPoolID(listedPools.items[0].id);
        }
        if (!networkID && listedNets.items?.[0]?.id) {
          setNetworkID(listedNets.items[0].id);
        }
      })
      .catch((err) => setError(err instanceof Error ? err.message : "Unavailable"));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  if (!mutate) {
    return (
      <section className="page">
        <h1>Import VM</h1>
        <p>Importing a disk image requires operator or admin.</p>
      </section>
    );
  }

  return (
    <section className="page page-wide" aria-labelledby="import-heading">
      <header className="page-header">
        <h1 id="import-heading">Import VM</h1>
        <p className="page-kicker">
          Import converts a library qcow2 into a new vm-disk with a new UUID. Import is privileged. Failed convert
          does not leave a half-adopted volume.
        </p>
      </header>
      {!admin ? (
        <p className="banner banner-warn" role="status">
          Import is an admin action. Operator cannot import.
        </p>
      ) : null}
      {error ? (
        <p className="banner banner-error" role="alert">
          {error}
        </p>
      ) : null}
      {images.length === 0 ? <p>No disk-image library items. Upload a qcow2 as a disk-image first.</p> : null}
      <Field id="imp-name" label="Name" value={name} onChange={(e) => setName(e.target.value)} />
      <label htmlFor="imp-lib">
        Library disk
        <select id="imp-lib" className="field-input" value={libraryID} onChange={(e) => setLibraryID(e.target.value)}>
          {images.map((i) => (
            <option key={i.id} value={i.id}>
              {i.display_name || i.id} ({i.kind})
            </option>
          ))}
        </select>
      </label>
      <label htmlFor="imp-pool">
        Pool
        <select id="imp-pool" className="field-input" value={poolID} onChange={(e) => setPoolID(e.target.value)}>
          {pools.map((p) => (
            <option key={p.id} value={p.id}>
              {p.name}
            </option>
          ))}
        </select>
      </label>
      <label htmlFor="imp-net">
        Network
        <select id="imp-net" className="field-input" value={networkID} onChange={(e) => setNetworkID(e.target.value)}>
          {nets.map((n) => (
            <option key={n.id} value={n.id}>
              {n.name}
            </option>
          ))}
        </select>
      </label>
      <label htmlFor="imp-fw">
        Firmware
        <select id="imp-fw" className="field-input" value={firmware} onChange={(e) => setFirmware(e.target.value)}>
          <option value="bios">BIOS</option>
          <option value="uefi">UEFI</option>
        </select>
      </label>
      <button
        className="btn btn-primary"
        type="button"
        disabled={busy || !admin || !libraryID || !networkID}
        onClick={() => {
          setBusy(true);
          setError(null);
          void importWorkload({ name, library_id: libraryID, pool_id: poolID, network_id: networkID, firmware })
            .then((wl) => navigate(`/workloads/${wl.id}`))
            .catch((err) => setError(err instanceof Error ? err.message : "Import failed"))
            .finally(() => setBusy(false));
        }}
      >
        Import
      </button>
    </section>
  );
}
