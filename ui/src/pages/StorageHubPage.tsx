import { navigate } from "../router";
import { StorageManagePage, StorageTabs, type StorageTab } from "./StorageManagePage";
import { StoragePage } from "./StoragePage";

/** StorageHubPage holds Storage and Manage under one sidebar entry, switched at the top. */
export function StorageHubPage({ tab }: { tab: StorageTab }) {
  return (
    <section className="page page-wide">
      <StorageTabs tab={tab} onChange={(t) => navigate(t === "manage" ? "/storage/manage" : "/storage")} />
      {tab === "storage" ? <StoragePage /> : <StorageManagePage />}
    </section>
  );
}
