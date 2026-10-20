import { OciContainerForm } from "../components/OciContainerForm";
import { PageHeader } from "../components/PageHeader";
import { navigate } from "../router";
import { canMutate } from "../ux";
import { useSession } from "../session";
import { num, str, useTemplateFromURL } from "../instanceTemplates";

/** OciCreatePage creates one OCI container; groups and Compose live on the OCI Containers page. */
export function OciCreatePage() {
  const session = useSession();
  const roles = session.status === "ready" ? session.user?.roles : undefined;
  const group = new URLSearchParams(window.location.search).get("group") || "";
  const wantsTemplate = new URLSearchParams(window.location.search).has("template");
  const { template, error } = useTemplateFromURL("oci");
  const preset = template
    ? {
        image: str(template.spec, "image"),
        cpus: num(template.spec, "cpus"),
        memory_bytes: num(template.spec, "memory_bytes"),
      }
    : undefined;
  return (
    <section className="page page-wide" aria-labelledby="create-oci-heading">
      <PageHeader
        id="create-oci-heading"
        title="Create OCI container"
        kicker="Runs any Docker-compatible image directly on this host. No Docker Engine or system container needed."
      />
      {error ? (
        <p className="banner banner-error" role="alert">
          {error}
        </p>
      ) : null}
      {wantsTemplate && !template && !error ? <p className="muted">Reading the template</p> : null}
      {canMutate(roles) && (!wantsTemplate || template || error) ? (
        <article className="panel">
          <OciContainerForm
            key={template?.id ?? "blank"}
            preset={preset}
            defaultGroupId={group}
            onCancel={() => navigate("/oci")}
            onDone={(w) => navigate(`/workloads/${w.id}`)}
          />
        </article>
      ) : canMutate(roles) ? null : (
        <p className="banner banner-error" role="alert">
          Creating containers requires operator or admin.
        </p>
      )}
    </section>
  );
}
