import { useSession } from "../session";

export function MePage() {
  const session = useSession();
  const user = session.status === "ready" ? session.user : null;

  if (!user) {
    return null;
  }

  return (
    <section className="page" aria-labelledby="me-heading">
      <header className="page-header">
        <h1 id="me-heading">Account</h1>
        <p className="page-kicker">Identity for the current session</p>
      </header>
      <dl className="panel definition-list">
        <div>
          <dt>Username</dt>
          <dd>{user.username}</dd>
        </div>
        <div>
          <dt>User ID</dt>
          <dd>
            <code>{user.user_id}</code>
          </dd>
        </div>
        <div>
          <dt>Roles</dt>
          <dd>{user.roles.length > 0 ? user.roles.join(", ") : "None"}</dd>
        </div>
        <div>
          <dt>Edition</dt>
          <dd>{user.edition}</dd>
        </div>
        {user.cluster_id ? (
          <div>
            <dt>Cluster ID</dt>
            <dd>
              <code>{user.cluster_id}</code>
            </dd>
          </div>
        ) : null}
      </dl>
    </section>
  );
}
