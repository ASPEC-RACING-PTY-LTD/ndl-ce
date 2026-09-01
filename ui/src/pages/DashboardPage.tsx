import { useSession } from "../session";

export function DashboardPage() {
  const session = useSession();
  const username = session.status === "ready" ? session.user?.username : undefined;

  return (
    <section className="page" aria-labelledby="dashboard-heading">
      <header className="page-header">
        <h1 id="dashboard-heading">Dashboard</h1>
        {username ? <p className="page-kicker">Signed in as {username}</p> : null}
      </header>
      <div className="panel empty-panel">
        <p className="empty-title">There is no host inventory yet (Phase 2).</p>
        <p>
          This appliance is running and you are authenticated. Host inventory
          is not part of Phase 1, so this page does not list machines,
          addresses, or sample capacity.
        </p>
      </div>
    </section>
  );
}
