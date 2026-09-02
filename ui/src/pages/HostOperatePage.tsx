import { getNode } from "../api/client";
import { Link } from "../components/Link";
import { PageHeader } from "../components/PageHeader";
import { ErrorState, LoadingState } from "../components/EmptyState";
import { honestStatus } from "../format";
import { usePath } from "../router";
import { useQuery } from "../query";

export function HostOperatePage() {
  const path = usePath();
  const nodeId = path.split("/").filter(Boolean)[1] ?? "";
  const { data: node, error, loading } = useQuery(`host-operate:${nodeId}`, () => getNode(nodeId), 10_000);

  if (loading && !node) {
    return (
      <section className="page">
        <LoadingState />
      </section>
    );
  }
  if (error || !node) {
    return (
      <section className="page">
        <ErrorState>{error ?? "Host not found"}</ErrorState>
      </section>
    );
  }

  return (
    <section className="page" aria-labelledby="host-operate-heading">
      <PageHeader
        id="host-operate-heading"
        title={node.name}
        kicker="Host operational surface. Commands here affect the appliance host, not a guest."
      />
      <p className="banner banner-warn host-operate-banner" role="status">
        <strong>Host</strong> {node.name} ({node.id}) · {honestStatus(node.status)}
        {node.role ? ` · ${node.role}` : ""}
      </p>
      <nav className="subnav" aria-label="Host operations">
        <Link href={`/nodes/${node.id}`} aria-current="page">
          Summary
        </Link>
        <Link href={`/nodes/${node.id}/terminal`}>Terminal</Link>
        <Link href={`/nodes/${node.id}/files`}>Files</Link>
      </nav>
      <article className="panel">
        <h2>Identity</h2>
        <p>
          This is the physical or virtual host, not a system container or virtual machine. Use guest targets when you
          intend to operate inside a workload.
        </p>
        <dl className="definition-list compact">
          <div>
            <dt>Name</dt>
            <dd>{node.name}</dd>
          </div>
          <div>
            <dt>Node ID</dt>
            <dd>
              <code>{node.id}</code>
            </dd>
          </div>
          {node.listen_addr ? (
            <div>
              <dt>Address</dt>
              <dd>
                <code>{node.listen_addr}</code>
              </dd>
            </div>
          ) : null}
          <div>
            <dt>Status</dt>
            <dd>{honestStatus(node.status)}</dd>
          </div>
          {node.role ? (
            <div>
              <dt>Role</dt>
              <dd>{node.role}</dd>
            </div>
          ) : null}
        </dl>
      </article>
    </section>
  );
}
