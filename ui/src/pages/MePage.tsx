import { useState } from "react";
import { PageHeader } from "../components/PageHeader";
import { UxModeToggle } from "../components/UxModeToggle";
import { editionLabel, roleLabel } from "../labels";
import { useSession } from "../session";
import { getUxLevelDefault, setUxLevelDefault, type UxLevel } from "../ux-mode";

export function MePage() {
  const session = useSession();
  const user = session.status === "ready" ? session.user : null;
  const [level, setLevel] = useState<UxLevel>(getUxLevelDefault);
  const [details, setDetails] = useState(false);

  if (!user) {
    return null;
  }

  return (
    <section className="page form-narrow" aria-labelledby="me-heading">
      <PageHeader id="me-heading" title="Account" kicker="Identity for the current session" />
      <article className="panel">
        <dl className="definition-list">
          <div>
            <dt>Username</dt>
            <dd>{user.username}</dd>
          </div>
          <div>
            <dt>Roles</dt>
            <dd>{user.roles.length > 0 ? user.roles.map(roleLabel).join(", ") : "None"}</dd>
          </div>
          <div>
            <dt>Edition</dt>
            <dd>{editionLabel(user.edition)}</dd>
          </div>
        </dl>
        <div className="btn-row">
          <button className="btn btn-ghost btn-sm" type="button" onClick={() => setDetails((v) => !v)}>
            {details ? "Hide details" : "Details"}
          </button>
        </div>
        {details ? (
          <dl className="definition-list">
            <div>
              <dt>User ID</dt>
              <dd>
                <code>{user.user_id}</code>
              </dd>
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
        ) : null}
      </article>
      <article className="panel stack">
        <h2>Preferences</h2>
        <p className="lede">
          Default configuration level for create and settings screens. Individual screens can
          override this for the current session.
        </p>
        <UxModeToggle
          value={level}
          onChange={(next) => {
            setLevel(next);
            setUxLevelDefault(next);
          }}
        />
      </article>
    </section>
  );
}
