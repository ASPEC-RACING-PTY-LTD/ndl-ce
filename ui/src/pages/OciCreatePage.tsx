import { OciContainerForm } from "../components/OciContainerForm";
import { PageHeader } from "../components/PageHeader";
import { navigate } from "../router";
import { canMutate } from "../ux";
import { useSession } from "../session";

/** OciCreatePage creates one OCI container; groups and Compose live on the OCI Containers page. */
export function OciCreatePage() {
  const session = useSession();
  const roles = session.status === "ready" ? session.user?.roles : undefined;
  const group = new URLSearchParams(window.location.search).get("group") || "";
  return (
    <section className="page page-wide" aria-labelledby="create-oci-heading">
      <PageHeader
        id="create-oci-heading"
        title="Create OCI container"
        kicker="Runs any Docker-compatible image directly on this host. No Docker Engine or system container needed."
      />
      {canMutate(roles) ? (
        <article className="panel">
          <OciContainerForm defaultGroupId={group} onCancel={() => navigate("/oci")} onDone={(w) => navigate(`/workloads/${w.id}`)} />
        </article>
      ) : (
        <p className="banner banner-error" role="alert">
          Creating containers requires operator or admin.
        </p>
      )}
    </section>
  );
}
